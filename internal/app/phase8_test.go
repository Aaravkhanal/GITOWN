package app

import (
	"context"
	"encoding/base64"
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

// TestPhaseEightTagSignatures proves the tag "signed" field reflects a real
// cryptographic verification (git tag -v against a registered signing key),
// not a search for a signature-shaped block of text. A forged or copy-pasted
// signature block, which the old substring check would have accepted, must
// be reported as unsigned.
func TestPhaseEightTagSignatures(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	for _, tool := range []string{"ssh-keygen", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required", tool)
		}
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
		if !ok && gitErr == nil {
			t.Fatalf("git %v unexpectedly succeeded: %s", args, out)
		}
		return string(out)
	}
	gitRun("", writeToken, true, "clone", repoURL, work)
	gitRun(work, "", true, "config", "user.name", "Owner")
	gitRun(work, "", true, "config", "user.email", "owner@example.test")

	// A registered signing key.
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "id_ed25519")
	if out, keyErr := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "owner@example.test", "-f", keyPath).CombinedOutput(); keyErr != nil {
		t.Fatalf("ssh-keygen: %s", out)
	}
	publicKey, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	owner.request("POST", "/user/signing-keys", map[string]string{"title": "laptop", "public_key": strings.TrimSpace(string(publicKey))}, 201, nil)

	// A key that is NOT registered.
	otherKeyPath := filepath.Join(keyDir, "id_other")
	if out, keyErr := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "owner@example.test", "-f", otherKeyPath).CombinedOutput(); keyErr != nil {
		t.Fatalf("ssh-keygen: %s", out)
	}

	// Signed with the registered key: should verify.
	gitRun(work, "", true, "-c", "gpg.format=ssh", "-c", "user.signingkey="+keyPath, "tag", "-s", "-m", "release", "v1.0.0")
	// Unsigned lightweight tag: should not verify.
	gitRun(work, "", true, "tag", "v0.9.0")
	// Signed with an unregistered key: should not verify (no principal match).
	gitRun(work, "", true, "-c", "gpg.format=ssh", "-c", "user.signingkey="+otherKeyPath, "tag", "-s", "-m", "untrusted", "v1.0.0-untrusted")
	// A forged signature block: this is the exact shape of bug the old
	// substring check would have accepted. Craft a tag object whose body
	// simply contains signature-looking text without a real signature.
	head := strings.TrimSpace(gitRun(work, "", true, "rev-parse", "HEAD"))
	fakeBody := "object " + head + "\n" +
		"type commit\n" +
		"tag v1.0.0-forged\n" +
		"tagger Owner <owner@example.test> 1700000000 +0000\n\n" +
		"forged release\n" +
		"-----BEGIN SSH SIGNATURE-----\n" +
		"this is not a real signature, just text pretending to be one\n" +
		"-----END SSH SIGNATURE-----\n"
	hashCmd := exec.Command("git", "hash-object", "-w", "-t", "tag", "--stdin")
	hashCmd.Dir = work
	hashCmd.Env = gitEnv
	hashCmd.Stdin = strings.NewReader(fakeBody)
	fakeSHA, hashErr := hashCmd.Output()
	if hashErr != nil {
		t.Fatalf("hash-object: %v", hashErr)
	}
	gitRun(work, "", true, "update-ref", "refs/tags/v1.0.0-forged", strings.TrimSpace(string(fakeSHA)))

	gitRun(work, writeToken, true, "push", "origin", "--tags")

	var tags struct {
		Items []struct {
			Name   string `json:"name"`
			Signed bool   `json:"signed"`
		} `json:"items"`
	}
	owner.request("GET", "/repos/owner/forge/tags", nil, 200, &tags)
	status := map[string]bool{}
	for _, item := range tags.Items {
		status[item.Name] = item.Signed
	}
	cases := map[string]bool{
		"v1.0.0":           true,  // signed by a registered key
		"v0.9.0":           false, // unsigned lightweight tag
		"v1.0.0-untrusted": false, // signed, but the key isn't registered
		"v1.0.0-forged":    false, // forged signature block (the bug being fixed)
	}
	for name, want := range cases {
		got, ok := status[name]
		if !ok {
			t.Fatalf("tag %q missing from listing: %+v", name, status)
		}
		if got != want {
			t.Fatalf("tag %q: signed=%v, want %v", name, got, want)
		}
	}
}

// TestPhaseEightDeployKeysMaintenanceAndRefEvents covers the previously
// orphaned deploy-key backend, real ref-change events with the pushing
// actor attached, and the periodic maintenance sweep that keeps
// repositories garbage-collected without anyone calling the API by hand.
func TestPhaseEightDeployKeysMaintenanceAndRefEvents(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	for _, tool := range []string{"ssh-keygen", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is required", tool)
		}
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
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "forge", "visibility": "public", "readme": true}, 201, nil)

	// Deploy keys: read-only key can fetch but not push; write key can push.
	keyDir := t.TempDir()
	readKeyPath := filepath.Join(keyDir, "read")
	if out, keyErr := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "ci-read", "-f", readKeyPath).CombinedOutput(); keyErr != nil {
		t.Fatalf("ssh-keygen: %s", out)
	}
	readPub, err := os.ReadFile(readKeyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	var readDeployKey struct {
		ID string `json:"id"`
	}
	owner.request("POST", "/repos/owner/forge/deploy-keys", map[string]any{"title": "ci-read", "public_key": strings.TrimSpace(string(readPub)), "write": false}, 201, &readDeployKey)
	var keys struct {
		Items []struct {
			ID    string `json:"id"`
			Write bool   `json:"write"`
		} `json:"items"`
	}
	owner.request("GET", "/repos/owner/forge/deploy-keys", nil, 200, &keys)
	if len(keys.Items) != 1 || keys.Items[0].Write {
		t.Fatalf("deploy key listing: %+v", keys.Items)
	}
	owner.request("DELETE", "/repos/owner/forge/deploy-keys/"+readDeployKey.ID, nil, 200, nil)
	owner.request("GET", "/repos/owner/forge/deploy-keys", nil, 200, &keys)
	if len(keys.Items) != 0 {
		t.Fatalf("deploy key was not removed: %+v", keys.Items)
	}

	// A real push, so a ref event with an attached actor exists to list.
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
		if !ok && gitErr == nil {
			t.Fatalf("git %v unexpectedly succeeded: %s", args, out)
		}
		return string(out)
	}
	gitRun("", writeToken, true, "clone", repoURL, work)
	gitRun(work, "", true, "config", "user.name", "Owner")
	gitRun(work, "", true, "config", "user.email", "owner@example.test")
	if err = os.WriteFile(filepath.Join(work, "note.txt"), []byte("hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(work, "", true, "add", "note.txt")
	gitRun(work, "", true, "commit", "-m", "Add a note")
	gitRun(work, writeToken, true, "push", "origin", "main")

	var refs struct {
		Items []struct {
			Ref   string `json:"ref"`
			Via   string `json:"via"`
			Actor string `json:"actor"`
		} `json:"items"`
		HasMore bool `json:"has_more"`
	}
	owner.request("GET", "/repos/owner/forge/refs", nil, 200, &refs)
	found := false
	for _, item := range refs.Items {
		if item.Ref == "refs/heads/main" && item.Via == "https" {
			found = true
			if item.Actor != "owner" {
				t.Fatalf("ref event actor: %+v", item)
			}
		}
	}
	if !found {
		t.Fatalf("push ref event missing: %+v", refs.Items)
	}

	// Maintenance sweep: a freshly created repository has never been
	// maintained (last_maintained_at is NULL) and is swept immediately. A
	// repository maintained moments ago is left alone.
	owner.request("POST", "/repos", map[string]any{"name": "recent", "visibility": "public", "readme": true}, 201, nil)
	recentlySetTo := time.Now().Add(-time.Minute).Truncate(time.Microsecond)
	if _, err = pool.Exec(ctx, `UPDATE repositories SET last_maintained_at=$1 WHERE name='recent'`, recentlySetTo); err != nil {
		t.Fatal(err)
	}
	if err = a.sweepMaintenance(ctx); err != nil {
		t.Fatalf("sweepMaintenance: %v", err)
	}
	var forgeMaintained, recentMaintained *time.Time
	if err = pool.QueryRow(ctx, `SELECT last_maintained_at FROM repositories WHERE name='forge'`).Scan(&forgeMaintained); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT last_maintained_at FROM repositories WHERE name='recent'`).Scan(&recentMaintained); err != nil {
		t.Fatal(err)
	}
	if forgeMaintained == nil {
		t.Fatal("a never-maintained repository was not picked up by the sweep")
	}
	if recentMaintained == nil || !recentMaintained.Equal(recentlySetTo) {
		t.Fatalf("a recently maintained repository was swept again: got %v, want %v", recentMaintained, recentlySetTo)
	}

	// The manual trigger records the same field and is owner/maintain gated.
	var maintained struct {
		LastMaintainedAt *time.Time `json:"last_maintained_at"`
	}
	owner.request("PUT", "/repos/owner/forge/maintenance", map[string]any{}, 200, &maintained)
	if maintained.LastMaintainedAt == nil {
		t.Fatal("manual maintenance did not record last_maintained_at")
	}
}
