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
	"github.com/Aaravkhanal/GITOWN/internal/gitstore"
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
	owner.request("GET", "/repos/owner/project/raw?ref=main&path=docs%2Fbrowser.md", nil, 200, nil)
	anon.request("GET", "/repos/owner/project/raw?ref=main&path=docs%2Fbrowser.md", nil, 404, nil)
	var fileHistory []gitstore.Commit
	owner.request("GET", "/repos/owner/project/commits?ref=main&path=docs%2Fbrowser.md", nil, 200, &fileHistory)
	if len(fileHistory) != 1 || fileHistory[0].Message != "Add browser guide" {
		t.Fatalf("browser file history was not returned: %+v", fileHistory)
	}
	owner.request("PUT", "/repos/owner/project/contents", map[string]string{
		"branch": "main", "path": "docs/stale.md", "content": "stale", "message": "Stale browser edit", "expected_head": webHead,
	}, 409, nil)
	owner.request("PUT", "/repos/owner/project/contents", map[string]string{
		"branch": "main", "path": "../config", "content": "unsafe", "message": "Unsafe browser edit", "expected_head": webCommit.SHA,
	}, 422, nil)
	owner.request("DELETE", "/repos/owner/project/contents", map[string]string{
		"branch": "main", "path": "docs/browser.md", "message": "Remove browser guide", "expected_head": webCommit.SHA,
	}, 200, &webCommit)
	owner.request("GET", "/repos/owner/project/tree?ref=main&path=docs%2Fbrowser.md", nil, 404, nil)
	owner.request("DELETE", "/repos/owner/project/contents", map[string]string{
		"branch": "main", "path": "README.md", "message": "Stale delete", "expected_head": strings.Repeat("0", 40),
	}, 409, nil)
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
	var rule BranchRule
	owner.request("PUT", "/repos/owner/project/branch-rules?branch=main", map[string]any{"required_approvals": 1, "block_changes_requested": true}, 200, &rule)
	if rule.Branch != "main" || rule.RequiredApprovals != 1 || !rule.BlockChangesRequested {
		t.Fatalf("unexpected branch rule: %+v", rule)
	}
	other.request("PUT", "/repos/owner/project/branch-rules?branch=main", map[string]any{"required_approvals": 0, "block_changes_requested": false}, 403, nil)
	owner.request("GET", "/repos/owner/project/branch-rules?branch=main", nil, 200, &rule)
	owner.request("POST", pullPath+"/merge", map[string]string{"head_sha": detail.HeadSHA, "base_sha": detail.BaseSHA}, 409, nil)
	owner.request("POST", pullPath+"/reviews", map[string]string{"state": "approved", "body": "Self approval", "head_sha": detail.HeadSHA}, 403, nil)
	other.request("POST", pullPath+"/reviews", map[string]string{"state": "approved", "head_sha": strings.Repeat("0", 40)}, 409, nil)
	var review PullReview
	other.request("POST", pullPath+"/reviews", map[string]string{"state": "approved", "body": "Ready to unite.", "head_sha": detail.HeadSHA}, 201, &review)
	if review.Reviewer != "other" || review.State != "approved" || review.HeadSHA != detail.HeadSHA {
		t.Fatalf("unexpected formal review: %+v", review)
	}
	var reviews []PullReview
	owner.request("GET", pullPath+"/reviews", nil, 200, &reviews)
	if len(reviews) != 1 || reviews[0].Stale {
		t.Fatalf("current review was not returned: %+v", reviews)
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
	var subscription struct {
		Subscribed bool `json:"subscribed"`
	}
	owner.request("GET", "/repos/owner/project/issues/1/subscription", nil, 200, &subscription)
	if !subscription.Subscribed {
		t.Fatal("issue author was not subscribed")
	}
	owner.request("GET", "/repos/owner/project/issues/999/subscription", nil, 404, nil)
	anon.request("GET", "/user/notifications", nil, 401, nil)
	var board []BoardItem
	owner.request("GET", "/repos/owner/project/board", nil, 200, &board)
	if len(board) != 1 || board[0].Status != "todo" {
		t.Fatalf("new issue did not appear on board: %+v", board)
	}
	owner.request("PUT", "/repos/owner/project/issues/1/board", map[string]string{"status": "progress"}, 200, nil)
	owner.request("GET", "/repos/owner/project/board", nil, 200, &board)
	if board[0].Status != "progress" || board[0].State != "open" {
		t.Fatalf("in-progress board state was not saved: %+v", board)
	}
	owner.request("PUT", "/repos/owner/project/issues/1/board", map[string]string{"status": "done"}, 200, nil)
	owner.request("GET", "/repos/owner/project/board", nil, 200, &board)
	if board[0].Status != "done" || board[0].State != "closed" {
		t.Fatalf("done did not close the issue: %+v", board)
	}
	owner.request("PUT", "/repos/owner/project/issues/1/board", map[string]string{"status": "todo"}, 200, nil)
	owner.request("PUT", "/repos/owner/project/issues/1/board", map[string]string{"status": "invalid"}, 422, nil)
	owner.request("PUT", "/repos/owner/project/issues/999/board", map[string]string{"status": "todo"}, 404, nil)
	other.request("PUT", "/repos/owner/project/issues/1/subscription", map[string]bool{"subscribed": true}, 200, &subscription)
	if !subscription.Subscribed {
		t.Fatal("collaborator could not subscribe")
	}
	var assignees IssueAssignees
	owner.request("PUT", "/repos/owner/project/issues/1/assignees", map[string]any{"usernames": []string{"owner", "other"}}, 200, &assignees)
	if len(assignees.Assigned) != 2 {
		t.Fatalf("issue assignees were not saved: %+v", assignees)
	}
	owner.request("GET", "/repos/owner/project/issues/1/assignees", nil, 200, &assignees)
	owner.request("PUT", "/repos/owner/project/issues/1/assignees", map[string]any{"usernames": []string{"missing"}}, 422, nil)
	owner.request("GET", "/repos/owner/project/issues/999/assignees", nil, 404, nil)
	var milestone Milestone
	owner.request("POST", "/repos/owner/project/milestones", map[string]string{"title": "First release", "description": "Ship collaboration", "due_date": "2026-12-31"}, 201, &milestone)
	if milestone.ID == "" || milestone.DueDate == nil || *milestone.DueDate != "2026-12-31" {
		t.Fatalf("milestone was not created: %+v", milestone)
	}
	owner.request("POST", "/repos/owner/project/milestones", map[string]string{"title": "first release"}, 409, nil)
	owner.request("POST", "/repos/owner/project/milestones", map[string]string{"title": "bad date", "due_date": "2026-02-31"}, 422, nil)
	owner.request("PUT", "/repos/owner/project/issues/1/milestone", map[string]any{"milestone_id": milestone.ID}, 200, nil)
	var assignedMilestone struct {
		Milestone *Milestone `json:"milestone"`
	}
	owner.request("GET", "/repos/owner/project/issues/1/milestone", nil, 200, &assignedMilestone)
	if assignedMilestone.Milestone == nil || assignedMilestone.Milestone.ID != milestone.ID || assignedMilestone.Milestone.OpenIssues != 1 {
		t.Fatalf("issue milestone was not assigned: %+v", assignedMilestone)
	}
	owner.request("PUT", "/repos/owner/project/issues/1/milestone", map[string]any{"milestone_id": auth.ID()}, 422, nil)
	owner.request("GET", "/repos/owner/project/issues/999/milestone", nil, 404, nil)
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
	var otherNotifications []Notification
	other.request("GET", "/user/notifications", nil, 200, &otherNotifications)
	if len(otherNotifications) != 1 || otherNotifications[0].Kind != "issue_comment" || otherNotifications[0].Actor != "owner" || otherNotifications[0].ReadAt != nil {
		t.Fatalf("subscriber did not receive issue comment: %+v", otherNotifications)
	}
	other.request("PUT", fmt.Sprintf("/user/notifications/%d/read", otherNotifications[0].ID), map[string]any{}, 200, nil)
	other.request("GET", "/user/notifications", nil, 200, &otherNotifications)
	if otherNotifications[0].ReadAt == nil {
		t.Fatal("notification was not marked read")
	}
	owner.request("PUT", fmt.Sprintf("/user/notifications/%d/read", otherNotifications[0].ID), map[string]any{}, 404, nil)
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
	owner.request("GET", "/repos/owner/project/board", nil, 200, &board)
	if board[0].Status != "done" || board[0].State != "closed" {
		t.Fatalf("closing an issue did not update board: %+v", board)
	}
	owner.request("PUT", "/repos/owner/project/milestones/"+milestone.ID, map[string]string{"title": "First release", "description": "Ship collaboration", "state": "closed", "due_date": "2026-12-31"}, 200, &milestone)
	if milestone.ClosedIssues != 1 || milestone.OpenIssues != 0 || milestone.State != "closed" {
		t.Fatalf("milestone progress or state was not updated: %+v", milestone)
	}
	owner.request("PATCH", "/repos/owner/project/members/other", map[string]string{"role": "triage"}, 200, &member)
	other.request("POST", "/repos/owner/project/issues", map[string]string{"title": "Triage issue", "body": "Created by a collaborator"}, 201, nil)
	other.request("POST", "/repos/owner/project/milestones", map[string]string{"title": "Next release"}, 201, nil)
	other.request("PUT", "/repos/owner/project/issues/2/milestone", map[string]any{"milestone_id": milestone.ID}, 200, nil)
	other.request("POST", "/repos/owner/project/issues/1/comments", map[string]string{"body": "A collaborator reply."}, 201, nil)
	var ownerNotifications []Notification
	owner.request("GET", "/user/notifications", nil, 200, &ownerNotifications)
	if len(ownerNotifications) == 0 || ownerNotifications[0].Actor != "other" || ownerNotifications[0].Kind != "issue_comment" {
		t.Fatalf("issue author did not receive collaborator update: %+v", ownerNotifications)
	}
	other.request("PUT", "/repos/owner/project/issues/1/subscription", map[string]bool{"subscribed": false}, 200, &subscription)
	if subscription.Subscribed {
		t.Fatal("collaborator could not unsubscribe")
	}
	other.request("DELETE", "/repos/owner/project/issues/1/labels/"+bugLabel.ID, nil, 200, nil)
	other.request("DELETE", "/repos/owner/project/labels/"+bugLabel.ID, nil, 200, nil)
	other.request("POST", "/repos/owner/project/pulls", map[string]string{"title": "No code permission", "base_branch": "main", "head_branch": "feature"}, 403, nil)
	owner.request("DELETE", "/repos/owner/project/members/other", nil, 200, nil)
	other.request("GET", "/user/notifications", nil, 200, &otherNotifications)
	if len(otherNotifications) != 0 {
		t.Fatalf("revoked collaborator retained private notifications: %+v", otherNotifications)
	}
	other.request("GET", "/repos/owner/project", nil, 404, nil)
	gitRun("", "other", otherToken, false, "ls-remote", repoURL)
	owner.request("POST", "/repos", map[string]any{"name": "public-project", "visibility": "public", "readme": true}, 201, nil)
	owner.request("PUT", "/user/profile", map[string]string{"display_name": "Owner", "bio": "Building in the open", "website": "http://insecure.example", "location": "Nepal"}, 422, nil)
	owner.request("PUT", "/user/profile", map[string]string{"display_name": "Owner", "bio": "Building in the open", "website": "https://example.test", "location": "Nepal"}, 200, nil)
	owner.request("PUT", "/user/showcase", map[string]any{"repository_ids": []string{repo.ID}}, 422, nil)
	var publicRepoDetail struct {
		Repository Repository `json:"repository"`
	}
	owner.request("GET", "/repos/owner/public-project", nil, 200, &publicRepoDetail)
	publicRepo := publicRepoDetail.Repository
	var topicState TopicState
	owner.request("PUT", "/repos/owner/public-project/topics", map[string]any{"topics": []string{"Go", "open-source"}}, 200, &topicState)
	if len(topicState.Topics) != 2 || topicState.Topics[0] != "go" || topicState.Topics[1] != "open-source" {
		t.Fatalf("repository topics not normalized: %+v", topicState)
	}
	owner.request("PUT", "/repos/owner/public-project/topics", map[string]any{"topics": []string{"go", "GO"}}, 422, nil)
	other.request("PUT", "/repos/owner/public-project/topics", map[string]any{"topics": []string{"other"}}, 403, nil)
	anon.request("GET", "/repos/owner/public-project/topics", nil, 200, &topicState)
	if len(topicState.Topics) != 2 {
		t.Fatalf("public topics are unavailable: %+v", topicState)
	}
	anon.request("GET", "/repos/owner/project/topics", nil, 404, nil)
	var search RepositorySearch
	anon.request("GET", "/search/repositories?q=public-project&topic=go&sort=name", nil, 200, &search)
	if len(search.Items) != 1 || search.Items[0].ID != publicRepo.ID || search.HasMore {
		t.Fatalf("public topic search is wrong: %+v", search)
	}
	anon.request("GET", "/search/repositories?q=project&topic=not-found", nil, 200, &search)
	if len(search.Items) != 0 {
		t.Fatalf("search ignored topic filter: %+v", search)
	}
	anon.request("GET", "/search/repositories?q=project&offset=-1", nil, 422, nil)
	anon.request("GET", "/search/repositories?sort=random", nil, 422, nil)
	anon.request("GET", "/search/repositories?sort=trending&topic=go", nil, 200, &search)
	if len(search.Items) != 1 || search.Items[0].ID != publicRepo.ID {
		t.Fatalf("trending topic search is wrong: %+v", search)
	}
	anon.request("GET", "/search/repositories?q=project", nil, 200, &search)
	for _, item := range search.Items {
		if item.ID == repo.ID {
			t.Fatal("private repository appeared in public search")
		}
	}
	var spark SparkState
	owner.request("GET", "/repos/owner/public-project/spark", nil, 200, &spark)
	if spark.Count != 0 || spark.Sparked {
		t.Fatalf("unexpected initial Spark: %+v", spark)
	}
	owner.request("PUT", "/repos/owner/public-project/spark", map[string]bool{"sparked": true}, 200, &spark)
	if spark.Count != 1 || !spark.Sparked {
		t.Fatalf("owner Spark was not saved: %+v", spark)
	}
	other.request("PUT", "/repos/owner/public-project/spark", map[string]bool{"sparked": true}, 200, &spark)
	other.request("PUT", "/repos/owner/public-project/spark", map[string]bool{"sparked": true}, 200, &spark)
	if spark.Count != 2 || !spark.Sparked {
		t.Fatalf("duplicate Spark changed count: %+v", spark)
	}
	other.request("PUT", "/repos/owner/public-project/spark", map[string]bool{"sparked": false}, 200, &spark)
	if spark.Count != 1 || spark.Sparked {
		t.Fatalf("Spark removal failed: %+v", spark)
	}
	anon.request("GET", "/repos/owner/public-project/spark", nil, 200, &spark)
	if spark.Count != 1 || spark.Sparked {
		t.Fatalf("anonymous Spark state leaked identity: %+v", spark)
	}
	anon.request("PUT", "/repos/owner/public-project/spark", map[string]bool{"sparked": true}, 401, nil)
	anon.request("GET", "/repos/owner/project/spark", nil, 404, nil)
	owner.request("PUT", "/user/showcase", map[string]any{"repository_ids": []string{publicRepo.ID}}, 200, nil)
	other.request("PUT", "/user/showcase", map[string]any{"repository_ids": []string{publicRepo.ID}}, 422, nil)
	var publicProfile Profile
	anon.request("GET", "/users/owner/profile", nil, 200, &publicProfile)
	if publicProfile.Bio != "Building in the open" || len(publicProfile.Showcase) != 1 || publicProfile.Showcase[0].ID != publicRepo.ID || len(publicProfile.Repositories) != 1 {
		t.Fatalf("public profile exposed wrong repositories: %+v", publicProfile)
	}
	owner.request("PUT", "/users/owner/follow", map[string]bool{"followed": true}, 422, nil)
	anon.request("PUT", "/users/owner/follow", map[string]bool{"followed": true}, 401, nil)
	other.request("PUT", "/users/owner/follow", map[string]bool{"followed": true}, 200, &publicProfile)
	other.request("PUT", "/users/owner/follow", map[string]bool{"followed": true}, 200, &publicProfile)
	if publicProfile.Followers != 1 || !publicProfile.Followed {
		t.Fatalf("follow state was not saved idempotently: %+v", publicProfile)
	}
	var feed []FeedEvent
	other.request("GET", "/user/feed", nil, 200, &feed)
	if len(feed) < 2 {
		t.Fatalf("following feed missed public repository and Spark activity: %+v", feed)
	}
	for _, event := range feed {
		if event.Repository == "project" {
			t.Fatalf("private repository leaked into following feed: %+v", feed)
		}
	}
	anon.request("GET", "/user/feed", nil, 401, nil)
	anon.request("GET", "/users/owner/profile", nil, 200, &publicProfile)
	if publicProfile.Followers != 1 || publicProfile.Followed {
		t.Fatalf("anonymous viewer follow state is wrong: %+v", publicProfile)
	}
	other.request("PUT", "/users/owner/follow", map[string]bool{"followed": false}, 200, &publicProfile)
	if publicProfile.Followers != 0 || publicProfile.Followed {
		t.Fatalf("unfollow failed: %+v", publicProfile)
	}
	other.request("GET", "/user/feed", nil, 200, &feed)
	if len(feed) != 0 {
		t.Fatalf("unfollowed builder remained in feed: %+v", feed)
	}
	owner.request("PATCH", "/repos/owner/public-project", map[string]string{"description": "Private for now", "visibility": "private"}, 200, nil)
	anon.request("GET", "/users/owner/profile", nil, 200, &publicProfile)
	if len(publicProfile.Showcase) != 0 || len(publicProfile.Repositories) != 0 {
		t.Fatalf("private repository remained visible on profile: %+v", publicProfile)
	}
	owner.request("PATCH", "/repos/owner/public-project", map[string]string{"description": "Public again", "visibility": "public"}, 200, nil)
	anon.request("GET", "/users/missing/profile", nil, 404, nil)
	anon.request("GET", "/repos/owner/public-project", nil, 200, nil)
	other.request("POST", "/repos/owner/public-project/issues", map[string]string{"title": "Unauthorized"}, 403, nil)
	other.request("POST", "/repos/owner/public-project/labels", map[string]string{"name": "Unauthorized", "color": "ffffff"}, 403, nil)
	other.request("POST", "/repos/owner/public-project/milestones", map[string]string{"title": "Unauthorized"}, 403, nil)
	owner.request("POST", "/repos/owner/public-project/issues", map[string]string{"title": "Public issue"}, 201, nil)
	owner.request("PUT", "/repos/owner/public-project/issues/1/milestone", map[string]any{"milestone_id": milestone.ID}, 422, nil)
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
