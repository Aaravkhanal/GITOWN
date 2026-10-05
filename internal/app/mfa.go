package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

const mfaRecoveryCodeCount = 10

func newTOTPSecret() (string, error) {
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), nil
}

func totpCode(secret string, step int64) (string, bool) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil || len(key) == 0 {
		return "", false
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(counter[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000), true
}

func verifyTOTP(secret, code string, now time.Time) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	for _, char := range code {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	current := now.Unix() / 30
	for step := current - 1; step <= current+1; step++ {
		expected, ok := totpCode(secret, step)
		if ok && subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

type mfaStatus struct {
	Enabled       bool `json:"enabled"`
	RecoveryCodes int  `json:"recovery_codes_remaining"`
	SetupPending  bool `json:"setup_pending"`
}

func (a *App) mfaSettings(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var status mfaStatus
	err := a.db.QueryRow(r.Context(), `SELECT COALESCE(enabled_at IS NOT NULL,false),pending_secret_cipher IS NOT NULL FROM user_mfa WHERE user_id=$1`, u.ID).Scan(&status.Enabled, &status.SetupPending)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	if status.Enabled {
		if err = a.db.QueryRow(r.Context(), `SELECT count(*) FROM mfa_recovery_codes WHERE user_id=$1`, u.ID).Scan(&status.RecoveryCodes); err != nil {
			serverError(w, err)
			return
		}
	}
	respond(w, 200, status)
}

func (a *App) setupMFA(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		CurrentPassword string `json:"current_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.CurrentPassword) == 0 || len(in.CurrentPassword) > 128 {
		fail(w, 422, "validation_failed", "Confirm your current password to continue.")
		return
	}
	if !a.authLimitFor(w, r, u.ID) {
		return
	}
	defer func() { <-a.passwords }()
	if !a.currentPasswordMatches(r, u.ID, in.CurrentPassword) {
		fail(w, 401, "invalid_credentials", "Current password is incorrect.")
		return
	}
	secret, err := newTOTPSecret()
	if err != nil {
		serverError(w, err)
		return
	}
	ciphertext, nonce, err := sealSecret(secret)
	if errors.Is(err, errSecretUnconfigured) {
		fail(w, 503, "secret_key_unconfigured", "Set GITOWN_SECRET_KEY before enabling MFA.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	result, err := a.db.Exec(r.Context(), `INSERT INTO user_mfa(user_id,pending_secret_cipher,pending_secret_nonce,pending_expires_at)
		VALUES($1,$2,$3,now()+interval '15 minutes') ON CONFLICT(user_id) DO UPDATE SET
		pending_secret_cipher=EXCLUDED.pending_secret_cipher,pending_secret_nonce=EXCLUDED.pending_secret_nonce,
		pending_expires_at=EXCLUDED.pending_expires_at WHERE user_mfa.enabled_at IS NULL`, u.ID, ciphertext, nonce)
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() != 1 {
		fail(w, 409, "mfa_already_enabled", "Disable MFA before setting up a new authenticator.")
		return
	}
	label := url.QueryEscape("GITOWN:" + u.Username)
	issuer := url.QueryEscape("GITOWN")
	uri := fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30", label, secret, issuer)
	respond(w, 200, map[string]string{"secret": secret, "otpauth_uri": uri})
}

func (a *App) confirmMFA(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		CurrentPassword string `json:"current_password"`
		Code            string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.CurrentPassword) == 0 || len(in.CurrentPassword) > 128 {
		fail(w, 422, "validation_failed", "Confirm your current password to continue.")
		return
	}
	if !a.authLimitFor(w, r, u.ID) {
		return
	}
	defer func() { <-a.passwords }()
	if !a.currentPasswordMatches(r, u.ID, in.CurrentPassword) {
		fail(w, 401, "invalid_credentials", "Current password is incorrect.")
		return
	}
	var pendingCipher, pendingNonce []byte
	err := a.db.QueryRow(r.Context(), `SELECT pending_secret_cipher,pending_secret_nonce FROM user_mfa WHERE user_id=$1 AND enabled_at IS NULL AND pending_expires_at>now()`, u.ID).Scan(&pendingCipher, &pendingNonce)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 409, "mfa_setup_expired", "Start MFA setup again to get a fresh secret.")
		} else {
			serverError(w, err)
		}
		return
	}
	secret, err := openSecret(pendingCipher, pendingNonce)
	if err != nil {
		serverError(w, err)
		return
	}
	step, valid := verifyTOTP(secret, in.Code, time.Now())
	if !valid {
		a.recordAuthAbuse(r, "mfa_setup_failed", u.ID, r.URL.Path)
		fail(w, 401, "invalid_mfa_code", "That authenticator code is not valid.")
		return
	}
	codes := make([]string, mfaRecoveryCodeCount)
	for i := range codes {
		codes[i] = auth.Secret("GITOWN-")
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	result, err := tx.Exec(r.Context(), `UPDATE user_mfa SET secret_cipher=pending_secret_cipher,secret_nonce=pending_secret_nonce,
		pending_secret_cipher=NULL,pending_secret_nonce=NULL,pending_expires_at=NULL,enabled_at=now(),last_totp_step=$2
		WHERE user_id=$1 AND enabled_at IS NULL AND pending_expires_at>now() AND last_totp_step<$2`, u.ID, step)
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() != 1 {
		fail(w, 409, "mfa_setup_expired", "Start MFA setup again to get a fresh secret.")
		return
	}
	for _, code := range codes {
		if _, err = tx.Exec(r.Context(), `INSERT INTO mfa_recovery_codes(user_id,code_hash) VALUES($1,$2)`, u.ID, auth.Digest(code)); err != nil {
			serverError(w, err)
			return
		}
	}
	if err = a.queueSecurityNotice(r, tx, u.ID, "Two-step sign-in was enabled", "Two-step sign-in was enabled for your GITOWN account. Save the recovery codes shown once in your browser."); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'account.mfa_enabled','self')`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"enabled": true, "recovery_codes": codes})
}

func (a *App) disableMFA(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
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
	if len(in.CurrentPassword) == 0 || len(in.CurrentPassword) > 128 || len(in.Code) > 6 || len(in.RecoveryCode) > 128 {
		fail(w, 422, "validation_failed", "Confirm your current password and provide one valid authenticator or recovery code.")
		return
	}
	if (in.Code == "") == (in.RecoveryCode == "") {
		fail(w, 422, "validation_failed", "Provide one authenticator code or one recovery code.")
		return
	}
	if !a.authLimitFor(w, r, u.ID) {
		return
	}
	defer func() { <-a.passwords }()
	if !a.currentPasswordMatches(r, u.ID, in.CurrentPassword) {
		fail(w, 401, "invalid_credentials", "Current password is incorrect.")
		return
	}
	var ciphertext, nonce []byte
	if err := a.db.QueryRow(r.Context(), `SELECT secret_cipher,secret_nonce FROM user_mfa WHERE user_id=$1 AND enabled_at IS NOT NULL`, u.ID).Scan(&ciphertext, &nonce); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 409, "mfa_not_enabled", "Two-step sign-in is not enabled.")
		} else {
			serverError(w, err)
		}
		return
	}
	secret, err := openSecret(ciphertext, nonce)
	if err != nil {
		serverError(w, err)
		return
	}
	step, codeValid := verifyTOTP(secret, in.Code, time.Now())
	recoveryHash := ""
	if !codeValid && in.RecoveryCode != "" {
		recoveryHash = auth.Digest(in.RecoveryCode)
	}
	if !codeValid && recoveryHash == "" {
		a.recordAuthAbuse(r, "mfa_recovery_failed", u.ID, r.URL.Path)
		fail(w, 401, "invalid_mfa_code", "Provide a valid authenticator or recovery code.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var lockedUserID string
	if err = tx.QueryRow(r.Context(), `SELECT user_id FROM user_mfa WHERE user_id=$1 AND enabled_at IS NOT NULL FOR UPDATE`, u.ID).Scan(&lockedUserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 409, "mfa_not_enabled", "Two-step sign-in is no longer enabled.")
		} else {
			serverError(w, err)
		}
		return
	}
	if codeValid {
		result, updateErr := tx.Exec(r.Context(), `UPDATE user_mfa SET last_totp_step=$2 WHERE user_id=$1 AND enabled_at IS NOT NULL AND last_totp_step<$2`, u.ID, step)
		if updateErr != nil {
			serverError(w, updateErr)
			return
		}
		if result.RowsAffected() != 1 {
			fail(w, 401, "invalid_mfa_code", "That authenticator code was already used.")
			return
		}
	} else {
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
	if _, err = tx.Exec(r.Context(), `UPDATE user_mfa SET secret_cipher=NULL,secret_nonce=NULL,pending_secret_cipher=NULL,pending_secret_nonce=NULL,pending_expires_at=NULL,enabled_at=NULL,last_totp_step=-1 WHERE user_id=$1`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM mfa_recovery_codes WHERE user_id=$1`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM mfa_login_challenges WHERE user_id=$1`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err = a.queueSecurityNotice(r, tx, u.ID, "Two-step sign-in was disabled", "Two-step sign-in was disabled for your GITOWN account."); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'account.mfa_disabled','self')`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"enabled": false})
}

// regenerateMFARecoveryCodes requires both the current password and a fresh
// second factor. Existing recovery codes are invalidated in the same
// transaction that stores the replacement set, and plaintext is returned
// only in this response.
func (a *App) regenerateMFARecoveryCodes(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
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
	if len(in.CurrentPassword) == 0 || len(in.CurrentPassword) > 128 || len(in.Code) > 6 || len(in.RecoveryCode) > 128 || (in.Code == "") == (in.RecoveryCode == "") {
		fail(w, 422, "validation_failed", "Confirm your password and provide one authenticator or recovery code.")
		return
	}
	if !a.authLimitFor(w, r, u.ID) {
		return
	}
	defer func() { <-a.passwords }()
	if !a.currentPasswordMatches(r, u.ID, in.CurrentPassword) {
		fail(w, 401, "invalid_credentials", "Current password is incorrect.")
		return
	}
	var ciphertext, nonce []byte
	if err := a.db.QueryRow(r.Context(), `SELECT secret_cipher,secret_nonce FROM user_mfa WHERE user_id=$1 AND enabled_at IS NOT NULL`, u.ID).Scan(&ciphertext, &nonce); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 409, "mfa_not_enabled", "Enable two-step sign-in before generating recovery codes.")
		} else {
			serverError(w, err)
		}
		return
	}
	secret, err := openSecret(ciphertext, nonce)
	if err != nil {
		serverError(w, err)
		return
	}
	step, codeValid := verifyTOTP(secret, in.Code, time.Now())
	recoveryHash := ""
	if in.RecoveryCode != "" {
		recoveryHash = auth.Digest(in.RecoveryCode)
	}
	if !codeValid && recoveryHash == "" {
		a.recordAuthAbuse(r, "mfa_recovery_failed", u.ID, r.URL.Path)
		fail(w, 401, "invalid_mfa_code", "That authenticator or recovery code is invalid.")
		return
	}
	codes := make([]string, mfaRecoveryCodeCount)
	for i := range codes {
		codes[i] = auth.Secret("GITOWN-")
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var lockedUserID string
	if err = tx.QueryRow(r.Context(), `SELECT user_id FROM user_mfa WHERE user_id=$1 AND enabled_at IS NOT NULL FOR UPDATE`, u.ID).Scan(&lockedUserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 409, "mfa_not_enabled", "Two-step sign-in is no longer enabled.")
		} else {
			serverError(w, err)
		}
		return
	}
	if codeValid {
		result, updateErr := tx.Exec(r.Context(), `UPDATE user_mfa SET last_totp_step=$2 WHERE user_id=$1 AND enabled_at IS NOT NULL AND last_totp_step<$2`, u.ID, step)
		if updateErr != nil {
			serverError(w, updateErr)
			return
		}
		if result.RowsAffected() != 1 {
			fail(w, 401, "invalid_mfa_code", "That authenticator code was already used.")
			return
		}
	} else {
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
	if _, err = tx.Exec(r.Context(), `DELETE FROM mfa_recovery_codes WHERE user_id=$1`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	for _, code := range codes {
		if _, err = tx.Exec(r.Context(), `INSERT INTO mfa_recovery_codes(user_id,code_hash) VALUES($1,$2)`, u.ID, auth.Digest(code)); err != nil {
			serverError(w, err)
			return
		}
	}
	if err = a.queueSecurityNotice(r, tx, u.ID, "MFA recovery codes regenerated", "Your GITOWN MFA recovery codes were regenerated. All previously issued recovery codes are no longer valid. If you did not do this, review your sessions and secure your account."); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'account.mfa_recovery_codes_regenerated','self')`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"recovery_codes": codes})
}

func (a *App) verifyMFAChallenge(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Challenge    string `json:"challenge"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Challenge) > 128 || len(in.Code) > 6 || len(in.RecoveryCode) > 128 || !strings.HasPrefix(in.Challenge, "mfachallenge_") || (in.Code == "") == (in.RecoveryCode == "") {
		a.recordAuthAbuse(r, "mfa_challenge_failed", in.Challenge, r.URL.Path)
		fail(w, 401, "invalid_mfa_challenge", "Use a valid sign-in challenge and one authenticator or recovery code.")
		return
	}
	principal := in.Challenge
	var challengeUserID string
	lookupErr := a.db.QueryRow(r.Context(), `SELECT user_id FROM mfa_login_challenges WHERE token_hash=$1 AND expires_at>now()`, auth.Digest(in.Challenge)).Scan(&challengeUserID)
	if lookupErr == nil {
		principal = challengeUserID
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		serverError(w, lookupErr)
		return
	}
	if !a.authLimitFor(w, r, principal) {
		return
	}
	defer func() { <-a.passwords }()
	var u User
	var ciphertext, nonce []byte
	err := a.db.QueryRow(r.Context(), `SELECT u.id,u.username,u.display_name,m.secret_cipher,m.secret_nonce
		FROM mfa_login_challenges c JOIN users u ON u.id=c.user_id JOIN user_mfa m ON m.user_id=u.id
		WHERE c.token_hash=$1 AND c.expires_at>now() AND m.enabled_at IS NOT NULL`, auth.Digest(in.Challenge)).Scan(&u.ID, &u.Username, &u.DisplayName, &ciphertext, &nonce)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			a.recordAuthAbuse(r, "mfa_challenge_failed", in.Challenge, r.URL.Path)
			fail(w, 401, "invalid_mfa_challenge", "This sign-in challenge is invalid or expired. Sign in again.")
		} else {
			serverError(w, err)
		}
		return
	}
	secret, err := openSecret(ciphertext, nonce)
	if err != nil {
		serverError(w, err)
		return
	}
	step, codeValid := verifyTOTP(secret, in.Code, time.Now())
	recoveryHash := ""
	if !codeValid && in.RecoveryCode != "" {
		recoveryHash = auth.Digest(in.RecoveryCode)
	}
	if !codeValid && recoveryHash == "" {
		a.recordAuthAbuse(r, "mfa_challenge_failed", u.ID, r.URL.Path)
		fail(w, 401, "invalid_mfa_code", "That authenticator or recovery code is invalid.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var lockedUserID string
	if err = tx.QueryRow(r.Context(), `SELECT user_id FROM user_mfa WHERE user_id=$1 AND enabled_at IS NOT NULL FOR UPDATE`, u.ID).Scan(&lockedUserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 409, "mfa_not_enabled", "Two-step sign-in is no longer enabled.")
		} else {
			serverError(w, err)
		}
		return
	}
	var challengeUser string
	err = tx.QueryRow(r.Context(), `DELETE FROM mfa_login_challenges WHERE token_hash=$1 AND expires_at>now() RETURNING user_id`, auth.Digest(in.Challenge)).Scan(&challengeUser)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && challengeUser != u.ID) {
		fail(w, 401, "invalid_mfa_challenge", "This sign-in challenge is invalid or expired. Sign in again.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if codeValid {
		result, updateErr := tx.Exec(r.Context(), `UPDATE user_mfa SET last_totp_step=$2 WHERE user_id=$1 AND enabled_at IS NOT NULL AND last_totp_step<$2`, u.ID, step)
		if updateErr != nil {
			serverError(w, updateErr)
			return
		}
		if result.RowsAffected() != 1 {
			fail(w, 401, "invalid_mfa_code", "That authenticator code was already used.")
			return
		}
	} else {
		result, deleteErr := tx.Exec(r.Context(), `DELETE FROM mfa_recovery_codes WHERE user_id=$1 AND code_hash=$2`, u.ID, recoveryHash)
		if deleteErr != nil {
			serverError(w, deleteErr)
			return
		}
		if result.RowsAffected() != 1 {
			fail(w, 401, "invalid_mfa_code", "That authenticator or recovery code is invalid.")
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	if err = a.session(w, r, u); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"user": u})
}

func (a *App) queueSecurityNotice(r *http.Request, tx pgx.Tx, userID, subject, body string) error {
	var email string
	if err := tx.QueryRow(r.Context(), `SELECT email FROM users WHERE id=$1`, userID).Scan(&email); err != nil {
		return err
	}
	return queueAddressMail(r.Context(), tx, email, "security_notice", subject, body, "")
}

func (a *App) currentPasswordMatches(r *http.Request, userID, password string) bool {
	var encoded string
	if err := a.db.QueryRow(r.Context(), `SELECT password_hash FROM users WHERE id=$1`, userID).Scan(&encoded); err != nil {
		return false
	}
	return auth.CheckPassword(encoded, password)
}
