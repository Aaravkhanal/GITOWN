package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	Excerpt    string     `json:"excerpt"`
	CreatedAt  time.Time  `json:"created_at"`
	ReadAt     *time.Time `json:"read_at"`
}

// Notification delivery rules
//
// Thread subscriptions (issue_subscriptions, pull_subscriptions):
//   - watch: every update on the thread, including check results.
//   - participate: conversation and state changes on a thread the person
//     opened, commented on, was assigned to, was asked to review, or was
//     mentioned in. Check results are left out.
//   - ignore: nothing from that thread, even direct mentions.
//
// Repository subscriptions (repository_subscriptions):
//   - watching: new issues and Unite requests plus all thread activity.
//   - participating (default): only threads the person subscribes to.
//   - ignoring: nothing from the repository except invitations/transfers.
//
// Owners and maintainers watch their repositories until they choose
// otherwise. Everyone must still be able to read the repository.

// canReadRepoSQL returns a predicate that is true when user can read the
// repository row aliased as repo, mirroring decorate().
func canReadRepoSQL(user, repo string) string {
	return fmt.Sprintf(`(%[2]s.visibility='public' OR %[2]s.owner_id=%[1]s
		OR EXISTS (SELECT 1 FROM repository_members crm WHERE crm.repository_id=%[2]s.id AND crm.user_id=%[1]s)
		OR EXISTS (SELECT 1 FROM districts cd LEFT JOIN district_members cdm ON cdm.district_id=cd.id AND cdm.user_id=%[1]s
			WHERE cd.id=%[2]s.district_id AND (cd.owner_id=%[1]s OR cdm.role='admin'
			OR (cdm.role='member' AND (cd.base_permission IN ('read','triage','write') OR %[2]s.visibility='internal')))))`, user, repo)
}

// repoWatchersSQL lists users who watch the repository with id repoID,
// counting owners and maintainers who never chose a mode.
func repoWatchersSQL(repoID string) string {
	return fmt.Sprintf(`SELECT rs.user_id FROM repository_subscriptions rs WHERE rs.repository_id=%[1]s AND rs.mode='watching'
		UNION SELECT wr.owner_id FROM repositories wr WHERE wr.id=%[1]s
			AND NOT EXISTS (SELECT 1 FROM repository_subscriptions x WHERE x.repository_id=wr.id AND x.user_id=wr.owner_id)
		UNION SELECT wm.user_id FROM repository_members wm WHERE wm.repository_id=%[1]s AND wm.role='maintain'
			AND NOT EXISTS (SELECT 1 FROM repository_subscriptions x WHERE x.repository_id=wm.repository_id AND x.user_id=wm.user_id)`, repoID)
}

func excerpt(body string) string {
	body = strings.Join(strings.Fields(body), " ")
	if len(body) <= 280 {
		return body
	}
	cut := 277
	for cut > 0 && !isRuneStart(body[cut]) {
		cut--
	}
	return body[:cut] + "…"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func notifyIssue(ctx context.Context, tx pgx.Tx, issueID, actorID, kind string) error {
	return notifyIssueEvent(ctx, tx, issueID, actorID, kind, "", "")
}

func notifyIssueKey(ctx context.Context, tx pgx.Tx, issueID, actorID, kind, dedupe string) error {
	return notifyIssueEvent(ctx, tx, issueID, actorID, kind, dedupe, "")
}

// notifyIssueEvent fans an issue event out to thread subscribers and
// repository watchers in the caller's transaction, then queues one email
// per created notification.
func notifyIssueEvent(ctx context.Context, tx pgx.Tx, issueID, actorID, kind, dedupe, body string) error {
	var key any
	if dedupe != "" {
		key = dedupe
	}
	rows, err := tx.Query(ctx, `WITH subject AS (
			SELECT i.id,i.repository_id FROM issues i JOIN repositories r ON r.id=i.repository_id WHERE i.id=$1 AND r.deleted_at IS NULL),
		recipients AS (
			SELECT s.user_id FROM issue_subscriptions s WHERE s.issue_id=$1 AND s.mode IN ('watch','participate')
			UNION `+repoWatchersSQL("(SELECT repository_id FROM subject)")+`)
		INSERT INTO notifications(recipient_id,actor_id,repository_id,issue_id,kind,dedupe_key,excerpt)
		SELECT rc.user_id,$2,subject.repository_id,subject.id,$3,$4,$5 FROM recipients rc CROSS JOIN subject JOIN repositories r ON r.id=subject.repository_id
		WHERE rc.user_id<>$2
		AND NOT EXISTS (SELECT 1 FROM issue_subscriptions x WHERE x.issue_id=subject.id AND x.user_id=rc.user_id AND x.mode='ignore')
		AND NOT EXISTS (SELECT 1 FROM repository_subscriptions x WHERE x.repository_id=r.id AND x.user_id=rc.user_id AND x.mode='ignoring')
		AND `+canReadRepoSQL("rc.user_id", "r")+`
		ON CONFLICT (recipient_id, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING
		RETURNING id`, issueID, actorID, kind, key, excerpt(body))
	if err != nil {
		return err
	}
	return queueNotificationRows(ctx, tx, rows)
}

func notifyPull(ctx context.Context, tx pgx.Tx, pullID, actorID, kind string) error {
	return notifyPullEvent(ctx, tx, pullID, actorID, kind, "", "")
}

func notifyPullKey(ctx context.Context, tx pgx.Tx, pullID, actorID, kind, dedupe string) error {
	return notifyPullEvent(ctx, tx, pullID, actorID, kind, dedupe, "")
}

// notifyPullEvent is the Unite request counterpart of notifyIssueEvent.
func notifyPullEvent(ctx context.Context, tx pgx.Tx, pullID, actorID, kind, dedupe, body string) error {
	var key any
	if dedupe != "" {
		key = dedupe
	}
	rows, err := tx.Query(ctx, `WITH subject AS (
			SELECT p.id,p.repository_id FROM pull_requests p JOIN repositories r ON r.id=p.repository_id WHERE p.id=$1 AND r.deleted_at IS NULL),
		recipients AS (
			SELECT s.user_id FROM pull_subscriptions s WHERE s.pull_request_id=$1 AND s.mode IN ('watch','participate')
			UNION `+repoWatchersSQL("(SELECT repository_id FROM subject)")+`)
		INSERT INTO notifications(recipient_id,actor_id,repository_id,pull_request_id,kind,dedupe_key,excerpt)
		SELECT rc.user_id,$2,subject.repository_id,subject.id,$3,$4,$5 FROM recipients rc CROSS JOIN subject JOIN repositories r ON r.id=subject.repository_id
		WHERE rc.user_id<>$2
		AND NOT EXISTS (SELECT 1 FROM pull_subscriptions x WHERE x.pull_request_id=subject.id AND x.user_id=rc.user_id AND x.mode='ignore')
		AND NOT EXISTS (SELECT 1 FROM repository_subscriptions x WHERE x.repository_id=r.id AND x.user_id=rc.user_id AND x.mode='ignoring')
		AND `+canReadRepoSQL("rc.user_id", "r")+`
		ON CONFLICT (recipient_id, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING
		RETURNING id`, pullID, actorID, kind, key, excerpt(body))
	if err != nil {
		return err
	}
	return queueNotificationRows(ctx, tx, rows)
}

// AnnounceIssue notifies repository watchers about a new issue and anyone
// mentioned in its body. Call it inside the transaction that creates it.
func (a *App) AnnounceIssue(ctx context.Context, tx pgx.Tx, repo *Repository, actor *User, issueID, body string) error {
	if err := notifyIssueEvent(ctx, tx, issueID, actor.ID, "issue_opened", "issue-opened:"+issueID, body); err != nil {
		return err
	}
	return a.noteMentions(ctx, tx, repo, actor, body, issueID, "", "issue-"+issueID)
}

// AnnouncePull notifies repository watchers about a new Unite request and
// anyone mentioned in its body.
func (a *App) AnnouncePull(ctx context.Context, tx pgx.Tx, repo *Repository, actor *User, pullID, body string) error {
	if err := notifyPullEvent(ctx, tx, pullID, actor.ID, "pull_opened", "pull-opened:"+pullID, body); err != nil {
		return err
	}
	return a.noteMentions(ctx, tx, repo, actor, body, "", pullID, "pull-"+pullID)
}

// notifyCheckResult tells Unite request authors, and people watching the
// thread, when a check on the current head finishes. Authors always hear
// about failures, even when their own automation posted the status.
func (a *App) notifyCheckResult(ctx context.Context, repo *Repository, actorID, sha, check, state, description string) {
	if state != "success" && state != "failure" && state != "error" {
		return
	}
	kind := "check_success"
	if state != "success" {
		kind = "check_failure"
	}
	out, err := a.git.Run(ctx, repo.ID, nil, "for-each-ref", "--format=%(objectname) %(refname:strip=2)", "refs/heads/")
	if err != nil {
		slog.Error("check notification branch lookup failed", "repository", repo.ID, "error", err)
		return
	}
	var branches []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if head, branch, ok := strings.Cut(line, " "); ok && head == sha {
			branches = append(branches, branch)
		}
	}
	if len(branches) == 0 {
		return
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		slog.Error("check notification failed", "error", err)
		return
	}
	defer tx.Rollback(ctx)
	text := check + ": " + state
	if description != "" {
		text += " — " + description
	}
	rows, err := tx.Query(ctx, `WITH subject AS (
			SELECT p.id,p.repository_id,p.author_id FROM pull_requests p WHERE p.repository_id=$1 AND p.state='open' AND p.head_branch=ANY($2)),
		recipients AS (
			SELECT subject.id AS pull_id,subject.author_id AS user_id FROM subject WHERE $6 OR subject.author_id<>$3
			UNION SELECT s.pull_request_id,s.user_id FROM pull_subscriptions s JOIN subject ON subject.id=s.pull_request_id WHERE s.mode='watch' AND s.user_id<>$3)
		INSERT INTO notifications(recipient_id,actor_id,repository_id,pull_request_id,kind,dedupe_key,excerpt)
		SELECT rc.user_id,$3,r.id,rc.pull_id,$4,$5,$7 FROM recipients rc JOIN repositories r ON r.id=$1
		WHERE NOT EXISTS (SELECT 1 FROM pull_subscriptions x WHERE x.pull_request_id=rc.pull_id AND x.user_id=rc.user_id AND x.mode='ignore')
		AND NOT EXISTS (SELECT 1 FROM repository_subscriptions x WHERE x.repository_id=r.id AND x.user_id=rc.user_id AND x.mode='ignoring')
		AND `+canReadRepoSQL("rc.user_id", "r")+`
		ON CONFLICT (recipient_id, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING
		RETURNING id`, repo.ID, branches, actorID, kind, "check:"+sha+":"+check+":"+state, state != "success", excerpt(text))
	if err == nil {
		err = queueNotificationRows(ctx, tx, rows)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		slog.Error("check notification failed", "repository", repo.ID, "sha", sha, "error", err)
	}
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

// repositoryWatchDefault reports the implicit mode for someone without a
// repository_subscriptions row: owners and maintainers watch.
func repositoryWatchDefault(repo *Repository) string {
	if repo.Role == "owner" || repo.Role == "maintain" {
		return "watching"
	}
	return "participating"
}

func (a *App) repositorySubscription(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	u := a.user(r)
	if u == nil {
		respond(w, 200, map[string]any{"mode": "", "implicit": true})
		return
	}
	var mode string
	err := a.db.QueryRow(r.Context(), `SELECT mode FROM repository_subscriptions WHERE repository_id=$1 AND user_id=$2`, repo.ID, u.ID).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		respond(w, 200, map[string]any{"mode": repositoryWatchDefault(repo), "implicit": true})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"mode": mode, "implicit": false})
}

func (a *App) updateRepositorySubscription(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Mode != "watching" && in.Mode != "participating" && in.Mode != "ignoring" {
		fail(w, 422, "validation_failed", "Repository notifications must be watching, participating, or ignoring.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `INSERT INTO repository_subscriptions(repository_id,user_id,mode) VALUES($1,$2,$3) ON CONFLICT (repository_id,user_id) DO UPDATE SET mode=excluded.mode`, repo.ID, u.ID, in.Mode); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.subscription_updated',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+in.Mode); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"mode": in.Mode, "implicit": false})
}

// inboxVisibleSQL hides notifications for repositories the recipient can no
// longer read. Pending invitations and ownership transfers stay visible so
// people can respond to them before they have access.
var inboxVisibleSQL = `(n.repository_id IS NULL OR (r.deleted_at IS NULL AND (` + canReadRepoSQL("$1", "r") + `
	OR (n.invitation_id IS NOT NULL AND EXISTS (SELECT 1 FROM repository_invitations iv WHERE iv.id=n.invitation_id AND iv.status='pending'))
	OR (n.transfer_id IS NOT NULL AND EXISTS (SELECT 1 FROM ownership_transfers ot WHERE ot.id=n.transfer_id AND ot.status='pending')))))`

var notificationKinds = map[string]bool{
	"issue_opened": true, "issue_comment": true, "issue_closed": true, "issue_reopened": true,
	"pull_opened": true, "pull_comment": true, "pull_review": true, "pull_closed": true, "pull_reopened": true, "pull_merged": true,
	"mention": true, "assignment": true, "review_request": true, "invitation": true, "ownership_transfer": true,
	"check_success": true, "check_failure": true, "follow": true,
}

// notifications lists the inbox newest first. It pages with ?before=<id>
// and filters with ?filter=unread and ?kind=<kind>.
func (a *App) notifications(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	query := r.URL.Query()
	before := int64(0)
	if raw := query.Get("before"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			fail(w, 422, "validation_failed", "Before must be a notification id.")
			return
		}
		before = value
	}
	limit := 50
	if raw := query.Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			fail(w, 422, "validation_failed", "Limit must be between 1 and 100.")
			return
		}
		limit = value
	}
	filter := query.Get("filter")
	if filter != "" && filter != "all" && filter != "unread" {
		fail(w, 422, "validation_failed", "Filter must be all or unread.")
		return
	}
	kind := query.Get("kind")
	if kind != "" && !notificationKinds[kind] {
		fail(w, 422, "validation_failed", "Unknown notification kind.")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT n.id,n.kind,actor.username,COALESCE(owner.username,''),COALESCE(r.name,''),i.number,p.number,
		COALESCE(i.title,p.title,CASE WHEN n.invitation_id IS NOT NULL THEN 'Repository invitation' WHEN n.transfer_id IS NOT NULL THEN 'Ownership transfer' WHEN n.kind='follow' THEN 'New follower' ELSE '' END),
		n.excerpt,n.created_at,n.read_at
		FROM notifications n JOIN users actor ON actor.id=n.actor_id
		LEFT JOIN repositories r ON r.id=n.repository_id LEFT JOIN users owner ON owner.id=r.owner_id
		LEFT JOIN issues i ON i.id=n.issue_id LEFT JOIN pull_requests p ON p.id=n.pull_request_id
		WHERE n.recipient_id=$1 AND `+inboxVisibleSQL+`
		AND ($2=0 OR n.id<$2) AND ($3=false OR n.read_at IS NULL) AND ($4='' OR n.kind=$4)
		ORDER BY n.id DESC LIMIT $5`, u.ID, before, filter == "unread", kind, limit)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []Notification{}
	for rows.Next() {
		var item Notification
		if err = rows.Scan(&item.ID, &item.Kind, &item.Actor, &item.Owner, &item.Repository, &item.Issue, &item.Pull, &item.Title, &item.Excerpt, &item.CreatedAt, &item.ReadAt); err != nil {
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

func (a *App) unreadNotifications(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var count int
	if err := a.db.QueryRow(r.Context(), `SELECT count(*)::int FROM notifications n LEFT JOIN repositories r ON r.id=n.repository_id
		WHERE n.recipient_id=$1 AND n.read_at IS NULL AND `+inboxVisibleSQL, u.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]int{"unread": count})
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

// notifyDirect sends a notification aimed at one person (mention,
// assignment, review request). Assignment and review-request keys are
// scoped to the current transaction, so assigning someone again later
// notifies them again while retries of the same request stay single.
func notifyDirect(ctx context.Context, tx pgx.Tx, recipientID, actorID, repositoryID, issueID, pullID, kind, dedupe string) error {
	if recipientID == "" || recipientID == actorID {
		return nil
	}
	var ignored bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM repository_subscriptions WHERE repository_id=$1 AND user_id=$2 AND mode='ignoring')
		OR ($3<>'' AND EXISTS(SELECT 1 FROM issue_subscriptions WHERE issue_id::text=$3 AND user_id=$2 AND mode='ignore'))
		OR ($4<>'' AND EXISTS(SELECT 1 FROM pull_subscriptions WHERE pull_request_id::text=$4 AND user_id=$2 AND mode='ignore'))`,
		repositoryID, recipientID, issueID, pullID).Scan(&ignored); err != nil {
		return err
	}
	if ignored {
		return nil
	}
	if dedupe != "" && (kind == "assignment" || kind == "review_request") {
		var event int64
		if err := tx.QueryRow(ctx, `SELECT txid_current()`).Scan(&event); err != nil {
			return err
		}
		dedupe += ":" + strconv.FormatInt(event, 10)
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
	rows, err := tx.Query(ctx, `INSERT INTO notifications(recipient_id,actor_id,repository_id,issue_id,pull_request_id,kind,dedupe_key)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (recipient_id, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING
		RETURNING id`, recipientID, actorID, repositoryID, issue, pull, kind, key)
	if err != nil {
		return err
	}
	return queueNotificationRows(ctx, tx, rows)
}

// notifyFollow tells someone they gained a follower, once per follower.
func notifyFollow(ctx context.Context, tx pgx.Tx, followedID, followerID string) error {
	rows, err := tx.Query(ctx, `INSERT INTO notifications(recipient_id,actor_id,kind,dedupe_key) VALUES($1,$2,'follow',$3)
		ON CONFLICT (recipient_id, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING RETURNING id`, followedID, followerID, "follow:"+followerID)
	if err != nil {
		return err
	}
	return queueNotificationRows(ctx, tx, rows)
}

var mentionPattern = regexp.MustCompile(`(?:^|[^a-z0-9])@([a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?)`)

// noteMentions notifies readable users mentioned in body. The dedupe key
// is scoped to sourceID, so calling it again after an edit only reaches
// people who were newly mentioned.
func (a *App) noteMentions(ctx context.Context, tx pgx.Tx, repo *Repository, actor *User, body, issueID, pullID, sourceID string) error {
	seen := map[string]bool{}
	for _, match := range mentionPattern.FindAllStringSubmatch(strings.ToLower(body), 20) {
		name := match[1]
		if seen[name] || name == actor.Username {
			continue
		}
		seen[name] = true
		var userID string
		var allowed bool
		err := tx.QueryRow(ctx, `SELECT u.id,`+canReadRepoSQL("u.id", "r")+` FROM users u JOIN repositories r ON r.id=$2 WHERE u.username=$1`, name, repo.ID).Scan(&userID, &allowed)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if !allowed {
			continue
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
		if err = notifyDirect(ctx, tx, userID, actor.ID, repo.ID, issueID, pullID, "mention", "mention:"+sourceID+":"+userID); err != nil {
			return err
		}
	}
	return nil
}

// noteIssueCommentMentions re-scans an edited issue comment and notifies
// only people who were not mentioned before.
func (a *App) noteIssueCommentMentions(ctx context.Context, tx pgx.Tx, repo *Repository, actor *User, commentID, body string) error {
	var issueID string
	if err := tx.QueryRow(ctx, `SELECT issue_id FROM issue_comments WHERE id=$1`, commentID).Scan(&issueID); err != nil {
		return err
	}
	return a.noteMentions(ctx, tx, repo, actor, body, issueID, "", commentID)
}

func queueNotificationRows(ctx context.Context, tx pgx.Tx, rows pgx.Rows) error {
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return queueNotificationMail(ctx, tx, ids)
}

var notificationActions = map[string]string{
	"issue_opened":       "opened",
	"issue_comment":      "commented on",
	"issue_closed":       "closed",
	"issue_reopened":     "reopened",
	"pull_opened":        "opened",
	"pull_comment":       "commented on",
	"pull_review":        "reviewed",
	"pull_closed":        "closed",
	"pull_reopened":      "reopened",
	"pull_merged":        "merged",
	"mention":            "mentioned you on",
	"assignment":         "assigned you to",
	"review_request":     "requested your review on",
	"invitation":         "invited you to collaborate on",
	"ownership_transfer": "offered you ownership of",
	"check_success":      "reported a passing check on",
	"check_failure":      "reported a failing check on",
}

// queueNotificationMail renders one email per notification in the same
// transaction. The notification id is the dedupe key, so every event gets
// its own email and a retried transaction never mails twice.
func queueNotificationMail(ctx context.Context, tx pgx.Tx, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT n.id,n.recipient_id,n.kind,actor.username,COALESCE(owner.username,''),COALESCE(r.name,''),
		i.number,p.number,COALESCE(i.title,p.title,''),n.excerpt
		FROM notifications n JOIN users actor ON actor.id=n.actor_id
		LEFT JOIN repositories r ON r.id=n.repository_id LEFT JOIN users owner ON owner.id=r.owner_id
		LEFT JOIN issues i ON i.id=n.issue_id LEFT JOIN pull_requests p ON p.id=n.pull_request_id
		WHERE n.id=ANY($1) ORDER BY n.id`, ids)
	if err != nil {
		return err
	}
	type rendered struct {
		id                                          int64
		recipient, kind, subject, body, link, dedup string
	}
	var mails []rendered
	for rows.Next() {
		var id int64
		var recipient, kind, actor, owner, name, title, text string
		var issue, pull *int
		if err = rows.Scan(&id, &recipient, &kind, &actor, &owner, &name, &issue, &pull, &title, &text); err != nil {
			rows.Close()
			return err
		}
		m := rendered{id: id, recipient: recipient, kind: kind, dedup: "notification:" + strconv.FormatInt(id, 10)}
		repoName := owner + "/" + name
		switch {
		case kind == "follow":
			m.subject = "@" + actor + " followed you on GITOWN"
			m.body = "@" + actor + " started following you on GITOWN."
			m.link = "/u/" + actor
		case kind == "invitation" || kind == "ownership_transfer":
			m.subject = "[" + repoName + "] " + map[string]string{"invitation": "Repository invitation", "ownership_transfer": "Ownership transfer"}[kind]
			m.body = "@" + actor + " " + notificationActions[kind] + " " + repoName + "."
			m.link = "/invitations"
		default:
			number, path := 0, ""
			if issue != nil {
				number, path = *issue, "issues"
			} else if pull != nil {
				number, path = *pull, "pulls"
			}
			m.subject = fmt.Sprintf("[%s] %s (#%d)", repoName, title, number)
			action := notificationActions[kind]
			if action == "" {
				action = "updated"
			}
			m.body = fmt.Sprintf("@%s %s %s#%d: %s", actor, action, repoName, number, title)
			m.link = fmt.Sprintf("/repos/%s/%s/%s/%d", owner, name, path, number)
		}
		if text != "" {
			m.body += "\n\n> " + text
		}
		mails = append(mails, m)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, m := range mails {
		if err = queueUserMailLink(ctx, tx, m.recipient, m.kind, m.subject, m.body, m.link, m.dedup); err != nil {
			return err
		}
	}
	return nil
}
