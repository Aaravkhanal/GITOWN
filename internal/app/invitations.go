package app

import (
	"context"
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
	AcceptURL  string    `json:"accept_url,omitempty"`
}

var errInvitationClosed = errors.New("invitation is no longer pending")

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

// Invitations to an existing account are accepted from that account's
// invitation list. Invitations to an address without an account can only be
// accepted with the emailed link, so registering an unverified copy of the
// address is not enough to join the repository.
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
	var inviteeID string
	if in.Username != "" {
		if !slug.MatchString(in.Username) {
			fail(w, 422, "validation_failed", "Choose another GITOWN user.")
			return
		}
		err := a.db.QueryRow(r.Context(), `SELECT id,email FROM users WHERE username=$1`, in.Username).Scan(&inviteeID, &in.Email)
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 404, "user_not_found", "No GITOWN user has that username. You can invite an email address instead.")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
	} else {
		if _, err := mail.ParseAddress(in.Email); err != nil || len(in.Email) > 200 || strings.ContainsAny(in.Email, "<>\r\n") {
			fail(w, 422, "validation_failed", "Provide a username or a valid email address.")
			return
		}
		err := a.db.QueryRow(r.Context(), `SELECT id,username FROM users WHERE email=$1`, in.Email).Scan(&inviteeID, &in.Username)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			serverError(w, err)
			return
		}
	}
	if inviteeID == repo.OwnerID {
		fail(w, 422, "validation_failed", "The owner already has full access.")
		return
	}
	allowed, err := a.outsideCollaboratorAllowed(r.Context(), repo.DistrictID, in.Username)
	if err != nil {
		serverError(w, err)
		return
	}
	if !allowed {
		fail(w, 403, "forbidden", "This district does not allow collaborators from outside the district.")
		return
	}
	if inviteeID != "" {
		var member bool
		if err = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM repository_members WHERE repository_id=$1 AND user_id=$2)`, repo.ID, inviteeID).Scan(&member); err != nil {
			serverError(w, err)
			return
		}
		if member {
			fail(w, 409, "collaborator_exists", "That user is already a collaborator. Change their role instead.")
			return
		}
	}
	a.expireInvitations(r, repo.ID)
	token := auth.Secret("inv_")
	item := Invitation{ID: auth.ID(), Owner: repo.Owner, Repository: repo.Name, Email: in.Email, Username: in.Username, Role: in.Role, Status: "pending"}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	err = tx.QueryRow(r.Context(), `INSERT INTO repository_invitations(id,repository_id,email,username,role,token_hash,invited_by,expires_at) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,now()+interval '14 days') RETURNING expires_at,created_at`, item.ID, repo.ID, item.Email, item.Username, item.Role, auth.Digest(token), u.ID).Scan(&item.ExpiresAt, &item.CreatedAt)
	if conflict(err) {
		fail(w, 409, "invitation_pending", "A pending invitation already exists for that person. Revoke it to send a new one.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	subject := "GITOWN invitation to " + repo.Owner + "/" + repo.Name
	if inviteeID != "" {
		if _, err = tx.Exec(r.Context(), `INSERT INTO notifications(recipient_id,actor_id,repository_id,invitation_id,kind) VALUES($1,$2,$3,$4,'invitation')`, inviteeID, u.ID, repo.ID, item.ID); err != nil {
			serverError(w, err)
			return
		}
		body := "@" + u.Username + " invited you to " + repo.Owner + "/" + repo.Name + " as " + item.Role + ".\n\nReview the invitation: " + a.cfg.Origin + "/invitations"
		if err = queueUserMail(r.Context(), tx, inviteeID, "invitation", subject, body, "invitation:"+item.ID); err != nil {
			serverError(w, err)
			return
		}
	} else {
		item.AcceptURL = a.cfg.Origin + "/invitations/accept?token=" + token
		body := "@" + u.Username + " invited you to " + repo.Owner + "/" + repo.Name + " on GITOWN as " + item.Role + ".\n\nSign in or create an account, then accept the invitation: " + item.AcceptURL + "\n\nThe link expires in 14 days. If you did not expect this invitation, you can ignore it."
		if err = queueAddressMail(r.Context(), tx, item.Email, "invitation", subject, body, "invitation:"+item.ID); err != nil {
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

func (a *App) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	result, err := a.db.Exec(r.Context(), `UPDATE repository_invitations SET status='revoked',responded_by=$1,responded_at=now() WHERE id=$2 AND repository_id=$3 AND status='pending'`, a.user(r).ID, r.PathValue("id"), repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		fail(w, 404, "not_found", "No pending invitation was found.")
		return
	}
	_, _ = a.db.Exec(r.Context(), `DELETE FROM notifications WHERE invitation_id=$1`, r.PathValue("id"))
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.invitation_revoked',$2)`, a.user(r).ID, repo.Owner+"/"+repo.Name+":"+r.PathValue("id"))
	respond(w, 200, map[string]string{"status": "revoked"})
}

func (a *App) userInvitations(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	a.expireInvitations(r, "")
	rows, err := a.db.Query(r.Context(), `SELECT i.id,owner.username,r.name,i.email,COALESCE(i.username,''),i.role,i.status,i.expires_at,i.created_at FROM repository_invitations i JOIN repositories r ON r.id=i.repository_id JOIN users owner ON owner.id=r.owner_id WHERE r.deleted_at IS NULL AND i.username=$1 ORDER BY i.created_at DESC LIMIT 100`, u.Username)
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

type pendingInvitation struct {
	id, repoID, districtID, ownerID, role, status, username string
	expired                                                 bool
}

func lockInvitation(ctx context.Context, tx pgx.Tx, where string, arg string) (pendingInvitation, error) {
	var inv pendingInvitation
	err := tx.QueryRow(ctx, `SELECT i.id,i.repository_id,COALESCE(r.district_id::text,''),r.owner_id,i.role,i.status,COALESCE(i.username,''),i.expires_at<=now() FROM repository_invitations i JOIN repositories r ON r.id=i.repository_id WHERE r.deleted_at IS NULL AND `+where+` FOR UPDATE OF i`, arg).Scan(&inv.id, &inv.repoID, &inv.districtID, &inv.ownerID, &inv.role, &inv.status, &inv.username, &inv.expired)
	return inv, err
}

// settleInvitation records the answer and, on acceptance, grants the role.
func (a *App) settleInvitation(ctx context.Context, tx pgx.Tx, inv pendingInvitation, u *User, accept bool) (string, error) {
	if inv.status != "pending" {
		return "", errInvitationClosed
	}
	if inv.expired {
		_, err := tx.Exec(ctx, `UPDATE repository_invitations SET status='expired' WHERE id=$1`, inv.id)
		if err != nil {
			return "", err
		}
		return "expired", nil
	}
	next := "declined"
	if accept {
		next = "accepted"
		if _, err := tx.Exec(ctx, `INSERT INTO repository_members(repository_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT (repository_id,user_id) DO UPDATE SET role=excluded.role`, inv.repoID, u.ID, inv.role); err != nil {
			return "", err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE repository_invitations SET status=$1,responded_by=$2,responded_at=now() WHERE id=$3`, next, u.ID, inv.id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE notifications SET read_at=COALESCE(read_at,now()) WHERE invitation_id=$1`, inv.id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,target) VALUES($1,$2,$3)`, u.ID, "repository.invitation_"+next, inv.id); err != nil {
		return "", err
	}
	return next, nil
}

func (a *App) answerInvitation(w http.ResponseWriter, r *http.Request, u *User, tx pgx.Tx, inv pendingInvitation, accept bool) {
	if accept {
		if inv.ownerID == u.ID {
			fail(w, 422, "validation_failed", "You already own this repository.")
			return
		}
		allowed, err := a.outsideCollaboratorAllowed(r.Context(), inv.districtID, u.Username)
		if err != nil {
			serverError(w, err)
			return
		}
		if !allowed {
			fail(w, 403, "forbidden", "This district no longer allows collaborators from outside the district.")
			return
		}
	}
	next, err := a.settleInvitation(r.Context(), tx, inv, u, accept)
	if errors.Is(err, errInvitationClosed) {
		fail(w, 409, "invitation_closed", "This invitation is no longer pending.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	if next == "expired" {
		fail(w, 409, "invitation_expired", "This invitation has expired.")
		return
	}
	respond(w, 200, map[string]string{"status": next})
}

func (a *App) respondInvitation(w http.ResponseWriter, r *http.Request) {
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
	inv, err := lockInvitation(r.Context(), tx, "i.id::text=$1", r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Invitation not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if inv.username == "" {
		fail(w, 403, "forbidden", "Open the invitation link sent to your email address to answer this invitation.")
		return
	}
	if inv.username != u.Username {
		fail(w, 403, "forbidden", "This invitation is for a different account.")
		return
	}
	a.answerInvitation(w, r, u, tx, inv, strings.HasSuffix(r.URL.Path, "/accept"))
}

func (a *App) acceptInvitationToken(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Token = strings.TrimSpace(in.Token)
	if !strings.HasPrefix(in.Token, "inv_") || len(in.Token) > 200 {
		fail(w, 404, "not_found", "Invitation not found.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	inv, err := lockInvitation(r.Context(), tx, "i.token_hash=$1", auth.Digest(in.Token))
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Invitation not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if inv.username != "" && inv.username != u.Username {
		fail(w, 403, "forbidden", "This invitation is for a different account.")
		return
	}
	a.answerInvitation(w, r, u, tx, inv, true)
}

func (a *App) createTransfer(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil || !activeRepository(w, repo) {
		return
	}
	u := a.user(r)
	var in struct {
		Username string `json:"username"`
		Confirm  string `json:"confirm"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if !slug.MatchString(in.Username) || in.Username == repo.Owner {
		fail(w, 422, "validation_failed", "Choose another GITOWN user to receive ownership.")
		return
	}
	if in.Confirm != repo.Owner+"/"+repo.Name {
		fail(w, 422, "confirmation_required", "Type "+repo.Owner+"/"+repo.Name+" to confirm the transfer.")
		return
	}
	var targetID string
	var taken bool
	err := a.db.QueryRow(r.Context(), `SELECT id,EXISTS(SELECT 1 FROM repositories WHERE owner_id=users.id AND name=$2) FROM users WHERE username=$1`, in.Username, repo.Name).Scan(&targetID, &taken)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "user_not_found", "No GITOWN user has that username.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if taken {
		fail(w, 409, "name_taken", "@"+in.Username+" already has a repository named "+repo.Name+". Rename this repository first.")
		return
	}
	id := auth.ID()
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO ownership_transfers(id,repository_id,from_user_id,to_user_id) VALUES($1,$2,$3,$4)`, id, repo.ID, repo.OwnerID, targetID)
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
	if err = queueUserMail(r.Context(), tx, targetID, "ownership_transfer", "GITOWN ownership transfer", "@"+u.Username+" wants to transfer "+repo.Owner+"/"+repo.Name+" to you.\n\nReview it: "+a.cfg.Origin+"/invitations", "transfer:"+id); err != nil {
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

func (a *App) repositoryTransfer(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	var id, username string
	var created time.Time
	err := a.db.QueryRow(r.Context(), `SELECT t.id,u.username,t.created_at FROM ownership_transfers t JOIN users u ON u.id=t.to_user_id WHERE t.repository_id=$1 AND t.status='pending'`, repo.ID).Scan(&id, &username, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		respond(w, 200, map[string]any{"pending": nil})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"pending": map[string]any{"id": id, "username": username, "created_at": created}})
}

func (a *App) cancelTransfer(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	var id string
	err := a.db.QueryRow(r.Context(), `UPDATE ownership_transfers SET status='cancelled' WHERE repository_id=$1 AND status='pending' RETURNING id`, repo.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "No ownership transfer is pending.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `DELETE FROM notifications WHERE transfer_id=$1`, id)
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.transfer_cancelled',$2)`, a.user(r).ID, repo.Owner+"/"+repo.Name)
	respond(w, 200, map[string]string{"status": "cancelled"})
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
	var repoID, fromID, status, name string
	err = tx.QueryRow(r.Context(), `SELECT t.repository_id,t.from_user_id,t.status,r.name FROM ownership_transfers t JOIN repositories r ON r.id=t.repository_id WHERE t.id=$1 AND t.to_user_id=$2 FOR UPDATE OF t, r`, r.PathValue("id"), u.ID).Scan(&repoID, &fromID, &status, &name)
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
		var taken bool
		if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM repositories WHERE owner_id=$1 AND name=$2)`, u.ID, name).Scan(&taken); err != nil {
			serverError(w, err)
			return
		}
		if taken {
			fail(w, 409, "name_taken", "You already have a repository named "+name+". Rename or delete it, then accept again.")
			return
		}
		result, updateErr := tx.Exec(r.Context(), `UPDATE repositories SET owner_id=$1 WHERE id=$2 AND owner_id=$3 AND deleted_at IS NULL`, u.ID, repoID, fromID)
		if updateErr != nil {
			serverError(w, updateErr)
			return
		}
		if result.RowsAffected() != 1 {
			if _, err = tx.Exec(r.Context(), `UPDATE ownership_transfers SET status='cancelled' WHERE id=$1`, r.PathValue("id")); err == nil {
				_ = tx.Commit(r.Context())
			}
			fail(w, 409, "transfer_stale", "The repository changed owner after this transfer was requested. Ask the current owner to send a new transfer.")
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
	if _, err = tx.Exec(r.Context(), `UPDATE notifications SET read_at=COALESCE(read_at,now()) WHERE transfer_id=$1`, r.PathValue("id")); err != nil {
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
