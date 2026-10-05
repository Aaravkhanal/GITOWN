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
