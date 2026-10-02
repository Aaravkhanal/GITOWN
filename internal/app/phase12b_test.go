package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
)

func TestPhase12ParsersStayBounded(t *testing.T) {
	if _, err := parseImportURL("github", "https://github.com/octo/demo"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"http://github.com/octo/demo",
		"https://user:token@github.com/octo/demo",
		"https://github.com/octo/demo?token=1",
		"https://github.com:443/octo/demo",
		"https://gitlab.com/octo/demo",
		"https://github.com/octo/demo/extra",
	} {
		if _, err := parseImportURL("github", raw); err == nil {
			t.Fatalf("accepted unsafe import %s", raw)
		}
	}
	if _, err := parseImportURL("sourcehut", "https://github.com/octo/demo"); err == nil {
		t.Fatal("unknown forge accepted")
	}
	if _, err := parseDevFile([]byte("name: box\nimage: label\nrun: echo hi\n")); err == nil {
		t.Fatal("dev environment file accepted a run command")
	}
	file, err := parseDevFile([]byte("name: box\nnotes: display only\nimage: label\n"))
	if err != nil || file.Image != "label" {
		t.Fatalf("dev file: %+v %v", file, err)
	}
	deps := parseGoMod("module demo\n\ngo 1.22\n\nrequire (\n\texample.com/leftpad v0.1.0\n)\n")
	if len(deps) != 1 || deps[0][0] != "example.com/leftpad" || deps[0][1] != "v0.1.0" {
		t.Fatalf("go.mod parse: %+v", deps)
	}
	owners := codeOwnersForPath(parseCodeOwners("* @everyone\ndocs/ @docs\ndocs/readme.md @page\n"), "docs/readme.md")
	if len(owners) != 1 || owners[0] != "page" {
		t.Fatalf("last code owner match wins: %+v", owners)
	}
	if secretMarker("token ghp_shouldnotstore") != "ghp_" {
		t.Fatal("marker detector missed ghp_")
	}
}

func TestPhase12Ecosystem(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	server, application, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()
	ownerJar, _ := cookiejar.New(nil)
	guestJar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: ownerJar}}
	guest := testClient{t, server.URL, &http.Client{Jar: guestJar}}
	anon := testClient{t, server.URL, &http.Client{}}
	owner.request("POST", "/auth/register", map[string]string{"username": "upstream", "email": "upstream@example.test", "password": "upstream-password"}, 201, nil)
	guest.request("POST", "/auth/register", map[string]string{"username": "remixer", "email": "remixer@example.test", "password": "remixer-password"}, 201, nil)
	var origin struct {
		ID string `json:"id"`
	}
	owner.request("POST", "/repos", map[string]any{"name": "library", "visibility": "public", "readme": true}, 201, &origin)
	main := tip(owner, "/repos/upstream/library/commits?ref=main")

	var remix struct {
		ID    string `json:"id"`
		Owner string `json:"owner"`
		Name  string `json:"name"`
	}
	guest.request("POST", "/repos/upstream/library/remix", map[string]string{"name": "library", "visibility": "public"}, 201, &remix)
	if remix.Owner != "remixer" || remix.Name != "library" {
		t.Fatalf("remix: %+v", remix)
	}
	pointBranch(t, application, remix.ID, "feature", tip(guest, "/repos/remixer/library/commits?ref=main"))
	putFile(guest, "remixer/library", "feature", "notes.txt", "from the remix\n", "Add notes", tip(guest, "/repos/remixer/library/commits?ref=feature"))
	var pull struct {
		Number    int    `json:"number"`
		HeadOwner string `json:"head_owner"`
		HeadRepo  string `json:"head_repository"`
	}
	guest.request("POST", "/repos/upstream/library/pulls", map[string]any{
		"title": "From the remix", "body": "Please look", "base_branch": "main", "head_branch": "feature",
		"head_owner": "remixer", "head_repository": "library",
	}, 201, &pull)
	if pull.Number != 1 || pull.HeadOwner != "remixer" || pull.HeadRepo != "library" {
		t.Fatalf("cross-project unite: %+v", pull)
	}
	head := tip(guest, "/repos/remixer/library/commits?ref=feature")
	owner.request("POST", "/repos/upstream/library/pulls/1/reviews", map[string]string{"state": "commented", "body": "Seen the remix head", "head_sha": head}, 201, nil)

	pointBranch(t, application, origin.ID, "queue-a", main)
	pointBranch(t, application, origin.ID, "queue-b", main)
	putFile(owner, "upstream/library", "queue-a", "a.txt", "a\n", "Branch A", main)
	putFile(owner, "upstream/library", "queue-b", "b.txt", "b\n", "Branch B", main)
	owner.request("POST", "/repos/upstream/library/pulls", map[string]string{"title": "A", "body": "a", "base_branch": "main", "head_branch": "queue-a"}, 201, nil)
	owner.request("POST", "/repos/upstream/library/pulls", map[string]string{"title": "B", "body": "b", "base_branch": "main", "head_branch": "queue-b"}, 201, nil)
	owner.request("POST", "/repos/upstream/library/merge-queue", map[string]int{"number": 2}, 201, nil)
	owner.request("POST", "/repos/upstream/library/merge-queue", map[string]int{"number": 3}, 201, nil)
	merge(owner, "/repos/upstream/library/pulls/3/merge", tip(owner, "/repos/upstream/library/commits?ref=queue-b"), tip(owner, "/repos/upstream/library/commits?ref=main"), 409)
	merge(owner, "/repos/upstream/library/pulls/2/merge", tip(owner, "/repos/upstream/library/commits?ref=queue-a"), tip(owner, "/repos/upstream/library/commits?ref=main"), 200)

	owner.request("POST", "/repos/upstream/library/discussions", map[string]string{"title": "Roadmap", "body": "What should we build?", "category": "ideas"}, 201, nil)
	guest.request("POST", "/repos/upstream/library/discussions/1/comments", map[string]string{"body": "A static site."}, 201, nil)
	anon.request("POST", "/repos/upstream/library/discussions", map[string]string{"title": "Nope", "body": "no"}, 403, nil)

	secretLine := "export TOKEN=ghp_phase12scantokenvalue"
	putFile(owner, "upstream/library", "main", "config.txt", secretLine+"\n", "Add a marked line", tip(owner, "/repos/upstream/library/commits?ref=main"))
	putFile(owner, "upstream/library", "main", "go.mod", "module library\n\ngo 1.22\n\nrequire example.com/leftpad v0.1.0\n", "Record a dependency", tip(owner, "/repos/upstream/library/commits?ref=main"))
	owner.request("POST", "/repos/upstream/library/advisories", map[string]string{
		"severity": "high", "summary": "leftpad overflow", "package_name": "example.com/leftpad",
		"ecosystem": "go", "patched_version": "v0.2.0", "state": "published",
	}, 201, nil)
	owner.request("POST", "/repos/upstream/library/supply-chain/scan", map[string]any{}, 200, nil)
	var findings json.RawMessage
	owner.request("GET", "/repos/upstream/library/secret-findings", nil, 200, &findings)
	if strings.Contains(string(findings), "ghp_phase12scantokenvalue") || !strings.Contains(string(findings), `"marker":"ghp_"`) {
		t.Fatalf("secret finding stored the line or missed the marker: %s", findings)
	}
	var alerts struct {
		Items []struct {
			Package string `json:"package_name"`
			State   string `json:"state"`
		} `json:"items"`
	}
	owner.request("GET", "/repos/upstream/library/vulnerability-alerts", nil, 200, &alerts)
	if len(alerts.Items) != 1 || alerts.Items[0].Package != "example.com/leftpad" {
		t.Fatalf("alerts: %+v", alerts.Items)
	}
	var issues []struct {
		Title string `json:"title"`
	}
	owner.request("GET", "/repos/upstream/library/issues", nil, 200, &issues)
	foundIssue := false
	for _, issue := range issues {
		if issue.Title == "Update dependency example.com/leftpad" {
			foundIssue = true
		}
	}
	if !foundIssue {
		t.Fatalf("dependency issue missing: %+v", issues)
	}
	var graph struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	owner.request("GET", "/repos/upstream/library/dependency-graph", nil, 200, &graph)
	if len(graph.Items) == 0 {
		t.Fatal("dependency graph empty")
	}

	putFile(owner, "upstream/library", "main", ".gitown/dev.yml", "name: box\nimage: label\nrun: echo hi\n", "Reject commands", tip(owner, "/repos/upstream/library/commits?ref=main"))
	owner.request("POST", "/repos/upstream/library/dev-environments", map[string]any{}, 422, nil)
	putFile(owner, "upstream/library", "main", ".gitown/dev.yml", "name: box\nnotes: display only\nimage: label\n", "Describe an environment", tip(owner, "/repos/upstream/library/commits?ref=main"))
	var desk struct {
		Status    string `json:"status"`
		Execution bool   `json:"execution_enabled"`
	}
	owner.request("POST", "/repos/upstream/library/dev-environments", map[string]any{}, 201, &desk)
	if desk.Status != "pending_sandbox_review" || desk.Execution {
		t.Fatalf("dev environment: %+v", desk)
	}

	putFile(owner, "upstream/library", "main", ".gitown/showcase/index.html", "<script>alert(1)</script><h1>Library</h1>", "Publish a page", tip(owner, "/repos/upstream/library/commits?ref=main"))
	page := rawStatus(t, server.URL+"/sites/upstream/library", nil)
	if page.code != 200 || !strings.Contains(page.header.Get("Content-Security-Policy"), "sandbox") || strings.Contains(page.header.Get("Content-Security-Policy"), "allow-scripts") {
		t.Fatalf("showcase policy: %d %s", page.code, page.header.Get("Content-Security-Policy"))
	}
	if !strings.Contains(page.body, "<script>alert(1)</script>") {
		t.Fatalf("showcase body: %s", page.body)
	}

	var snippet struct {
		ID string `json:"id"`
	}
	owner.request("POST", "/snippets", map[string]string{"title": "Hello", "filename": "hello.txt", "content": "hi", "visibility": "public"}, 201, &snippet)
	anon.request("GET", "/snippets/"+snippet.ID, nil, 200, nil)
	owner.request("POST", "/snippets", map[string]string{"title": "Quiet", "filename": "quiet.txt", "content": "hidden", "visibility": "private"}, 201, &snippet)
	anon.request("GET", "/snippets/"+snippet.ID, nil, 404, nil)

	guest.request("POST", "/users/upstream/sponsorships", map[string]any{"amount_cents": 500, "message": "thanks", "public": true}, 201, nil)
	var pledges struct {
		Charges bool `json:"charges"`
	}
	anon.request("GET", "/users/upstream/sponsorships", nil, 200, &pledges)
	if pledges.Charges {
		t.Fatal("sponsorships must not charge a card")
	}
	owner.request("POST", "/users/upstream/sponsorships", map[string]any{"amount_cents": 100, "message": "self"}, 422, nil)

	var device struct {
		Token string `json:"token"`
	}
	owner.request("POST", "/user/devices", map[string]string{"name": "phone", "platform": "ios"}, 201, &device)
	if !strings.HasPrefix(device.Token, "mob_") {
		t.Fatalf("device token: %+v", device)
	}
	feed := rawRequest(t, server.URL, "GET", "/api/v1/mobile/feed", &http.Client{}, "Bearer "+device.Token)
	if !strings.Contains(string(feed), `"delivery":"pull"`) {
		t.Fatalf("mobile feed: %s", feed)
	}

	owner.request("PUT", "/repos/upstream/library/wiki/home", map[string]string{"content": "Searchable wiki phrase", "expected_head": tip(owner, "/repos/upstream/library/commits?ref=main")}, 201, nil)
	var hits struct {
		Items []struct {
			Slug string `json:"slug"`
		} `json:"items"`
	}
	owner.request("GET", "/repos/upstream/library/wiki-search?q=Searchable", nil, 200, &hits)
	if len(hits.Items) != 1 || hits.Items[0].Slug != "home" {
		t.Fatalf("wiki search: %+v", hits.Items)
	}
}

func merge(c testClient, path, head, base string, status int) {
	c.t.Helper()
	c.request("POST", path, map[string]any{"head_sha": head, "base_sha": base, "method": "merge"}, status, nil)
}

type httpResult struct {
	code   int
	header http.Header
	body   string
}

func rawStatus(t *testing.T, url string, client *http.Client) httpResult {
	t.Helper()
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return httpResult{code: res.StatusCode, header: res.Header, body: string(body)}
}
