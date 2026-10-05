package app

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

func (a *App) register(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.Signup {
		fail(w, 403, "signup_disabled", "Registration is disabled on this instance.")
		return
	}
	var in struct {
		Username    string `json:"username"`
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	email, err := mail.ParseAddress(in.Email)
	reserved := map[string]bool{"api": true, "git": true, "settings": true, "login": true, "register": true, "new": true, "explore": true, "admin": true, "healthz": true}
	if !slug.MatchString(in.Username) || reserved[in.Username] || err != nil || email.Address != in.Email || len(in.Email) > 254 || len(in.Password) < 12 || len(in.Password) > 128 || len(in.DisplayName) > 80 {
		fail(w, 422, "validation_failed", "Use a valid username and email, and a password between 12 and 128 characters.")
		return
	}
	if !a.authLimitFor(w, r, in.Email) {
		return
	}
	defer func() { <-a.passwords }()
	if in.DisplayName == "" {
		in.DisplayName = in.Username
	}
	u := User{ID: auth.ID(), Username: in.Username, DisplayName: in.DisplayName}
	password := auth.HashPassword(in.Password)
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO users(id,username,email,display_name,password_hash) VALUES($1,$2,$3,$4,$5)`, u.ID, u.Username, in.Email, u.DisplayName, password)
	if conflict(err) {
		fail(w, 409, "account_exists", "That username or email is unavailable.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'account.created',$2)`, u.ID, u.Username); err != nil {
		serverError(w, err)
		return
	}
	requireEmailVerification := a.emailVerificationRequired()
	if requireEmailVerification {
		if err = queueEmailVerification(r.Context(), tx, u.ID, in.Email, a.cfg.Origin); err != nil {
			serverError(w, err)
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	if requireEmailVerification {
		respond(w, 201, map[string]any{"user": u, "verification_required": true})
		return
	}
	if err = a.session(w, r, u); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]any{"user": u})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Username) > 254 || len(in.Password) > 128 {
		fail(w, 401, "invalid_credentials", "Incorrect username or password.")
		return
	}
	var u User
	var encoded string
	var emailVerified bool
	var mfaEnabled bool
	principal := strings.ToLower(strings.TrimSpace(in.Username))
	err := a.db.QueryRow(r.Context(), `SELECT u.id,u.username,u.display_name,u.password_hash,u.email_verified_at IS NOT NULL,COALESCE(m.enabled_at IS NOT NULL,false)
		FROM users u LEFT JOIN user_mfa m ON m.user_id=u.id WHERE u.username=$1 OR u.email=$1`, strings.ToLower(strings.TrimSpace(in.Username))).Scan(&u.ID, &u.Username, &u.DisplayName, &encoded, &emailVerified, &mfaEnabled)
	if err != nil {
		encoded = a.dummyHash
	} else {
		principal = u.ID // Username and email use one canonical account throttle bucket.
	}
	if !a.authLimitFor(w, r, principal) {
		return
	}
	defer func() { <-a.passwords }()
	valid := auth.CheckPassword(encoded, in.Password)
	if err != nil || !valid {
		a.recordFailedLogin(r, principal)
		fail(w, 401, "invalid_credentials", "Incorrect username or password.")
		return
	}
	if a.emailVerificationRequired() && !emailVerified {
		fail(w, 403, "email_verification_required", "Verify your email before signing in. Request a new link from the verification page.")
		return
	}
	if mfaEnabled {
		challenge := auth.Secret("mfachallenge_")
		tx, txErr := a.db.Begin(r.Context())
		if txErr != nil {
			serverError(w, txErr)
			return
		}
		defer tx.Rollback(r.Context())
		if _, txErr = tx.Exec(r.Context(), `DELETE FROM mfa_login_challenges WHERE user_id=$1 AND expires_at<=now()`, u.ID); txErr != nil {
			serverError(w, txErr)
			return
		}
		if _, txErr = tx.Exec(r.Context(), `INSERT INTO mfa_login_challenges(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '5 minutes')`, auth.Digest(challenge), u.ID); txErr != nil {
			serverError(w, txErr)
			return
		}
		if txErr = tx.Commit(r.Context()); txErr != nil {
			serverError(w, txErr)
			return
		}
		respond(w, 200, map[string]any{"mfa_required": true, "challenge": challenge})
		return
	}
	if err := a.session(w, r, u); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"user": u})
}

func (a *App) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if len(in.Email) > 254 {
		in.Email = ""
	}
	if !a.authLimitFor(w, r, in.Email) {
		return
	}
	defer func() { <-a.passwords }()
	var userID, email string
	err := a.db.QueryRow(r.Context(), `SELECT id,email FROM users WHERE email=$1`, in.Email).Scan(&userID, &email)
	if err == nil {
		var recentlyIssued bool
		if err = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM password_reset_tokens WHERE user_id=$1 AND created_at>now()-interval '1 minute')`, userID).Scan(&recentlyIssued); err != nil {
			serverError(w, err)
			return
		}
		if !recentlyIssued {
			token := auth.Secret("pwreset_")
			tx, txErr := a.db.Begin(r.Context())
			if txErr != nil {
				serverError(w, txErr)
				return
			}
			defer tx.Rollback(r.Context())
			if _, txErr = tx.Exec(r.Context(), `DELETE FROM password_reset_tokens WHERE user_id=$1`, userID); txErr != nil {
				serverError(w, txErr)
				return
			}
			if _, txErr = tx.Exec(r.Context(), `INSERT INTO password_reset_tokens(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '30 minutes')`, auth.Digest(token), userID); txErr != nil {
				serverError(w, txErr)
				return
			}
			link := strings.TrimRight(a.cfg.Origin, "/") + "/reset-password?token=" + token
			body := "Use this one-time link within 30 minutes to reset your GITOWN password:\n\n" + link + "\n\nIf you did not request this, ignore this message."
			if txErr = queueAddressMail(r.Context(), tx, email, "password_reset", "Reset your GITOWN password", body, ""); txErr != nil {
				serverError(w, txErr)
				return
			}
			if _, txErr = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'account.password_reset_requested','self')`, userID); txErr != nil {
				serverError(w, txErr)
				return
			}
			if txErr = tx.Commit(r.Context()); txErr != nil {
				serverError(w, txErr)
				return
			}
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"message": "If an account matches and email delivery is configured, a reset link will be sent shortly."})
}

func (a *App) resetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Token) > 128 || len(in.NewPassword) < 12 || len(in.NewPassword) > 128 {
		fail(w, 422, "validation_failed", "Use a valid reset link and a password between 12 and 128 characters.")
		return
	}
	if !strings.HasPrefix(in.Token, "pwreset_") {
		fail(w, 422, "invalid_or_expired_token", "This reset link is invalid or expired. Request a new one.")
		return
	}
	if !a.authLimitFor(w, r, in.Token) {
		return
	}
	defer func() { <-a.passwords }()
	var userID string
	err := a.db.QueryRow(r.Context(), `SELECT user_id FROM password_reset_tokens WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>now()`, auth.Digest(in.Token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 422, "invalid_or_expired_token", "This reset link is invalid or expired. Request a new one.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	newHash := auth.HashPassword(in.NewPassword)
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var consumedUserID, username, email string
	err = tx.QueryRow(r.Context(), `UPDATE password_reset_tokens SET consumed_at=now() WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>now() RETURNING user_id`, auth.Digest(in.Token)).Scan(&consumedUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 422, "invalid_or_expired_token", "This reset link is invalid or expired. Request a new one.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if consumedUserID != userID {
		fail(w, 422, "invalid_or_expired_token", "This reset link is invalid or expired. Request a new one.")
		return
	}
	if err = tx.QueryRow(r.Context(), `UPDATE users SET password_hash=$1 WHERE id=$2 RETURNING username,email`, newHash, userID).Scan(&username, &email); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM sessions WHERE user_id=$1`, userID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM access_tokens WHERE user_id=$1`, userID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'account.password_reset',$2)`, userID, username); err != nil {
		serverError(w, err)
		return
	}
	if err = queueAddressMail(r.Context(), tx, email, "security_notice", "Your GITOWN password was changed", "Your password was reset. All active sessions and access tokens were revoked. If this was not you, secure your email account and contact the GITOWN instance owner.", ""); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "gitown_session", Value: "", Path: "/", HttpOnly: true, Secure: a.cfg.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	respond(w, 200, map[string]bool{"ok": true})
}

type Token struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Scope     string    `json:"scope"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type BrowserSession struct {
	ID         string    `json:"id"`
	IPAddress  string    `json:"ip_address"`
	UserAgent  string    `json:"user_agent"`
	Current    bool      `json:"current"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (a *App) sessions(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	current := ""
	if cookie, err := r.Cookie("gitown_session"); err == nil {
		current = auth.Digest(cookie.Value)
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,ip_address,user_agent,(token_hash=$2),created_at,last_seen_at,expires_at FROM sessions WHERE user_id=$1 AND expires_at>now() ORDER BY created_at DESC LIMIT 20`, u.ID, current)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []BrowserSession{}
	for rows.Next() {
		var session BrowserSession
		if err = rows.Scan(&session.ID, &session.IPAddress, &session.UserAgent, &session.Current, &session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, session)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, items)
}

func (a *App) deleteSession(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	current := ""
	if cookie, err := r.Cookie("gitown_session"); err == nil {
		current = auth.Digest(cookie.Value)
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var tokenHash string
	err = tx.QueryRow(r.Context(), `DELETE FROM sessions WHERE id=$1 AND user_id=$2 RETURNING token_hash`, r.PathValue("id"), u.ID).Scan(&tokenHash)
	if err != nil {
		fail(w, 404, "not_found", "Session not found.")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'session.revoked',$2)`, u.ID, r.PathValue("id")); err != nil {
		serverError(w, err)
		return
	}
	if err = a.queueSecurityNotice(r, tx, u.ID, "A GITOWN session was revoked", "A browser session was revoked from your GITOWN account settings. If you did not do this, change your password and review active sessions."); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	if tokenHash == current {
		http.SetCookie(w, &http.Cookie{Name: "gitown_session", Value: "", Path: "/", HttpOnly: true, Secure: a.cfg.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	}
	respond(w, 200, map[string]bool{"ok": true, "current": tokenHash == current})
}

func (a *App) changePassword(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		CurrentPassword    string `json:"current_password"`
		NewPassword        string `json:"new_password"`
		Code               string `json:"code"`
		RecoveryCode       string `json:"recovery_code"`
		RevokeAccessTokens bool   `json:"revoke_access_tokens"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.CurrentPassword) > 128 || len(in.NewPassword) < 12 || len(in.NewPassword) > 128 {
		fail(w, 422, "validation_failed", "Use a new password between 12 and 128 characters.")
		return
	}
	if !a.authLimitFor(w, r, u.ID) {
		return
	}
	defer func() { <-a.passwords }()
	var currentHash string
	if err := a.db.QueryRow(r.Context(), `SELECT password_hash FROM users WHERE id=$1`, u.ID).Scan(&currentHash); err != nil {
		serverError(w, err)
		return
	}
	if !auth.CheckPassword(currentHash, in.CurrentPassword) {
		fail(w, 401, "invalid_credentials", "Current password is incorrect.")
		return
	}
	if auth.CheckPassword(currentHash, in.NewPassword) {
		fail(w, 422, "password_unchanged", "Choose a password you have not just used.")
		return
	}
	if len(in.Code) > 6 || len(in.RecoveryCode) > 128 || (in.Code != "" && in.RecoveryCode != "") {
		fail(w, 422, "validation_failed", "Provide at most one authenticator or recovery code.")
		return
	}
	var mfaEnabled bool
	var mfaCipher, mfaNonce []byte
	if err := a.db.QueryRow(r.Context(), `SELECT enabled_at IS NOT NULL,secret_cipher,secret_nonce FROM user_mfa WHERE user_id=$1`, u.ID).Scan(&mfaEnabled, &mfaCipher, &mfaNonce); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	var totpStep int64
	totpValid := false
	recoveryHash := ""
	if mfaEnabled {
		if (in.Code == "") == (in.RecoveryCode == "") {
			fail(w, 422, "mfa_required", "Enter one authenticator or unused recovery code to change your password.")
			return
		}
		secret, err := openSecret(mfaCipher, mfaNonce)
		if err != nil {
			serverError(w, err)
			return
		}
		totpStep, totpValid = verifyTOTP(secret, in.Code, time.Now())
		if !totpValid && in.RecoveryCode != "" {
			recoveryHash = auth.Digest(in.RecoveryCode)
		}
		if !totpValid && recoveryHash == "" {
			a.recordAuthAbuse(r, "password_change_mfa_failed", u.ID, r.URL.Path)
			fail(w, 401, "invalid_mfa_code", "That authenticator or recovery code is invalid.")
			return
		}
	} else if in.Code != "" || in.RecoveryCode != "" {
		fail(w, 422, "mfa_not_enabled", "This account does not have an authenticator configured.")
		return
	}
	currentSession := ""
	if cookie, err := r.Cookie("gitown_session"); err == nil {
		currentSession = auth.Digest(cookie.Value)
	}
	newHash := auth.HashPassword(in.NewPassword)
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if mfaEnabled {
		var lockedUser string
		if err = tx.QueryRow(r.Context(), `SELECT user_id FROM user_mfa WHERE user_id=$1 AND enabled_at IS NOT NULL FOR UPDATE`, u.ID).Scan(&lockedUser); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				fail(w, 409, "mfa_changed", "MFA changed while the request was in progress. Retry with a fresh code.")
			} else {
				serverError(w, err)
			}
			return
		}
		if totpValid {
			result, updateErr := tx.Exec(r.Context(), `UPDATE user_mfa SET last_totp_step=$2 WHERE user_id=$1 AND last_totp_step<$2`, u.ID, totpStep)
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
	}
	result, err := tx.Exec(r.Context(), `UPDATE users SET password_hash=$1 WHERE id=$2 AND password_hash=$3`, newHash, u.ID, currentHash)
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() != 1 {
		fail(w, 409, "password_changed", "Your password changed in another session. Sign in again and retry.")
		return
	}
	sessionsResult, err := tx.Exec(r.Context(), `DELETE FROM sessions WHERE user_id=$1 AND token_hash<>$2`, u.ID, currentSession)
	if err != nil {
		serverError(w, err)
		return
	}
	var tokenCount int64
	if in.RevokeAccessTokens {
		tokensResult, deleteErr := tx.Exec(r.Context(), `DELETE FROM access_tokens WHERE user_id=$1`, u.ID)
		if deleteErr != nil {
			serverError(w, deleteErr)
			return
		}
		tokenCount = tokensResult.RowsAffected()
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'account.password_changed',$2)`, u.ID, u.Username); err != nil {
		serverError(w, err)
		return
	}
	if err = a.queueSecurityNotice(r, tx, u.ID, "Your GITOWN password was changed", "The password for your GITOWN account was changed. Other browser sessions were signed out. If you did not make this change, use password recovery and review your account security."); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE sessions SET step_up_at=NULL WHERE token_hash=$1`, currentSession); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"ok": true, "sessions_revoked": sessionsResult.RowsAffected(), "tokens_revoked": tokenCount})
}

func (a *App) tokens(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,scope,created_at,expires_at FROM access_tokens WHERE user_id=$1 ORDER BY created_at DESC`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	tokens := []Token{}
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.Name, &t.Scope, &t.CreatedAt, &t.ExpiresAt); err != nil {
			serverError(w, err)
			return
		}
		tokens = append(tokens, t)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, tokens)
}

func (a *App) createToken(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Name  string `json:"name"`
		Scope string `json:"scope"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 || (in.Scope != "repo:read" && in.Scope != "repo:write" && in.Scope != "package:read" && in.Scope != "package:write") {
		fail(w, 422, "validation_failed", "Provide a token name and a scope of repo:read, repo:write, package:read, or package:write.")
		return
	}
	raw := auth.Secret("gtn_")
	t := Token{ID: auth.ID(), Name: in.Name, Scope: in.Scope, ExpiresAt: time.Now().Add(30 * 24 * time.Hour)}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	// Serialize per-user token creation so the cap holds under concurrent requests.
	if _, err = tx.Exec(r.Context(), `SELECT id FROM users WHERE id=$1 FOR UPDATE`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	var count int
	if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM access_tokens WHERE user_id=$1 AND expires_at>now()`, u.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 20 {
		fail(w, 422, "token_limit", "Revoke an existing token before creating another.")
		return
	}
	err = tx.QueryRow(r.Context(), `INSERT INTO access_tokens(id,user_id,name,token_hash,scope,expires_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at`, t.ID, u.ID, t.Name, auth.Digest(raw), t.Scope, t.ExpiresAt).Scan(&t.CreatedAt)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'token.created',$2)`, u.ID, t.Name); err != nil {
		serverError(w, err)
		return
	}
	if err = a.queueSecurityNotice(r, tx, u.ID, "A GITOWN access token was created", "A personal access token named '"+t.Name+"' with scope '"+t.Scope+"' was created for your account. If this was not you, revoke it from Account security and change your password."); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]any{"token": raw, "details": t})
}

func (a *App) deleteToken(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var name string
	err = tx.QueryRow(r.Context(), `DELETE FROM access_tokens WHERE id::text=$1 AND user_id=$2 RETURNING name`, r.PathValue("id"), u.ID).Scan(&name)
	if err != nil {
		fail(w, 404, "not_found", "Token not found.")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'token.revoked',$2)`, u.ID, name); err != nil {
		serverError(w, err)
		return
	}
	if err = a.queueSecurityNotice(r, tx, u.ID, "A GITOWN access token was revoked", "The personal access token named '"+name+"' was revoked from your account."); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func (a *App) activity(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,action,target,created_at FROM audit_events WHERE actor_id=$1 ORDER BY id DESC LIMIT 30`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type event struct {
		ID        int64     `json:"id"`
		Action    string    `json:"action"`
		Target    string    `json:"target"`
		CreatedAt time.Time `json:"created_at"`
	}
	events := []event{}
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.ID, &e.Action, &e.Target, &e.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, events)
}
