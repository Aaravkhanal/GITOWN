package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Notification struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`
	Actor      string     `json:"actor"`
	Owner      string     `json:"owner"`
	Repository string     `json:"repository"`
	Issue      *int       `json:"issue"`
	Pull       *int       `json:"pull"`
	Title      string     `json:"title"`
	CreatedAt  time.Time  `json:"created_at"`
	ReadAt     *time.Time `json:"read_at"`
}

func notifyIssue(ctx context.Context, tx pgx.Tx, issueID, actorID, kind string) error {
	return notifyIssueKey(ctx, tx, issueID, actorID, kind, "")
}

func notifyIssueKey(ctx context.Context, tx pgx.Tx, issueID, actorID, kind, dedupe string) error {
	var key any
	if dedupe != "" {
		key = dedupe
	}
	rows, err := tx.Query(ctx, `WITH inserted AS (
		INSERT INTO notifications(recipient_id,actor_id,repository_id,issue_id,kind,dedupe_key)
		SELECT s.user_id,$2,i.repository_id,i.id,$3,$4 FROM issue_subscriptions s
		JOIN issues i ON i.id=s.issue_id JOIN repositories r ON r.id=i.repository_id
		WHERE s.issue_id=$1 AND s.user_id<>$2 AND s.mode<>'ignore' AND r.deleted_at IS NULL
		AND (r.visibility='public' OR r.owner_id=s.user_id OR EXISTS (
			SELECT 1 FROM repository_members rm WHERE rm.repository_id=r.id AND rm.user_id=s.user_id))
		ON CONFLICT (recipient_id, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING
		RETURNING recipient_id)
		SELECT recipient_id FROM inserted`, issueID, actorID, kind, key)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return queueNotificationMail(ctx, tx, ids, kind, "issue:"+issueID+":"+kind+":"+dedupe)
}

func notifyPull(ctx context.Context, tx pgx.Tx, pullID, actorID, kind string) error {
	return notifyPullKey(ctx, tx, pullID, actorID, kind, "")
}

func notifyPullKey(ctx context.Context, tx pgx.Tx, pullID, actorID, kind, dedupe string) error {
	var key any
	if dedupe != "" {
		key = dedupe
	}
	rows, err := tx.Query(ctx, `WITH inserted AS (
		INSERT INTO notifications(recipient_id,actor_id,repository_id,pull_request_id,kind,dedupe_key)
		SELECT s.user_id,$2,p.repository_id,p.id,$3,$4 FROM pull_subscriptions s
		JOIN pull_requests p ON p.id=s.pull_request_id JOIN repositories r ON r.id=p.repository_id
		WHERE s.pull_request_id=$1 AND s.user_id<>$2 AND s.mode<>'ignore' AND r.deleted_at IS NULL
		AND (r.visibility='public' OR r.owner_id=s.user_id OR EXISTS (
			SELECT 1 FROM repository_members rm WHERE rm.repository_id=r.id AND rm.user_id=s.user_id))
		ON CONFLICT (recipient_id, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING
		RETURNING recipient_id)
		SELECT recipient_id FROM inserted`, pullID, actorID, kind, key)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return queueNotificationMail(ctx, tx, ids, kind, "pull:"+pullID+":"+kind+":"+dedupe)
}

func (a *App) pullSubscription(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	u := a.user(r)
	if u == nil {
		respond(w, 200, map[string]any{"subscribed": false, "mode": ""})
		return
	}
	mode, subscribed, err := subscriptionMode(r.Context(), a.db, `SELECT mode FROM pull_subscriptions WHERE pull_request_id=$1 AND user_id=$2`, p.ID, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"subscribed": subscribed, "mode": mode})
}

func (a *App) updatePullSubscription(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	var in struct {
		Subscribed bool   `json:"subscribed"`
		Mode       string `json:"mode"`
	}
	if !decode(w, r, &in) {
		return
	}
	mode, subscribed, ok := normalizeSubscription(in.Subscribed, in.Mode)
	if !ok {
		fail(w, 422, "validation_failed", "Subscription mode must be watch, ignore, or participate.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if mode == "" {
		_, err = tx.Exec(r.Context(), `DELETE FROM pull_subscriptions WHERE pull_request_id=$1 AND user_id=$2`, p.ID, u.ID)
	} else {
		_, err = tx.Exec(r.Context(), `INSERT INTO pull_subscriptions(pull_request_id,user_id,mode) VALUES($1,$2,$3) ON CONFLICT (pull_request_id,user_id) DO UPDATE SET mode=excluded.mode`, p.ID, u.ID, mode)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.subscription_updated',$2)`, u.ID, fmt.Sprintf("%s/%s#%d:%s", repo.Owner, repo.Name, p.Number, mode)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"subscribed": subscribed, "mode": mode})
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
		respond(w, 200, map[string]any{"subscribed": false, "mode": ""})
		return
	}
	mode, subscribed, err := subscriptionMode(r.Context(), a.db, `SELECT mode FROM issue_subscriptions WHERE issue_id=$1 AND user_id=$2`, issueID, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"subscribed": subscribed, "mode": mode})
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
		Subscribed bool   `json:"subscribed"`
		Mode       string `json:"mode"`
	}
	if !decode(w, r, &in) {
		return
	}
	mode, subscribed, ok := normalizeSubscription(in.Subscribed, in.Mode)
	if !ok {
		fail(w, 422, "validation_failed", "Subscription mode must be watch, ignore, or participate.")
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
	if mode == "" {
		_, err = tx.Exec(r.Context(), `DELETE FROM issue_subscriptions WHERE issue_id=$1 AND user_id=$2`, issueID, u.ID)
	} else {
		_, err = tx.Exec(r.Context(), `INSERT INTO issue_subscriptions(issue_id,user_id,mode) VALUES($1,$2,$3) ON CONFLICT (issue_id,user_id) DO UPDATE SET mode=excluded.mode`, issueID, u.ID, mode)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,$2,$3)`, u.ID, "issue.subscription_updated", fmt.Sprintf("%s/%s#%d:%s", repo.Owner, repo.Name, number, mode)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"subscribed": subscribed, "mode": mode})
}

func (a *App) notifications(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT n.id,n.kind,actor.username,owner.username,r.name,i.number,p.number,COALESCE(i.title,p.title,CASE WHEN n.invitation_id IS NOT NULL THEN 'Repository invitation' WHEN n.transfer_id IS NOT NULL THEN 'Ownership transfer' ELSE '' END),n.created_at,n.read_at
		FROM notifications n JOIN users actor ON actor.id=n.actor_id
		JOIN repositories r ON r.id=n.repository_id JOIN users owner ON owner.id=r.owner_id
		LEFT JOIN issues i ON i.id=n.issue_id LEFT JOIN pull_requests p ON p.id=n.pull_request_id
		WHERE n.recipient_id=$1 AND r.deleted_at IS NULL
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
		if err = rows.Scan(&item.ID, &item.Kind, &item.Actor, &item.Owner, &item.Repository, &item.Issue, &item.Pull, &item.Title, &item.CreatedAt, &item.ReadAt); err != nil {
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

func (a *App) readAllNotifications(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE notifications SET read_at=COALESCE(read_at,now()) WHERE recipient_id=$1 AND read_at IS NULL`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func subscriptionMode(ctx context.Context, db queryRower, query string, args ...any) (string, bool, error) {
	var mode string
	err := db.QueryRow(ctx, query, args...).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return mode, mode == "watch" || mode == "participate", nil
}

func normalizeSubscription(subscribed bool, mode string) (string, bool, bool) {
	switch mode {
	case "":
		if subscribed {
			return "participate", true, true
		}
		return "", false, true
	case "ignore":
		return "ignore", false, true
	case "watch", "participate":
		return mode, true, true
	default:
		return "", false, false
	}
}

func notifyDirect(ctx context.Context, tx pgx.Tx, recipientID, actorID, repositoryID, issueID, pullID, kind, dedupe string) error {
	if recipientID == "" || recipientID == actorID {
		return nil
	}
	var ignored bool
	if issueID != "" {
		_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_subscriptions WHERE issue_id=$1 AND user_id=$2 AND mode='ignore')`, issueID, recipientID).Scan(&ignored)
	}
	if pullID != "" {
		_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pull_subscriptions WHERE pull_request_id=$1 AND user_id=$2 AND mode='ignore')`, pullID, recipientID).Scan(&ignored)
	}
	if ignored {
		return nil
	}
	var key any
	if dedupe != "" {
		key = dedupe
	}
	var issue any
	var pull any
	if issueID != "" {
		issue = issueID
	}
	if pullID != "" {
		pull = pullID
	}
	var id string
	err := tx.QueryRow(ctx, `INSERT INTO notifications(recipient_id,actor_id,repository_id,issue_id,pull_request_id,kind,dedupe_key)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (recipient_id, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING
		RETURNING recipient_id`, recipientID, actorID, repositoryID, issue, pull, kind, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return queueNotificationMail(ctx, tx, []string{id}, kind, dedupe)
}

var mentionPattern = regexp.MustCompile(`(?:^|[^a-z0-9])@([a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?)`)

func (a *App) noteMentions(ctx context.Context, tx pgx.Tx, repo *Repository, actor *User, body, issueID, pullID, commentID string) error {
	seen := map[string]bool{}
	for _, match := range mentionPattern.FindAllStringSubmatch(strings.ToLower(body), 20) {
		name := match[1]
		if seen[name] || name == actor.Username {
			continue
		}
		seen[name] = true
		var userID string
		err := tx.QueryRow(ctx, `SELECT id FROM users WHERE username=$1`, name).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if repo.Visibility != "public" {
			var allowed bool
			if err = tx.QueryRow(ctx, `SELECT $1=$2 OR EXISTS(SELECT 1 FROM repository_members WHERE repository_id=$3 AND user_id=$1)`, userID, repo.OwnerID, repo.ID).Scan(&allowed); err != nil {
				return err
			}
			if !allowed {
				continue
			}
		}
		if issueID != "" {
			if _, err = tx.Exec(ctx, `INSERT INTO issue_subscriptions(issue_id,user_id,mode) VALUES($1,$2,'participate') ON CONFLICT DO NOTHING`, issueID, userID); err != nil {
				return err
			}
		}
		if pullID != "" {
			if _, err = tx.Exec(ctx, `INSERT INTO pull_subscriptions(pull_request_id,user_id,mode) VALUES($1,$2,'participate') ON CONFLICT DO NOTHING`, pullID, userID); err != nil {
				return err
			}
		}
		if err = notifyDirect(ctx, tx, userID, actor.ID, repo.ID, issueID, pullID, "mention", "mention:"+commentID+":"+userID); err != nil {
			return err
		}
	}
	return nil
}
