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
