package app

import (
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
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
	if !a.authLimit(w, r) {
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
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
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
	if !a.authLimit(w, r) {
		return
	}
	defer func() { <-a.passwords }()
	var u User
	var encoded string
	err := a.db.QueryRow(r.Context(), `SELECT id,username,display_name,password_hash FROM users WHERE username=$1 OR email=$1`, strings.ToLower(strings.TrimSpace(in.Username))).Scan(&u.ID, &u.Username, &u.DisplayName, &encoded)
	if err != nil {
		encoded = a.dummyHash
	}
	valid := auth.CheckPassword(encoded, in.Password)
	if err != nil || !valid {
		fail(w, 401, "invalid_credentials", "Incorrect username or password.")
		return
	}
	if err := a.session(w, r, u); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"user": u})
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
		RevokeAccessTokens bool   `json:"revoke_access_tokens"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.CurrentPassword) > 128 || len(in.NewPassword) < 12 || len(in.NewPassword) > 128 {
		fail(w, 422, "validation_failed", "Use a new password between 12 and 128 characters.")
		return
	}
	if !a.authLimit(w, r) {
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
	if in.Name == "" || len(in.Name) > 80 || (in.Scope != "repo:read" && in.Scope != "repo:write") {
		fail(w, 422, "validation_failed", "Provide a token name and a valid repository scope.")
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
