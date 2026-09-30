package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/Aaravkhanal/GITOWN/internal/config"
	"github.com/Aaravkhanal/GITOWN/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPhaseCompletion(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "gitown_test_" + strings.ReplaceAll(auth.ID(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	storage := t.TempDir()
	a, err := New(config.Config{DataDir: storage, Origin: "http://localhost:3000", GitURL: "http://localhost/git", Signup: true}, pool)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	jar2, _ := cookiejar.New(nil)
	other := testClient{t, server.URL, &http.Client{Jar: jar2}}
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)
	other.request("POST", "/auth/register", map[string]string{"username": "other", "email": "other@example.test", "password": "other-long-password"}, 201, nil)

	var repo Repository
	owner.request("POST", "/repos", map[string]any{"name": "atlas", "visibility": "public", "readme": true}, 201, &repo)
	var code struct {
		Items  []CodeMatch `json:"items"`
		Ranked bool        `json:"ranked"`
	}
	owner.request("GET", "/search/code?q=Welcome", nil, 200, &code)
	found := false
	for _, item := range code.Items {
		if item.Repository == "atlas" && strings.Contains(item.Snippet, "Welcome") && item.Rank > 0 {
			found = true
		}
	}
	if !code.Ranked || !found {
		t.Fatalf("ranked code search: %+v", code)
	}
	owner.request("PUT", "/repos/owner/atlas/topics", map[string]any{"topics": []string{"beginner-friendly"}}, 200, nil)
	var topics struct {
		Items []struct {
			Topic string `json:"topic"`
		} `json:"items"`
	}
	owner.request("GET", "/topics", nil, 200, &topics)
	if len(topics.Items) != 1 || topics.Items[0].Topic != "beginner-friendly" {
		t.Fatalf("topics: %+v", topics.Items)
	}
	var page struct {
		Items []Repository `json:"items"`
	}
	owner.request("GET", "/topics/beginner-friendly", nil, 200, &page)
	if len(page.Items) != 1 || page.Items[0].Name != "atlas" {
		t.Fatalf("topic page: %+v", page.Items)
	}
	owner.request("POST", "/collections", map[string]string{"slug": "picks", "title": "Picks"}, 201, nil)
	owner.request("PATCH", "/collections/owner/picks", map[string]bool{"featured": true}, 403, nil)
	t.Setenv("GITOWN_OPERATORS", "owner")
	owner.request("PATCH", "/collections/owner/picks", map[string]bool{"featured": true}, 200, nil)
	var featured struct {
		Items []struct {
			Slug     string `json:"slug"`
			Featured bool   `json:"featured"`
		} `json:"items"`
	}
	owner.request("GET", "/collections?featured=1", nil, 200, &featured)
	if len(featured.Items) != 1 || !featured.Items[0].Featured {
		t.Fatalf("featured collections: %+v", featured.Items)
	}
	owner.request("POST", "/repos/owner/atlas/issues", map[string]string{"title": "Help wanted example", "body": "A first task"}, 201, nil)
	var activity struct {
		Total int `json:"total"`
	}
	owner.request("GET", "/users/owner/contributions", nil, 200, &activity)
	if activity.Total < 1 {
		t.Fatalf("contributions: %+v", activity)
	}

	owner.request("POST", "/districts", map[string]string{"slug": "acme", "name": "Acme", "visibility": "public"}, 201, nil)
	owner.request("PATCH", "/districts/acme", map[string]any{"allow_public": false, "allow_outside_collaborators": false}, 200, nil)
	owner.request("POST", "/repos", map[string]any{"name": "open", "visibility": "public", "district": "acme"}, 403, nil)
	owner.request("POST", "/repos", map[string]any{"name": "closed", "visibility": "private", "district": "acme"}, 201, nil)
	owner.request("POST", "/repos/owner/closed/invitations", map[string]string{"username": "other", "role": "read"}, 403, nil)
	var usage struct {
		Repositories int  `json:"repositories"`
		Charges      bool `json:"charges"`
	}
	owner.request("GET", "/districts/acme/usage", nil, 200, &usage)
	if usage.Repositories != 1 || usage.Charges {
		t.Fatalf("usage: %+v", usage)
	}
	var invoice struct {
		ID string `json:"id"`
	}
	owner.request("POST", "/districts/acme/invoices", map[string]any{"period": "2026-09", "amount_cents": 500}, 201, &invoice)
	owner.request("POST", "/districts/acme/invoices/"+invoice.ID+"/pay", map[string]any{}, 200, nil)
	exportReq, err := http.NewRequest("GET", server.URL+"/api/v1/districts/acme/audit?download=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	exportReq.Header.Set("Origin", "http://localhost:3000")
	exportRes, err := owner.client.Do(exportReq)
	if err != nil {
		t.Fatal(err)
	}
	exportBody, _ := io.ReadAll(exportRes.Body)
	exportRes.Body.Close()
	if exportRes.StatusCode != 200 || !strings.Contains(string(exportBody), "district.created") || !strings.Contains(exportRes.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("audit export: %d %s", exportRes.StatusCode, exportBody)
	}

	writeToken := owner.token("repo:write")
	lockBody := `{"path":"assets/demo.bin"}`
	lockAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("owner:"+writeToken))
	lockReq, err := http.NewRequest("POST", server.URL+"/git/owner/atlas.git/info/lfs/locks", strings.NewReader(lockBody))
	if err != nil {
		t.Fatal(err)
	}
	lockReq.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	lockReq.Header.Set("Authorization", lockAuth)
	lockRes, err := http.DefaultClient.Do(lockReq)
	if err != nil {
		t.Fatal(err)
	}
	lockPayload, _ := io.ReadAll(lockRes.Body)
	lockRes.Body.Close()
	if lockRes.StatusCode != 201 || !strings.Contains(string(lockPayload), "assets/demo.bin") {
		t.Fatalf("lfs lock: %d %s", lockRes.StatusCode, lockPayload)
	}
	againReq, err := http.NewRequest("POST", server.URL+"/git/owner/atlas.git/info/lfs/locks", strings.NewReader(lockBody))
	if err != nil {
		t.Fatal(err)
	}
	againReq.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	againReq.Header.Set("Authorization", lockAuth)
	againRes, err := http.DefaultClient.Do(againReq)
	if err != nil {
		t.Fatal(err)
	}
	againBody, _ := io.ReadAll(againRes.Body)
	againRes.Body.Close()
	if againRes.StatusCode != 409 {
		t.Fatalf("second lfs lock: %d %s", againRes.StatusCode, againBody)
	}

	keyDir := t.TempDir()
	run(t, "", "ssh-keygen", "-q", "-t", "ed25519", "-f", filepath.Join(keyDir, "id"), "-N", "", "-C", "gitown")
	publicKey, err := os.ReadFile(filepath.Join(keyDir, "id.pub"))
	if err != nil {
		t.Fatal(err)
	}
	var sshKey struct {
		Fingerprint string `json:"fingerprint"`
	}
	owner.request("POST", "/user/ssh-keys", map[string]string{"title": "laptop", "public_key": string(publicKey)}, 201, &sshKey)
	owner.request("POST", "/user/signing-keys", map[string]string{"title": "release", "public_key": string(publicKey)}, 201, nil)
	provenance := "signed by owner"
	messagePath := filepath.Join(keyDir, "message")
	if err = os.WriteFile(messagePath, []byte(provenance), 0600); err != nil {
		t.Fatal(err)
	}
	run(t, "", "ssh-keygen", "-Y", "sign", "-f", filepath.Join(keyDir, "id"), "-n", "git", messagePath)
	signature, err := os.ReadFile(messagePath + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	var drop struct {
		ProvenanceVerified bool `json:"provenance_verified"`
	}
	owner.request("POST", "/repos/owner/atlas/drops", map[string]any{"tag": "v9.0.0", "title": "Signed", "provenance": provenance, "signature": string(signature), "create_tag": true}, 201, &drop)
	if !drop.ProvenanceVerified {
		t.Fatal("drop signature was not verified")
	}

	packageToken := owner.token("package:write")
	payload := []byte("finish-kit")
	document := map[string]any{
		"name":      "finish-kit",
		"dist-tags": map[string]string{"latest": "1.0.0"},
		"versions":  map[string]any{"1.0.0": map[string]string{"name": "finish-kit", "version": "1.0.0", "description": "demo"}},
		"_attachments": map[string]any{
			"finish-kit-1.0.0.tgz": map[string]any{"data": base64.StdEncoding.EncodeToString(payload)},
		},
	}
	rawDocument, _ := json.Marshal(document)
	npmReq, err := http.NewRequest("PUT", server.URL+"/npm/finish-kit", bytes.NewReader(rawDocument))
	if err != nil {
		t.Fatal(err)
	}
	npmReq.Header.Set("Content-Type", "application/json")
	npmReq.Header.Set("Authorization", "Bearer "+packageToken)
	npmRes, err := http.DefaultClient.Do(npmReq)
	if err != nil {
		t.Fatal(err)
	}
	npmBody, _ := io.ReadAll(npmRes.Body)
	npmRes.Body.Close()
	if npmRes.StatusCode != 201 {
		t.Fatalf("npm publish: %d %s", npmRes.StatusCode, npmBody)
	}
	getReq, err := http.NewRequest("GET", server.URL+"/npm/finish-kit/-/finish-kit-1.0.0.tgz", nil)
	if err != nil {
		t.Fatal(err)
	}
	getRes, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(getRes.Body)
	getRes.Body.Close()
	if getRes.StatusCode != 200 || !bytes.Equal(got, payload) {
		t.Fatalf("npm tarball: %d %q", getRes.StatusCode, got)
	}
	bad := map[string]any{
		"dist-tags":    map[string]string{"latest": "1.0.1"},
		"versions":     map[string]any{"1.0.1": map[string]string{"version": "1.0.1"}},
		"_attachments": map[string]any{"x.tgz": map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("curl | sh"))}},
	}
	badRaw, _ := json.Marshal(bad)
	badReq, err := http.NewRequest("PUT", server.URL+"/npm/finish-kit", bytes.NewReader(badRaw))
	if err != nil {
		t.Fatal(err)
	}
	badReq.Header.Set("Content-Type", "application/json")
	badReq.Header.Set("Authorization", "Bearer "+packageToken)
	badRes, err := http.DefaultClient.Do(badReq)
	if err != nil {
		t.Fatal(err)
	}
	badRes.Body.Close()
	if badRes.StatusCode != 422 {
		t.Fatalf("dangerous package status %d", badRes.StatusCode)
	}

	blob := []byte("oci-layer")
	sumBytes := sha256.Sum256(blob)
	sum := hex.EncodeToString(sumBytes[:])
	startReq, err := http.NewRequest("POST", server.URL+"/v2/demo/blobs/uploads/", nil)
	if err != nil {
		t.Fatal(err)
	}
	startReq.Header.Set("Authorization", "Bearer "+packageToken)
	startRes, err := http.DefaultClient.Do(startReq)
	if err != nil {
		t.Fatal(err)
	}
	startRes.Body.Close()
	if startRes.StatusCode != 202 || startRes.Header.Get("Location") == "" {
		t.Fatalf("oci upload start %d", startRes.StatusCode)
	}
	putBlob, err := http.NewRequest("PUT", server.URL+startRes.Header.Get("Location")+"?digest=sha256:"+sum, bytes.NewReader(blob))
	if err != nil {
		t.Fatal(err)
	}
	putBlob.Header.Set("Authorization", "Bearer "+packageToken)
	putBlobRes, err := http.DefaultClient.Do(putBlob)
	if err != nil {
		t.Fatal(err)
	}
	putBlobRes.Body.Close()
	if putBlobRes.StatusCode != 201 {
		t.Fatalf("oci blob %d", putBlobRes.StatusCode)
	}
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	manifestReq, err := http.NewRequest("PUT", server.URL+"/v2/demo/manifests/latest", bytes.NewReader(manifest))
	if err != nil {
		t.Fatal(err)
	}
	manifestReq.Header.Set("Authorization", "Bearer "+packageToken)
	manifestReq.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	manifestRes, err := http.DefaultClient.Do(manifestReq)
	if err != nil {
		t.Fatal(err)
	}
	manifestRes.Body.Close()
	if manifestRes.StatusCode != 201 || !strings.HasPrefix(manifestRes.Header.Get("Docker-Content-Digest"), "sha256:") {
		t.Fatalf("oci manifest %d %s", manifestRes.StatusCode, manifestRes.Header.Get("Docker-Content-Digest"))
	}
	pullReq, err := http.NewRequest("GET", server.URL+"/v2/demo/manifests/latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	pullReq.Header.Set("Authorization", "Bearer "+packageToken)
	pullRes, err := http.DefaultClient.Do(pullReq)
	if err != nil {
		t.Fatal(err)
	}
	pulled, _ := io.ReadAll(pullRes.Body)
	pullRes.Body.Close()
	if pullRes.StatusCode != 200 || !bytes.Equal(pulled, manifest) {
		t.Fatalf("oci pull: %d %s", pullRes.StatusCode, pulled)
	}

	sessionCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	sshErr := a.SSHSession(sessionCtx, sshKey.Fingerprint, "git-upload-pack 'owner/atlas.git'", bytes.NewReader(nil), &stdout, &stderr)
	if !strings.Contains(stdout.String(), "refs/heads/main") {
		t.Fatalf("ssh upload-pack advertisement: %v %s %s", sshErr, stdout.String(), stderr.String())
	}
	head, err := a.git.Run(ctx, repo.ID, nil, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(head))
	zero := strings.Repeat("0", 40)
	if _, code, cmdErr := a.git.Command(ctx, 20*time.Second, repo.ID, "update-ref", "refs/heads/rollback-test", sha, zero); cmdErr != nil || code != 0 {
		t.Fatalf("temporary ref: %v %d", cmdErr, code)
	}
	a.rollbackRefs(ctx, repo.ID, []refUpdate{{Old: zero, New: sha, Ref: "refs/heads/rollback-test"}})
	if _, code, _ := a.git.Command(ctx, 20*time.Second, repo.ID, "rev-parse", "--verify", "--end-of-options", "refs/heads/rollback-test"); code == 0 {
		t.Fatal("quota rollback left the temporary ref in place")
	}
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v %s", name, strings.Join(args, " "), err, out)
	}
}
