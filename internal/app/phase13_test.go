package app

import (
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"testing"
)

func TestPhase13PasswordRecovery(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	server, application, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()
	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	anon := testClient{t, server.URL, &http.Client{}}
	owner.request("POST", "/auth/register", map[string]string{"username": "recover", "email": "recover@example.test", "password": "initial-password-long"}, 201, nil)
	owner.token("repo:write")
	var unknown, known map[string]string
	anon.request("POST", "/auth/password/forgot", map[string]string{"email": "missing@example.test"}, 200, &unknown)
	anon.request("POST", "/auth/password/forgot", map[string]string{"email": "recover@example.test"}, 200, &known)
	if unknown["message"] == "" || unknown["message"] != known["message"] {
		t.Fatalf("password recovery responses reveal account existence: unknown=%v known=%v", unknown, known)
	}
	var body string
	if err := application.db.QueryRow(t.Context(), `SELECT body FROM email_messages WHERE recipient_email=$1 AND kind='password_reset' ORDER BY created_at DESC LIMIT 1`, "recover@example.test").Scan(&body); err != nil {
		t.Fatal(err)
	}
	start := strings.LastIndex(body, "token=")
	if start < 0 {
		t.Fatalf("reset mail has no reset link: %q", body)
	}
	token := strings.Fields(body[start+len("token="):])[0]
	anon.request("POST", "/auth/password/reset", map[string]string{"token": token, "new_password": "replacement-password-long"}, 200, nil)
	var remaining int
	if err := application.db.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM sessions WHERE user_id=u.id)+(SELECT count(*) FROM access_tokens WHERE user_id=u.id) FROM users u WHERE username='recover'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("reset did not revoke sessions and tokens: %d remain", remaining)
	}
	anon.request("POST", "/auth/password/reset", map[string]string{"token": token, "new_password": "another-password-long"}, 422, nil)
	owner.request("POST", "/auth/login", map[string]string{"username": "recover", "password": "initial-password-long"}, 401, nil)
	owner.request("POST", "/auth/login", map[string]string{"username": "recover", "password": "replacement-password-long"}, 200, nil)
}
