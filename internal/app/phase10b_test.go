package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
)

// postForm issues a raw, form-encoded POST the way a third-party OAuth
// client actually calls the token endpoint — no Origin header, no JSON —
// unlike testClient.request, which always sends JSON with an Origin set.
func postForm(t *testing.T, base, path string, values url.Values) (int, map[string]any) {
	t.Helper()
	res, err := http.Post(base+"/api/v1"+path, "application/x-www-form-urlencoded", strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	return res.StatusCode, out
}

// TestPhase10OAuthAuthorizationCodeFlow proves the full authorization-code
// flow end to end: registering an app, a user approving it through the
// consent endpoint, exchanging the resulting code for a token, and using
// that token like any other Bearer credential on a real API call.
func TestPhase10OAuthAuthorizationCodeFlow(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	server, _, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()

	devJar, _ := cookiejar.New(nil)
	dev := testClient{t, server.URL, &http.Client{Jar: devJar}}
	dev.request("POST", "/auth/register", map[string]string{"username": "appdev", "email": "appdev@example.test", "password": "appdev-long-password"}, 201, nil)

	var app struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	dev.request("POST", "/user/oauth-apps", map[string]string{
		"name": "CI Bot", "redirect_uri": "https://ci.example.test/callback",
	}, 201, &app)
	if app.ClientID == "" || app.ClientSecret == "" {
		t.Fatalf("expected a client_id and client_secret, got %+v", app)
	}

	userJar, _ := cookiejar.New(nil)
	authorizer := testClient{t, server.URL, &http.Client{Jar: userJar}}
	authorizer.request("POST", "/auth/register", map[string]string{"username": "granter", "email": "granter@example.test", "password": "granter-long-password"}, 201, nil)

	var info struct {
		Name string `json:"name"`
	}
	authorizer.request("GET", "/oauth/authorize?client_id="+app.ClientID+"&redirect_uri=https://ci.example.test/callback&scope=repo:read", nil, 200, &info)
	if info.Name != "CI Bot" {
		t.Fatalf("expected consent info to name the app, got %+v", info)
	}

	var decision struct {
		RedirectTo string `json:"redirect_to"`
	}
	authorizer.request("POST", "/oauth/authorize", map[string]any{
		"client_id": app.ClientID, "redirect_uri": "https://ci.example.test/callback",
		"scope": "repo:read", "state": "xyz", "approve": true,
	}, 200, &decision)
	redirect, err := url.Parse(decision.RedirectTo)
	if err != nil {
		t.Fatal(err)
	}
	code := redirect.Query().Get("code")
	if code == "" || redirect.Query().Get("state") != "xyz" {
		t.Fatalf("expected a code and the original state on the redirect, got %s", decision.RedirectTo)
	}

	status, token := postForm(t, server.URL, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"https://ci.example.test/callback"},
		"client_id": {app.ClientID}, "client_secret": {app.ClientSecret},
	})
	if status != 200 {
		t.Fatalf("expected 200 exchanging the code, got %d: %+v", status, token)
	}
	accessToken, _ := token["access_token"].(string)
	refreshToken, _ := token["refresh_token"].(string)
	if accessToken == "" || refreshToken == "" {
		t.Fatalf("expected an access_token and refresh_token, got %+v", token)
	}

	// The same code must not exchange twice.
	status, _ = postForm(t, server.URL, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"https://ci.example.test/callback"},
		"client_id": {app.ClientID}, "client_secret": {app.ClientSecret},
	})
	if status != 400 {
		t.Fatalf("expected a used code to be rejected with 400, got %d", status)
	}

	// The issued token authenticates a normal read on the general API —
	// /auth/me reflects identity in its body regardless of status, so this
	// actually proves the token resolved to the right user rather than
	// just hitting a route that happens to answer 200 either way.
	if who := meUsername(t, server.URL, accessToken); who != "granter" {
		t.Fatalf("expected the OAuth token to authenticate as granter, got %q", who)
	}

	// Refreshing rotates the token: the old refresh token stops working.
	status, refreshed := postForm(t, server.URL, "/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {app.ClientID}, "client_secret": {app.ClientSecret},
	})
	if status != 200 || refreshed["access_token"] == accessToken {
		t.Fatalf("expected a fresh access token from refresh, got %d %+v", status, refreshed)
	}
	status, _ = postForm(t, server.URL, "/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {app.ClientID}, "client_secret": {app.ClientSecret},
	})
	if status != 400 {
		t.Fatalf("expected the rotated-out refresh token to be rejected, got %d", status)
	}

	// The authorizing user can see and revoke the grant; revocation is
	// immediate, not just "expires eventually."
	var authorized struct {
		Items []map[string]any `json:"items"`
	}
	authorizer.request("GET", "/user/authorized-apps", nil, 200, &authorized)
	if len(authorized.Items) != 1 {
		t.Fatalf("expected one authorized app, got %d", len(authorized.Items))
	}
	appID, _ := authorized.Items[0]["oauth_app_id"].(string)
	authorizer.request("DELETE", "/user/authorized-apps/"+appID, nil, 200, nil)

	newAccess, _ := refreshed["access_token"].(string)
	if who := meUsername(t, server.URL, newAccess); who != "" {
		t.Fatalf("expected the token to stop working immediately after the grant was revoked, but it still authenticated as %q", who)
	}
}

// meUsername calls /auth/me with the given Bearer token (or none, if
// empty) and returns the resolved username, or "" if nothing resolved.
func meUsername(t *testing.T, base, token string) string {
	t.Helper()
	req, err := http.NewRequest("GET", base+"/api/v1/auth/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		User *struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if err = json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.User == nil {
		return ""
	}
	return out.User.Username
}

// TestPhase10OAuthRedirectURIMismatch proves a redirect_uri that does not
// exactly match what the app registered is rejected — this is a
// well-known OAuth security requirement, not a convenience check.
func TestPhase10OAuthRedirectURIMismatch(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	server, _, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()

	jar, _ := cookiejar.New(nil)
	c := testClient{t, server.URL, &http.Client{Jar: jar}}
	c.request("POST", "/auth/register", map[string]string{"username": "mismatchdev", "email": "mismatchdev@example.test", "password": "mismatchdev-long-password"}, 201, nil)
	var app struct {
		ClientID string `json:"client_id"`
	}
	c.request("POST", "/user/oauth-apps", map[string]string{"name": "Strict App", "redirect_uri": "https://real.example.test/callback"}, 201, &app)

	c.request("GET", "/oauth/authorize?client_id="+app.ClientID+"&redirect_uri=https://attacker.example.test/callback&scope=repo:read", nil, 422, nil)
	c.request("POST", "/oauth/authorize", map[string]any{
		"client_id": app.ClientID, "redirect_uri": "https://attacker.example.test/callback", "scope": "repo:read", "approve": true,
	}, 422, nil)
}

// TestPhase10GitownAppInstallation proves an installation token is scoped
// to exactly the repository it was installed on, cannot exceed the app's
// requested scope, and stops working immediately on uninstall.
func TestPhase10GitownAppInstallation(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	server, _, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()

	devJar, _ := cookiejar.New(nil)
	dev := testClient{t, server.URL, &http.Client{Jar: devJar}}
	dev.request("POST", "/auth/register", map[string]string{"username": "appmaker", "email": "appmaker@example.test", "password": "appmaker-long-password"}, 201, nil)
	var gitownApp struct {
		ID string `json:"id"`
	}
	dev.request("POST", "/user/gitown-apps", map[string]string{"name": "Linter Bot", "requested_scope": "repo:read"}, 201, &gitownApp)

	ownerJar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: ownerJar}}
	owner.request("POST", "/auth/register", map[string]string{"username": "installowner", "email": "installowner@example.test", "password": "installowner-long-password"}, 201, nil)
	var repoA, repoB struct {
		Owner string
		Name  string
	}
	// Both private, so a 200 can only come from an actual granted role —
	// a public repo would be visible to any Bearer token (or none at all),
	// which would not prove the installation grant did anything.
	owner.request("POST", "/repos", map[string]any{"name": "installed-on", "visibility": "private"}, 201, &repoA)
	owner.request("POST", "/repos", map[string]any{"name": "not-installed-on", "visibility": "private"}, 201, &repoB)

	// A repo:read app cannot be granted repo:write, even if the installer asks for it.
	owner.request("POST", "/repos/"+repoA.Owner+"/"+repoA.Name+"/gitown-apps", map[string]string{"client_id": gitownApp.ID, "scope": "repo:write"}, 422, nil)

	var install struct {
		ID                 string `json:"id"`
		InstallationToken  string `json:"installation_token"`
	}
	owner.request("POST", "/repos/"+repoA.Owner+"/"+repoA.Name+"/gitown-apps", map[string]string{"client_id": gitownApp.ID, "scope": "repo:read"}, 201, &install)
	if install.InstallationToken == "" {
		t.Fatal("expected an installation token")
	}

	// Works, read-only, against the installed repo.
	req, _ := http.NewRequest("GET", server.URL+"/api/v1/repos/"+repoA.Owner+"/"+repoA.Name, nil)
	req.Header.Set("Authorization", "Bearer "+install.InstallationToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("expected the installation token to read the installed repo, got %d", res.StatusCode)
	}

	// The installing owner's OWN broader access must not leak onto the
	// bot's installation token: repoB is private and the bot was never
	// installed on it, so it must be invisible, just as it would be to any
	// other unrelated account.
	req, _ = http.NewRequest("GET", server.URL+"/api/v1/repos/"+repoB.Owner+"/"+repoB.Name, nil)
	req.Header.Set("Authorization", "Bearer "+install.InstallationToken)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode == 200 {
		t.Fatal("expected the installation token to have no access to a repository it was not installed on")
	}

	owner.request("DELETE", "/repos/"+repoA.Owner+"/"+repoA.Name+"/gitown-apps/"+install.ID, nil, 200, nil)

	req, _ = http.NewRequest("GET", server.URL+"/api/v1/repos/"+repoA.Owner+"/"+repoA.Name, nil)
	req.Header.Set("Authorization", "Bearer "+install.InstallationToken)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode == 200 {
		t.Fatal("expected the installation token to stop working immediately after uninstall")
	}
}

// TestPhase10ChatWebhookFormatting proves Slack and Discord targets receive
// the JSON shape each platform actually expects, delivered through the
// same signed, SSRF-protected path every other webhook uses.
func TestPhase10ChatWebhookFormatting(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	t.Setenv("GITOWN_SECRET_KEY", "phase10b-test-secret-key-not-random")
	server, application, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()

	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	owner.request("POST", "/auth/register", map[string]string{"username": "chatowner", "email": "chatowner@example.test", "password": "chatowner-long-password"}, 201, nil)
	var repo struct {
		ID string
	}
	owner.request("POST", "/repos", map[string]any{"name": "chatty", "visibility": "public"}, 201, &repo)

	var mu sync.Mutex
	var bodies []map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer fake.Close()
	application.webhookClient = fake.Client()

	for _, kind := range []string{"slack", "discord"} {
		id := insertTestWebhook(t, application, repo.ID, fake.URL, "chat-secret-"+kind, []string{"issue.opened"})
		if _, err := application.db.Exec(context.Background(), `UPDATE webhooks SET kind=$1 WHERE id=$2`, kind, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := application.fireWebhook(context.Background(), application.db, repo.ID, "", "issue.opened", map[string]any{"repository": "chatowner/chatty"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if n, err := application.deliverWebhooks(context.Background()); err != nil {
			t.Fatal(err)
		} else if n == 0 {
			break
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("expected 2 deliveries (slack + discord), got %d", len(bodies))
	}
	var sawText, sawContent bool
	for _, body := range bodies {
		if _, ok := body["text"]; ok {
			sawText = true
		}
		if _, ok := body["content"]; ok {
			sawContent = true
		}
	}
	if !sawText || !sawContent {
		t.Fatalf("expected one delivery shaped as Slack's {text} and one as Discord's {content}, got %+v", bodies)
	}
}
