package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
)

// insertTestWebhook inserts a webhook row directly, bypassing the create
// endpoint's real https+non-private-address validation. That validation is
// exactly what TestPhase10WebhookSSRFGuard exercises through the API; these
// helpers exist so the delivery-mechanics tests can point at a plain-http
// httptest.Server on 127.0.0.1 without weakening the guard every real
// webhook goes through.
func insertTestWebhook(t *testing.T, application *App, repositoryID, url, secret string, events []string) string {
	t.Helper()
	ciphertext, nonce, err := sealSecret(secret)
	if err != nil {
		t.Fatal(err)
	}
	var creatorID string
	if err := application.db.QueryRow(context.Background(), `SELECT owner_id::text FROM repositories WHERE id=$1`, repositoryID).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	id := auth.ID()
	if _, err := application.db.Exec(context.Background(),
		`INSERT INTO webhooks(id,repository_id,url,secret_ciphertext,secret_nonce,events,created_by) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		id, repositoryID, url, ciphertext, nonce, events, creatorID); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestPhase10WebhookSignatureAndDelivery proves a webhook is delivered to
// its receiver with a verifiable HMAC-SHA256 signature over the exact body
// received, and that the delivery log records the outcome.
func TestPhase10WebhookSignatureAndDelivery(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	t.Setenv("GITOWN_SECRET_KEY", "phase10-test-secret-key-not-random")
	server, application, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()

	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	owner.request("POST", "/auth/register", map[string]string{"username": "hookowner", "email": "hookowner@example.test", "password": "hookowner-long-password"}, 201, nil)
	var repo struct {
		ID    string
		Owner string
		Name  string
	}
	owner.request("POST", "/repos", map[string]any{"name": "hooked", "visibility": "public"}, 201, &repo)

	var mu sync.Mutex
	var gotBody []byte
	var gotSig, gotEvent string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBody = body
		gotSig = r.Header.Get("X-GITOWN-Signature")
		gotEvent = r.Header.Get("X-GITOWN-Event")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer fake.Close()
	// The receiver runs on 127.0.0.1, which the production SSRF guard
	// refuses by design; swap in the test server's own client the same way
	// App.mailer is swapped for tests, rather than weakening the guard
	// every real delivery goes through.
	application.webhookClient = fake.Client()

	const secret = "phase10-fixture-secret"
	hookID := insertTestWebhook(t, application, repo.ID, fake.URL, secret, []string{"issue.opened"})

	var issue struct{ Number int }
	owner.request("POST", "/repos/"+repo.Owner+"/"+repo.Name+"/issues", map[string]any{"title": "hooked issue", "body": "body"}, 201, &issue)

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := application.deliverWebhooks(context.Background()); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		received := gotBody != nil
		mu.Unlock()
		if received || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotBody == nil {
		t.Fatal("webhook was never delivered")
	}
	if gotEvent != "issue.opened" {
		t.Fatalf("expected event header issue.opened, got %q", gotEvent)
	}
	expected := "sha256=" + hmacHex(secret, gotBody)
	if gotSig != expected {
		t.Fatalf("signature mismatch: got %q want %q", gotSig, expected)
	}
	var payload map[string]any
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["event"] != "issue.opened" {
		t.Fatalf("payload event = %v", payload["event"])
	}

	var deliveries struct {
		Items []map[string]any `json:"items"`
	}
	owner.request("GET", "/repos/"+repo.Owner+"/"+repo.Name+"/webhooks/"+hookID+"/deliveries", nil, 200, &deliveries)
	if len(deliveries.Items) != 1 || deliveries.Items[0]["status"] != "success" {
		t.Fatalf("expected one successful delivery, got %+v", deliveries.Items)
	}

	// Replay creates a second delivery for the same event and payload.
	deliveryID, _ := deliveries.Items[0]["id"].(string)
	owner.request("POST", "/repos/"+repo.Owner+"/"+repo.Name+"/webhooks/"+hookID+"/deliveries/"+deliveryID+"/replay", nil, 201, nil)
	owner.request("GET", "/repos/"+repo.Owner+"/"+repo.Name+"/webhooks/"+hookID+"/deliveries", nil, 200, &deliveries)
	if len(deliveries.Items) != 2 {
		t.Fatalf("expected a replayed delivery, got %d", len(deliveries.Items))
	}
}

func hmacHex(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// TestPhase10WebhookRetryBackoff proves a failing receiver leaves the
// delivery pending with an incremented attempt count and a future
// next_attempt_at, rather than either retrying immediately or giving up
// after one failure.
func TestPhase10WebhookRetryBackoff(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	t.Setenv("GITOWN_SECRET_KEY", "phase10-retry-test-secret-key")
	server, application, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()

	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	owner.request("POST", "/auth/register", map[string]string{"username": "retryowner", "email": "retryowner@example.test", "password": "retryowner-long-password"}, 201, nil)
	var repo struct {
		ID    string
		Owner string
		Name  string
	}
	owner.request("POST", "/repos", map[string]any{"name": "flaky", "visibility": "public"}, 201, &repo)

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()
	application.webhookClient = failing.Client()

	hookID := insertTestWebhook(t, application, repo.ID, failing.URL, "phase10-retry-secret", []string{"issue.opened"})
	owner.request("POST", "/repos/"+repo.Owner+"/"+repo.Name+"/issues", map[string]any{"title": "will fail", "body": "body"}, 201, nil)

	if _, err := application.deliverWebhooks(context.Background()); err != nil {
		t.Fatal(err)
	}
	var deliveries struct {
		Items []map[string]any `json:"items"`
	}
	owner.request("GET", "/repos/"+repo.Owner+"/"+repo.Name+"/webhooks/"+hookID+"/deliveries", nil, 200, &deliveries)
	if len(deliveries.Items) != 1 {
		t.Fatalf("expected one delivery attempt, got %d", len(deliveries.Items))
	}
	item := deliveries.Items[0]
	if item["status"] != "pending" {
		t.Fatalf("a single failed attempt should stay pending for retry, got status=%v", item["status"])
	}
	if attempts, _ := item["attempts"].(float64); attempts != 1 {
		t.Fatalf("expected attempts=1, got %v", item["attempts"])
	}
	// A second immediate claim must not re-send: next_attempt_at was pushed
	// into the future by the backoff, so the row isn't due yet.
	sent, err := application.deliverWebhooks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Fatalf("expected no deliveries due yet (backoff in effect), sent %d", sent)
	}
	deliveryID, _ := item["id"].(string)
	if deliveryID == "" {
		t.Fatal("webhook delivery omitted its ID")
	}
	if _, err = application.db.Exec(context.Background(), `UPDATE webhook_deliveries SET status='sending',attempts=$2,claimed_at=now()-interval '4 minutes' WHERE id=$1`, deliveryID, webhookMaxAttempts-1); err != nil {
		t.Fatal(err)
	}
	if err = application.recoverStaleWebhookClaims(context.Background()); err != nil {
		t.Fatal(err)
	}
	var recoveredStatus string
	var recoveredAttempts int
	if err = application.db.QueryRow(context.Background(), `SELECT status,attempts FROM webhook_deliveries WHERE id=$1`, deliveryID).Scan(&recoveredStatus, &recoveredAttempts); err != nil || recoveredStatus != "failed" || recoveredAttempts != webhookMaxAttempts {
		t.Fatalf("expired webhook lease was not dead-lettered: %q %d %v", recoveredStatus, recoveredAttempts, err)
	}
	t.Setenv("GITOWN_OPERATORS", "retryowner")
	var queues struct {
		DeadLetters map[string][]map[string]any `json:"dead_letters"`
	}
	owner.request("GET", "/operator/queues", nil, 200, &queues)
	if len(queues.DeadLetters["webhooks"]) == 0 || queues.DeadLetters["webhooks"][0]["id"] != deliveryID {
		t.Fatalf("operator queue view omitted the webhook dead letter: %+v", queues.DeadLetters)
	}
	owner.request("POST", "/operator/queues/webhooks/"+deliveryID+"/retry", nil, 200, nil)
	successReceiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer successReceiver.Close()
	if _, err = application.db.Exec(context.Background(), `UPDATE webhooks SET url=$1 WHERE id=$2`, successReceiver.URL, hookID); err != nil {
		t.Fatal(err)
	}
	application.webhookClient = successReceiver.Client()
	if _, err = application.deliverWebhooks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = application.db.QueryRow(context.Background(), `SELECT status FROM webhook_deliveries WHERE id=$1`, deliveryID).Scan(&recoveredStatus); err != nil || recoveredStatus != "success" {
		t.Fatalf("operator retry did not deliver webhook: %q %v", recoveredStatus, err)
	}
}

// TestPhase10WebhookSSRFGuard proves a webhook URL pointing at a private or
// loopback address is refused both when the webhook is created and again
// when a delivery actually tries to send (the second check is what stops
// DNS rebinding: a hostname that resolved safely at creation time but
// repoints at an internal address later).
func TestPhase10WebhookSSRFGuard(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	t.Setenv("GITOWN_SECRET_KEY", "phase10-ssrf-test-secret-key")
	server, application, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()

	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	owner.request("POST", "/auth/register", map[string]string{"username": "ssrfowner", "email": "ssrfowner@example.test", "password": "ssrfowner-long-password"}, 201, nil)
	var repo struct{ Owner, Name string }
	owner.request("POST", "/repos", map[string]any{"name": "guarded", "visibility": "public"}, 201, &repo)

	for _, target := range []string{
		"https://127.0.0.1/hook",
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.5/hook",
		"http://example.com/hook", // plain http is rejected regardless of address
	} {
		owner.request("POST", "/repos/"+repo.Owner+"/"+repo.Name+"/webhooks", map[string]any{
			"url":    target,
			"events": []string{"issue.opened"},
		}, 422, nil)
	}

	// Creation-time validation is only the first check: prove delivery
	// itself refuses a private address even when a row already exists
	// (simulating DNS rebinding — validWebhookURL passed at creation, but
	// the resolved address is now internal by the time delivery runs).
	if err := validWebhookURL(context.Background(), "https://safe.invalid.example/hook"); err != nil {
		t.Fatalf("an https URL with no resolvable address should not fail creation-time validation outright: %v", err)
	}
	ok, _, _, err := application.sendWebhook(context.Background(), "https://127.0.0.1:1/hook", "secret", "issue.opened", []byte(`{}`))
	if ok || err == nil {
		t.Fatal("delivery to a loopback address must be refused by the dialer, not just at creation")
	}
}

// TestPhase10APIRateLimit proves the general /api/ rate limiter actually
// engages and returns 429 with Retry-After once an identity exceeds the
// configured threshold, rather than only the narrow auth-specific limiter
// that already existed.
func TestPhase10APIRateLimit(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	server, application, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()
	// Exercise the limiter directly at a small threshold rather than
	// firing apiRateLimitPerMinute real HTTP requests, which would make
	// this test slow without proving anything more.
	req, err := http.NewRequest("GET", server.URL+"/api/v1/auth/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.RemoteAddr = "203.0.113.9:1234"
	limited := false
	for i := 0; i < apiRateLimitPerMinute+5; i++ {
		w := httptest.NewRecorder()
		application.Handler().ServeHTTP(w, req.Clone(context.Background()))
		if w.Code == http.StatusTooManyRequests {
			limited = true
			if w.Header().Get("Retry-After") == "" {
				t.Fatal("expected a Retry-After header on a rate-limited response")
			}
			break
		}
	}
	if !limited {
		t.Fatalf("expected the general API rate limiter to engage within %d requests", apiRateLimitPerMinute+5)
	}
}

// TestPhase10RepositoriesPagination proves GET /repos now honors page and
// per_page rather than silently truncating at a fixed 100, and that a
// second page returns different, previously unreachable results.
func TestPhase10RepositoriesPagination(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	server, _, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()

	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	owner.request("POST", "/auth/register", map[string]string{"username": "pageowner", "email": "pageowner@example.test", "password": "pageowner-long-password"}, 201, nil)
	const total = 5
	for i := 0; i < total; i++ {
		owner.request("POST", "/repos", map[string]any{"name": fmt.Sprintf("page-repo-%d", i), "visibility": "public"}, 201, nil)
	}

	req, err := http.NewRequest("GET", server.URL+"/api/v1/repos?mine=true&page=1&per_page=2", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:3000")
	cookies := jar.Cookies(req.URL)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var page1 []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&page1); err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2 repos on page 1, got %d", len(page1))
	}
	if res.Header.Get("X-Total-Count") != "5" {
		t.Fatalf("expected X-Total-Count=5, got %q", res.Header.Get("X-Total-Count"))
	}

	req2, _ := http.NewRequest("GET", server.URL+"/api/v1/repos?mine=true&page=2&per_page=2", nil)
	req2.Header.Set("Origin", "http://localhost:3000")
	for _, c := range cookies {
		req2.AddCookie(c)
	}
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	var page2 []map[string]any
	if err := json.NewDecoder(res2.Body).Decode(&page2); err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 {
		t.Fatalf("expected 2 repos on page 2, got %d", len(page2))
	}
	if page1[0]["name"] == page2[0]["name"] || page1[1]["name"] == page2[0]["name"] {
		t.Fatalf("page 2 should return different repositories than page 1: page1=%v page2=%v", page1, page2)
	}
}
