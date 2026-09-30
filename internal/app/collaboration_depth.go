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

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

func pageLines(r *http.Request, text string) (string, bool) {
	limit := 400
	offset := 0
	if raw := r.URL.Query().Get("diff_limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 2000 {
			limit = n
		}
	}
	if raw := r.URL.Query().Get("diff_offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 && n <= 100000 {
			offset = n
		}
	}
	lines := strings.Split(text, "\n")
	if offset >= len(lines) {
		return "", false
	}
	end := offset + limit
	truncated := end < len(lines)
	if !truncated {
		end = len(lines)
	}
	return strings.Join(lines[offset:end], "\n"), truncated
}

var closingReference = regexp.MustCompile(`(?i)(?:fix(?:e[sd])?|close[sd]?|resolve[sd]?)\s+#([0-9]+)`)

func (a *App) closeReferencedIssues(ctx context.Context, tx pgx.Tx, repo *Repository, p *Pull, actor *User) error {
	numbers := map[int]bool{}
	for _, match := range closingReference.FindAllStringSubmatch(p.Body, 50) {
		n, _ := strconv.Atoi(match[1])
		if n > 0 {
			numbers[n] = true
		}
	}
	if base, err1 := a.git.Resolve(ctx, repo.ID, p.Base); err1 == nil {
		if head, err2 := a.git.Resolve(ctx, repo.ID, p.Head); err2 == nil && base != head {
			if out, logErr := a.git.Run(ctx, repo.ID, nil, "log", "--format=%s%n%b%n", base+".."+head); logErr == nil {
				for _, match := range closingReference.FindAllStringSubmatch(string(out), 50) {
					n, _ := strconv.Atoi(match[1])
					if n > 0 {
						numbers[n] = true
					}
				}
			}
		}
	}
	rows, err := tx.Query(ctx, `SELECT i.number FROM pull_issue_links l JOIN issues i ON i.id=l.issue_id WHERE l.pull_request_id=$1 AND l.closes`, p.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var n int
		if err = rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		numbers[n] = true
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for number := range numbers {
		var issueID, state string
		err = tx.QueryRow(ctx, `SELECT id,state FROM issues WHERE repository_id=$1 AND number=$2`, repo.ID, number).Scan(&issueID, &state)
		if errors.Is(err, pgx.ErrNoRows) || state != "open" {
			continue
		}
		if err != nil {
			return err
		}
		blocked, err := hasOpenBlockers(ctx, tx, issueID)
		if err != nil || blocked {
			if err != nil {
				return err
			}
			continue
		}
		if _, err = tx.Exec(ctx, `UPDATE issues SET state='closed' WHERE id=$1`, issueID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO issue_board_status(issue_id,status) VALUES($1,'done') ON CONFLICT (issue_id) DO UPDATE SET status='done',updated_at=now()`, issueID); err != nil {
			return err
		}
		if err = notifyIssue(ctx, tx, issueID, actor.ID, "issue_closed"); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.closed',$2)`, actor.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)); err != nil {
			return err
		}
	}
	return nil
}

type ReviewThread struct {
	ID         string    `json:"id"`
	Path       string    `json:"path"`
	Side       string    `json:"side"`
	Line       int       `json:"line"`
	CommitSHA  string    `json:"commit_sha"`
	Body       string    `json:"body"`
	Author     string    `json:"author"`
	CreatedAt  time.Time `json:"created_at"`
	Resolved   bool      `json:"resolved"`
	Outdated   bool      `json:"outdated"`
	ReplyCount int       `json:"reply_count"`
}

func (a *App) pullThreads(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	head, _ := a.git.Resolve(r.Context(), repo.ID, p.Head)
	rows, err := a.db.Query(r.Context(), `SELECT t.id,t.path,t.side,t.line,t.commit_sha,t.body,u.username,t.created_at,t.resolved_at IS NOT NULL,(SELECT count(*)::int FROM pull_thread_replies rr WHERE rr.thread_id=t.id) FROM pull_review_threads t JOIN users u ON u.id=t.author_id WHERE t.pull_request_id=$1 ORDER BY t.created_at,t.id LIMIT 200`, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []ReviewThread{}
	for rows.Next() {
		var item ReviewThread
		if err = rows.Scan(&item.ID, &item.Path, &item.Side, &item.Line, &item.CommitSHA, &item.Body, &item.Author, &item.CreatedAt, &item.Resolved, &item.ReplyCount); err != nil {
			serverError(w, err)
			return
		}
		item.Outdated = head == "" || item.CommitSHA != head
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, items)
}

func (a *App) createPullThread(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil || !activeRepository(w, repo) {
		return
	}
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if !repo.CanComment && repo.Role == "" && repo.Visibility == "private" {
		fail(w, 403, "forbidden", "You cannot comment on this unite request.")
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	var in struct {
		CommitSHA string `json:"commit_sha"`
		Path      string `json:"path"`
		Side      string `json:"side"`
		Line      int    `json:"line"`
		Body      string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	in.Path = strings.TrimSpace(in.Path)
	head, err := a.git.Resolve(r.Context(), repo.ID, p.Head)
	if err != nil || head != in.CommitSHA || (in.Side != "left" && in.Side != "right") || in.Line < 1 || in.Line > 100000 || !safeDiffPath(in.Path) || in.Body == "" || len(in.Body) > 10000 {
		fail(w, 422, "validation_failed", "Comment on a path and line in the latest commit, with a body up to 10,000 characters.")
		return
	}
	thread := ReviewThread{ID: auth.ID(), Path: in.Path, Side: in.Side, Line: in.Line, CommitSHA: head, Body: in.Body, Author: u.Username}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	err = tx.QueryRow(r.Context(), `INSERT INTO pull_review_threads(id,pull_request_id,author_id,commit_sha,path,side,line,body) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`, thread.ID, p.ID, u.ID, thread.CommitSHA, thread.Path, thread.Side, thread.Line, thread.Body).Scan(&thread.CreatedAt)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO pull_subscriptions(pull_request_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, p.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO pull_events(pull_request_id,actor_id,kind,body) VALUES($1,$2,'inline',$3)`, p.ID, u.ID, thread.Path); err != nil {
		serverError(w, err)
		return
	}
	if err = notifyPull(r.Context(), tx, p.ID, u.ID, "pull_comment"); err != nil {
		serverError(w, err)
		return
	}
	if err = a.noteMentions(r.Context(), tx, repo, u, thread.Body, "", p.ID, thread.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.inline_comment',$2)`, u.ID, fmt.Sprintf("%s/%s#%d:%s", repo.Owner, repo.Name, p.Number, thread.Path)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, thread)
}

func safeDiffPath(path string) bool {
	if path == "" || len(path) > 4096 || strings.HasPrefix(path, "/") || strings.Contains(path, "..") || strings.ContainsAny(path, "\x00\r\n\\") {
		return false
	}
	return true
}

func (a *App) resolvePullThread(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil || !activeRepository(w, repo) {
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
		Resolved bool `json:"resolved"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !repo.CanTriage {
		fail(w, 403, "forbidden", "Repository triage permission is required to resolve a conversation.")
		return
	}
	var resolved any
	var resolver any
	if in.Resolved {
		resolved = time.Now()
		resolver = u.ID
	}
	result, err := a.db.Exec(r.Context(), `UPDATE pull_review_threads SET resolved_at=$1,resolved_by=$2 WHERE id=$3 AND pull_request_id=$4`, resolved, resolver, r.PathValue("id"), p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Conversation not found.")
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO pull_events(pull_request_id,actor_id,kind,body) VALUES($1,$2,'resolved',$3)`, p.ID, u.ID, r.PathValue("id"))
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.thread_resolved',$2)`, u.ID, fmt.Sprintf("%s/%s#%d:%t", repo.Owner, repo.Name, p.Number, in.Resolved))
	respond(w, 200, map[string]bool{"resolved": in.Resolved})
}

func (a *App) dismissPullReview(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil || !activeRepository(w, repo) {
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
		Reason string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" || len(in.Reason) > 2000 {
		fail(w, 422, "validation_failed", "A dismissal reason up to 2,000 characters is required.")
		return
	}
	var reviewer string
	err := a.db.QueryRow(r.Context(), `SELECT u.username FROM pull_reviews rv JOIN users u ON u.id=rv.reviewer_id WHERE rv.id=$1 AND rv.pull_request_id=$2`, r.PathValue("id"), p.ID).Scan(&reviewer)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Review not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if repo.Role != "owner" && repo.Role != "maintain" && reviewer != u.Username {
		fail(w, 403, "forbidden", "Only the reviewer, a maintainer, or the owner can dismiss a review.")
		return
	}
	result, err := a.db.Exec(r.Context(), `UPDATE pull_reviews SET dismissed_at=now(),dismissed_by=$1,dismissal_reason=$2 WHERE id=$3 AND dismissed_at IS NULL`, u.ID, in.Reason, r.PathValue("id"))
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		fail(w, 409, "already_dismissed", "This review is already dismissed.")
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO pull_events(pull_request_id,actor_id,kind,body) VALUES($1,$2,'review_dismissed',$3)`, p.ID, u.ID, in.Reason)
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.review_dismissed',$2)`, u.ID, fmt.Sprintf("%s/%s#%d:%s", repo.Owner, repo.Name, p.Number, r.PathValue("id")))
	respond(w, 200, map[string]bool{"dismissed": true})
}

func (a *App) pullReviewers(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT u.username,u.display_name FROM pull_review_requests rr JOIN users u ON u.id=rr.reviewer_id WHERE rr.pull_request_id=$1 ORDER BY u.username`, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var username, name string
		if err = rows.Scan(&username, &name); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]string{"username": username, "display_name": name})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, items)
}

func (a *App) updatePullReviewers(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil || !repo.CanTriage || !activeRepository(w, repo) {
		if repo != nil && !repo.CanTriage {
			fail(w, 403, "forbidden", "Repository triage permission is required.")
		}
		return
	}
	u := a.user(r)
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	var in struct {
		Usernames []string `json:"usernames"`
	}
	if !decode(w, r, &in) || len(in.Usernames) > 10 {
		if len(in.Usernames) > 10 {
			fail(w, 422, "validation_failed", "Request up to 10 reviewers.")
		}
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `DELETE FROM pull_review_requests WHERE pull_request_id=$1`, p.ID); err != nil {
		serverError(w, err)
		return
	}
	for _, name := range in.Usernames {
		name = strings.ToLower(strings.TrimSpace(name))
		if !slug.MatchString(name) || name == p.Author {
			fail(w, 422, "validation_failed", "Reviewers must be repository writers other than the author.")
			return
		}
		var reviewerID string
		err = tx.QueryRow(r.Context(), `SELECT u.id FROM users u WHERE u.username=$1 AND (u.id=$2 OR EXISTS(SELECT 1 FROM repository_members rm WHERE rm.repository_id=$3 AND rm.user_id=u.id AND rm.role IN ('write','maintain')))`, name, repo.OwnerID, repo.ID).Scan(&reviewerID)
		if err != nil {
			fail(w, 422, "validation_failed", "Reviewers must be repository writers other than the author.")
			return
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO pull_review_requests(pull_request_id,reviewer_id,requested_by) VALUES($1,$2,$3)`, p.ID, reviewerID, u.ID); err != nil {
			serverError(w, err)
			return
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO pull_subscriptions(pull_request_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, p.ID, reviewerID); err != nil {
			serverError(w, err)
			return
		}
		if err = notifyDirect(r.Context(), tx, reviewerID, u.ID, repo.ID, "", p.ID, "review_request", "review-request:"+p.ID+":"+reviewerID); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.reviewers_updated',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, p.Number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.pullReviewers(w, r)
}

func (a *App) updateCommitStatus(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, true)
	if repo == nil {
		return
	}
	sha := strings.ToLower(r.PathValue("sha"))
	if len(sha) != 40 {
		fail(w, 422, "validation_failed", "Provide a full commit SHA.")
		return
	}
	if _, err := a.git.Run(r.Context(), repo.ID, nil, "cat-file", "-t", sha); err != nil {
		fail(w, 404, "not_found", "Commit not found.")
		return
	}
	var in struct {
		Context     string `json:"context"`
		State       string `json:"state"`
		Description string `json:"description"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Description = strings.TrimSpace(in.Description)
	if !checkContext.MatchString(in.Context) || (in.State != "pending" && in.State != "success" && in.State != "failure" && in.State != "error") || len(in.Description) > 300 {
		fail(w, 422, "validation_failed", "Provide a check context and a state of pending, success, failure, or error.")
		return
	}
	u := a.user(r)
	if _, err := a.db.Exec(r.Context(), `INSERT INTO commit_statuses(repository_id,sha,context,state,description,updated_by) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(repository_id,sha,context) DO UPDATE SET state=excluded.state,description=excluded.description,updated_by=excluded.updated_by,updated_at=now()`, repo.ID, sha, in.Context, in.State, in.Description, u.ID); err != nil {
		serverError(w, err)
		return
	}
	a.notifyCheckResult(r.Context(), repo, u.ID, sha, in.Context, in.State, in.Description)
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'commit.status',$2)`, u.ID, fmt.Sprintf("%s/%s@%s:%s=%s", repo.Owner, repo.Name, sha, in.Context, in.State))
	respond(w, 200, map[string]string{"sha": sha, "context": in.Context, "state": in.State, "description": in.Description})
}

func (a *App) commitStatuses(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	sha := strings.ToLower(r.PathValue("sha"))
	rows, err := a.db.Query(r.Context(), `SELECT context,state,description,updated_at FROM commit_statuses WHERE repository_id=$1 AND sha=$2 ORDER BY context`, repo.ID, sha)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var contextName, state, description string
		var updated time.Time
		if err = rows.Scan(&contextName, &state, &description, &updated); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"context": contextName, "state": state, "description": description, "updated_at": updated})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, items)
}

type TimelineItem struct {
	Kind      string    `json:"kind"`
	Actor     string    `json:"actor"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

func (a *App) pullTimeline(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 1000 {
			fail(w, 422, "validation_failed", "Offset must be between zero and 1000.")
			return
		}
		offset = n
	}
	rows, err := a.db.Query(r.Context(), `SELECT kind,actor,body,created_at FROM (
		SELECT 'comment' AS kind,u.username AS actor,c.body,c.created_at,c.id::text AS id FROM pull_comments c JOIN users u ON u.id=c.author_id WHERE c.pull_request_id=$1
		UNION ALL
		SELECT 'review.'||rv.state,u.username,rv.body,rv.created_at,rv.id::text FROM pull_reviews rv JOIN users u ON u.id=rv.reviewer_id WHERE rv.pull_request_id=$1
		UNION ALL
		SELECT 'inline',u.username,t.path||': '||t.body,t.created_at,t.id::text FROM pull_review_threads t JOIN users u ON u.id=t.author_id WHERE t.pull_request_id=$1
		UNION ALL
		SELECT e.kind,COALESCE(u.username,''),e.body,e.created_at,e.id::text FROM pull_events e LEFT JOIN users u ON u.id=e.actor_id WHERE e.pull_request_id=$1
	) timeline ORDER BY created_at,id LIMIT 51 OFFSET $2`, p.ID, offset)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []TimelineItem{}
	for rows.Next() {
		var item TimelineItem
		if err = rows.Scan(&item.Kind, &item.Actor, &item.Body, &item.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		if len(items) == 50 {
			respond(w, 200, map[string]any{"items": items, "has_more": true})
			return
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "has_more": false})
}
