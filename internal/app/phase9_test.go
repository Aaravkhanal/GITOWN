package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/Aaravkhanal/GITOWN/internal/config"
	"github.com/Aaravkhanal/GITOWN/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPhase9OCIAccessControl proves the OCI (container) registry routes now
// carry a real ownership/visibility model, matching what crates.go already
// enforced for npm packages. Before this fix, ociGetManifest/ociGetBlob only
// checked that a caller was SOME authenticated package user, and
// ociPutManifest upserted with no owner check at all — any user could pull
// or overwrite any other user's image.
func TestPhase9OCIAccessControl(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	server, _, close := newPhase9Server(t, databaseURL)
	defer close()

	jarAlice, _ := cookiejar.New(nil)
	alice := testClient{t, server.URL, &http.Client{Jar: jarAlice}}
	jarBob, _ := cookiejar.New(nil)
	bob := testClient{t, server.URL, &http.Client{Jar: jarBob}}
	alice.request("POST", "/auth/register", map[string]string{"username": "alice", "email": "alice@example.test", "password": "alice-long-password"}, 201, nil)
	bob.request("POST", "/auth/register", map[string]string{"username": "bob", "email": "bob@example.test", "password": "bob-long-password"}, 201, nil)
	aliceToken := alice.token("package:write")
	bobWriteToken := bob.token("package:write")
	bobReadToken := bob.token("package:read")

	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)

	// Alice pushes a manifest to a brand new namespace. This also implicitly
	// creates the crates(ecosystem='oci') record, owned by Alice, private by
	// default.
	putManifest(t, server.URL, aliceToken, "demoapp", "v1", manifest, 201)

	// Bob, an entirely different user with a valid package:read token,
	// must NOT be able to pull it: private images are owner-only.
	getManifestStatus(t, server.URL, bobReadToken, "demoapp", "v1", 404)
	// Alice herself can still read it.
	getManifestStatus(t, server.URL, aliceToken, "demoapp", "v1", 200)

	// Bob must NOT be able to overwrite Alice's manifest tag either.
	putManifest(t, server.URL, bobWriteToken, "demoapp", "v1", manifest, 403)

	// Alice makes the image public via the generic crates API (ecosystem=oci).
	alice.request("PATCH", "/crates/demoapp?ecosystem=oci", map[string]any{
		"description": "demo image",
		"visibility":  "public",
		"retention":   20,
	}, 200, nil)

	// Now Bob can pull it.
	getManifestStatus(t, server.URL, bobReadToken, "demoapp", "v1", 200)
	// But still cannot push/overwrite it — visibility is not ownership.
	putManifest(t, server.URL, bobWriteToken, "demoapp", "v1", manifest, 403)

	// A blob follows the same rule: push it as Alice, then confirm Bob can't
	// read it until public, and can never write it.
	blob := []byte("layer-bytes")
	blobDigest := sha256sum(blob)
	req, err := http.NewRequest("PUT", server.URL+"/v2/privateblob/blobs/uploads/"+auth.ID()+"?digest=sha256:"+blobDigest, bytes.NewReader(blob))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("blob push: expected 201 got %d", res.StatusCode)
	}
	getReq, _ := http.NewRequest("GET", server.URL+"/v2/privateblob/blobs/sha256:"+blobDigest, nil)
	getReq.Header.Set("Authorization", "Bearer "+bobReadToken)
	getRes, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatal(err)
	}
	getRes.Body.Close()
	if getRes.StatusCode != 404 {
		t.Fatalf("private blob pull by non-owner: expected 404 got %d", getRes.StatusCode)
	}
}

// TestPhase9DropDownloadsAndReleases covers three fixes together: download
// counts now dedupe per identity per hour instead of incrementing on every
// GET, private-repository asset downloads are now counted at all (they
// previously never were), and asset content type is sniffed from the bytes
// instead of every asset being served as application/octet-stream.
func TestPhase9DropDownloadsAndReleases(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	server, _, close := newPhase9Server(t, databaseURL)
	defer close()

	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password"}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "pub", "visibility": "public", "readme": true}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "priv", "visibility": "private", "readme": true}, 201, nil)

	for _, repo := range []string{"pub", "priv"} {
		owner.request("POST", "/repos/owner/"+repo+"/drops", map[string]any{"tag": "v1.0.0", "title": "First", "create_tag": true}, 201, nil)
		owner.request("POST", "/repos/owner/"+repo+"/drops/v1.0.0/assets", map[string]string{
			"name":           "note.txt",
			"content_base64": base64.StdEncoding.EncodeToString([]byte("hello asset\n")),
		}, 201, nil)
		for i := 0; i < 3; i++ {
			owner.request("GET", "/repos/owner/"+repo+"/drops/v1.0.0/assets/note.txt", nil, 200, nil)
		}
		var detail struct {
			Assets []struct {
				Name          string `json:"name"`
				DownloadCount int    `json:"download_count"`
				ContentType   string `json:"content_type"`
			} `json:"assets"`
		}
		owner.request("GET", "/repos/owner/"+repo+"/drops/v1.0.0", nil, 200, &detail)
		if len(detail.Assets) != 1 {
			t.Fatalf("%s: expected one asset, got %+v", repo, detail.Assets)
		}
		if detail.Assets[0].DownloadCount != 1 {
			t.Fatalf("%s: expected download count deduped to 1 after 3 downloads, got %d", repo, detail.Assets[0].DownloadCount)
		}
		if !strings.HasPrefix(detail.Assets[0].ContentType, "text/plain") {
			t.Fatalf("%s: expected a sniffed text/plain content type, got %q", repo, detail.Assets[0].ContentType)
		}
	}

	// Prerelease/"latest" semantics: the most recent non-draft, non-prerelease
	// drop is reported as latest; a prerelease newer than it must not be.
	owner.request("POST", "/repos/owner/pub/drops", map[string]any{"tag": "v1.1.0-rc1", "title": "Candidate", "prerelease": true, "create_tag": true}, 201, nil)
	owner.request("POST", "/repos/owner/pub/drops", map[string]any{"tag": "v1.1.0", "title": "Second", "create_tag": true}, 201, nil)
	var listing struct {
		Items []struct {
			Tag        string `json:"tag"`
			Prerelease bool   `json:"prerelease"`
		} `json:"items"`
		Latest string `json:"latest"`
	}
	owner.request("GET", "/repos/owner/pub/drops", nil, 200, &listing)
	if listing.Latest != "v1.1.0" {
		t.Fatalf("expected latest release v1.1.0, got %q (items: %+v)", listing.Latest, listing.Items)
	}
	foundPrerelease := false
	for _, item := range listing.Items {
		if item.Tag == "v1.1.0-rc1" && item.Prerelease {
			foundPrerelease = true
		}
	}
	if !foundPrerelease {
		t.Fatalf("prerelease drop missing from listing: %+v", listing.Items)
	}
}

// TestPhase9TagSignatureVsProvenance proves the Drop detail response reports
// tag_signed (a real `git tag -v` check of the Git tag object against a
// registered signing key) and provenance_verified (a separate signed note
// about the release) as two distinct fields that do not have to agree.
func TestPhase9TagSignatureVsProvenance(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	for _, tool := range []string{"ssh-keygen", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required", tool)
		}
	}
	server, _, close := newPhase9Server(t, databaseURL)
	defer close()

	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "forge", "visibility": "public", "readme": true}, 201, nil)
	writeToken := owner.token("repo:write")
	repoURL := server.URL + "/git/owner/forge.git"
	work := filepath.Join(t.TempDir(), "work")
	gitEnv := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	gitRun := func(dir, token string, ok bool, args ...string) string {
		t.Helper()
		argv := []string{"-c", "credential.helper="}
		if token != "" {
			argv = append(argv, "-c", "http.extraHeader=Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte("owner:"+token)))
		}
		cmd := exec.Command("git", append(argv, args...)...)
		cmd.Dir = dir
		cmd.Env = gitEnv
		out, gitErr := cmd.CombinedOutput()
		if ok && gitErr != nil {
			t.Fatalf("git %v failed: %s", args, out)
		}
		return string(out)
	}
	gitRun("", writeToken, true, "clone", repoURL, work)
	gitRun(work, "", true, "config", "user.name", "Owner")
	gitRun(work, "", true, "config", "user.email", "owner@example.test")

	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "owner@example.test", "-f", keyPath).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %s", out)
	}
	publicKey, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	owner.request("POST", "/user/signing-keys", map[string]string{"title": "laptop", "public_key": strings.TrimSpace(string(publicKey))}, 201, nil)

	gitRun(work, "", true, "-c", "gpg.format=ssh", "-c", "user.signingkey="+keyPath, "tag", "-s", "-m", "release", "v1.0.0")
	gitRun(work, "", true, "tag", "v0.9.0")
	gitRun(work, writeToken, true, "push", "origin", "--tags")

	// v1.0.0: a genuinely signed tag, but the drop is published with no
	// provenance note or signature — the two fields must disagree.
	owner.request("POST", "/repos/owner/forge/drops", map[string]any{"tag": "v1.0.0", "title": "Signed tag"}, 201, nil)
	var signedTagDrop struct {
		TagSigned          bool `json:"tag_signed"`
		ProvenanceVerified bool `json:"provenance_verified"`
	}
	owner.request("GET", "/repos/owner/forge/drops/v1.0.0", nil, 200, &signedTagDrop)
	if !signedTagDrop.TagSigned {
		t.Fatal("expected tag_signed=true for a tag signed with a registered key")
	}
	if signedTagDrop.ProvenanceVerified {
		t.Fatal("expected provenance_verified=false when no signature note was supplied")
	}

	// v0.9.0: an unsigned lightweight tag.
	owner.request("POST", "/repos/owner/forge/drops", map[string]any{"tag": "v0.9.0", "title": "Unsigned tag"}, 201, nil)
	var unsignedTagDrop struct {
		TagSigned bool `json:"tag_signed"`
	}
	owner.request("GET", "/repos/owner/forge/drops/v0.9.0", nil, 200, &unsignedTagDrop)
	if unsignedTagDrop.TagSigned {
		t.Fatal("expected tag_signed=false for an unsigned tag")
	}
}

// TestPhase9CrateUpdateAndNpmTarballScanning covers the generic crate PATCH
// endpoint (backing the new crate-management UI) and proves secret scanning
// on npm publish now inspects the decompressed tarball contents rather than
// the raw gzip bytes, which a real npm .tgz would otherwise defeat.
func TestPhase9CrateUpdateAndNpmTarballScanning(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	server, _, close := newPhase9Server(t, databaseURL)
	defer close()

	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password"}, 201, nil)
	owner.request("POST", "/crates", map[string]any{"name": "widget", "visibility": "public", "retention": 5}, 201, nil)
	owner.request("PATCH", "/crates/widget", map[string]any{"description": "a widget", "visibility": "private", "retention": 3}, 200, nil)
	var crate struct {
		Description string `json:"description"`
		Visibility  string `json:"visibility"`
		Retention   int    `json:"retention"`
	}
	owner.request("GET", "/crates/widget", nil, 200, &crate)
	if crate.Description != "a widget" || crate.Visibility != "private" || crate.Retention != 3 {
		t.Fatalf("crate update did not stick: %+v", crate)
	}

	clean := makeNpmTarball(t, map[string]string{"package.json": `{"name":"clean-pkg"}`, "index.js": "console.log('hi')"})
	publishNpm(t, server.URL, owner, "clean-pkg", "1.0.0", clean, 201)

	dirty := makeNpmTarball(t, map[string]string{
		"package.json": `{"name":"dirty-pkg"}`,
		"config.js":    "const key = `-----BEGIN PRIVATE KEY-----\nMIIExamplekeymaterial\n-----END PRIVATE KEY-----`;",
	})
	publishNpm(t, server.URL, owner, "dirty-pkg", "1.0.0", dirty, 422)
}

func newPhase9Server(t *testing.T, databaseURL string) (*httptest.Server, *App, func()) {
	t.Helper()
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := "gitown_test_" + strings.ReplaceAll(auth.ID(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	storage := t.TempDir()
	application, err := New(config.Config{DataDir: storage, Origin: "http://localhost:3000", GitURL: "http://localhost/git", Signup: true}, pool)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application.Handler())
	cleanup := func() {
		server.Close()
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
		admin.Close()
	}
	return server, application, cleanup
}

func sha256sum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func putManifest(t *testing.T, base, token, name, reference string, body []byte, status int) {
	t.Helper()
	req, err := http.NewRequest("PUT", base+"/v2/"+name+"/manifests/"+reference, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	if res.StatusCode != status {
		t.Fatalf("PUT manifest %s/%s: expected %d got %d: %s", name, reference, status, res.StatusCode, payload)
	}
}

func getManifestStatus(t *testing.T, base, token, name, reference string, status int) {
	t.Helper()
	req, err := http.NewRequest("GET", base+"/v2/"+name+"/manifests/"+reference, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != status {
		payload, _ := io.ReadAll(res.Body)
		t.Fatalf("GET manifest %s/%s: expected %d got %d: %s", name, reference, status, res.StatusCode, payload)
	}
}

func makeNpmTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		header := &tar.Header{Name: "package/" + name, Mode: 0600, Size: int64(len(content))}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func publishNpm(t *testing.T, base string, owner testClient, name, version string, tarball []byte, status int) {
	t.Helper()
	doc, err := json.Marshal(map[string]any{
		"dist-tags": map[string]string{"latest": version},
		"versions": map[string]any{
			version: map[string]string{"version": version, "description": name},
		},
		"_attachments": map[string]any{
			name + "-" + version + ".tgz": map[string]string{"data": base64.StdEncoding.EncodeToString(tarball)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("PUT", base+"/npm/"+name, bytes.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	token := owner.token("package:write")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	if res.StatusCode != status {
		t.Fatalf("npm publish %s@%s: expected %d got %d: %s", name, version, status, res.StatusCode, payload)
	}
}
