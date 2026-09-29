package app

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

type Notification struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`
	Actor      string     `json:"actor"`
	Owner      string     `json:"owner"`
	Repository string     `json:"repository"`
	Issue      int        `json:"issue"`
	Title      string     `json:"title"`
	CreatedAt  time.Time  `json:"created_at"`
	ReadAt     *time.Time `json:"read_at"`
}

func notifyIssue(ctx context.Context, tx pgx.Tx, issueID, actorID, kind string) error {
	_, err := tx.Exec(ctx, `INSERT INTO notifications(recipient_id,actor_id,repository_id,issue_id,kind)
		SELECT s.user_id,$2,i.repository_id,i.id,$3 FROM issue_subscriptions s
		JOIN issues i ON i.id=s.issue_id JOIN repositories r ON r.id=i.repository_id
		WHERE s.issue_id=$1 AND s.user_id<>$2 AND r.deleted_at IS NULL
		AND (r.visibility='public' OR r.owner_id=s.user_id OR EXISTS (
			SELECT 1 FROM repository_members rm WHERE rm.repository_id=r.id AND rm.user_id=s.user_id))`, issueID, actorID, kind)
	return err
}

func (a *App) issueSubscription(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var issueID string
	err := a.db.QueryRow(r.Context(), `SELECT id FROM issues WHERE repository_id=$1 AND number=$2`, repo.ID, number).Scan(&issueID)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	if u == nil {
		respond(w, 200, map[string]bool{"subscribed": false})
		return
	}
	var subscribed bool
	if err = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM issue_subscriptions WHERE issue_id=$1 AND user_id=$2)`, issueID, u.ID).Scan(&subscribed); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"subscribed": subscribed})
}

func (a *App) updateIssueSubscription(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var in struct {
		Subscribed bool `json:"subscribed"`
	}
	if !decode(w, r, &in) {
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var issueID string
	err = tx.QueryRow(r.Context(), `SELECT id FROM issues WHERE repository_id=$1 AND number=$2`, repo.ID, number).Scan(&issueID)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if in.Subscribed {
		_, err = tx.Exec(r.Context(), `INSERT INTO issue_subscriptions(issue_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, issueID, u.ID)
	} else {
		_, err = tx.Exec(r.Context(), `DELETE FROM issue_subscriptions WHERE issue_id=$1 AND user_id=$2`, issueID, u.ID)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,$2,$3)`, u.ID, "issue.subscription_updated", fmt.Sprintf("%s/%s#%d:%t", repo.Owner, repo.Name, number, in.Subscribed)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"subscribed": in.Subscribed})
}

func (a *App) notifications(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT n.id,n.kind,actor.username,owner.username,r.name,i.number,i.title,n.created_at,n.read_at
		FROM notifications n JOIN users actor ON actor.id=n.actor_id
		JOIN repositories r ON r.id=n.repository_id JOIN users owner ON owner.id=r.owner_id
		JOIN issues i ON i.id=n.issue_id WHERE n.recipient_id=$1 AND r.deleted_at IS NULL
		AND (r.visibility='public' OR r.owner_id=$1 OR EXISTS (
			SELECT 1 FROM repository_members rm WHERE rm.repository_id=r.id AND rm.user_id=$1))
		ORDER BY n.id DESC LIMIT 100`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []Notification{}
	for rows.Next() {
		var item Notification
		if err = rows.Scan(&item.ID, &item.Kind, &item.Actor, &item.Owner, &item.Repository, &item.Issue, &item.Title, &item.CreatedAt, &item.ReadAt); err != nil {
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

func (a *App) readNotification(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		fail(w, 404, "not_found", "Notification not found.")
		return
	}
	result, err := a.db.Exec(r.Context(), `UPDATE notifications SET read_at=COALESCE(read_at,now()) WHERE id=$1 AND recipient_id=$2`, id, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Notification not found.")
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}
