package app

import (
	"net/http"
	"net/http/cookiejar"
	"os"
	"testing"
	"time"
)

func TestTOTPTimeStepAndValidation(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	code, ok := totpCode(secret, 1)
	if !ok || code != "287082" {
		t.Fatalf("RFC TOTP vector mismatch: %q %v", code, ok)
	}
	step, valid := verifyTOTP(secret, code, time.Unix(59, 0))
	if !valid || step != 1 {
		t.Fatalf("valid current TOTP rejected: step=%d valid=%v", step, valid)
	}
	if _, valid = verifyTOTP(secret, "12x456", time.Unix(59, 0)); valid {
		t.Fatal("accepted a non-numeric code")
	}
	wrong := "000000"
	if wrong == code {
		wrong = "000001"
	}
	if _, valid = verifyTOTP(secret, wrong, time.Unix(59, 0)); valid {
		t.Fatal("accepted an incorrect code")
	}
}

func TestPhase13MFAChallengeAndRecoveryCode(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	t.Setenv("GITOWN_REQUIRE_VERIFIED_EMAIL", "false")
	t.Setenv("GITOWN_SECRET_KEY", "phase13-mfa-test-secret-key")
	server, _, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()
	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	anon := testClient{t, server.URL, &http.Client{}}
	password := "mfa-owner-password-long"
	owner.request("POST", "/auth/register", map[string]string{"username": "mfaowner", "email": "mfaowner@example.test", "password": password}, 201, nil)
	var setup struct {
		Secret string `json:"secret"`
	}
	owner.request("POST", "/user/mfa/setup", map[string]string{"current_password": password}, 200, &setup)
	stepNow := time.Now().Unix() / 30
	setupCode, _ := totpCode(setup.Secret, stepNow)
	var activated struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	owner.request("POST", "/user/mfa/confirm", map[string]string{"current_password": password, "code": setupCode}, 200, &activated)
	if len(activated.RecoveryCodes) != mfaRecoveryCodeCount {
		t.Fatalf("expected one-time recovery codes, got %d", len(activated.RecoveryCodes))
	}
	credentials := map[string]string{"username": "mfaowner", "password": password}
	var challenge struct {
		Required  bool   `json:"mfa_required"`
		Challenge string `json:"challenge"`
	}
	anon.request("POST", "/auth/login", credentials, 200, &challenge)
	if !challenge.Required || challenge.Challenge == "" {
		t.Fatalf("password-only login did not stop for MFA: %+v", challenge)
	}
	stepCode, _ := totpCode(setup.Secret, stepNow+1)
	anon.request("POST", "/auth/mfa/verify", map[string]string{"challenge": challenge.Challenge, "code": stepCode}, 200, nil)
	anon.request("POST", "/auth/mfa/verify", map[string]string{"challenge": challenge.Challenge, "code": stepCode}, 401, nil)
	anon.request("POST", "/auth/login", credentials, 200, &challenge)
	anon.request("POST", "/auth/mfa/verify", map[string]string{"challenge": challenge.Challenge, "recovery_code": activated.RecoveryCodes[0]}, 200, nil)
	anon.request("POST", "/auth/login", credentials, 200, &challenge)
	anon.request("POST", "/auth/mfa/verify", map[string]string{"challenge": challenge.Challenge, "recovery_code": activated.RecoveryCodes[0]}, 401, nil)
}

func TestPhase1RecoveryRotationAndStepUp(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL and real Git integration tests")
	}
	t.Setenv("GITOWN_REQUIRE_VERIFIED_EMAIL", "false")
	t.Setenv("GITOWN_SECRET_KEY", "phase1-step-up-test-secret-key")
	t.Setenv("GITOWN_OPERATORS", "phase1guard")
	server, application, closeServer := newPhase9Server(t, databaseURL)
	defer closeServer()
	jar, _ := cookiejar.New(nil)
	owner := testClient{t, server.URL, &http.Client{Jar: jar}}
	anon := testClient{t, server.URL, &http.Client{}}
	owner.request("POST", "/auth/register", map[string]string{"username": "phase1guard", "email": "phase1guard@example.test", "password": "phase1-account-password"}, 201, nil)
	anon.request("POST", "/auth/login", map[string]string{"username": "phase1guard", "password": "incorrect-password"}, 401, nil)
	var abuse struct {
		Events []struct {
			Kind  string `json:"kind"`
			Scope string `json:"scope_fingerprint"`
			IP    string `json:"network_fingerprint"`
		} `json:"events"`
	}
	owner.request("GET", "/operator/security/abuse", nil, 200, &abuse)
	if len(abuse.Events) == 0 || abuse.Events[0].Kind != "login_failed" || len(abuse.Events[0].Scope) != 12 || len(abuse.Events[0].IP) != 12 {
		t.Fatalf("operator abuse view did not return redacted authentication events: %+v", abuse.Events)
	}
	var setup struct {
		Secret string `json:"secret"`
	}
	owner.request("POST", "/user/mfa/setup", map[string]string{"current_password": "phase1-account-password"}, 200, &setup)
	step := time.Now().Unix() / 30
	code, _ := totpCode(setup.Secret, step)
	var enabled struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	owner.request("POST", "/user/mfa/confirm", map[string]string{"current_password": "phase1-account-password", "code": code}, 200, &enabled)
	owner.request("PATCH", "/user/password", map[string]any{"current_password": "phase1-account-password", "new_password": "replacement-account-password", "revoke_access_tokens": true}, 422, nil)

	// Rotate with a fresh time step and verify the replacement set is returned once.
	rotationCode, _ := totpCode(setup.Secret, step+1)
	var rotated struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	owner.request("POST", "/user/mfa/recovery-codes/regenerate", map[string]string{"current_password": "phase1-account-password", "code": rotationCode}, 200, &rotated)
	if len(rotated.RecoveryCodes) != mfaRecoveryCodeCount {
		t.Fatalf("expected %d replacement recovery codes, got %d", mfaRecoveryCodeCount, len(rotated.RecoveryCodes))
	}
	var remaining int
	if err := application.db.QueryRow(t.Context(), `SELECT count(*) FROM mfa_recovery_codes c JOIN users u ON u.id=c.user_id WHERE u.username='phase1guard'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != mfaRecoveryCodeCount {
		t.Fatalf("old recovery set was not replaced atomically: %d records remain", remaining)
	}
	var challenge struct {
		Challenge string `json:"challenge"`
	}
	anon.request("POST", "/auth/login", map[string]string{"username": "phase1guard", "password": "phase1-account-password"}, 200, &challenge)
	anon.request("POST", "/auth/mfa/verify", map[string]string{"challenge": challenge.Challenge, "recovery_code": enabled.RecoveryCodes[0]}, 401, nil)

	// Age the session's confirmation and prove sensitive credential creation is blocked.
	if _, err := application.db.Exec(t.Context(), `UPDATE sessions SET step_up_at=now()-interval '11 minutes' WHERE user_id=(SELECT id FROM users WHERE username='phase1guard')`); err != nil {
		t.Fatal(err)
	}
	owner.request("POST", "/user/tokens", map[string]string{"name": "must-confirm", "scope": "repo:read"}, 428, nil)
	owner.request("POST", "/user/step-up", map[string]string{"current_password": "phase1-account-password", "recovery_code": rotated.RecoveryCodes[0]}, 200, nil)
	owner.request("POST", "/user/tokens", map[string]string{"name": "confirmed", "scope": "repo:read"}, 201, nil)
}
