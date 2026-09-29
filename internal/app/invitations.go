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

type Invitation struct {
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	Repository string    `json:"repository"`
	Email      string    `json:"email"`
	Username   string    `json:"username"`
	Role       string    `json:"role"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expires_at"`
	CreatedAt  time.Time `json:"created_at"`
	Token      string    `json:"token,omitempty"`
}

func (a *App) expireInvitations(r *http.Request, repoID string) {
	if repoID == "" {
		_, _ = a.db.Exec(r.Context(), `UPDATE repository_invitations SET status='expired' WHERE status='pending' AND expires_at<=now()`)
		return
	}
	_, _ = a.db.Exec(r.Context(), `UPDATE repository_invitations SET status='expired' WHERE repository_id=$1 AND status='pending' AND expires_at<=now()`, repoID)
}

func (a *App) repositoryInvitations(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	a.expireInvitations(r, repo.ID)
	rows, err := a.db.Query(r.Context(), `SELECT id,email,COALESCE(username,''),role,status,expires_at,created_at FROM repository_invitations WHERE repository_id=$1 ORDER BY created_at DESC LIMIT 100`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []Invitation{}
	for rows.Next() {
		var item Invitation
		item.Owner = repo.Owner
		item.Repository = repo.Name
		if err = rows.Scan(&item.ID, &item.Email, &item.Username, &item.Role, &item.Status, &item.ExpiresAt, &item.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, items)
}

func (a *App) createInvitation(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil || !activeRepository(w, repo) {
		return
	}
	u := a.user(r)
	var in struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Role     string `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if !validMemberRole(in.Role) {
		fail(w, 422, "validation_failed", "Choose read, triage, write, or maintain.")
		return
	}
	if in.Username != "" {
		if !slug.MatchString(in.Username) || in.Username == repo.Owner {
			fail(w, 422, "validation_failed", "Choose another GITOWN user.")
			return
		}
		err := a.db.QueryRow(r.Context(), `SELECT email FROM users WHERE username=$1`, in.Username).Scan(&in.Email)
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 404, "user_not_found", "No GITOWN user has that username. You can invite an email address instead.")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err := mail.ParseAddress(in.Email); err != nil || len(in.Email) > 200 {
		fail(w, 422, "validation_failed", "Provide a username or a valid email address.")
		return
	}
	token := auth.Secret("inv_")
	item := Invitation{ID: auth.ID(), Owner: repo.Owner, Repository: repo.Name, Email: in.Email, Username: in.Username, Role: in.Role, Status: "pending", Token: token}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	err = tx.QueryRow(r.Context(), `INSERT INTO repository_invitations(id,repository_id,email,username,role,token_hash,invited_by,expires_at) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,now()+interval '14 days') RETURNING expires_at,created_at`, item.ID, repo.ID, item.Email, item.Username, item.Role, auth.Digest(token), u.ID).Scan(&item.ExpiresAt, &item.CreatedAt)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO notifications(recipient_id,actor_id,repository_id,invitation_id,kind)
		SELECT id,$2,$3,$4,'invitation' FROM users WHERE email=$1 AND id<>$2`, item.Email, u.ID, repo.ID, item.ID); err != nil {
		serverError(w, err)
		return
	}
	var inviteeID string
	_ = tx.QueryRow(r.Context(), `SELECT id FROM users WHERE email=$1`, item.Email).Scan(&inviteeID)
	if inviteeID != "" {
		if err = queueUserMail(r.Context(), tx, inviteeID, "invitation", "GITOWN repository invitation", "You were invited to "+repo.Owner+"/"+repo.Name+" as "+item.Role+". Accept it from your invitations page. Token: "+token, "invitation:"+item.ID); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.invitation_created',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+item.Email+":"+item.Role); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, item)
}

func (a *App) userInvitations(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	a.expireInvitations(r, "")
	var email string
	if err := a.db.QueryRow(r.Context(), `SELECT email FROM users WHERE id=$1`, u.ID).Scan(&email); err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT i.id,owner.username,r.name,i.email,COALESCE(i.username,''),i.role,i.status,i.expires_at,i.created_at FROM repository_invitations i JOIN repositories r ON r.id=i.repository_id JOIN users owner ON owner.id=r.owner_id WHERE r.deleted_at IS NULL AND (i.email=$1 OR i.username=$2) ORDER BY i.created_at DESC LIMIT 100`, email, u.Username)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []Invitation{}
	for rows.Next() {
		var item Invitation
		if err = rows.Scan(&item.ID, &item.Owner, &item.Repository, &item.Email, &item.Username, &item.Role, &item.Status, &item.ExpiresAt, &item.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, items)
}

func (a *App) respondInvitation(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	accept := strings.HasSuffix(r.URL.Path, "/accept")
	var email string
	if err := a.db.QueryRow(r.Context(), `SELECT email FROM users WHERE id=$1`, u.ID).Scan(&email); err != nil {
		serverError(w, err)
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var repoID, role, status, inviteEmail, username string
	err = tx.QueryRow(r.Context(), `SELECT repository_id,role,status,email,COALESCE(username,'') FROM repository_invitations WHERE id=$1 FOR UPDATE`, r.PathValue("id")).Scan(&repoID, &role, &status, &inviteEmail, &username)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Invitation not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if inviteEmail != email && username != u.Username {
		fail(w, 403, "forbidden", "This invitation is for a different account.")
		return
	}
	if status != "pending" {
		fail(w, 409, "invitation_closed", "This invitation is no longer pending.")
		return
	}
	var expired bool
	_ = tx.QueryRow(r.Context(), `SELECT expires_at<=now() FROM repository_invitations WHERE id=$1`, r.PathValue("id")).Scan(&expired)
	if expired {
		_, _ = tx.Exec(r.Context(), `UPDATE repository_invitations SET status='expired' WHERE id=$1`, r.PathValue("id"))
		_ = tx.Commit(r.Context())
		fail(w, 409, "invitation_expired", "This invitation has expired.")
		return
	}
	next := "declined"
	if accept {
		next = "accepted"
		if _, err = tx.Exec(r.Context(), `INSERT INTO repository_members(repository_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT (repository_id,user_id) DO UPDATE SET role=excluded.role`, repoID, u.ID, role); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `UPDATE repository_invitations SET status=$1 WHERE id=$2`, next, r.PathValue("id")); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,$2,$3)`, u.ID, "repository.invitation_"+next, r.PathValue("id")); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"status": next})
}

func (a *App) createTransfer(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil || !activeRepository(w, repo) {
		return
	}
	u := a.user(r)
	var in struct {
		Username string `json:"username"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if !slug.MatchString(in.Username) || in.Username == repo.Owner {
		fail(w, 422, "validation_failed", "Choose another GITOWN user to receive ownership.")
		return
	}
	var targetID string
	if err := a.db.QueryRow(r.Context(), `SELECT id FROM users WHERE username=$1`, in.Username).Scan(&targetID); err != nil {
		fail(w, 404, "user_not_found", "No GITOWN user has that username.")
		return
	}
	id := auth.ID()
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO ownership_transfers(id,repository_id,from_user_id,to_user_id) VALUES($1,$2,$3,$4)`, id, repo.ID, u.ID, targetID)
	if conflict(err) {
		fail(w, 409, "transfer_pending", "An ownership transfer is already pending. Cancel it before starting another.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO notifications(recipient_id,actor_id,repository_id,transfer_id,kind) VALUES($1,$2,$3,$4,'ownership_transfer')`, targetID, u.ID, repo.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if err = queueUserMail(r.Context(), tx, targetID, "ownership_transfer", "GITOWN ownership transfer", repo.Owner+"/"+repo.Name+" is waiting for you to accept ownership.", "transfer:"+id); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.transfer_requested',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+in.Username); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]string{"id": id, "status": "pending", "username": in.Username})
}

func (a *App) userTransfers(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT t.id,owner.username,r.name,actor.username,t.status,t.created_at FROM ownership_transfers t JOIN repositories r ON r.id=t.repository_id JOIN users owner ON owner.id=r.owner_id JOIN users actor ON actor.id=t.from_user_id WHERE t.to_user_id=$1 AND r.deleted_at IS NULL ORDER BY t.created_at DESC LIMIT 50`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, owner, name, actor, status string
		var created time.Time
		if err = rows.Scan(&id, &owner, &name, &actor, &status, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "owner": owner, "repository": name, "actor": actor, "status": status, "created_at": created})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, items)
}

func (a *App) respondTransfer(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	accept := strings.HasSuffix(r.URL.Path, "/accept")
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var repoID, fromID, status string
	err = tx.QueryRow(r.Context(), `SELECT repository_id,from_user_id,status FROM ownership_transfers WHERE id=$1 AND to_user_id=$2 FOR UPDATE`, r.PathValue("id"), u.ID).Scan(&repoID, &fromID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Ownership transfer not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if status != "pending" {
		fail(w, 409, "transfer_closed", "This ownership transfer is no longer pending.")
		return
	}
	next := "declined"
	if accept {
		next = "accepted"
		if _, err = tx.Exec(r.Context(), `UPDATE repositories SET owner_id=$1 WHERE id=$2 AND owner_id=$3`, u.ID, repoID, fromID); err != nil {
			serverError(w, err)
			return
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO repository_members(repository_id,user_id,role) VALUES($1,$2,'maintain') ON CONFLICT (repository_id,user_id) DO UPDATE SET role='maintain'`, repoID, fromID); err != nil {
			serverError(w, err)
			return
		}
		if _, err = tx.Exec(r.Context(), `DELETE FROM repository_members WHERE repository_id=$1 AND user_id=$2`, repoID, u.ID); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `UPDATE ownership_transfers SET status=$1 WHERE id=$2`, next, r.PathValue("id")); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,$2,$3)`, u.ID, "repository.transfer_"+next, r.PathValue("id")); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"status": next})
}
