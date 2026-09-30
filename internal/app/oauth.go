package app

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

// oauthCodeTTL is short on purpose: a code is meant to be exchanged within
// the same browser round trip that issued it, matching the OAuth spec's
// expectation of a short-lived, single-use code.
const oauthCodeTTL = 5 * time.Minute

func validOAuthScope(scope string) bool {
	return scope == "repo:read" || scope == "repo:write" || scope == "package:read" || scope == "package:write"
}

// --- Developer-facing app registration ---------------------------------

func (a *App) oauthApps(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,description,homepage_url,redirect_uri,client_id,created_at,
		(SELECT count(*) FROM oauth_authorizations WHERE oauth_app_id=oauth_apps.id) FROM oauth_apps WHERE owner_id=$1 ORDER BY created_at DESC`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, description, homepage, redirect, clientID string
		var created time.Time
		var authorizations int
		if err = rows.Scan(&id, &name, &description, &homepage, &redirect, &clientID, &created, &authorizations); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "description": description, "homepage_url": homepage,
			"redirect_uri": redirect, "client_id": clientID, "created_at": created, "authorized_users": authorizations,
		})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) createOAuthApp(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		HomepageURL string `json:"homepage_url"`
		RedirectURI string `json:"redirect_uri"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.HomepageURL = strings.TrimSpace(in.HomepageURL)
	in.RedirectURI = strings.TrimSpace(in.RedirectURI)
	redirect, err := url.Parse(in.RedirectURI)
	validRedirect := err == nil && (redirect.Scheme == "https" || redirect.Scheme == "http") && redirect.Host != "" && redirect.Fragment == ""
	if in.Name == "" || len(in.Name) > 80 || len(in.Description) > 500 || len(in.HomepageURL) > 300 || !validRedirect || len(in.RedirectURI) > 500 {
		fail(w, 422, "validation_failed", "Provide a name and a valid https:// (or http:// for local development) redirect URI.")
		return
	}
	var count int
	if err = a.db.QueryRow(r.Context(), `SELECT count(*) FROM oauth_apps WHERE owner_id=$1`, u.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 20 {
		fail(w, 422, "app_limit", "Up to 20 OAuth applications are allowed per account.")
		return
	}
	clientID := auth.Secret("cid_")
	secret := auth.Secret("csec_")
	id := auth.ID()
	var created time.Time
	err = a.db.QueryRow(r.Context(), `INSERT INTO oauth_apps(id,owner_id,name,description,homepage_url,redirect_uri,client_id,client_secret_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`,
		id, u.ID, in.Name, in.Description, in.HomepageURL, in.RedirectURI, clientID, auth.Digest(secret)).Scan(&created)
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'oauth_app.created',$2)`, u.ID, in.Name)
	respond(w, 201, map[string]any{
		"id": id, "name": in.Name, "description": in.Description, "homepage_url": in.HomepageURL,
		"redirect_uri": in.RedirectURI, "client_id": clientID, "client_secret": secret, "created_at": created,
	})
}

func (a *App) regenerateOAuthAppSecret(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	secret := auth.Secret("csec_")
	tag, err := a.db.Exec(r.Context(), `UPDATE oauth_apps SET client_secret_hash=$1 WHERE id=$2 AND owner_id=$3`, auth.Digest(secret), r.PathValue("id"), u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "OAuth application not found.")
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'oauth_app.secret_regenerated',$2)`, u.ID, r.PathValue("id"))
	respond(w, 200, map[string]string{"client_secret": secret})
}

func (a *App) deleteOAuthApp(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	tag, err := a.db.Exec(r.Context(), `DELETE FROM oauth_apps WHERE id=$1 AND owner_id=$2`, r.PathValue("id"), u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "OAuth application not found.")
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'oauth_app.deleted',$2)`, u.ID, r.PathValue("id"))
	respond(w, 200, map[string]bool{"removed": true})
}

// --- Authorization-code flow --------------------------------------------

// oauthAuthorizeInfo backs the consent screen: given the query parameters a
// third-party app's browser redirect carries, it returns what to show the
// signed-in user without side effects, so the page can render before they
// decide anything.
func (a *App) oauthAuthorizeInfo(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	clientID := r.URL.Query().Get("client_id")
	redirectURI := r.URL.Query().Get("redirect_uri")
	scope := r.URL.Query().Get("scope")
	var name, description, registeredRedirect string
	err := a.db.QueryRow(r.Context(), `SELECT name,description,redirect_uri FROM oauth_apps WHERE client_id=$1`, clientID).Scan(&name, &description, &registeredRedirect)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Unknown OAuth application.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if redirectURI != registeredRedirect {
		fail(w, 422, "redirect_uri_mismatch", "This application's redirect URI does not match what was registered.")
		return
	}
	if !validOAuthScope(scope) {
		fail(w, 422, "validation_failed", "Unknown scope requested.")
		return
	}
	respond(w, 200, map[string]any{"name": name, "description": description, "scope": scope, "username": u.Username})
}

// oauthAuthorizeDecide is called by our own consent-screen UI (so it goes
// through the normal Origin/JSON checks, unlike the token endpoint below,
// which a third party calls directly). Approving mints a single-use code
// and returns the exact redirect the frontend should navigate the browser
// to next — never a bare 3xx, since that would leak the code to whatever
// else is watching the SPA's own network log for no reason.
func (a *App) oauthAuthorizeDecide(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		ClientID    string `json:"client_id"`
		RedirectURI string `json:"redirect_uri"`
		Scope       string `json:"scope"`
		State       string `json:"state"`
		Approve     bool   `json:"approve"`
	}
	if !decode(w, r, &in) {
		return
	}
	var appID, registeredRedirect string
	err := a.db.QueryRow(r.Context(), `SELECT id,redirect_uri FROM oauth_apps WHERE client_id=$1`, in.ClientID).Scan(&appID, &registeredRedirect)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Unknown OAuth application.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if in.RedirectURI != registeredRedirect || !validOAuthScope(in.Scope) {
		fail(w, 422, "validation_failed", "The authorization request does not match a registered application.")
		return
	}
	target, _ := url.Parse(in.RedirectURI)
	q := target.Query()
	if in.State != "" {
		q.Set("state", in.State)
	}
	if !in.Approve {
		q.Set("error", "access_denied")
		target.RawQuery = q.Encode()
		respond(w, 200, map[string]string{"redirect_to": target.String()})
		return
	}
	code := auth.Secret("ghc_")
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `INSERT INTO oauth_codes(code_hash,oauth_app_id,user_id,scope,redirect_uri,expires_at) VALUES($1,$2,$3,$4,$5,$6)`,
		auth.Digest(code), appID, u.ID, in.Scope, in.RedirectURI, time.Now().Add(oauthCodeTTL)); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO oauth_authorizations(oauth_app_id,user_id,scope) VALUES($1,$2,$3) ON CONFLICT (oauth_app_id,user_id) DO UPDATE SET scope=excluded.scope`,
		appID, u.ID, in.Scope); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	q.Set("code", code)
	target.RawQuery = q.Encode()
	respond(w, 200, map[string]string{"redirect_to": target.String()})
}

// oauthToken is called directly by third-party clients per the OAuth spec —
// form-encoded, no Origin header, no browser session — and is specifically
// exempted from the general JSON/Origin middleware checks in Handler().
func (a *App) oauthToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		fail(w, 400, "invalid_input", "Could not parse the request body.")
		return
	}
	clientID := r.PostForm.Get("client_id")
	clientSecret := r.PostForm.Get("client_secret")
	var appID string
	err := a.db.QueryRow(r.Context(), `SELECT id FROM oauth_apps WHERE client_id=$1 AND client_secret_hash=$2`, clientID, auth.Digest(clientSecret)).Scan(&appID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 401, "invalid_client", "Unknown client_id or client_secret.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		a.exchangeOAuthCode(w, r, appID)
	case "refresh_token":
		a.refreshOAuthToken(w, r, appID)
	default:
		fail(w, 400, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token.")
	}
}

func (a *App) issueOAuthToken(w http.ResponseWriter, r *http.Request, appID, userID, scope string) {
	raw := auth.Secret("gto_")
	refresh := auth.Secret("ghr_")
	id := auth.ID()
	name := "OAuth app token"
	_, err := a.db.Exec(r.Context(), `INSERT INTO access_tokens(id,user_id,name,token_hash,scope,expires_at,oauth_app_id,refresh_token_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, userID, name, auth.Digest(raw), scope, time.Now().Add(365*24*time.Hour), appID, auth.Digest(refresh))
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{
		"access_token": raw, "refresh_token": refresh, "token_type": "bearer", "scope": scope,
		"expires_in": int((365 * 24 * time.Hour).Seconds()),
	})
}

func (a *App) exchangeOAuthCode(w http.ResponseWriter, r *http.Request, appID string) {
	code := r.PostForm.Get("code")
	redirectURI := r.PostForm.Get("redirect_uri")
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var userID, scope, storedRedirect string
	var expiresAt time.Time
	var used bool
	err = tx.QueryRow(r.Context(), `SELECT user_id,scope,redirect_uri,expires_at,used FROM oauth_codes WHERE code_hash=$1 AND oauth_app_id=$2 FOR UPDATE`,
		auth.Digest(code), appID).Scan(&userID, &scope, &storedRedirect, &expiresAt, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 400, "invalid_grant", "Unknown or already-used authorization code.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if used || time.Now().After(expiresAt) || redirectURI != storedRedirect {
		fail(w, 400, "invalid_grant", "This authorization code is invalid, expired, or was already used.")
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE oauth_codes SET used=true WHERE code_hash=$1`, auth.Digest(code)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.issueOAuthToken(w, r, appID, userID, scope)
}

func (a *App) refreshOAuthToken(w http.ResponseWriter, r *http.Request, appID string) {
	refreshToken := r.PostForm.Get("refresh_token")
	var userID, scope string
	err := a.db.QueryRow(r.Context(), `SELECT user_id,scope FROM access_tokens WHERE refresh_token_hash=$1 AND oauth_app_id=$2 AND expires_at>now()`,
		auth.Digest(refreshToken), appID).Scan(&userID, &scope)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 400, "invalid_grant", "Unknown or expired refresh token.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	// Rotate: the old token (and its refresh token) stop working once a new
	// pair is issued, so a leaked-then-replayed refresh token has a single
	// use, matching common OAuth provider behavior.
	if _, err = a.db.Exec(r.Context(), `DELETE FROM access_tokens WHERE refresh_token_hash=$1`, auth.Digest(refreshToken)); err != nil {
		serverError(w, err)
		return
	}
	a.issueOAuthToken(w, r, appID, userID, scope)
}

// --- What a user has authorized -----------------------------------------

func (a *App) userOAuthAuthorizations(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT app.id,app.name,app.homepage_url,auth.scope,auth.created_at
		FROM oauth_authorizations auth JOIN oauth_apps app ON app.id=auth.oauth_app_id WHERE auth.user_id=$1 ORDER BY auth.created_at DESC`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, homepage, scope string
		var created time.Time
		if err = rows.Scan(&id, &name, &homepage, &scope, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"oauth_app_id": id, "name": name, "homepage_url": homepage, "scope": scope, "created_at": created})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

// revokeOAuthAuthorization removes the grant and every token issued under
// it in one transaction, so revocation is immediate and complete rather
// than leaving an already-issued access token usable until it expires.
func (a *App) revokeOAuthAuthorization(w http.ResponseWriter, r *http.Request) {
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
	tag, err := tx.Exec(r.Context(), `DELETE FROM oauth_authorizations WHERE oauth_app_id=$1 AND user_id=$2`, r.PathValue("id"), u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "You have not authorized that application.")
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM access_tokens WHERE oauth_app_id=$1 AND user_id=$2`, r.PathValue("id"), u.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'oauth_app.revoked',$2)`, u.ID, r.PathValue("id")); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"revoked": true})
}
