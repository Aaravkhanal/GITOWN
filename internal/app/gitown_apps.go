package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

// GITOWN Apps are simplified from a full GitHub-Apps-style model: there is
// no JWT-based app identity and no short-lived, auto-refreshed installation
// token. An installation instead mints one long-lived token up front,
// revoked immediately on uninstall. This is a deliberate, documented
// bound (see docs/PRODUCT_PHASES.md) rather than an oversight.
//
// The key property that DOES match the real thing: an installation token
// is not "the installing user's own access, borrowed." It authenticates as
// the app's own synthetic bot user, which owns nothing and belongs to no
// repository except through an explicit repository_members row created at
// install time — so every existing permission check (decorate, CanWrite,
// branch protection, quotas, everything) already enforces exactly the
// installation's own grant with no new authorization logic to audit.

func (a *App) gitownApps(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT ga.id,ga.name,ga.description,ga.homepage_url,ga.webhook_url,ga.requested_scope,ga.created_at,bot.username,
		(SELECT count(*) FROM gitown_app_installations WHERE gitown_app_id=ga.id) FROM gitown_apps ga JOIN users bot ON bot.id=ga.bot_user_id WHERE ga.owner_id=$1 ORDER BY ga.created_at DESC`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, description, homepage, webhookURL, scope, botUsername string
		var created time.Time
		var installs int
		if err = rows.Scan(&id, &name, &description, &homepage, &webhookURL, &scope, &created, &botUsername, &installs); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{
			"id": id, "name": name, "description": description, "homepage_url": homepage,
			"webhook_url": webhookURL, "requested_scope": scope, "created_at": created,
			"bot_username": botUsername, "installations": installs,
		})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) createGitownApp(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Name           string `json:"name"`
		Description    string `json:"description"`
		HomepageURL    string `json:"homepage_url"`
		WebhookURL     string `json:"webhook_url"`
		RequestedScope string `json:"requested_scope"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.HomepageURL = strings.TrimSpace(in.HomepageURL)
	in.WebhookURL = strings.TrimSpace(in.WebhookURL)
	if in.Name == "" || len(in.Name) > 80 || len(in.Description) > 500 || len(in.HomepageURL) > 300 ||
		(in.RequestedScope != "repo:read" && in.RequestedScope != "repo:write") {
		fail(w, 422, "validation_failed", "Provide a name and a requested scope of repo:read or repo:write.")
		return
	}
	if in.WebhookURL != "" {
		if err := validWebhookURL(r.Context(), in.WebhookURL); err != nil {
			fail(w, 422, "validation_failed", "Webhook URL: "+err.Error())
			return
		}
	}
	var count int
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM gitown_apps WHERE owner_id=$1`, u.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 20 {
		fail(w, 422, "app_limit", "Up to 20 GITOWN Apps are allowed per account.")
		return
	}
	// The bot user is a real users row (so every foreign key elsewhere just
	// works) with an unguessable, never-communicated password and a
	// synthetic email in a reserved, non-routable-looking domain — nobody
	// can log into it directly, it only ever acts through an installation
	// token.
	botID := auth.ID()
	botUsername := "app-" + strings.ToLower(strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, in.Name))
	if len(botUsername) > 30 {
		botUsername = botUsername[:30]
	}
	if botUsername == "app-" || botUsername == "" {
		botUsername = "app"
	}
	botUsername += "-" + botID[:8]
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `INSERT INTO users(id,username,email,display_name,password_hash,is_bot) VALUES($1,$2,$3,$4,$5,true)`,
		botID, botUsername, botUsername+"@apps.gitown.invalid", in.Name+" (app)", auth.HashPassword(auth.Secret("bot_"))); err != nil {
		serverError(w, err)
		return
	}
	id := auth.ID()
	var created time.Time
	if err = tx.QueryRow(r.Context(), `INSERT INTO gitown_apps(id,owner_id,bot_user_id,name,description,homepage_url,webhook_url,requested_scope) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`,
		id, u.ID, botID, in.Name, in.Description, in.HomepageURL, in.WebhookURL, in.RequestedScope).Scan(&created); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'gitown_app.created',$2)`, u.ID, in.Name); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]any{
		"id": id, "name": in.Name, "description": in.Description, "homepage_url": in.HomepageURL,
		"webhook_url": in.WebhookURL, "requested_scope": in.RequestedScope, "bot_username": botUsername, "created_at": created,
	})
}

func (a *App) deleteGitownApp(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	// Deleting the bot user cascades to every installation (repository_members
	// rows the bot held are not touched by this FK — clean those up first)
	// and every access token issued to it.
	var botID string
	err := a.db.QueryRow(r.Context(), `SELECT bot_user_id FROM gitown_apps WHERE id=$1 AND owner_id=$2`, r.PathValue("id"), u.ID).Scan(&botID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "GITOWN App not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = a.db.Exec(r.Context(), `DELETE FROM repository_members WHERE user_id=$1`, botID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = a.db.Exec(r.Context(), `DELETE FROM users WHERE id=$1`, botID); err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'gitown_app.deleted',$2)`, u.ID, r.PathValue("id"))
	respond(w, 200, map[string]bool{"removed": true})
}

// --- Installation on a repository ----------------------------------------

func (a *App) repoGitownApps(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT i.id,ga.id,ga.name,ga.description,i.granted_scope,i.created_at
		FROM gitown_app_installations i JOIN gitown_apps ga ON ga.id=i.gitown_app_id WHERE i.repository_id=$1 ORDER BY i.created_at DESC`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, appID, name, description, scope string
		var created time.Time
		if err = rows.Scan(&id, &appID, &name, &description, &scope, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "gitown_app_id": appID, "name": name, "description": description, "granted_scope": scope, "created_at": created})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) installGitownApp(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	u := a.user(r)
	var in struct {
		ClientID string `json:"client_id"`
		Scope    string `json:"scope"`
	}
	if !decode(w, r, &in) {
		return
	}
	// GITOWN Apps aren't looked up by a client_id like OAuth apps — reuse
	// the request field name for a friendlier request shape and treat it
	// as the app's id, validated below either way.
	appID := in.ClientID
	var botID, requestedScope string
	err := a.db.QueryRow(r.Context(), `SELECT bot_user_id,requested_scope FROM gitown_apps WHERE id=$1`, appID).Scan(&botID, &requestedScope)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "GITOWN App not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	// An installer may grant at most what the app asked for: repo:write
	// requested apps may be installed as read-only, but a repo:read app can
	// never be granted write.
	if in.Scope == "" {
		in.Scope = requestedScope
	}
	if (in.Scope != "repo:read" && in.Scope != "repo:write") || (in.Scope == "repo:write" && requestedScope != "repo:write") {
		fail(w, 422, "validation_failed", "The granted scope cannot exceed what the app requested.")
		return
	}
	role := "read"
	if in.Scope == "repo:write" {
		role = "write"
	}
	installID := auth.ID()
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var created time.Time
	err = tx.QueryRow(r.Context(), `INSERT INTO gitown_app_installations(id,gitown_app_id,repository_id,granted_scope,installed_by) VALUES($1,$2,$3,$4,$5) RETURNING created_at`,
		installID, appID, repo.ID, in.Scope, u.ID).Scan(&created)
	if conflict(err) {
		fail(w, 409, "already_installed", "This app is already installed on this repository.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO repository_members(repository_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT (repository_id,user_id) DO UPDATE SET role=excluded.role`,
		repo.ID, botID, role); err != nil {
		serverError(w, err)
		return
	}
	raw := auth.Secret("gta_")
	if _, err = tx.Exec(r.Context(), `INSERT INTO access_tokens(id,user_id,name,token_hash,scope,expires_at,installation_id) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		auth.ID(), botID, "Installation token", auth.Digest(raw), in.Scope, time.Now().Add(10*365*24*time.Hour), installID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'gitown_app.installed',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+appID); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	go a.notifyAppEvent(repo.ID, "app.installed", appID)
	respond(w, 201, map[string]any{"id": installID, "gitown_app_id": appID, "granted_scope": in.Scope, "created_at": created, "installation_token": raw})
}

func (a *App) uninstallGitownApp(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	u := a.user(r)
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var appID, botID string
	err = tx.QueryRow(r.Context(), `SELECT i.gitown_app_id,ga.bot_user_id FROM gitown_app_installations i JOIN gitown_apps ga ON ga.id=i.gitown_app_id
		WHERE i.id=$1 AND i.repository_id=$2`, r.PathValue("id"), repo.ID).Scan(&appID, &botID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Installation not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	// Deleting the installation row cascades to its access_tokens (FK
	// installation_id ON DELETE CASCADE), revoking the token immediately;
	// the repository_members row is removed explicitly since it has no FK
	// back to the installation.
	if _, err = tx.Exec(r.Context(), `DELETE FROM gitown_app_installations WHERE id=$1`, r.PathValue("id")); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM repository_members WHERE repository_id=$1 AND user_id=$2`, repo.ID, botID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'gitown_app.uninstalled',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+appID); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	go a.notifyAppEvent(repo.ID, "app.uninstalled", appID)
	respond(w, 200, map[string]bool{"removed": true})
}

// notifyAppEvent fires the shared webhook outbox for an install/uninstall,
// detached from the request context since the HTTP response has already
// been sent by the time this matters to a receiver.
func (a *App) notifyAppEvent(repositoryID, kind, appID string) {
	_ = a.fireWebhook(context.Background(), a.db, repositoryID, "", kind, map[string]any{"gitown_app_id": appID})
}
