package app

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

// requiresStepUp enumerates high-impact browser actions. Repository pushes
// and ordinary collaboration writes remain governed by repository policy;
// irreversible credential and ownership changes require fresh proof.
func requiresStepUp(method, path string) bool {
	switch {
	case method == http.MethodPost && (path == "/api/v1/user/tokens" || path == "/api/v1/user/oauth-apps" || path == "/api/v1/user/gitown-apps" || path == "/api/v1/user/ssh-keys" || path == "/api/v1/user/signing-keys"):
		return true
	case (method == http.MethodDelete || method == http.MethodPost) && (strings.HasPrefix(path, "/api/v1/user/tokens/") || strings.HasPrefix(path, "/api/v1/user/sessions/") || strings.HasPrefix(path, "/api/v1/user/authorized-apps/") || strings.HasPrefix(path, "/api/v1/user/oauth-apps/") || strings.HasPrefix(path, "/api/v1/user/gitown-apps/") || strings.HasPrefix(path, "/api/v1/user/ssh-keys/") || strings.HasPrefix(path, "/api/v1/user/signing-keys/")):
		return true
	case strings.HasPrefix(path, "/api/v1/repos/"):
		return sensitiveRepositoryAction(method, path)
	case strings.HasPrefix(path, "/api/v1/districts/"):
		return sensitiveDistrictAction(method, path)
	}
	return false
}

func sensitiveRepositoryAction(method, path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 5 { // api/v1/repos/{owner}/{repo}
		return false
	}
	if len(parts) == 5 {
		return method == http.MethodPatch || method == http.MethodDelete
	}
	switch parts[5] {
	case "transfer":
		return method == http.MethodPost || method == http.MethodDelete
	case "rename":
		return method == http.MethodPost
	case "archive", "unarchive":
		return method == http.MethodPost
	case "tags":
		return method == http.MethodPost || method == http.MethodDelete
	case "members":
		return method == http.MethodPatch || method == http.MethodDelete
	case "invitations", "deploy-keys", "gitown-apps":
		return method == http.MethodPost || method == http.MethodDelete
	case "branch-rules":
		return method == http.MethodPut
	case "webhooks":
		return method == http.MethodPost || method == http.MethodPatch || method == http.MethodDelete
	default:
		return false
	}
}

func sensitiveDistrictAction(method, path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 5 { // api/v1/districts/{slug}/{area}
		return false
	}
	switch parts[4] {
	case "secrets":
		return method == http.MethodPost || method == http.MethodDelete
	case "webhooks":
		return method == http.MethodPost || method == http.MethodPatch || method == http.MethodDelete
	default:
		return false
	}
}

func (a *App) hasRecentStepUp(w http.ResponseWriter, r *http.Request) bool {
	cookie, err := r.Cookie("gitown_session")
	if err != nil || cookie.Value == "" {
		fail(w, 428, "step_up_required", "Confirm your identity with your password and second factor before this action.")
		return false
	}
	var recent bool
	err = a.db.QueryRow(r.Context(), `SELECT COALESCE(step_up_at > now()-interval '10 minutes',false) FROM sessions WHERE token_hash=$1 AND expires_at>now()`, auth.Digest(cookie.Value)).Scan(&recent)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return false
	}
	if !recent {
		w.Header().Set("X-GITOWN-Step-Up", "required")
		var mfaEnabled bool
		if err := a.db.QueryRow(r.Context(), `SELECT COALESCE(enabled_at IS NOT NULL,false) FROM user_mfa WHERE user_id=(SELECT user_id FROM sessions WHERE token_hash=$1 AND expires_at>now())`, auth.Digest(cookie.Value)).Scan(&mfaEnabled); err == nil {
			if mfaEnabled {
				w.Header().Set("X-GITOWN-MFA-Required", "true")
			} else {
				w.Header().Set("X-GITOWN-MFA-Required", "false")
			}
		}
		fail(w, 428, "step_up_required", "Confirm your identity with your password and second factor before this action.")
		return false
	}
	return true
}

func (a *App) stepUpAuthentication(w http.ResponseWriter, r *http.Request) {
	u := a.sessionUser(r)
	if u == nil {
		fail(w, 401, "authentication_required", "Sign in with a browser session to confirm your identity.")
		return
	}
	var in struct {
		CurrentPassword string `json:"current_password"`
		Code            string `json:"code"`
		RecoveryCode    string `json:"recovery_code"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.CurrentPassword) == 0 || len(in.CurrentPassword) > 128 || len(in.Code) > 6 || len(in.RecoveryCode) > 128 || (in.Code != "" && in.RecoveryCode != "") {
		fail(w, 422, "validation_failed", "Enter your password and, when enabled, one authenticator or recovery code.")
		return
	}
	if !a.authLimitFor(w, r, u.ID) {
		return
	}
	defer func() { <-a.passwords }()
	if !a.currentPasswordMatches(r, u.ID, in.CurrentPassword) {
		a.recordAuthAbuse(r, "step_up_failed", u.ID, r.URL.Path)
		fail(w, 401, "invalid_credentials", "Current password is incorrect.")
		return
	}
	var mfaEnabled bool
	var ciphertext, nonce []byte
	err := a.db.QueryRow(r.Context(), `SELECT enabled_at IS NOT NULL,secret_cipher,secret_nonce FROM user_mfa WHERE user_id=$1`, u.ID).Scan(&mfaEnabled, &ciphertext, &nonce)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	var step int64
	codeValid := false
	recoveryHash := ""
	if mfaEnabled {
		if (in.Code == "") == (in.RecoveryCode == "") {
			fail(w, 422, "mfa_required", "Enter one authenticator or unused recovery code to confirm your identity.")
			return
		}
		secret, openErr := openSecret(ciphertext, nonce)
		if openErr != nil {
			serverError(w, openErr)
			return
		}
		step, codeValid = verifyTOTP(secret, in.Code, time.Now())
		if !codeValid && in.RecoveryCode != "" {
			recoveryHash = auth.Digest(in.RecoveryCode)
		}
		if !codeValid && recoveryHash == "" {
			fail(w, 401, "invalid_mfa_code", "That authenticator or recovery code is invalid.")
			return
		}
	} else if in.Code != "" || in.RecoveryCode != "" {
		fail(w, 422, "mfa_not_enabled", "This account does not have an authenticator configured.")
		return
	}
	cookie, _ := r.Cookie("gitown_session")
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if mfaEnabled && codeValid {
		result, updateErr := tx.Exec(r.Context(), `UPDATE user_mfa SET last_totp_step=$2 WHERE user_id=$1 AND enabled_at IS NOT NULL AND last_totp_step<$2`, u.ID, step)
		if updateErr != nil {
			serverError(w, updateErr)
			return
		}
		if result.RowsAffected() != 1 {
			fail(w, 401, "invalid_mfa_code", "That authenticator code was already used.")
			return
		}
	} else if mfaEnabled {
		result, deleteErr := tx.Exec(r.Context(), `DELETE FROM mfa_recovery_codes WHERE user_id=$1 AND code_hash=$2`, u.ID, recoveryHash)
		if deleteErr != nil {
			serverError(w, deleteErr)
			return
		}
		if result.RowsAffected() != 1 {
			fail(w, 401, "invalid_mfa_code", "That recovery code is invalid or was already used.")
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `UPDATE sessions SET step_up_at=now() WHERE token_hash=$1 AND user_id=$2 AND expires_at>now()`, auth.Digest(cookie.Value), u.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'account.step_up','self')`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"confirmed": true, "valid_for_seconds": 600})
}
