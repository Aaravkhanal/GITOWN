package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

func TestParseGitSSHCommand(t *testing.T) {
	service, owner, name, err := ParseGitSSHCommand("git-upload-pack 'owner/my.repo.git'")
	if err != nil || service != "git-upload-pack" || owner != "owner" || name != "my.repo" {
		t.Fatalf("quoted command: %s %s %s %v", service, owner, name, err)
	}
	service, owner, name, err = ParseGitSSHCommand("git-receive-pack /owner/my.repo.git")
	if err != nil || service != "git-receive-pack" || owner != "owner" || name != "my.repo" {
		t.Fatalf("absolute command: %s %s %s %v", service, owner, name, err)
	}
	if _, _, _, err = ParseGitSSHCommand("bash -c id"); err == nil {
		t.Fatal("shell command was accepted")
	}
	if _, _, _, err = ParseGitSSHCommand("git-upload-pack owner/name;id"); err == nil {
		t.Fatal("metacharacter command was accepted")
	}
	if _, _, _, err = ParseGitSSHCommand("git-upload-pack owner/sub/name.git"); err == nil {
		t.Fatal("nested path was accepted")
	}
}

func TestParseSSHPublicKey(t *testing.T) {
	fingerprint, err := parseSSHPublicKey(syntheticPublicKey(7))
	if err != nil || !strings.HasPrefix(fingerprint, "SHA256:") {
		t.Fatalf("fingerprint: %s %v", fingerprint, err)
	}
	if _, err = parseSSHPublicKey("ssh-dss AAAA comment"); err == nil {
		t.Fatal("unsupported algorithm was accepted")
	}
}

func syntheticPublicKey(seed byte) string {
	var buf bytes.Buffer
	write := func(value []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(value)))
		buf.Write(n[:])
		buf.Write(value)
	}
	write([]byte("ssh-ed25519"))
	write(bytes.Repeat([]byte{seed}, 32))
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(buf.Bytes()) + " test"
}

func (c testClient) bearer(token, method, path string, body any, status int, out any) {
	c.t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(method, c.url+"/api/v1"+path, bytes.NewReader(raw))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	payload, _ := io.ReadAll(res.Body)
	if res.StatusCode != status {
		c.t.Fatalf("%s %s: expected %d got %d: %s", method, path, status, res.StatusCode, payload)
	}
	if out != nil {
		if err = json.Unmarshal(payload, out); err != nil {
			c.t.Fatal(err)
		}
	}
}

func TestPhaseSixToNine(t *testing.T) {
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
	application, err := New(config.Config{DataDir: storage, Origin: "http://localhost:3000", GitURL: "http://localhost/git", Signup: true}, pool)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application.Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	otherJar, _ := cookiejar.New(nil)
	other := testClient{t, server.URL, &http.Client{Jar: otherJar}}
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)
	other.request("POST", "/auth/register", map[string]string{"username": "other", "email": "other@example.test", "password": "other-long-password", "display_name": "Other"}, 201, nil)

	var district struct {
		Slug string `json:"slug"`
		Role string `json:"role"`
	}
	owner.request("POST", "/districts", map[string]string{"slug": "acme", "name": "Acme", "visibility": "public"}, 201, &district)
	if district.Slug != "acme" || district.Role != "owner" {
		t.Fatalf("district: %+v", district)
	}
	other.request("GET", "/districts/acme", nil, 200, nil)
	owner.request("POST", "/districts", map[string]string{"slug": "quiet", "name": "Quiet", "visibility": "private"}, 201, nil)
	other.request("GET", "/districts/quiet", nil, 404, nil)

	var created Repository
	owner.request("POST", "/repos", map[string]any{"name": "atlas", "visibility": "public", "readme": true, "district": "acme"}, 201, &created)
	if created.Language != "Markdown" || created.District != "acme" {
		t.Fatalf("repository facts: %+v", created)
	}
	owner.request("POST", "/repos", map[string]any{"name": "vault", "visibility": "internal", "readme": true, "district": "acme"}, 201, nil)
	other.request("GET", "/repos/owner/vault", nil, 404, nil)
	owner.request("POST", "/districts/acme/members", map[string]string{"username": "other", "role": "member"}, 200, nil)
	other.request("POST", "/repos", map[string]any{"name": "nope", "visibility": "public", "district": "acme"}, 403, nil)
	var view struct {
		Repository Repository `json:"repository"`
	}
	other.request("GET", "/repos/owner/vault", nil, 200, &view)
	if view.Repository.CanManage || view.Repository.Role != "read" {
		t.Fatalf("member access: %+v", view.Repository)
	}
	owner.request("POST", "/districts/acme/members", map[string]string{"username": "other", "role": "admin"}, 200, nil)
	other.request("GET", "/repos/owner/vault", nil, 200, &view)
	if !view.Repository.CanManage {
		t.Fatalf("district admin cannot manage: %+v", view.Repository)
	}
	owner.request("POST", "/districts/acme/crews", map[string]string{"slug": "reviewers", "name": "Reviewers"}, 201, nil)
	owner.request("POST", "/districts/acme/crews/reviewers/members", map[string]string{"username": "other"}, 200, nil)
	var audit struct {
		Items []struct {
			Action string `json:"action"`
		} `json:"items"`
	}
	owner.request("GET", "/districts/acme/audit", nil, 200, &audit)
	if len(audit.Items) == 0 {
		t.Fatal("district audit export was empty")
	}
	owner.request("POST", "/districts/acme/secrets", map[string]string{"name": "PACK_TOKEN", "value": "super-secret-value"}, 503, nil)
	t.Setenv("GITOWN_SECRET_KEY", "test-secret-key")
	owner.request("POST", "/districts/acme/secrets", map[string]string{"name": "PACK_TOKEN", "value": "super-secret-value"}, 201, nil)
	var secret struct {
		Value string `json:"value"`
	}
	owner.request("GET", "/districts/acme/secrets/PACK_TOKEN", nil, 200, &secret)
	if secret.Value != "super-secret-value" {
		t.Fatalf("secret value: %+v", secret)
	}
	var names struct {
		Items []map[string]any `json:"items"`
	}
	owner.request("GET", "/districts/acme/secrets", nil, 200, &names)
	encoded, _ := json.Marshal(names)
	if strings.Contains(string(encoded), "super-secret-value") {
		t.Fatal("secret list included the value")
	}

	owner.request("POST", "/collections", map[string]string{"slug": "starters", "title": "Starters"}, 201, nil)
	owner.request("POST", "/collections/owner/starters/items", map[string]string{"repository": "owner/atlas"}, 200, nil)
	var collection struct {
		Items []Repository `json:"items"`
	}
	owner.request("GET", "/collections/owner/starters", nil, 200, &collection)
	if len(collection.Items) != 1 || collection.Items[0].Name != "atlas" {
		t.Fatalf("collection: %+v", collection.Items)
	}
	var code struct {
		Items []CodeMatch `json:"items"`
	}
	owner.request("GET", "/search/code?q=Welcome", nil, 200, &code)
	foundCode := false
	for _, item := range code.Items {
		if item.Repository == "atlas" && strings.Contains(item.Snippet, "Welcome") {
			foundCode = true
		}
	}
	if !foundCode {
		t.Fatalf("code search missed the README: %+v", code.Items)
	}
	owner.request("PUT", "/repos/owner/atlas/spark", map[string]bool{"sparked": true}, 200, nil)
	owner.request("PUT", "/repos/owner/atlas/topics", map[string]any{"topics": []string{"beginner-friendly"}}, 200, nil)
	owner.request("GET", "/search/repositories?language=Markdown&beginner=1&sort=updated", nil, 200, nil)
	var label Label
	owner.request("POST", "/repos/owner/atlas/labels", map[string]string{"name": "help wanted", "color": "1f6feb", "description": "Needs a builder"}, 201, &label)
	var issue Issue
	owner.request("POST", "/repos/owner/atlas/issues", map[string]string{"title": "First task", "body": "A good place to start"}, 201, &issue)
	owner.request("POST", fmt.Sprintf("/repos/owner/atlas/issues/%d/labels", issue.Number), map[string]string{"label_id": label.ID}, 200, nil)
	var tasks struct {
		Items []map[string]any `json:"items"`
	}
	owner.request("GET", "/search/tasks?kind=help", nil, 200, &tasks)
	if len(tasks.Items) == 0 {
		t.Fatal("help-wanted search was empty")
	}
	owner.request("GET", "/search/recommendations", nil, 200, nil)

	var key struct {
		Fingerprint string `json:"fingerprint"`
	}
	owner.request("POST", "/user/ssh-keys", map[string]string{"title": "laptop", "public_key": syntheticPublicKey(3)}, 201, &key)
	if !strings.HasPrefix(key.Fingerprint, "SHA256:") {
		t.Fatalf("ssh key: %+v", key)
	}
	if err = application.SSHSession(ctx, key.Fingerprint, "bash", bytes.NewReader(nil), io.Discard, io.Discard); err == nil {
		t.Fatal("ssh shell command was accepted")
	}
	var deploy struct {
		Fingerprint string `json:"fingerprint"`
	}
	owner.request("POST", "/repos/owner/atlas/deploy-keys", map[string]any{"title": "ci", "public_key": syntheticPublicKey(9), "write": false}, 201, &deploy)
	if err = application.SSHSession(ctx, deploy.Fingerprint, "git-receive-pack 'owner/atlas.git'", bytes.NewReader(nil), io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "cannot push") {
		t.Fatalf("read-only deploy key: %v", err)
	}
	owner.request("PUT", "/repos/owner/atlas/branch-rules?branch=main", map[string]any{"required_approvals": 0, "restrict_push": true, "required_checks": []string{}}, 200, nil)
	var writer struct {
		Fingerprint string `json:"fingerprint"`
	}
	owner.request("POST", "/repos/owner/atlas/deploy-keys", map[string]any{"title": "release", "public_key": syntheticPublicKey(11), "write": true}, 201, &writer)
	packet := fmt.Sprintf("%04x%s", 4+len(strings.Repeat("a", 40)+" "+strings.Repeat("b", 40)+" refs/heads/main\n"), strings.Repeat("a", 40)+" "+strings.Repeat("b", 40)+" refs/heads/main\n") + "0000"
	if err = application.SSHSession(ctx, writer.Fingerprint, "git-receive-pack owner/atlas.git", strings.NewReader(packet), io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "maintainer") {
		t.Fatalf("deploy key bypassed restricted push: %v", err)
	}

	writeToken := owner.token("repo:write")
	repoURL := server.URL + "/git/owner/atlas.git"
	work := filepath.Join(t.TempDir(), "work")
	gitRun := func(dir, token string, ok bool, args ...string) string {
		t.Helper()
		argv := []string{"-c", "credential.helper="}
		if token != "" {
			argv = append(argv, "-c", "http.extraHeader=Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte("owner:"+token)))
		}
		cmd := exec.Command("git", append(argv, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
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
	if err = os.WriteFile(filepath.Join(work, "note.txt"), []byte("note\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(work, "", true, "add", "note.txt")
	gitRun(work, "", true, "commit", "-m", "Add note")
	gitRun(work, "", true, "checkout", "-b", "feature")
	gitRun(work, writeToken, true, "push", "-u", "origin", "feature")
	var pull Pull
	owner.request("POST", "/repos/owner/atlas/pulls", map[string]string{"title": "Add note", "base_branch": "main", "head_branch": "feature"}, 201, &pull)
	owner.request("PUT", fmt.Sprintf("/repos/owner/atlas/pulls/%d/crews", pull.Number), map[string]any{"slugs": []string{"reviewers"}}, 200, nil)
	var notes []Notification
	other.request("GET", "/user/notifications", nil, 200, &notes)
	requested := false
	for _, note := range notes {
		if note.Kind == "review_request" {
			requested = true
		}
	}
	if !requested {
		t.Fatalf("crew review was not requested: %+v", notes)
	}
	gitRun(work, "", true, "tag", "v1.0.0")
	gitRun(work, writeToken, true, "push", "origin", "v1.0.0")
	var drop struct {
		Body string `json:"body"`
	}
	owner.request("POST", "/repos/owner/atlas/drops", map[string]any{"tag": "v1.0.0", "title": "First drop"}, 201, &drop)
	if !strings.Contains(drop.Body, "Initial commit") && !strings.Contains(drop.Body, "Add note") {
		t.Fatalf("changelog: %s", drop.Body)
	}
	payload := []byte("hello drop\n")
	owner.request("POST", "/repos/owner/atlas/drops/v1.0.0/assets", map[string]string{"name": "note.txt", "content_base64": base64.StdEncoding.EncodeToString(payload)}, 201, nil)
	owner.request("GET", "/repos/owner/atlas/drops/v1.0.0/assets/note.txt", nil, 200, nil)
	var published struct {
		Assets []struct {
			Name          string `json:"name"`
			SHA256        string `json:"sha256"`
			DownloadCount int    `json:"download_count"`
		} `json:"assets"`
		ProvenanceVerified bool `json:"provenance_verified"`
	}
	owner.request("GET", "/repos/owner/atlas/drops/v1.0.0", nil, 200, &published)
	sum := sha256.Sum256(payload)
	if len(published.Assets) != 1 || published.Assets[0].DownloadCount != 1 || published.Assets[0].SHA256 != hex.EncodeToString(sum[:]) || published.ProvenanceVerified {
		t.Fatalf("drop asset: %+v", published)
	}
	var refs []map[string]any
	owner.request("GET", "/repos/owner/atlas/refs", nil, 200, &refs)
	if len(refs) == 0 {
		t.Fatal("ref events were not recorded")
	}
	owner.request("PUT", "/repos/owner/atlas/maintenance", map[string]any{}, 200, nil)

	owner.request("POST", "/crates", map[string]any{"name": "atlas-kit", "visibility": "public", "retention": 1}, 201, nil)
	owner.request("POST", "/crates/atlas-kit/versions", map[string]string{"version": "1.0.0", "metadata": `{"name":"atlas-kit"}`, "content_base64": base64.StdEncoding.EncodeToString([]byte("version-one"))}, 201, nil)
	owner.request("POST", "/crates/atlas-kit/versions", map[string]string{"version": "1.0.1", "content_base64": base64.StdEncoding.EncodeToString([]byte("version-two"))}, 201, nil)
	owner.request("GET", "/crates/atlas-kit/versions/1.0.0/download", nil, 404, nil)
	owner.request("GET", "/crates/atlas-kit/versions/1.0.1/download", nil, 200, nil)
	owner.request("POST", "/crates/atlas-kit/versions", map[string]string{"version": "1.0.2", "content_base64": base64.StdEncoding.EncodeToString([]byte("-----BEGIN PRIVATE KEY-----\nabc"))}, 422, nil)
	packageToken := owner.token("package:write")
	bare := testClient{t, server.URL, &http.Client{}}
	bare.bearer(packageToken, "POST", "/crates", map[string]any{"name": "token-kit", "visibility": "public"}, 201, nil)
	if err = os.WriteFile(filepath.Join(work, "package.txt"), []byte("package\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(work, "", true, "add", "package.txt")
	gitRun(work, "", true, "commit", "-m", "Add package note")
	gitRun(work, packageToken, false, "push", "origin", "feature")

	object := []byte("hello lfs\n")
	oidSum := sha256.Sum256(object)
	oid := hex.EncodeToString(oidSum[:])
	batchBody := fmt.Sprintf(`{"operation":"upload","objects":[{"oid":"%s","size":%d}]}`, oid, len(object))
	batchReq, err := http.NewRequest("POST", server.URL+"/git/owner/atlas.git/info/lfs/objects/batch", strings.NewReader(batchBody))
	if err != nil {
		t.Fatal(err)
	}
	batchReq.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	batchReq.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("owner:"+writeToken)))
	batchRes, err := http.DefaultClient.Do(batchReq)
	if err != nil {
		t.Fatal(err)
	}
	batchPayload, _ := io.ReadAll(batchRes.Body)
	batchRes.Body.Close()
	if batchRes.StatusCode != 200 {
		t.Fatalf("lfs batch: %d %s", batchRes.StatusCode, batchPayload)
	}
	var batch struct {
		Objects []struct {
			Actions map[string]struct {
				Href string `json:"href"`
			} `json:"actions"`
		} `json:"objects"`
	}
	if err = json.Unmarshal(batchPayload, &batch); err != nil || len(batch.Objects) != 1 || batch.Objects[0].Actions["upload"].Href == "" {
		t.Fatalf("lfs batch body: %s", batchPayload)
	}
	putReq, err := http.NewRequest("PUT", batch.Objects[0].Actions["upload"].Href, bytes.NewReader(object))
	if err != nil {
		t.Fatal(err)
	}
	putReq.Header.Set("Authorization", batchReq.Header.Get("Authorization"))
	putRes, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatal(err)
	}
	putPayload, _ := io.ReadAll(putRes.Body)
	putRes.Body.Close()
	if putRes.StatusCode != 200 {
		t.Fatalf("lfs upload: %d %s", putRes.StatusCode, putPayload)
	}
	getReq, err := http.NewRequest("GET", server.URL+"/git/owner/atlas.git/info/lfs/objects/"+oid, nil)
	if err != nil {
		t.Fatal(err)
	}
	getReq.Header.Set("Authorization", batchReq.Header.Get("Authorization"))
	getRes, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(getRes.Body)
	getRes.Body.Close()
	if getRes.StatusCode != 200 || !bytes.Equal(got, object) {
		t.Fatalf("lfs download: %d %q", getRes.StatusCode, got)
	}

	shallow := filepath.Join(t.TempDir(), "shallow")
	gitRun("", writeToken, true, "clone", "--depth", "1", repoURL, shallow)
	if !strings.Contains(gitRun(shallow, "", true, "rev-parse", "--is-shallow-repository"), "true") {
		t.Fatal("depth clone was not shallow")
	}

	t.Setenv("GITOWN_REPO_QUOTA_BYTES", "1")
	if err = os.WriteFile(filepath.Join(work, "extra.txt"), []byte("extra\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(work, "", true, "add", "extra.txt")
	gitRun(work, "", true, "commit", "-m", "Add extra")
	output := gitRun(work, writeToken, false, "push", "origin", "feature")
	if !strings.Contains(output, "413") {
		t.Fatalf("quota rejection was not an HTTP 413: %s", output)
	}
}
