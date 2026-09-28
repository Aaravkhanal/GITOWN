package app

import (
	"bytes"
	"context"
	"encoding/base64"
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

type testClient struct {
	t      *testing.T
	url    string
	client *http.Client
}

func (c testClient) request(method, path string, body any, status int, out any) {
	c.t.Helper()
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(method, c.url+"/api/v1"+path, bytes.NewReader(raw))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:3000")
	res, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != status {
		c.t.Fatalf("%s %s: expected %d got %d: %s", method, path, status, res.StatusCode, b)
	}
	if out != nil {
		if err = json.Unmarshal(b, out); err != nil {
			c.t.Fatal(err)
		}
	}
}
func (c testClient) token(scope string) string {
	c.t.Helper()
	var result struct{ Token string }
	c.request("POST", "/user/tokens", map[string]string{"name": "integration", "scope": scope}, 201, &result)
	return result.Token
}

func TestPlatformWorkflow(t *testing.T) {
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
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("migration is not repeatable: %v", err)
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
	anon := testClient{t, server.URL, &http.Client{}}
	jar2, _ := cookiejar.New(nil)
	other := testClient{t, server.URL, &http.Client{Jar: jar2}}
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)
	other.request("POST", "/auth/register", map[string]string{"username": "other", "email": "other@example.test", "password": "other-long-password"}, 201, nil)
	var browserSessions []BrowserSession
	owner.request("GET", "/user/sessions", nil, 200, &browserSessions)
	if len(browserSessions) != 1 || !browserSessions[0].Current {
		t.Fatalf("current browser session was not identified: %+v", browserSessions)
	}
	secondJar, _ := cookiejar.New(nil)
	ownerSecond := testClient{t, server.URL, &http.Client{Jar: secondJar}}
	ownerSecond.request("POST", "/auth/login", map[string]string{"username": "owner", "password": "owner-long-password"}, 200, nil)
	owner.request("GET", "/user/sessions", nil, 200, &browserSessions)
	var otherSessionID string
	for _, session := range browserSessions {
		if !session.Current {
			otherSessionID = session.ID
		}
	}
	if len(browserSessions) != 2 || otherSessionID == "" {
		t.Fatalf("second browser session was not listed: %+v", browserSessions)
	}
	owner.request("DELETE", "/user/sessions/"+otherSessionID, nil, 200, nil)
	var revokedState struct {
		User *User `json:"user"`
	}
	ownerSecond.request("GET", "/auth/me", nil, 200, &revokedState)
	if revokedState.User != nil {
		t.Fatal("revoked browser session remained authenticated")
	}
	_ = owner.token("repo:read")
	owner.request("PATCH", "/user/password", map[string]any{"current_password": "incorrect-password", "new_password": "updated-owner-password", "revoke_access_tokens": true}, 401, nil)
	owner.request("PATCH", "/user/password", map[string]any{"current_password": "owner-long-password", "new_password": "updated-owner-password", "revoke_access_tokens": true}, 200, nil)
	owner.request("GET", "/auth/me", nil, 200, nil)
	var revokedTokens []Token
	owner.request("GET", "/user/tokens", nil, 200, &revokedTokens)
	if len(revokedTokens) != 0 {
		t.Fatalf("password change did not revoke access tokens: %+v", revokedTokens)
	}
	ownerSecond.request("POST", "/auth/login", map[string]string{"username": "owner", "password": "owner-long-password"}, 401, nil)
	ownerSecond.request("POST", "/auth/login", map[string]string{"username": "owner", "password": "updated-owner-password"}, 200, nil)
	anon.request("POST", "/auth/login", map[string]string{"username": "owner", "password": "incorrect-password"}, 401, nil)
	var repo Repository
	owner.request("POST", "/repos", map[string]any{"name": "project", "description": "Integration repository", "visibility": "private", "readme": true}, 201, &repo)
	owner.request("POST", "/repos", map[string]any{"name": "project", "visibility": "private"}, 409, nil)
	owner.request("POST", "/repos", map[string]any{"name": "../../escape", "visibility": "private"}, 422, nil)
	anon.request("GET", "/repos/owner/project", nil, 404, nil)
	other.request("GET", "/repos/owner/project", nil, 404, nil)
	other.request("PATCH", "/repos/owner/project", map[string]string{"description": "not yours", "visibility": "public"}, 404, nil)
	owner.request("PATCH", "/repos/owner/project", map[string]string{"description": strings.Repeat("x", 501), "visibility": "private"}, 422, nil)
	owner.request("PATCH", "/repos/owner/project", map[string]string{"description": "Now visible", "visibility": "public"}, 200, &repo)
	if repo.Visibility != "public" || repo.Description != "Now visible" {
		t.Fatalf("repository settings were not updated: %+v", repo)
	}
	anon.request("GET", "/repos/owner/project", nil, 200, nil)
	owner.request("PATCH", "/repos/owner/project", map[string]string{"description": "Integration repository", "visibility": "private"}, 200, &repo)
	webHead, err := a.git.Resolve(ctx, repo.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	var webCommit struct {
		SHA string `json:"sha"`
	}
	owner.request("PUT", "/repos/owner/project/contents", map[string]string{
		"branch": "main", "path": "docs/browser.md", "content": "# Browser editing\n", "message": "Add browser guide", "expected_head": webHead,
	}, 201, &webCommit)
	if webCommit.SHA == "" || webCommit.SHA == webHead {
		t.Fatalf("browser commit did not advance main: %+v", webCommit)
	}
	owner.request("GET", "/repos/owner/project/tree?ref=main&path=docs%2Fbrowser.md", nil, 200, nil)
	owner.request("PUT", "/repos/owner/project/contents", map[string]string{
		"branch": "main", "path": "docs/stale.md", "content": "stale", "message": "Stale browser edit", "expected_head": webHead,
	}, 409, nil)
	owner.request("PUT", "/repos/owner/project/contents", map[string]string{
		"branch": "main", "path": "../config", "content": "unsafe", "message": "Unsafe browser edit", "expected_head": webCommit.SHA,
	}, 422, nil)
	owner.request("POST", "/repos/owner/project/members", map[string]string{"username": "missing", "role": "read"}, 404, nil)
	owner.request("POST", "/repos/owner/project/members", map[string]string{"username": "owner", "role": "write"}, 422, nil)
	var member RepositoryMember
	owner.request("POST", "/repos/owner/project/members", map[string]string{"username": "other", "role": "read"}, 201, &member)
	if member.Username != "other" || member.Role != "read" {
		t.Fatalf("unexpected collaborator: %+v", member)
	}
	owner.request("POST", "/repos/owner/project/members", map[string]string{"username": "other", "role": "write"}, 409, nil)
	other.request("GET", "/repos/owner/project", nil, 200, nil)
	other.request("PATCH", "/repos/owner/project", map[string]string{"description": "not yours", "visibility": "public"}, 403, nil)
	other.request("GET", "/repos/owner/project/members", nil, 403, nil)
	var memberRepos []Repository
	other.request("GET", "/repos?mine=true", nil, 200, &memberRepos)
	if len(memberRepos) != 1 || memberRepos[0].Role != "read" || memberRepos[0].CanWrite {
		t.Fatalf("collaborator repository listing has wrong permissions: %+v", memberRepos)
	}
	var anonymousRepos []Repository
	anon.request("GET", "/repos", nil, 200, &anonymousRepos)
	if len(anonymousRepos) != 0 {
		t.Fatal("private repository leaked in listing")
	}
	owner.request("GET", "/repos/owner/project/tree?path=..%2Fconfig", nil, 404, nil)
	request, _ := http.NewRequest("POST", server.URL+"/api/v1/repos", strings.NewReader(`{"name":"csrf","visibility":"public"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://untrusted.example")
	response, err := owner.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("CSRF origin check failed")
	}
	writeToken := owner.token("repo:write")
	readToken := owner.token("repo:read")
	otherToken := other.token("repo:write")
	repoURL := server.URL + "/git/owner/project.git"
	work := filepath.Join(t.TempDir(), "work")
	gitRun := func(dir, username, token string, success bool, args ...string) string {
		t.Helper()
		argv := []string{"-c", "credential.helper="}
		if token != "" {
			argv = append(argv, "-c", "http.extraHeader=Authorization: Basic "+base64.StdEncoding.EncodeToString([]byte(username+":"+token)))
		}
		cmd := exec.Command("git", append(argv, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if success && err != nil {
			t.Fatalf("git %s failed: %s", args[0], out)
		}
		if !success && err == nil {
			t.Fatalf("git %s unexpectedly succeeded", args[0])
		}
		return string(out)
	}
	gitRun("", "", "", false, "ls-remote", repoURL)
	gitRun("", "owner", "owner-long-password", false, "ls-remote", repoURL)
	gitRun("", "other", otherToken, true, "ls-remote", repoURL)
	gitRun("", "owner", readToken, true, "ls-remote", repoURL)
	gitRun("", "owner", writeToken, true, "clone", repoURL, work)
	gitRun(work, "", "", true, "config", "user.name", "Owner")
	gitRun(work, "", "", true, "config", "user.email", "owner@example.test")
	gitRun(work, "", "", true, "checkout", "-b", "feature")
	if err = os.WriteFile(filepath.Join(work, "hello.txt"), []byte("Hello from a real Git push!\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(work, "", "", true, "add", "hello.txt")
	gitRun(work, "", "", true, "commit", "-m", "Add hello file")
	gitRun(work, "owner", readToken, false, "push", "origin", "feature")
	gitRun(work, "owner", writeToken, true, "push", "-u", "origin", "feature")
	gitRun(work, "other", otherToken, false, "push", "origin", "feature")
	owner.request("PATCH", "/repos/owner/project/members/other", map[string]string{"role": "write"}, 200, &member)
	gitRun(work, "other", otherToken, true, "push", "origin", "feature")
	owner.request("GET", "/repos/owner/project/tree?ref=feature&path=hello.txt", nil, 200, nil)
	gitRun(work, "owner", writeToken, false, "push", "origin", "--delete", "feature")
	gitRun(work, "", "", true, "tag", "v0.1.0")
	gitRun(work, "owner", writeToken, true, "push", "origin", "v0.1.0")
	// A non-fast-forward push must fail even with write credentials.
	gitRun(work, "", "", true, "reset", "--hard", "HEAD~1")
	gitRun(work, "owner", writeToken, false, "push", "--force", "origin", "feature")
	gitRun(work, "", "", true, "reset", "--hard", "origin/feature")
	var pull Pull
	owner.request("POST", "/repos/owner/project/pulls", map[string]string{"title": "Add hello", "body": "Real integration test", "base_branch": "main", "head_branch": "feature"}, 201, &pull)
	owner.request("POST", "/repos/owner/project/pulls", map[string]string{"title": "Duplicate", "base_branch": "main", "head_branch": "feature"}, 409, nil)
	var detail struct {
		HeadSHA   string `json:"head_sha"`
		BaseSHA   string `json:"base_sha"`
		Diff      string `json:"diff"`
		Mergeable bool   `json:"mergeable"`
	}
	pullPath := fmt.Sprintf("/repos/owner/project/pulls/%d", pull.Number)
	anon.request("GET", pullPath+"/comments", nil, 404, nil)
	var pullComment PullComment
	owner.request("POST", pullPath+"/comments", map[string]string{"body": "Please review this change."}, 201, &pullComment)
	if pullComment.Author != "owner" || pullComment.Body != "Please review this change." {
		t.Fatalf("unexpected pull comment: %+v", pullComment)
	}
	var pullComments []PullComment
	owner.request("GET", pullPath+"/comments", nil, 200, &pullComments)
	if len(pullComments) != 1 || pullComments[0].ID != pullComment.ID {
		t.Fatalf("pull comments were not returned: %+v", pullComments)
	}
	other.request("POST", pullPath+"/comments", map[string]string{"body": "A collaborator reply."}, 201, nil)
	owner.request("POST", pullPath+"/comments", map[string]string{"body": strings.Repeat("x", 10001)}, 422, nil)
	owner.request("POST", "/repos/owner/project/pulls/999/comments", map[string]string{"body": "Missing pull"}, 404, nil)
	owner.request("PATCH", pullPath, map[string]string{"state": "closed"}, 200, &pull)
	if pull.State != "closed" {
		t.Fatalf("pull request was not closed: %+v", pull)
	}
	owner.request("PATCH", pullPath, map[string]string{"state": "open"}, 200, &pull)
	owner.request("PATCH", pullPath, map[string]string{"state": "invalid"}, 422, nil)
	owner.request("GET", pullPath, nil, 200, &detail)
	if !detail.Mergeable || !strings.Contains(detail.Diff, "Hello from a real Git push") {
		t.Fatal("missing real pull-request diff")
	}
	owner.request("POST", pullPath+"/merge", map[string]string{"head_sha": strings.Repeat("0", 40), "base_sha": detail.BaseSHA}, 409, nil)
	other.request("POST", pullPath+"/merge", map[string]string{"head_sha": detail.HeadSHA, "base_sha": detail.BaseSHA}, 200, nil)
	owner.request("POST", pullPath+"/merge", map[string]string{"head_sha": detail.HeadSHA, "base_sha": detail.BaseSHA}, 200, nil)
	owner.request("POST", pullPath+"/merge", map[string]string{"head_sha": detail.HeadSHA, "base_sha": detail.BaseSHA}, 200, nil)
	gitRun(work, "", "", true, "checkout", "main")
	gitRun(work, "owner", writeToken, true, "pull", "--ff-only", "origin", "main")
	if b, err := os.ReadFile(filepath.Join(work, "hello.txt")); err != nil || !strings.Contains(string(b), "real Git push") {
		t.Fatal("merged history did not pull back to client")
	}
	// Simulate an interruption after the Git update but before database finalization.
	if _, err = pool.Exec(ctx, `UPDATE pull_requests SET state='merging',merged_at=NULL WHERE id=$1`, pull.ID); err != nil {
		t.Fatal(err)
	}
	owner.request("POST", pullPath+"/merge", map[string]string{"head_sha": detail.HeadSHA, "base_sha": detail.BaseSHA}, 200, nil)
	// Reconciliation must preserve the exact merge commit already in Git.
	var recoveredSHA string
	if err = pool.QueryRow(ctx, `SELECT merge_sha FROM pull_requests WHERE id=$1 AND state='merged'`, pull.ID).Scan(&recoveredSHA); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(gitRun(work, "", "", true, "rev-parse", "HEAD")) != recoveredSHA {
		t.Fatal("merge recovery changed the committed history")
	}
	// Conflicting changes must fail without moving the base ref.
	gitRun(work, "", "", true, "checkout", "-b", "conflicting-feature")
	if err = os.WriteFile(filepath.Join(work, "hello.txt"), []byte("Feature version\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(work, "", "", true, "commit", "-am", "Change the feature greeting")
	gitRun(work, "owner", writeToken, true, "push", "origin", "conflicting-feature")
	gitRun(work, "", "", true, "checkout", "main")
	if err = os.WriteFile(filepath.Join(work, "hello.txt"), []byte("Main version\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(work, "", "", true, "commit", "-am", "Change the base greeting")
	gitRun(work, "owner", writeToken, true, "push", "origin", "main")
	var conflictPull Pull
	owner.request("POST", "/repos/owner/project/pulls", map[string]string{"title": "Conflicting change", "base_branch": "main", "head_branch": "conflicting-feature"}, 201, &conflictPull)
	conflictPath := fmt.Sprintf("/repos/owner/project/pulls/%d", conflictPull.Number)
	owner.request("GET", conflictPath, nil, 200, &detail)
	if detail.Mergeable {
		t.Fatal("conflicting branches reported mergeable")
	}
	owner.request("POST", conflictPath+"/merge", map[string]string{"head_sha": detail.HeadSHA, "base_sha": detail.BaseSHA}, 409, nil)
	currentBase, err := a.git.Resolve(ctx, repo.ID, "main")
	if err != nil || currentBase != detail.BaseSHA {
		t.Fatal("conflict changed base history")
	}
	var issue Issue
	owner.request("POST", "/repos/owner/project/issues", map[string]string{"title": "First issue", "body": "Track something useful"}, 201, &issue)
	var bugLabel Label
	owner.request("POST", "/repos/owner/project/labels", map[string]string{"name": "bug", "color": "#d73a4a", "description": "Something is not working"}, 201, &bugLabel)
	owner.request("POST", "/repos/owner/project/labels", map[string]string{"name": "BUG", "color": "d73a4a"}, 409, nil)
	owner.request("POST", "/repos/owner/project/labels", map[string]string{"name": "bad", "color": "purple"}, 422, nil)
	owner.request("POST", "/repos/owner/project/issues/1/labels", map[string]string{"label_id": bugLabel.ID}, 200, nil)
	var issueLabels []Label
	owner.request("GET", "/repos/owner/project/issues/1/labels", nil, 200, &issueLabels)
	if len(issueLabels) != 1 || issueLabels[0].ID != bugLabel.ID {
		t.Fatalf("issue labels were not returned: %+v", issueLabels)
	}
	owner.request("GET", "/repos/owner/project/issues/999/labels", nil, 404, nil)
	var comment IssueComment
	owner.request("POST", "/repos/owner/project/issues/1/comments", map[string]string{"body": "The first discussion reply."}, 201, &comment)
	if comment.Author != "owner" || comment.Body != "The first discussion reply." {
		t.Fatalf("unexpected issue comment: %+v", comment)
	}
	var comments []IssueComment
	owner.request("GET", "/repos/owner/project/issues/1/comments", nil, 200, &comments)
	if len(comments) != 1 || comments[0].ID != comment.ID {
		t.Fatalf("issue comments were not returned: %+v", comments)
	}
	owner.request("POST", "/repos/owner/project/issues/1/comments", map[string]string{"body": strings.Repeat("x", 10001)}, 422, nil)
	owner.request("POST", "/repos/owner/project/issues/999/comments", map[string]string{"body": "Missing issue"}, 404, nil)
	owner.request("PATCH", "/repos/owner/project/issues/1", map[string]string{"state": "closed"}, 200, nil)
	owner.request("PATCH", "/repos/owner/project/members/other", map[string]string{"role": "triage"}, 200, &member)
	other.request("POST", "/repos/owner/project/issues", map[string]string{"title": "Triage issue", "body": "Created by a collaborator"}, 201, nil)
	other.request("POST", "/repos/owner/project/issues/1/comments", map[string]string{"body": "A collaborator reply."}, 201, nil)
	other.request("DELETE", "/repos/owner/project/issues/1/labels/"+bugLabel.ID, nil, 200, nil)
	other.request("DELETE", "/repos/owner/project/labels/"+bugLabel.ID, nil, 200, nil)
	other.request("POST", "/repos/owner/project/pulls", map[string]string{"title": "No code permission", "base_branch": "main", "head_branch": "feature"}, 403, nil)
	owner.request("DELETE", "/repos/owner/project/members/other", nil, 200, nil)
	other.request("GET", "/repos/owner/project", nil, 404, nil)
	gitRun("", "other", otherToken, false, "ls-remote", repoURL)
	owner.request("POST", "/repos", map[string]any{"name": "public-project", "visibility": "public", "readme": true}, 201, nil)
	anon.request("GET", "/repos/owner/public-project", nil, 200, nil)
	other.request("POST", "/repos/owner/public-project/issues", map[string]string{"title": "Unauthorized"}, 403, nil)
	other.request("POST", "/repos/owner/public-project/labels", map[string]string{"name": "Unauthorized", "color": "ffffff"}, 403, nil)
	var publicLabels []Label
	anon.request("GET", "/repos/owner/public-project/labels", nil, 200, &publicLabels)
	anon.request("POST", "/repos/owner/public-project/issues/1/comments", map[string]string{"body": "Anonymous reply"}, 401, nil)
	gitRun("", "", "", true, "ls-remote", server.URL+"/git/owner/public-project.git")
	var lifecycleRepo Repository
	owner.request("POST", "/repos", map[string]any{"name": "lifecycle", "visibility": "private", "readme": true}, 201, &lifecycleRepo)
	owner.request("POST", "/repos/owner/lifecycle/rename", map[string]string{"name": "renamed-repository"}, 200, &lifecycleRepo)
	if lifecycleRepo.Name != "renamed-repository" || !strings.Contains(lifecycleRepo.CloneURL, "renamed-repository.git") {
		t.Fatalf("repository rename returned wrong metadata: %+v", lifecycleRepo)
	}
	owner.request("GET", "/repos/owner/lifecycle", nil, 404, nil)
	owner.request("POST", "/repos/owner/renamed-repository/archive", map[string]any{}, 200, &lifecycleRepo)
	if !lifecycleRepo.Archived {
		t.Fatal("repository was not archived")
	}
	owner.request("POST", "/repos/owner/renamed-repository/issues", map[string]string{"title": "Archived change"}, 409, nil)
	gitRun("", "owner", readToken, true, "ls-remote", server.URL+"/git/owner/renamed-repository.git")
	owner.request("POST", "/repos/owner/renamed-repository/unarchive", map[string]any{}, 200, &lifecycleRepo)
	owner.request("DELETE", "/repos/owner/renamed-repository", map[string]string{"confirmation": "wrong"}, 422, nil)
	owner.request("DELETE", "/repos/owner/renamed-repository", map[string]string{"confirmation": "renamed-repository"}, 200, nil)
	owner.request("GET", "/repos/owner/renamed-repository", nil, 404, nil)
	var deleted []DeletedRepository
	owner.request("GET", "/user/deleted-repositories", nil, 200, &deleted)
	if len(deleted) != 1 || deleted[0].Name != "renamed-repository" {
		t.Fatalf("deleted repository missing from recovery list: %+v", deleted)
	}
	owner.request("POST", "/user/deleted-repositories/"+deleted[0].ID+"/restore", map[string]any{}, 200, nil)
	owner.request("GET", "/repos/owner/renamed-repository", nil, 200, nil)
	var tokens []Token
	owner.request("GET", "/user/tokens", nil, 200, &tokens)
	for _, token := range tokens {
		owner.request("DELETE", "/user/tokens/"+token.ID, nil, 200, nil)
	}
	gitRun("", "owner", writeToken, false, "ls-remote", repoURL)
	owner.request("GET", "/user/activity", nil, 200, nil)

	t.Run("backup_restore", func(t *testing.T) {
		if _, err := exec.LookPath("pg_dump"); err != nil {
			t.Skip("pg_dump is required for the backup/restore drill")
		}
		backup := filepath.Join(t.TempDir(), "metadata.dump")
		command := exec.Command("pg_dump", databaseURL, "--schema="+schema, "--format=custom", "--file="+backup)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("backup failed: %s", out)
		}
		gitSnapshot := filepath.Join(t.TempDir(), "repository.git")
		if err = os.CopyFS(gitSnapshot, os.DirFS(a.git.Path(repo.ID))); err != nil {
			t.Fatal(err)
		}
		// Only this test's generated schema is dropped. No shared database is reset.
		if _, err = admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Fatal(err)
		}
		command = exec.Command("pg_restore", "--dbname="+databaseURL, "--no-owner", backup)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("restore failed: %s", out)
		}
		owner.request("GET", "/repos/owner/project", nil, 200, nil)
		out, err := exec.Command("git", "--git-dir="+gitSnapshot, "show", recoveredSHA+":hello.txt").CombinedOutput()
		if err != nil || !strings.Contains(string(out), "real Git push") {
			t.Fatalf("Git backup did not restore merged content: %s", out)
		}
		var merged int
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM pull_requests WHERE state='merged'`).Scan(&merged); err != nil || merged != 1 {
			t.Fatal("pull metadata was not restored")
		}
	})
	owner.request("POST", "/auth/logout", map[string]any{}, 200, nil)
	owner.request("GET", "/user/tokens", nil, 401, nil)
}
