package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

// closeReferencedIssues closes issues named by closing keywords in the unite
// request body, in the merged commits' messages, or by closing links. Callers
// pass the commit messages collected before refs move, because afterwards the
// head branch may be gone or already contained in the base.
func (a *App) closeReferencedIssues(ctx context.Context, tx pgx.Tx, repo *Repository, p *Pull, actor *User, messages string) error {
	numbers := map[int]bool{}
	addReferences := func(text string) {
		for _, match := range closingReference.FindAllStringSubmatch(text, 50) {
			n, _ := strconv.Atoi(match[1])
			if n > 0 {
				numbers[n] = true
			}
		}
	}
	addReferences(p.Body)
	addReferences(messages)
	if p.ExpectedBase != nil && p.MergeSHA != nil {
		// Recovery after an interrupted merge: the merge result still names the commits.
		if out, logErr := a.git.Run(ctx, repo.ID, nil, "log", "--format=%s%n%b%n", *p.ExpectedBase+".."+*p.MergeSHA); logErr == nil {
			addReferences(string(out))
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
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if state != "open" {
			continue
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
	base, err := a.git.Resolve(r.Context(), repo.ID, p.Base)
	if err != nil {
		fail(w, 409, "stale_branches", "The base branch no longer exists.")
		return
	}
	patch, err := a.git.Run(r.Context(), repo.ID, nil, "diff", "--no-ext-diff", "--no-textconv", "--unified=3", base+"..."+head, "--", in.Path)
	if err != nil {
		serverError(w, err)
		return
	}
	left, right := diffLineNumbers(string(patch))
	if (in.Side == "right" && !right[in.Line]) || (in.Side == "left" && !left[in.Line]) {
		fail(w, 422, "line_not_in_diff", "That line is not part of this unite request's changes. Comment on a changed or nearby line.")
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

// diffLineNumbers lists the old-file (left) and new-file (right) line numbers
// shown in a unified diff, including context lines.
func diffLineNumbers(patch string) (map[int]bool, map[int]bool) {
	left, right := map[int]bool{}, map[int]bool{}
	oldLine, newLine := 0, 0
	inHunk := false
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@") {
			match := hunkHeader.FindStringSubmatch(line)
			if match == nil {
				inHunk = false
				continue
			}
			oldLine, _ = strconv.Atoi(match[1])
			newLine, _ = strconv.Atoi(match[2])
			inHunk = true
			continue
		}
		if !inHunk || line == "" {
			continue
		}
		switch line[0] {
		case '+':
			right[newLine] = true
			newLine++
		case '-':
			left[oldLine] = true
			oldLine++
		case ' ':
			left[oldLine] = true
			right[newLine] = true
			oldLine++
			newLine++
		case '\\':
		default:
			inHunk = false
		}
	}
	return left, right
}

var hunkHeader = regexp.MustCompile(`^@@ -([0-9]+)(?:,[0-9]+)? \+([0-9]+)(?:,[0-9]+)? @@`)

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
	var authorID string
	err := a.db.QueryRow(r.Context(), `SELECT author_id FROM pull_review_threads WHERE id=$1 AND pull_request_id=$2`, r.PathValue("id"), p.ID).Scan(&authorID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Conversation not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if !repo.CanTriage && authorID != u.ID {
		fail(w, 403, "forbidden", "Only the conversation author or a triager can resolve a conversation.")
		return
	}
	var resolved any
	var resolver any
	if in.Resolved {
		resolved = time.Now()
		resolver = u.ID
	}
	kind := "resolved"
	if !in.Resolved {
		kind = "unresolved"
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `UPDATE pull_review_threads SET resolved_at=$1,resolved_by=$2 WHERE id=$3 AND pull_request_id=$4`, resolved, resolver, r.PathValue("id"), p.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO pull_events(pull_request_id,actor_id,kind,body) VALUES($1,$2,$3,$4)`, p.ID, u.ID, kind, r.PathValue("id")); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.thread_resolved',$2)`, u.ID, fmt.Sprintf("%s/%s#%d:%t", repo.Owner, repo.Name, p.Number, in.Resolved)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
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
	if !repo.CanMaintain && reviewer != u.Username {
		fail(w, 403, "forbidden", "Only the reviewer, a maintainer, or the owner can dismiss a review.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	result, err := tx.Exec(r.Context(), `UPDATE pull_reviews SET dismissed_at=now(),dismissed_by=$1,dismissal_reason=$2 WHERE id=$3 AND dismissed_at IS NULL`, u.ID, in.Reason, r.PathValue("id"))
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		fail(w, 409, "already_dismissed", "This review is already dismissed.")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO pull_events(pull_request_id,actor_id,kind,body) VALUES($1,$2,'review_dismissed',$3)`, p.ID, u.ID, "@"+reviewer+": "+in.Reason); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.review_dismissed',$2)`, u.ID, fmt.Sprintf("%s/%s#%d:%s:%s", repo.Owner, repo.Name, p.Number, r.PathValue("id"), reviewer)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
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

// statusActor accepts a browser session or, for CI systems, a personal access
// token with repo:write scope sent as a bearer token. When a bearer token is
// present the session cookie is ignored.
func (a *App) statusActor(w http.ResponseWriter, r *http.Request) *User {
	raw, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !bearer {
		return a.requireUser(w, r)
	}
	var u User
	var scope string
	if !strings.HasPrefix(raw, "gtn_") || len(raw) > 200 {
		fail(w, 401, "authentication_required", "Use a personal access token with repo:write scope.")
		return nil
	}
	err := a.db.QueryRow(r.Context(), `SELECT u.id::text,u.username,u.display_name,t.scope FROM access_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=$1 AND t.expires_at>now()`, auth.Digest(raw)).Scan(&u.ID, &u.Username, &u.DisplayName, &scope)
	if err != nil {
		fail(w, 401, "authentication_required", "Use a personal access token with repo:write scope.")
		return nil
	}
	if scope != "repo:write" {
		fail(w, 403, "forbidden", "The token needs repo:write scope to report checks.")
		return nil
	}
	return &u
}

// isTokenStatusRequest identifies CI status reports, which carry a bearer token
// instead of browser credentials and are therefore not subject to CSRF.
func isTokenStatusRequest(r *http.Request) bool {
	if r.Method != "POST" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return false
	}
	return tokenStatusPath.MatchString(r.URL.Path)
}

var tokenStatusPath = regexp.MustCompile(`^/api/v1/repos/[^/]+/[^/]+/commits/[0-9a-fA-F]{40}/status$`)

func (a *App) updateCommitStatus(w http.ResponseWriter, r *http.Request) {
	u := a.statusActor(w, r)
	if u == nil {
		return
	}
	repo := a.accessAs(w, r, u, true)
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
		TargetURL   string `json:"target_url"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Description = strings.TrimSpace(in.Description)
	in.TargetURL = strings.TrimSpace(in.TargetURL)
	if !checkContext.MatchString(in.Context) || (in.State != "pending" && in.State != "success" && in.State != "failure" && in.State != "error") || len(in.Description) > 300 {
		fail(w, 422, "validation_failed", "Provide a check context and a state of pending, success, failure, or error.")
		return
	}
	if in.TargetURL != "" && (len(in.TargetURL) > 500 || !safeLink(in.TargetURL)) {
		fail(w, 422, "validation_failed", "The target URL must be an http or https link up to 500 characters.")
		return
	}
	var matches []string
	if in.State != "pending" {
		rows, err := a.db.Query(r.Context(), `SELECT id,head_branch FROM pull_requests WHERE repository_id=$1 AND state='open'`, repo.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		type candidate struct{ id, branch string }
		var candidates []candidate
		for rows.Next() {
			var item candidate
			if err = rows.Scan(&item.id, &item.branch); err != nil {
				rows.Close()
				serverError(w, err)
				return
			}
			candidates = append(candidates, item)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			serverError(w, err)
			return
		}
		for _, item := range candidates {
			if head, resolveErr := a.git.Resolve(r.Context(), repo.ID, item.branch); resolveErr == nil && head == sha {
				matches = append(matches, item.id)
			}
		}
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `INSERT INTO commit_statuses(repository_id,sha,context,state,description,target_url,updated_by) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(repository_id,sha,context) DO UPDATE SET state=excluded.state,description=excluded.description,target_url=excluded.target_url,updated_by=excluded.updated_by,updated_at=now()`, repo.ID, sha, in.Context, in.State, in.Description, in.TargetURL, u.ID); err != nil {
		serverError(w, err)
		return
	}
	kind := "check_success"
	if in.State != "success" {
		kind = "check_failure"
	}
	for _, pullID := range matches {
		if err = notifyPullKey(r.Context(), tx, pullID, u.ID, kind, sha+":"+in.Context+":"+in.State); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'commit.status',$2)`, u.ID, fmt.Sprintf("%s/%s@%s:%s=%s", repo.Owner, repo.Name, sha, in.Context, in.State)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"sha": sha, "context": in.Context, "state": in.State, "description": in.Description, "target_url": in.TargetURL})
}

func safeLink(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil
}

func (a *App) commitStatuses(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	sha := strings.ToLower(r.PathValue("sha"))
	rows, err := a.db.Query(r.Context(), `SELECT cs.context,cs.state,cs.description,cs.target_url,COALESCE(u.username,''),cs.updated_at FROM commit_statuses cs LEFT JOIN users u ON u.id=cs.updated_by WHERE cs.repository_id=$1 AND cs.sha=$2 ORDER BY cs.context`, repo.ID, sha)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var contextName, state, description, target, reporter string
		var updated time.Time
		if err = rows.Scan(&contextName, &state, &description, &target, &reporter, &updated); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"context": contextName, "state": state, "description": description, "target_url": target, "reporter": reporter, "updated_at": updated})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, items)
}

type ThreadReply struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

func (a *App) threadReplies(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	var found bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM pull_review_threads WHERE id::text=$1 AND pull_request_id=$2)`, r.PathValue("id"), p.ID).Scan(&found); err != nil {
		serverError(w, err)
		return
	}
	if !found {
		fail(w, 404, "not_found", "Conversation not found.")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT rp.id,u.username,rp.body,rp.created_at FROM pull_thread_replies rp JOIN users u ON u.id=rp.author_id WHERE rp.thread_id::text=$1 ORDER BY rp.created_at,rp.id LIMIT 200`, r.PathValue("id"))
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []ThreadReply{}
	for rows.Next() {
		var item ThreadReply
		if err = rows.Scan(&item.ID, &item.Author, &item.Body, &item.CreatedAt); err != nil {
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
	head, _ := a.git.Resolve(r.Context(), repo.ID, p.Head)
	// Comments, reviews, and inline threads come from their own tables, which
	// predate pull_events; the event log contributes every other kind.
	rows, err := a.db.Query(r.Context(), `SELECT kind,actor,body,created_at FROM (
		SELECT 'comment' AS kind,u.username AS actor,c.body,c.created_at,c.id::text AS id FROM pull_comments c JOIN users u ON u.id=c.author_id WHERE c.pull_request_id=$1
		UNION ALL
		SELECT 'review.'||rv.state,u.username,rv.body,rv.created_at,rv.id::text FROM pull_reviews rv JOIN users u ON u.id=rv.reviewer_id WHERE rv.pull_request_id=$1
		UNION ALL
		SELECT 'inline',u.username,t.path||':'||t.line||' '||t.body,t.created_at,t.id::text FROM pull_review_threads t JOIN users u ON u.id=t.author_id WHERE t.pull_request_id=$1
		UNION ALL
		SELECT 'reply',u.username,t.path||':'||t.line||' '||rp.body,rp.created_at,rp.id::text FROM pull_thread_replies rp JOIN pull_review_threads t ON t.id=rp.thread_id JOIN users u ON u.id=rp.author_id WHERE t.pull_request_id=$1
		UNION ALL
		SELECT e.kind,COALESCE(u.username,''),e.body,e.created_at,lpad(e.id::text,20,'0') FROM pull_events e LEFT JOIN users u ON u.id=e.actor_id WHERE e.pull_request_id=$1 AND e.kind NOT IN ('comment','inline') AND e.kind NOT LIKE 'review.%'
		UNION ALL
		SELECT 'check.'||cs.state,COALESCE(u.username,''),cs.context||CASE WHEN cs.description<>'' THEN ': '||cs.description ELSE '' END,cs.updated_at,cs.sha||cs.context FROM commit_statuses cs LEFT JOIN users u ON u.id=cs.updated_by
			WHERE cs.repository_id=$3 AND (cs.sha=$4 OR cs.sha IN (SELECT split_part(pe.body,'..',2) FROM pull_events pe WHERE pe.pull_request_id=$1 AND pe.kind='push'))
	) timeline ORDER BY created_at,id LIMIT 51 OFFSET $2`, p.ID, offset, repo.ID, head)
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
