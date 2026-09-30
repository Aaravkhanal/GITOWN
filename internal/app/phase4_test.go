package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/Aaravkhanal/GITOWN/internal/config"
	"github.com/Aaravkhanal/GITOWN/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func phase4Server(t *testing.T) *httptest.Server {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := "gitown_test_" + strings.ReplaceAll(auth.ID(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE") })
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	a, err := New(config.Config{DataDir: t.TempDir(), Origin: "http://localhost:3000", GitURL: "http://localhost/git", Signup: true}, pool)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(a.Handler())
	t.Cleanup(server.Close)
	return server
}

func phase4Client(t *testing.T, server *httptest.Server) testClient {
	jar, _ := cookiejar.New(nil)
	return testClient{t, server.URL, &http.Client{Jar: jar}}
}

// headers performs a GET and returns the decoded body and response headers.
func (c testClient) headers(path string, out any) http.Header {
	c.t.Helper()
	req, err := http.NewRequest("GET", c.url+"/api/v1"+path, nil)
	if err != nil {
		c.t.Fatal(err)
	}
	res, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		c.t.Fatalf("GET %s: expected 200 got %d", path, res.StatusCode)
	}
	if err = json.NewDecoder(res.Body).Decode(out); err != nil {
		c.t.Fatal(err)
	}
	return res.Header
}

func issueNumbers(items []Issue) []int {
	numbers := []int{}
	for _, item := range items {
		numbers = append(numbers, item.Number)
	}
	return numbers
}

func TestIssueTrackingDepth(t *testing.T) {
	server := phase4Server(t)
	owner, other, anon := phase4Client(t, server), phase4Client(t, server), phase4Client(t, server)
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)
	other.request("POST", "/auth/register", map[string]string{"username": "other", "email": "other@example.test", "password": "other-long-password", "display_name": "Other"}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "plan", "visibility": "public", "readme": true}, 201, nil)
	base := "/repos/owner/plan"

	// Visitors can report issues and manage only their own.
	anon.request("POST", base+"/issues", map[string]string{"title": "Anonymous"}, 401, nil)
	var visitor Issue
	other.request("POST", base+"/issues", map[string]string{"title": "Visitor report", "body": "Something broke"}, 201, &visitor)
	owner.request("POST", base+"/issues", map[string]string{"title": "Search is slow", "body": "Profiling needed"}, 201, nil)
	owner.request("POST", base+"/issues", map[string]string{"title": "Search results wrong"}, 201, nil)
	other.request("PATCH", base+"/issues/2", map[string]string{"state": "closed"}, 403, nil)
	other.request("PATCH", base+"/issues/1", map[string]string{"title": "Visitor report (edited)"}, 200, nil)
	other.request("PATCH", base+"/issues/1", map[string]string{"state": "closed", "state_reason": "not_planned"}, 200, nil)
	other.request("PUT", base+"/issues/1/planning", map[string]any{"pinned": true}, 403, nil)
	var detail Issue
	anon.request("GET", base+"/issues/1", nil, 200, &detail)
	if detail.State != "closed" || detail.StateReason != "not_planned" || detail.Title != "Visitor report (edited)" {
		t.Fatalf("author edit or close was not saved: %+v", detail)
	}

	// Multi-label, milestone, author, and assignee filters with pagination.
	var bug, ui Label
	owner.request("POST", base+"/labels", map[string]string{"name": "bug", "color": "d73a4a"}, 201, &bug)
	owner.request("POST", base+"/labels", map[string]string{"name": "ui", "color": "0e8a16"}, 201, &ui)
	owner.request("POST", base+"/issues/2/labels", map[string]string{"label_id": bug.ID}, 200, nil)
	owner.request("POST", base+"/issues/2/labels", map[string]string{"label_id": ui.ID}, 200, nil)
	owner.request("POST", base+"/issues/3/labels", map[string]string{"label_id": bug.ID}, 200, nil)
	var milestone Milestone
	owner.request("POST", base+"/milestones", map[string]string{"title": "Beta"}, 201, &milestone)
	owner.request("PUT", base+"/issues/3/milestone", map[string]any{"milestone_id": milestone.ID}, 200, nil)
	var listed []Issue
	owner.request("GET", base+"/issues?state=open&label=bug&label=UI", nil, 200, &listed)
	if fmt.Sprint(issueNumbers(listed)) != "[2]" || len(listed[0].Labels) != 2 {
		t.Fatalf("label AND filter failed: %+v", listed)
	}
	owner.request("GET", base+"/issues?milestone=beta", nil, 200, &listed)
	if fmt.Sprint(issueNumbers(listed)) != "[3]" || listed[0].Milestone == nil || *listed[0].Milestone != "Beta" {
		t.Fatalf("milestone filter failed: %+v", listed)
	}
	owner.request("GET", base+"/issues?author=other", nil, 200, &listed)
	if fmt.Sprint(issueNumbers(listed)) != "[1]" {
		t.Fatalf("author filter failed: %+v", listed)
	}
	owner.request("GET", base+"/issues?assignee=none&state=all", nil, 200, &listed)
	if len(listed) != 3 {
		t.Fatalf("unassigned filter failed: %+v", listed)
	}
	header := owner.headers(base+"/issues?state=open&per_page=1&page=2", &listed)
	if fmt.Sprint(issueNumbers(listed)) != "[2]" || header.Get("X-Total-Count") != "2" || header.Get("X-Open-Count") != "2" || header.Get("X-Closed-Count") != "1" {
		t.Fatalf("pagination failed: %v %+v", header, listed)
	}
	owner.request("GET", base+"/issues?per_page=500", nil, 422, nil)
	owner.request("GET", base+"/issues?label=a&label=b&label=c&label=d&label=e&label=f", nil, 422, nil)

	// Comments report whether the viewer may edit them and whether they were edited.
	var comment IssueComment
	other.request("POST", base+"/issues/2/comments", map[string]string{"body": "I can reproduce this."}, 201, &comment)
	other.request("PATCH", base+"/issues/2/comments/"+comment.ID, map[string]string{"body": "I can reproduce this on main."}, 200, nil)
	var comments []IssueComment
	owner.request("GET", base+"/issues/2/comments", nil, 200, &comments)
	if len(comments) != 1 || comments[0].Editable || !comments[0].Edited || comments[0].UpdatedAt == nil {
		t.Fatalf("owner should see an edited, read-only comment: %+v", comments)
	}
	other.request("GET", base+"/issues/2/comments", nil, 200, &comments)
	if !comments[0].Editable {
		t.Fatalf("author should be able to edit: %+v", comments)
	}

	// Structured issue forms and built-in bug and feature forms.
	owner.request("PUT", base+"/issue-templates", map[string]any{"templates": []map[string]any{{
		"name": "Crash", "title": "Crash: ", "kind": "bug", "fields": []map[string]any{
			{"id": "steps", "label": "Steps", "type": "textarea", "required": true},
			{"id": "platform", "label": "Platform", "type": "dropdown", "options": []string{"Linux", "macOS"}},
		},
	}}}, 200, nil)
	owner.request("PUT", base+"/issue-templates", map[string]any{"templates": []map[string]any{{
		"name": "Broken", "fields": []map[string]any{{"id": "Bad ID", "label": "x", "type": "text"}},
	}}}, 422, nil)
	var templates []IssueTemplate
	owner.request("GET", base+"/issue-templates", nil, 200, &templates)
	if len(templates) != 1 || len(templates[0].Fields) != 2 {
		t.Fatalf("stored template fields missing: %+v", templates)
	}
	owner.request("GET", base+"/issue-templates?include_defaults=true", nil, 200, &templates)
	if len(templates) != 2 || templates[1].Name != "Feature request" || !templates[1].Builtin {
		t.Fatalf("feature form should be offered because no feature template exists: %+v", templates)
	}
	other.request("POST", base+"/issues", map[string]any{"title": "Crash on start", "template": "Crash", "fields": map[string]string{"platform": "Linux"}}, 422, nil)
	other.request("POST", base+"/issues", map[string]any{"title": "Crash on start", "template": "Crash", "fields": map[string]string{"steps": "Run it", "platform": "Windows"}}, 422, nil)
	other.request("POST", base+"/issues", map[string]any{"title": "Crash on start", "template": "Crash", "fields": map[string]string{"steps": "Run it", "extra": "x"}}, 422, nil)
	var crash Issue
	other.request("POST", base+"/issues", map[string]any{"title": "Crash on start", "template": "Crash", "body": "Happens daily", "fields": map[string]string{"steps": "Run it", "platform": "Linux"}}, 201, &crash)
	if !strings.Contains(crash.Body, "### Steps\n\nRun it") || !strings.Contains(crash.Body, "### Platform\n\nLinux") || !strings.Contains(crash.Body, "### Additional context\n\nHappens daily") {
		t.Fatalf("form body was not composed: %q", crash.Body)
	}
	var feature Issue
	other.request("POST", base+"/issues", map[string]any{"title": "Dark mode", "template": "Feature request", "fields": map[string]string{"problem": "Too bright", "proposal": "Add a theme", "contribute": "true"}}, 201, &feature)
	if !strings.Contains(feature.Body, "- [x] I would like to help build this") {
		t.Fatalf("built-in form body was not composed: %q", feature.Body)
	}

	// Duplicates close the issue, reject cycles, and reopen when cleared.
	owner.request("PUT", base+"/issues/3/planning", map[string]any{"duplicate_of": 3}, 422, nil)
	owner.request("PUT", base+"/issues/3/planning", map[string]any{"duplicate_of": 2}, 200, nil)
	owner.request("GET", base+"/issues/3", nil, 200, &detail)
	if detail.State != "closed" || detail.StateReason != "duplicate" || detail.DuplicateOf == nil || *detail.DuplicateOf != 2 {
		t.Fatalf("duplicate was not closed: %+v", detail)
	}
	owner.request("PUT", base+"/issues/2/planning", map[string]any{"duplicate_of": 3}, 422, nil)
	owner.request("PUT", base+"/issues/3/planning", map[string]any{"clear_duplicate": true}, 200, nil)
	owner.request("GET", base+"/issues/3", nil, 200, &detail)
	if detail.State != "open" || detail.DuplicateOf != nil {
		t.Fatalf("clearing the duplicate should reopen: %+v", detail)
	}
	owner.request("PUT", base+"/issues/2/planning", map[string]any{"estimate": 5}, 200, nil)
	owner.request("PUT", base+"/issues/2/planning", map[string]any{"clear_estimate": true}, 200, nil)
	owner.request("GET", base+"/issues/2", nil, 200, &detail)
	if detail.Estimate != nil {
		t.Fatalf("estimate was not cleared: %+v", detail)
	}

	// Sub-issues with progress and cycle protection.
	var child Issue
	other.request("POST", base+"/issues", map[string]any{"title": "Child", "parent": 2}, 403, nil)
	owner.request("POST", base+"/issues", map[string]any{"title": "Index titles", "parent": 2}, 201, &child)
	owner.request("PUT", base+fmt.Sprintf("/issues/%d/parent", crash.Number), map[string]any{"parent": 2}, 200, nil)
	owner.request("PUT", base+"/issues/2/parent", map[string]any{"parent": child.Number}, 422, nil)
	owner.request("PUT", base+"/issues/2/parent", map[string]any{"parent": 2}, 422, nil)
	owner.request("PATCH", base+fmt.Sprintf("/issues/%d", child.Number), map[string]string{"state": "closed"}, 200, nil)
	var subs SubIssues
	owner.request("GET", base+"/issues/2/sub-issues", nil, 200, &subs)
	if subs.Total != 2 || subs.Closed != 1 || subs.Parent != nil {
		t.Fatalf("sub-issue progress wrong: %+v", subs)
	}
	owner.request("GET", base+fmt.Sprintf("/issues/%d/sub-issues", child.Number), nil, 200, &subs)
	if subs.Parent == nil || subs.Parent.Number != 2 {
		t.Fatalf("parent link missing: %+v", subs)
	}
	owner.request("GET", base+"/issues/2", nil, 200, &detail)
	if detail.SubIssues.Total != 2 || detail.SubIssues.Closed != 1 {
		t.Fatalf("issue JSON lacks sub-issue progress: %+v", detail)
	}
	owner.request("PUT", base+fmt.Sprintf("/issues/%d/parent", crash.Number), map[string]any{"parent": nil}, 200, nil)

	// Cross-references are stored, listed both ways, and rewritten on edit.
	var mention Issue
	owner.request("POST", base+"/issues", map[string]string{"title": "Follow-up", "body": "Related to #2 and #999"}, 201, &mention)
	var refs IssueReferences
	owner.request("GET", base+"/issues/2/references", nil, 200, &refs)
	if len(refs.ReferencedBy) != 1 || refs.ReferencedBy[0].Number != mention.Number || refs.ReferencedBy[0].Kind != "issue" {
		t.Fatalf("referenced-by missing: %+v", refs)
	}
	owner.request("GET", base+fmt.Sprintf("/issues/%d/references", mention.Number), nil, 200, &refs)
	if len(refs.Mentions) != 1 || refs.Mentions[0].Number != 2 {
		t.Fatalf("mentions missing: %+v", refs)
	}
	var note IssueComment
	other.request("POST", base+"/issues/3/comments", map[string]string{"body": "Probably caused by owner/plan#2"}, 201, &note)
	owner.request("GET", base+"/issues/2/references", nil, 200, &refs)
	if len(refs.ReferencedBy) != 2 {
		t.Fatalf("comment reference missing: %+v", refs)
	}
	other.request("PATCH", base+"/issues/3/comments/"+note.ID, map[string]string{"body": "Never mind."}, 200, nil)
	owner.request("GET", base+"/issues/2/references", nil, 200, &refs)
	if len(refs.ReferencedBy) != 1 {
		t.Fatalf("edited comment reference should be removed: %+v", refs)
	}

	// Transfers renumber the issue, drop repository data, and leave a pointer.
	owner.request("POST", "/repos", map[string]any{"name": "target", "visibility": "public"}, 201, nil)
	other.request("POST", base+fmt.Sprintf("/issues/%d/transfer", mention.Number), map[string]string{"repository": "owner/target"}, 403, nil)
	owner.request("POST", base+fmt.Sprintf("/issues/%d/transfer", mention.Number), map[string]string{"repository": "owner/missing"}, 422, nil)
	var moved struct {
		Owner      string `json:"owner"`
		Repository string `json:"repository"`
		Number     int    `json:"number"`
	}
	owner.request("POST", base+fmt.Sprintf("/issues/%d/transfer", mention.Number), map[string]string{"repository": "owner/target"}, 200, &moved)
	if moved.Repository != "target" || moved.Number != 1 {
		t.Fatalf("transfer response wrong: %+v", moved)
	}
	var failure struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	anon.request("GET", base+fmt.Sprintf("/issues/%d", mention.Number), nil, 404, &failure)
	if failure.Error.Code != "issue_transferred" || !strings.Contains(failure.Error.Message, "owner/target#1") {
		t.Fatalf("transferred issue should point to its new home: %+v", failure)
	}
	owner.request("GET", base+"/issues/2/references", nil, 200, &refs)
	if len(refs.ReferencedBy) != 0 {
		t.Fatalf("references from a transferred issue should be dropped: %+v", refs)
	}
	owner.request("GET", "/repos/owner/target/issues/1/comments", nil, 200, &comments)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "Transferred from owner/plan#") {
		t.Fatalf("transfer note missing: %+v", comments)
	}

	// Closing keywords in commits on the default branch close issues.
	var commits []struct {
		SHA string `json:"sha"`
	}
	owner.request("GET", base+"/commits", nil, 200, &commits)
	owner.request("PUT", base+"/contents", map[string]string{"branch": "main", "path": "NOTES.md", "content": "notes\n", "message": "Add notes\n\nFixes #3 and prefix #2", "expected_head": commits[0].SHA}, 201, nil)
	owner.request("GET", base+"/issues/3", nil, 200, &detail)
	if detail.State != "closed" || detail.StateReason != "completed" {
		t.Fatalf("commit did not close #3: %+v", detail)
	}
	owner.request("GET", base+"/issues/2", nil, 200, &detail)
	if detail.State != "open" {
		t.Fatalf("\"prefix #2\" must not close #2: %+v", detail)
	}
	owner.request("GET", base+"/issues/3/comments", nil, 200, &comments)
	if !strings.Contains(comments[len(comments)-1].Body, "Closed by commit") {
		t.Fatalf("closing note missing: %+v", comments)
	}

	// Saved searches still work for the issue filter bar.
	owner.request("POST", "/user/saved-searches", map[string]string{"name": "Plan bugs", "query": "repo=owner/plan&state=open&label=bug"}, 201, nil)
}

func TestConfigurableBoards(t *testing.T) {
	server := phase4Server(t)
	owner, other := phase4Client(t, server), phase4Client(t, server)
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)
	other.request("POST", "/auth/register", map[string]string{"username": "other", "email": "other@example.test", "password": "other-long-password", "display_name": "Other"}, 201, nil)
	owner.request("POST", "/districts", map[string]string{"slug": "acme", "name": "Acme", "visibility": "public"}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "app", "visibility": "public", "district": "acme"}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "site", "visibility": "public", "district": "acme"}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "secret", "visibility": "private", "district": "acme"}, 201, nil)
	base := "/repos/owner/app"
	owner.request("POST", base+"/issues", map[string]string{"title": "Design the API"}, 201, nil)
	owner.request("POST", base+"/issues", map[string]string{"title": "Write docs"}, 201, nil)
	owner.request("POST", "/repos/owner/site/issues", map[string]string{"title": "Landing page"}, 201, nil)
	owner.request("POST", "/repos/owner/secret/issues", map[string]string{"title": "Hidden work"}, 201, nil)

	var settings BoardSettings
	owner.request("GET", base+"/board/settings", nil, 200, &settings)
	if len(settings.Columns) != 3 || settings.Columns[2].Key != "done" || !settings.Automation {
		t.Fatalf("default board settings wrong: %+v", settings)
	}
	owner.request("PUT", base+"/board/settings", map[string]any{"columns": []BoardColumn{{"done", "Done"}, {"todo", "To do"}}, "automation": true}, 422, nil)
	owner.request("PUT", base+"/board/settings", map[string]any{"columns": []BoardColumn{{"todo", "To do"}, {"todo", "Again"}, {"done", "Done"}}, "automation": true}, 422, nil)
	other.request("PUT", base+"/board/settings", map[string]any{"columns": []BoardColumn{{"todo", "To do"}, {"done", "Done"}}, "automation": true}, 403, nil)
	owner.request("PUT", base+"/issues/1/board", map[string]string{"status": "progress"}, 200, nil)
	owner.request("PUT", base+"/board/settings", map[string]any{"columns": []BoardColumn{{"backlog", "Backlog"}, {"review", "In review"}, {"done", "Shipped"}}, "automation": false}, 200, &settings)
	if settings.Columns[1].Name != "In review" || settings.Automation {
		t.Fatalf("board settings were not saved: %+v", settings)
	}
	var board []BoardItem
	owner.request("GET", base+"/board", nil, 200, &board)
	if len(board) != 2 || board[0].Status != "backlog" || board[1].Status != "backlog" {
		t.Fatalf("items in removed columns should move to the first column: %+v", board)
	}
	owner.request("PUT", base+"/issues/1/board", map[string]string{"status": "progress"}, 422, nil)
	owner.request("PUT", base+"/issues/1/board", map[string]string{"status": "review"}, 200, nil)
	owner.request("PUT", base+"/issues/1/board", map[string]string{"status": "done"}, 200, nil)
	owner.request("PATCH", base+"/issues/1", map[string]string{"state": "open"}, 200, nil)
	owner.request("GET", base+"/board", nil, 200, &board)
	for _, item := range board {
		if item.Number == 1 && (item.Status != "backlog" || item.State != "open") {
			t.Fatalf("reopening should return the issue to the first column: %+v", item)
		}
	}

	// Custom fields.
	var size, points, due BoardField
	owner.request("POST", base+"/board/fields", map[string]any{"name": "Size", "kind": "single_select", "options": []string{"S", "M", "L"}}, 201, &size)
	owner.request("POST", base+"/board/fields", map[string]any{"name": "Points", "kind": "number"}, 201, &points)
	owner.request("POST", base+"/board/fields", map[string]any{"name": "Ship date", "kind": "date"}, 201, &due)
	owner.request("POST", base+"/board/fields", map[string]any{"name": "size", "kind": "text"}, 409, nil)
	owner.request("POST", base+"/board/fields", map[string]any{"name": "Empty", "kind": "single_select", "options": []string{}}, 422, nil)
	owner.request("POST", base+"/board/fields", map[string]any{"name": "Color", "kind": "color"}, 422, nil)
	owner.request("PUT", base+"/board/values", map[string]any{"kind": "issue", "number": 1, "field_id": size.ID, "value": "XL"}, 422, nil)
	owner.request("PUT", base+"/board/values", map[string]any{"kind": "issue", "number": 1, "field_id": points.ID, "value": "many"}, 422, nil)
	owner.request("PUT", base+"/board/values", map[string]any{"kind": "issue", "number": 1, "field_id": due.ID, "value": "2026-13-01"}, 422, nil)
	owner.request("PUT", base+"/board/values", map[string]any{"kind": "issue", "number": 1, "field_id": size.ID, "value": "M"}, 200, nil)
	owner.request("PUT", base+"/board/values", map[string]any{"kind": "issue", "number": 1, "field_id": points.ID, "value": "3"}, 200, nil)
	owner.request("PUT", base+"/board/values", map[string]any{"kind": "issue", "number": 2, "field_id": due.ID, "value": "2026-11-01"}, 200, nil)
	other.request("PUT", base+"/board/values", map[string]any{"kind": "issue", "number": 1, "field_id": size.ID, "value": "S"}, 403, nil)
	owner.request("GET", base+"/board", nil, 200, &board)
	values := map[int]map[string]string{}
	for _, item := range board {
		values[item.Number] = item.Fields
	}
	if values[1][size.ID] != "M" || values[1][points.ID] != "3" || values[2][due.ID] != "2026-11-01" {
		t.Fatalf("field values missing from board: %+v", board)
	}
	owner.request("PATCH", base+"/board/fields/"+size.ID, map[string]any{"name": "T-shirt", "options": []string{"S", "L"}}, 200, nil)
	board = nil
	owner.request("GET", base+"/board", nil, 200, &board)
	for _, item := range board {
		if _, kept := item.Fields[size.ID]; kept && item.Number == 1 {
			t.Fatalf("value for a removed option should be cleared: %+v", item)
		}
	}
	owner.request("PUT", base+"/board/values", map[string]any{"kind": "issue", "number": 1, "field_id": points.ID, "value": ""}, 200, nil)
	owner.request("DELETE", base+"/board/fields/"+due.ID, nil, 200, nil)
	owner.request("GET", base+"/board/settings", nil, 200, &settings)
	if len(settings.Fields) != 2 || settings.Fields[0].Name != "T-shirt" {
		t.Fatalf("field list wrong: %+v", settings.Fields)
	}
	owner.request("PUT", base+"/pulls/7/board", map[string]string{"status": "review"}, 404, nil)
	owner.request("DELETE", base+"/pulls/7/board", nil, 404, nil)

	// District boards aggregate readable repositories only.
	var district struct {
		Columns []BoardColumn `json:"columns"`
		Items   []BoardItem   `json:"items"`
		Total   int           `json:"total"`
	}
	other.request("GET", "/districts/acme/board", nil, 200, &district)
	if district.Total != 3 || len(district.Items) != 3 {
		t.Fatalf("district board should hide private repositories from outsiders: %+v", district)
	}
	for _, item := range district.Items {
		if item.Repository == "secret" {
			t.Fatalf("private repository leaked: %+v", item)
		}
	}
	owner.request("GET", "/districts/acme/board", nil, 200, &district)
	if district.Total != 4 || district.Columns[len(district.Columns)-1].Key != "done" || !columnKnown(district.Columns, "backlog") || !columnKnown(district.Columns, "todo") {
		t.Fatalf("district board columns or items wrong: %+v", district)
	}
	owner.request("GET", "/districts/acme/board?per_page=2", nil, 200, &district)
	if len(district.Items) != 2 || district.Total != 4 {
		t.Fatalf("district board pagination failed: %+v", district)
	}
}

func TestPushClosesIssuesAndBoardAutomation(t *testing.T) {
	server := phase4Server(t)
	owner := phase4Client(t, server)
	owner.request("POST", "/auth/register", map[string]string{"username": "owner", "email": "owner@example.test", "password": "owner-long-password", "display_name": "Owner"}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "flow", "visibility": "public", "readme": true}, 201, nil)
	base := "/repos/owner/flow"
	owner.request("POST", base+"/issues", map[string]string{"title": "Crash on launch"}, 201, nil)
	owner.request("POST", base+"/issues", map[string]string{"title": "Blocked work"}, 201, nil)
	owner.request("POST", base+"/issues", map[string]string{"title": "Blocker"}, 201, nil)
	owner.request("PUT", base+"/issues/2/dependencies", map[string]any{"blocked_by": []int{3}}, 200, nil)
	token := owner.token("repo:write")
	work := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		credentials := base64.StdEncoding.EncodeToString([]byte("owner:" + token))
		argv := append([]string{"-c", "credential.helper=", "-c", "http.extraHeader=Authorization: Basic " + credentials, "-c", "user.name=Owner", "-c", "user.email=owner@example.test"}, args...)
		cmd := exec.Command("git", argv...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("clone", server.URL+"/git/owner/flow.git", ".")
	if err := os.WriteFile(work+"/fix.txt", []byte("fixed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "fix.txt")
	run("commit", "-m", "Handle launch crash", "-m", "Closes #1. Also fixes #2 once unblocked.")
	run("push", "origin", "HEAD:main")
	var detail Issue
	owner.request("GET", base+"/issues/1", nil, 200, &detail)
	if detail.State != "closed" {
		t.Fatalf("push to the default branch did not close #1: %+v", detail)
	}
	owner.request("GET", base+"/issues/2", nil, 200, &detail)
	if detail.State != "open" {
		t.Fatalf("an issue with open blockers must stay open: %+v", detail)
	}
	var comments []IssueComment
	owner.request("GET", base+"/issues/1/comments", nil, 200, &comments)
	if len(comments) != 1 || !strings.HasPrefix(comments[0].Body, "Closed by commit ") {
		t.Fatalf("closing note missing: %+v", comments)
	}

	// A push to another branch never closes issues.
	run("checkout", "-b", "feature")
	if err := os.WriteFile(work+"/feature.txt", []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "feature.txt")
	run("commit", "-m", "Start feature, closes #3")
	run("push", "origin", "feature")
	owner.request("GET", base+"/issues/3", nil, 200, &detail)
	if detail.State != "open" {
		t.Fatalf("a push to a feature branch closed #3: %+v", detail)
	}

	// Opening, closing, and reopening a unite request moves it on the board.
	var pull Pull
	owner.request("POST", base+"/pulls", map[string]string{"title": "Feature", "body": "Part of #3", "base_branch": "main", "head_branch": "feature"}, 201, &pull)
	status := func() string {
		t.Helper()
		var board []BoardItem
		owner.request("GET", base+"/board", nil, 200, &board)
		for _, item := range board {
			if item.Kind == "pull" && item.Number == pull.Number {
				return item.Status
			}
		}
		return ""
	}
	if got := status(); got != "todo" {
		t.Fatalf("opened unite request should join the first column, got %q", got)
	}
	owner.request("PUT", base+fmt.Sprintf("/pulls/%d/board", pull.Number), map[string]string{"status": "progress"}, 200, nil)
	owner.request("PATCH", base+fmt.Sprintf("/pulls/%d", pull.Number), map[string]string{"state": "closed"}, 200, nil)
	if got := status(); got != "done" {
		t.Fatalf("closed unite request should move to done, got %q", got)
	}
	owner.request("PATCH", base+fmt.Sprintf("/pulls/%d", pull.Number), map[string]string{"state": "open"}, 200, nil)
	if got := status(); got != "todo" {
		t.Fatalf("reopened unite request should return to the first column, got %q", got)
	}
	var refs IssueReferences
	owner.request("GET", base+"/issues/3/references", nil, 200, &refs)
	if len(refs.ReferencedBy) != 1 || refs.ReferencedBy[0].Kind != "pull" {
		t.Fatalf("unite request reference missing: %+v", refs)
	}
	owner.request("DELETE", base+fmt.Sprintf("/pulls/%d/board", pull.Number), nil, 200, nil)
	if got := status(); got != "" {
		t.Fatalf("removed unite request still on board: %q", got)
	}
}
