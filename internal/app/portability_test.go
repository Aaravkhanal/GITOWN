package app

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Aaravkhanal/GITOWN/internal/gitstore"
)

func TestReadExportPackageChecksChecksumAndExactArchiveShape(t *testing.T) {
	bundle := []byte("sample bundle payload")
	sum := sha256.Sum256(bundle)
	manifest := repositoryExportManifest{SchemaVersion: 1, Format: "gitown.repository-export/v1"}
	manifest.Bundle.File = "repository.bundle"
	manifest.Bundle.SHA256 = hex.EncodeToString(sum[:])
	manifest.Bundle.SizeBytes = int64(len(bundle))
	manifest.Bundle.Refs = []gitstore.BundleRef{}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	makeRequest := func(t *testing.T, manifestBytes []byte, extra bool) *http.Request {
		t.Helper()
		var archive bytes.Buffer
		tarWriter := tar.NewWriter(&archive)
		for name, data := range map[string][]byte{"manifest.json": manifestBytes, "repository.bundle": bundle} {
			if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
				t.Fatal(err)
			}
			if _, err := tarWriter.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if extra {
			if err := tarWriter.WriteHeader(&tar.Header{Name: "extra.txt", Mode: 0600, Size: 1, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
				t.Fatal(err)
			}
			_, _ = tarWriter.Write([]byte("x"))
		}
		if err := tarWriter.Close(); err != nil {
			t.Fatal(err)
		}
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("package", "export.tar")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(archive.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/imports/bundle", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		return request
	}

	t.Run("accepts matching checksum", func(t *testing.T) {
		parsed, path, err := readExportPackage(makeRequest(t, manifestData, false))
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, bundle) || parsed.Bundle.SHA256 != manifest.Bundle.SHA256 {
			t.Fatalf("parsed bundle: %q, manifest %s, error %v", data, parsed.Bundle.SHA256, err)
		}
	})
	t.Run("rejects tampered checksum and extra files", func(t *testing.T) {
		bad := manifest
		bad.Bundle.SHA256 = string(bytes.Repeat([]byte("0"), 64))
		badManifest, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		if _, path, err := readExportPackage(makeRequest(t, badManifest, false)); err == nil {
			if path != "" {
				_ = os.Remove(path)
			}
			t.Fatal("accepted a tampered bundle checksum")
		}
		if _, path, err := readExportPackage(makeRequest(t, manifestData, true)); err == nil {
			if path != "" {
				_ = os.Remove(path)
			}
			t.Fatal("accepted an extra archive file")
		}
	})
}

func TestRepositoryExportPackageRoundTripsThroughImport(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git portability tests")
	}
	server, application, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()
	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	owner.request("POST", "/auth/register", map[string]string{
		"username": "portability", "email": "portability@example.test", "password": "portability-long-password",
	}, 201, nil)
	var source Repository
	owner.request("POST", "/repos", map[string]any{
		"name": "source", "description": "A portable project", "visibility": "private", "readme": true,
	}, 201, &source)
	main, err := application.git.Resolve(context.Background(), source.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.git.Run(context.Background(), source.ID, nil, "update-ref", "refs/heads/feature", main); err != nil {
		t.Fatal(err)
	}
	if _, err = application.git.Run(context.Background(), source.ID, nil, "update-ref", "refs/tags/v1.0.0", main); err != nil {
		t.Fatal(err)
	}

	response, err := owner.client.Get(server.URL + "/api/v1/repos/portability/source/export/package")
	if err != nil {
		t.Fatal(err)
	}
	packageBytes, err := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("export status %d: %s", response.StatusCode, packageBytes)
	}
	if response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("private package cache policy: %q", response.Header.Get("Cache-Control"))
	}

	var manifest repositoryExportManifest
	bundle := []byte(nil)
	reader := tar.NewReader(bytes.NewReader(packageBytes))
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		switch header.Name {
		case "manifest.json":
			data, readErr := io.ReadAll(reader)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if err = json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
		case "repository.bundle":
			bundle, err = io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unexpected package entry %q", header.Name)
		}
	}
	if manifest.SchemaVersion != 1 || manifest.Format != "gitown.repository-export/v1" ||
		manifest.Repository.Description != "A portable project" || manifest.Repository.DefaultBranch != "main" ||
		manifest.Bundle.SizeBytes != int64(len(bundle)) || len(manifest.Bundle.Refs) != 4 {
		t.Fatalf("incomplete export manifest: %+v refs=%d bundle=%d", manifest, len(manifest.Bundle.Refs), len(bundle))
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("name", "restored")
	_ = writer.WriteField("visibility", "private")
	part, err := writer.CreateFormFile("package", "source-export.tar")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(packageBytes); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/imports/bundle", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Origin", "http://localhost:3000")
	response, err = owner.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	result, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("package import status %d: %s", response.StatusCode, result)
	}
	var restored Repository
	if err = json.Unmarshal(result, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Name != "restored" || restored.Description != source.Description || restored.DefaultBranch != "main" {
		t.Fatalf("restored repository metadata: %+v", restored)
	}
	for _, ref := range manifest.Bundle.Refs {
		var got string
		switch ref.Name {
		case "HEAD":
			got, err = application.git.Resolve(context.Background(), restored.ID, "main")
		case "refs/heads/main", "refs/heads/feature":
			got, err = application.git.Resolve(context.Background(), restored.ID, ref.Name[len("refs/heads/"):])
		case "refs/tags/v1.0.0":
			got, err = application.git.ResolveTag(context.Background(), restored.ID, ref.Name[len("refs/tags/"):])
		default:
			t.Fatalf("unexpected ref in test bundle: %s", ref.Name)
		}
		if err != nil || got != ref.SHA {
			t.Fatalf("restored %s: got %s, err %v, want %s", ref.Name, got, err, ref.SHA)
		}
	}

	// A corrupt upload is rejected before a repository is created.
	badBody := &bytes.Buffer{}
	badWriter := multipart.NewWriter(badBody)
	_ = badWriter.WriteField("name", "corrupt")
	_ = badWriter.WriteField("visibility", "private")
	badPart, _ := badWriter.CreateFormFile("package", "corrupt.tar")
	_, _ = badPart.Write([]byte("not a tar archive"))
	_ = badWriter.Close()
	badRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/imports/bundle", badBody)
	badRequest.Header.Set("Content-Type", badWriter.FormDataContentType())
	badRequest.Header.Set("Origin", "http://localhost:3000")
	badResponse, err := owner.client.Do(badRequest)
	if err != nil {
		t.Fatal(err)
	}
	badResponse.Body.Close()
	if badResponse.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("corrupt package status %d, want 422", badResponse.StatusCode)
	}
	var created int
	if err = application.db.QueryRow(context.Background(), "SELECT count(*) FROM repositories WHERE owner_id=(SELECT id FROM users WHERE username='portability') AND name='corrupt'").Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("corrupt package created %d repositories", created)
	}
}
