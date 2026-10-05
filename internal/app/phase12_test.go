package app

import (
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"

	"github.com/Aaravkhanal/GITOWN/internal/gitstore"
)

func TestPhase12GitBackedWiki(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	server, _, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()
	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	anon := testClient{t, server.URL, &http.Client{}}
	owner.request("POST", "/auth/register", map[string]string{"username": "wikiowner", "email": "wikiowner@example.test", "password": "wiki-owner-password"}, 201, nil)
	owner.request("POST", "/repos", map[string]any{"name": "guide", "visibility": "public", "readme": true}, 201, nil)
	var index struct {
		Pages   []string `json:"pages"`
		HeadSHA string   `json:"head_sha"`
	}
	anon.request("GET", "/repos/wikiowner/guide/wiki", nil, 200, &index)
	if len(index.Pages) != 0 || len(index.HeadSHA) != 40 {
		t.Fatalf("expected empty wiki on initialized repository: %+v", index)
	}
	anon.request("PUT", "/repos/wikiowner/guide/wiki/home", map[string]string{"content": "# Hello", "expected_head": index.HeadSHA}, 403, nil)
	owner.request("PUT", "/repos/wikiowner/guide/wiki/BadSlug", map[string]string{"content": "bad", "expected_head": index.HeadSHA}, 422, nil)
	var saved struct {
		Slug string `json:"slug"`
		SHA  string `json:"sha"`
	}
	owner.request("PUT", "/repos/wikiowner/guide/wiki/home", map[string]string{"content": "# Hello\nSafe **Markdown**.", "expected_head": index.HeadSHA}, 201, &saved)
	if saved.Slug != "home" || len(saved.SHA) != 40 || saved.SHA == index.HeadSHA {
		t.Fatalf("wiki commit was not created: %+v", saved)
	}
	var page struct {
		Content string `json:"content"`
		HeadSHA string `json:"head_sha"`
	}
	anon.request("GET", "/repos/wikiowner/guide/wiki/home", nil, 200, &page)
	if page.Content != "# Hello\nSafe **Markdown**." || page.HeadSHA != saved.SHA {
		t.Fatalf("wiki page did not read back: %+v", page)
	}
	owner.request("PUT", "/repos/wikiowner/guide/wiki/home", map[string]string{"content": "stale", "expected_head": index.HeadSHA}, 409, nil)
	anon.request("GET", "/repos/wikiowner/guide/wiki", nil, 200, &index)
	if len(index.Pages) != 1 || index.Pages[0] != "home" {
		t.Fatalf("wiki page was not listed: %+v", index)
	}
	var commits []gitstore.Commit
	owner.request("GET", "/repos/wikiowner/guide/commits?ref=main&path=.gitown/wiki/home.md", nil, 200, &commits)
	if len(commits) == 0 || !strings.Contains(commits[0].Message, "wiki page home") {
		t.Fatalf("wiki Git history missing: %+v", commits)
	}
	owner.request("PUT", "/repos/wikiowner/guide/branch-rules?branch=main", map[string]any{"require_unite": true, "required_approvals": 0, "block_changes_requested": false}, 200, nil)
	owner.request("PUT", "/repos/wikiowner/guide/wiki/home", map[string]string{"content": "blocked", "expected_head": saved.SHA}, 409, nil)
}
