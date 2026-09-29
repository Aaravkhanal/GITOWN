package app

import (
	"context"
	"encoding/base64"
	"fmt"
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

func TestCollaborationDepth(t *testing.T) {
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
	a, err := New(config.Config{DataDir: t.TempDir(), Origin: "http://localhost:3000", GitURL: "http://localhost/git", Signup: true}, pool)
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
	other.request("POST", "/auth/register", map[string]string{"username": "other", "email": "other@example.test", "password": "other-long-password", "display_name": "Other"}, 201, nil)

	owner.request("PUT", "/user/profile", map[string]any{"display_name": "Owner", "bio": "Builds things", "website": "", "location": "", "skills": "Go", "availability": "Evenings", "open_to_collaborators": true}, 200, nil)
	var profile Profile
	other.request("GET", "/users/owner/profile", nil, 200, &profile)
	if !profile.OpenToCollaborators || profile.Skills != "Go" || profile.Availability != "Evenings" {
		t.Fatalf("profile fields were not saved: %+v", profile)
	}
	var builders BuilderSearch
	other.request("GET", "/search/builders?available=1", nil, 200, &builders)
	if len(builders.Items) != 1 || !builders.Items[0].OpenToCollaborators {
		t.Fatalf("open collaborators were not searchable: %+v", builders.Items)
	}
	owner.request("PUT", "/user/email-notifications", map[string]string{"email_notifications": "off"}, 200, nil)

	var repo Repository
	owner.request("POST", "/repos", map[string]any{"name": "depth", "description": "Depth", "visibility": "public", "readme": true}, 201, &repo)
	other.request("GET", "/users/owner/profile", nil, 200, &profile)
	if len(profile.Badges) == 0 || profile.Contributions.PublicRepositories != 1 {
		t.Fatalf("contribution badge missing: %+v", profile)
	}
	owner.request("PUT", "/repos/owner/depth/presentation", map[string]string{"homepage": "https://example.com", "stack": "Go"}, 200, nil)
	var shown struct {
		Repository Repository `json:"repository"`
	}
	owner.request("GET", "/repos/owner/depth", nil, 200, &shown)
	repo = shown.Repository
	if repo.Homepage != "https://example.com" || repo.Stack != "Go" {
		t.Fatalf("presentation was not saved: %+v", repo)
	}
	owner.request("PUT", "/repos/owner/depth/issue-templates", map[string]any{"templates": []map[string]string{{"name": "Bug", "title": "Bug", "body": "Steps", "kind": "bug"}}}, 200, nil)
	var templates []IssueTemplate
	owner.request("GET", "/repos/owner/depth/issue-templates", nil, 200, &templates)
	if len(templates) != 1 || templates[0].Kind != "bug" {
		t.Fatalf("template kind was not saved: %+v", templates)
	}

	var issue Issue
	owner.request("POST", "/repos/owner/depth/issues", map[string]string{"title": "First", "body": "Track this"}, 201, &issue)
	var comment IssueComment
	owner.request("POST", "/repos/owner/depth/issues/1/comments", map[string]string{"body": "See #1 later"}, 201, &comment)
	owner.request("PATCH", "/repos/owner/depth/issues/1/comments/"+comment.ID, map[string]string{"body": "Edited. See #1"}, 200, nil)
	var history []map[string]any
	owner.request("GET", "/repos/owner/depth/issues/1/comments/"+comment.ID+"/history", nil, 200, &history)
	if len(history) != 1 || history[0]["body"] != "See #1 later" {
		t.Fatalf("comment history missing: %+v", history)
	}
	other.request("PATCH", "/repos/owner/depth/issues/1/comments/"+comment.ID, map[string]string{"body": "nope"}, 403, nil)
	var references []map[string]any
	owner.request("GET", "/repos/owner/depth/issues/1/references", nil, 200, &references)
	if len(references) != 0 {
		t.Fatalf("an issue should not list itself: %+v", references)
	}
	owner.request("PUT", "/repos/owner/depth/issues/1/planning", map[string]any{"pinned": true, "priority": "high", "estimate": 3, "iteration": "now", "due_date": "2026-10-01"}, 200, nil)
	var issues []Issue
	owner.request("GET", "/repos/owner/depth/issues?q=First", nil, 200, &issues)
	if len(issues) != 1 || !issues[0].Pinned || issues[0].Priority != "high" || issues[0].Iteration != "now" {
		t.Fatalf("planning filters failed: %+v", issues)
	}
	var board []BoardItem
	owner.request("GET", "/repos/owner/depth/board", nil, 200, &board)
	if len(board) != 1 || board[0].Priority != "high" || board[0].Iteration != "now" {
		t.Fatalf("board planning fields missing: %+v", board)
	}
	owner.request("POST", "/user/saved-searches", map[string]string{"name": "open bugs", "query": "is:open"}, 201, nil)
	var saved []map[string]any
	owner.request("GET", "/user/saved-searches", nil, 200, &saved)
	if len(saved) != 1 {
		t.Fatalf("saved search missing: %+v", saved)
	}
	var work WorkSearch
	owner.request("GET", "/search/work?q=First", nil, 200, &work)
	if len(work.Items) != 1 || work.Items[0].Kind != "issue" {
		t.Fatalf("work search missed the issue: %+v", work.Items)
	}

	other.request("PUT", "/repos/owner/depth/issues/1/subscription", map[string]any{"subscribed": true, "mode": "participate"}, 200, nil)
	owner.request("POST", "/repos/owner/depth/issues/1/comments", map[string]string{"body": "Hello @other"}, 201, nil)
	var notes []Notification
	other.request("GET", "/user/notifications", nil, 200, &notes)
	if len(notes) == 0 {
		t.Fatal("expected a notification before ignore")
	}
	before := len(notes)
	other.request("PUT", "/repos/owner/depth/issues/1/subscription", map[string]any{"subscribed": true, "mode": "ignore"}, 200, nil)
	owner.request("POST", "/repos/owner/depth/issues/1/comments", map[string]string{"body": "Ignored update"}, 201, nil)
	other.request("GET", "/user/notifications", nil, 200, &notes)
	if len(notes) != before {
		t.Fatalf("ignore mode still notified: before %d after %d", before, len(notes))
	}

	var invitation struct {
		ID string `json:"id"`
	}
	owner.request("POST", "/repos/owner/depth/invitations", map[string]string{"username": "other", "role": "write"}, 201, &invitation)
	other.request("POST", "/user/invitations/"+invitation.ID+"/accept", map[string]any{}, 200, nil)
	var memberView struct {
		Repository Repository `json:"repository"`
	}
	other.request("GET", "/repos/owner/depth", nil, 200, &memberView)
	if !memberView.Repository.CanWrite {
		t.Fatal("accepted invitation did not grant write")
	}

	var second Repository
	owner.request("POST", "/repos", map[string]any{"name": "otherbox", "visibility": "public", "readme": true}, 201, &second)
	var movable Issue
	owner.request("POST", "/repos/owner/depth/issues", map[string]string{"title": "Move me", "body": ""}, 201, &movable)
	var moved struct {
		Number int `json:"number"`
	}
	owner.request("POST", fmt.Sprintf("/repos/owner/depth/issues/%d/transfer", movable.Number), map[string]string{"repository": "otherbox"}, 200, &moved)
	if moved.Number < 1 {
		t.Fatalf("issue was not transferred: %+v", moved)
	}

	writeToken := owner.token("repo:write")
	repoURL := server.URL + "/git/owner/depth.git"
	workDir := filepath.Join(t.TempDir(), "work")
	gitRun := func(dir string, ok bool, args ...string) {
		t.Helper()
		argv := []string{"-c", "credential.helper=", "-c", "http.extraHeader=Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("owner:"+writeToken))}
		cmd := exec.Command("git", append(argv, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, gitErr := cmd.CombinedOutput()
		if ok && gitErr != nil {
			t.Fatalf("git %s failed: %s", args[0], out)
		}
		if !ok && gitErr == nil {
			t.Fatalf("git %s unexpectedly succeeded: %s", args[0], out)
		}
	}
	gitRun("", true, "clone", repoURL, workDir)
	gitRun(workDir, true, "config", "user.name", "Owner")
	gitRun(workDir, true, "config", "user.email", "owner@example.test")
	gitRun(workDir, true, "checkout", "-b", "feature")
	if err = os.WriteFile(filepath.Join(workDir, "note.txt"), []byte("note\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(workDir, true, "add", "note.txt")
	gitRun(workDir, true, "commit", "-m", "Add note")
	gitRun(workDir, true, "push", "-u", "origin", "feature")

	var pull Pull
	owner.request("POST", "/repos/owner/depth/pulls", map[string]any{"title": "Add note", "body": "Fixes #1", "base_branch": "main", "head_branch": "feature", "draft": true}, 201, &pull)
	var detail struct {
		HeadSHA string `json:"head_sha"`
		BaseSHA string `json:"base_sha"`
	}
	pullPath := fmt.Sprintf("/repos/owner/depth/pulls/%d", pull.Number)
	owner.request("GET", pullPath, nil, 200, &detail)
	owner.request("POST", pullPath+"/merge", map[string]string{"head_sha": detail.HeadSHA, "base_sha": detail.BaseSHA}, 409, nil)
	owner.request("PATCH", pullPath, map[string]any{"draft": false}, 200, nil)
	var thread struct {
		ID string `json:"id"`
	}
	owner.request("POST", pullPath+"/threads", map[string]any{"commit_sha": detail.HeadSHA, "path": "note.txt", "side": "right", "line": 1, "body": "Looks good"}, 201, &thread)
	owner.request("PUT", "/repos/owner/depth/branch-rules?branch=main", map[string]any{"required_approvals": 0, "require_resolved": true, "required_checks": []string{"ci"}}, 200, nil)
	owner.request("POST", pullPath+"/merge", map[string]any{"head_sha": detail.HeadSHA, "base_sha": detail.BaseSHA, "method": "squash"}, 409, nil)
	owner.request("POST", pullPath+"/threads/"+thread.ID+"/resolve", map[string]any{"resolved": true}, 200, nil)
	owner.request("POST", pullPath+"/merge", map[string]any{"head_sha": detail.HeadSHA, "base_sha": detail.BaseSHA, "method": "squash"}, 409, nil)
	owner.request("POST", "/repos/owner/depth/commits/"+detail.HeadSHA+"/status", map[string]string{"context": "ci", "state": "success", "description": "ok"}, 200, nil)
	owner.request("POST", pullPath+"/merge", map[string]any{"head_sha": detail.HeadSHA, "base_sha": detail.BaseSHA, "method": "squash", "delete_branch": true}, 200, nil)
	owner.request("GET", "/repos/owner/depth/issues?state=closed", nil, 200, &issues)
	closed := false
	for _, item := range issues {
		if item.Number == 1 && item.State == "closed" {
			closed = true
		}
	}
	if !closed {
		t.Fatalf("unite request did not close issue 1: %+v", issues)
	}
}
