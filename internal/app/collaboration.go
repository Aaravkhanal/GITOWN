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

type Issue struct {
	ID        string     `json:"id"`
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	State     string     `json:"state"`
	Author    string     `json:"author"`
	CreatedAt time.Time  `json:"created_at"`
	Pinned    bool       `json:"pinned"`
	Priority  string     `json:"priority"`
	Iteration string     `json:"iteration"`
	Estimate  *int       `json:"estimate"`
	DueDate   *time.Time `json:"due_date"`
}

type IssueComment struct {
	ID        string    `json:"id"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

type Label struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Color       string    `json:"color"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

var labelColor = regexp.MustCompile(`^[0-9a-f]{6}$`)

type Pull struct {
	ID           string    `json:"id"`
	Number       int       `json:"number"`
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	State        string    `json:"state"`
	Author       string    `json:"author"`
	Base         string    `json:"base_branch"`
	Head         string    `json:"head_branch"`
	MergeSHA     *string   `json:"merge_sha"`
	ExpectedBase *string   `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	Draft        bool      `json:"draft"`
	MergeMethod  *string   `json:"merge_method"`
	// Set only in a merge response when deleting the source branch was requested.
	BranchDeleted     *bool  `json:"branch_deleted,omitempty"`
	BranchDeleteError string `json:"branch_delete_error,omitempty"`
}

type PullComment struct {
	ID        string    `json:"id"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

type PullReview struct {
	ID              string    `json:"id"`
	State           string    `json:"state"`
	Body            string    `json:"body"`
	Reviewer        string    `json:"reviewer"`
	HeadSHA         string    `json:"head_sha"`
	Stale           bool      `json:"stale"`
	Dismissed       bool      `json:"dismissed"`
	DismissalReason string    `json:"dismissal_reason,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

const pullColumns = `p.id,p.number,p.title,p.body,p.state,u.username,p.base_branch,p.head_branch,p.merge_sha,p.expected_base_sha,p.created_at,p.draft,p.merge_method`

func scanPull(row scanner) (Pull, error) {
	var p Pull
	err := row.Scan(&p.ID, &p.Number, &p.Title, &p.Body, &p.State, &p.Author, &p.Base, &p.Head, &p.MergeSHA, &p.ExpectedBase, &p.CreatedAt, &p.Draft, &p.MergeMethod)
	return p, err
}

func (a *App) issues(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	state := r.URL.Query().Get("state")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	label := strings.TrimSpace(r.URL.Query().Get("label"))
	assignee := strings.TrimSpace(r.URL.Query().Get("assignee"))
	if (state != "" && state != "open" && state != "closed") || len(q) > 100 || len(label) > 50 || (assignee != "" && !slug.MatchString(assignee)) {
		fail(w, 422, "validation_failed", "Filter by open or closed, a query up to 100 characters, a label, and an assignee username.")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT i.id,i.number,i.title,i.body,i.state,u.username,i.created_at,i.pinned,i.priority,i.iteration,i.estimate,i.due_date FROM issues i JOIN users u ON u.id=i.author_id WHERE repository_id=$1
		AND ($2='' OR i.state=$2)
		AND ($3='' OR strpos(lower(i.title||' '||i.body), lower($3))>0)
		AND ($4='' OR EXISTS(SELECT 1 FROM issue_labels il JOIN labels l ON l.id=il.label_id WHERE il.issue_id=i.id AND lower(l.name)=lower($4)))
		AND ($5='' OR EXISTS(SELECT 1 FROM issue_assignees ia JOIN users au ON au.id=ia.user_id WHERE ia.issue_id=i.id AND au.username=$5))
		ORDER BY i.pinned DESC, number DESC LIMIT 100`, repo.ID, state, q, label, assignee)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []Issue{}
	for rows.Next() {
		var i Issue
		if err := rows.Scan(&i.ID, &i.Number, &i.Title, &i.Body, &i.State, &i.Author, &i.CreatedAt, &i.Pinned, &i.Priority, &i.Iteration, &i.Estimate, &i.DueDate); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, items)
}

func validContent(title, body string) bool {
	return strings.TrimSpace(title) != "" && len(title) <= 200 && len(body) <= 20000
}
func activeRepository(w http.ResponseWriter, repo *Repository) bool {
	if repo.Archived {
		fail(w, 409, "repository_archived", "Unarchive this repository before making changes.")
		return false
	}
	return true
}
func (a *App) createIssue(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanTriage {
		fail(w, 403, "forbidden", "Repository triage permission is required.")
		return
	}
	if !activeRepository(w, repo) {
		return
	}
	u := a.user(r)
	var in struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validContent(in.Title, in.Body) {
		fail(w, 422, "validation_failed", "A title up to 200 characters and body up to 20,000 characters are required.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT id FROM repositories WHERE id=$1 FOR UPDATE`, repo.ID); err != nil {
		serverError(w, err)
		return
	}
	i := Issue{ID: auth.ID(), Title: strings.TrimSpace(in.Title), Body: in.Body, State: "open", Author: u.Username}
	err = tx.QueryRow(r.Context(), `INSERT INTO issues(id,repository_id,number,author_id,title,body) SELECT $1,$2,COALESCE(MAX(number),0)+1,$3,$4,$5 FROM issues WHERE repository_id=$2 RETURNING number,created_at`, i.ID, repo.ID, u.ID, i.Title, i.Body).Scan(&i.Number, &i.CreatedAt)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO issue_subscriptions(issue_id,user_id) VALUES($1,$2)`, i.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.opened',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, i.Number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, i)
}

func (a *App) updateIssue(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanTriage {
		fail(w, 403, "forbidden", "Repository triage permission is required.")
		return
	}
	if !activeRepository(w, repo) {
		return
	}
	u := a.user(r)
	var in struct {
		State string `json:"state"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.State != "open" && in.State != "closed" {
		fail(w, 422, "validation_failed", "State must be open or closed.")
		return
	}
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT id FROM repositories WHERE id=$1 FOR UPDATE`, repo.ID); err != nil {
		serverError(w, err)
		return
	}
	var issueID, previousState string
	if err = tx.QueryRow(r.Context(), `SELECT id,state FROM issues WHERE repository_id=$1 AND number=$2 FOR UPDATE`, repo.ID, number).Scan(&issueID, &previousState); err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Issue not found.")
		return
	} else if err != nil {
		serverError(w, err)
		return
	}
	if in.State == "closed" {
		blocked, blockerErr := hasOpenBlockers(r.Context(), tx, issueID)
		if blockerErr != nil {
			serverError(w, blockerErr)
			return
		}
		if blocked {
			fail(w, 409, "open_blockers", "Close this issue's blockers before closing the issue.")
			return
		}
	}
	result, err := tx.Exec(r.Context(), `UPDATE issues SET state=$1 WHERE repository_id=$2 AND number=$3`, in.State, repo.ID, number)
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	kind := "issue_closed"
	if in.State == "open" {
		kind = "issue_reopened"
	}
	if previousState != in.State {
		err = notifyIssue(r.Context(), tx, issueID, u.ID, kind)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if in.State == "closed" {
		if _, err = tx.Exec(r.Context(), `INSERT INTO issue_board_status(issue_id,status)
			SELECT id,'done' FROM issues WHERE repository_id=$1 AND number=$2
			ON CONFLICT (issue_id) DO UPDATE SET status='done',updated_at=now()`, repo.ID, number); err != nil {
			serverError(w, err)
			return
		}
	} else {
		if _, err = tx.Exec(r.Context(), `UPDATE issue_board_status SET status='todo',updated_at=now()
			WHERE issue_id IN (SELECT id FROM issues WHERE repository_id=$1 AND number=$2) AND status='done'`, repo.ID, number); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,$2,$3)`, u.ID, "issue."+in.State, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"state": in.State})
}

func issueNumber(w http.ResponseWriter, r *http.Request) (int, bool) {
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || number < 1 {
		fail(w, 404, "not_found", "Issue not found.")
		return 0, false
	}
	return number, true
}

func (a *App) issueComments(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var exists bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM issues WHERE repository_id=$1 AND number=$2)`, repo.ID, number).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if !exists {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT c.id,c.body,u.username,c.created_at FROM issue_comments c JOIN issues i ON i.id=c.issue_id JOIN users u ON u.id=c.author_id WHERE i.repository_id=$1 AND i.number=$2 ORDER BY c.created_at,c.id LIMIT 200`, repo.ID, number)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	comments := []IssueComment{}
	for rows.Next() {
		var comment IssueComment
		if err = rows.Scan(&comment.ID, &comment.Body, &comment.Author, &comment.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		comments = append(comments, comment)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, comments)
}

func (a *App) createIssueComment(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	u := a.requireUser(w, r)
	if u == nil || !activeRepository(w, repo) {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" || len(in.Body) > 10000 {
		fail(w, 422, "validation_failed", "A comment between 1 and 10,000 characters is required.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	comment := IssueComment{ID: auth.ID(), Body: in.Body, Author: u.Username}
	var issueID string
	err = tx.QueryRow(r.Context(), `INSERT INTO issue_comments(id,issue_id,author_id,body) SELECT $1,i.id,$2,$3 FROM issues i WHERE i.repository_id=$4 AND i.number=$5 RETURNING issue_id,created_at`, comment.ID, u.ID, comment.Body, repo.ID, number).Scan(&issueID, &comment.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO issue_subscriptions(issue_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, issueID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err = notifyIssue(r.Context(), tx, issueID, u.ID, "issue_comment"); err != nil {
		serverError(w, err)
		return
	}
	if err = a.noteMentions(r.Context(), tx, repo, u, comment.Body, issueID, "", comment.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.commented',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, comment)
}

func (a *App) labels(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,color,description,created_at FROM labels WHERE repository_id=$1 ORDER BY lower(name),id LIMIT 100`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	labels := []Label{}
	for rows.Next() {
		var label Label
		if err = rows.Scan(&label.ID, &label.Name, &label.Color, &label.Description, &label.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		labels = append(labels, label)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, labels)
}

func (a *App) createLabel(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanTriage {
		fail(w, 403, "forbidden", "Repository triage permission is required.")
		return
	}
	if !activeRepository(w, repo) {
		return
	}
	u := a.user(r)
	var in struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Color = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(in.Color), "#"))
	in.Description = strings.TrimSpace(in.Description)
	if in.Name == "" || len(in.Name) > 50 || !labelColor.MatchString(in.Color) || len(in.Description) > 200 {
		fail(w, 422, "validation_failed", "Use a label name up to 50 characters, a six-digit hex color, and a description up to 200 characters.")
		return
	}
	label := Label{ID: auth.ID(), Name: in.Name, Color: in.Color, Description: in.Description}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	err = tx.QueryRow(r.Context(), `INSERT INTO labels(id,repository_id,name,color,description) VALUES($1,$2,$3,$4,$5) RETURNING created_at`, label.ID, repo.ID, label.Name, label.Color, label.Description).Scan(&label.CreatedAt)
	if conflict(err) {
		fail(w, 409, "label_exists", "A label with that name already exists.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'label.created',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+label.Name); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, label)
}

func (a *App) deleteLabel(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanTriage {
		fail(w, 403, "forbidden", "Repository triage permission is required.")
		return
	}
	if !activeRepository(w, repo) {
		return
	}
	u := a.user(r)
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var name string
	err = tx.QueryRow(r.Context(), `DELETE FROM labels WHERE id=$1 AND repository_id=$2 RETURNING name`, r.PathValue("id"), repo.ID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Label not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'label.deleted',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+name); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func (a *App) issueLabels(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var exists bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM issues WHERE repository_id=$1 AND number=$2)`, repo.ID, number).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if !exists {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT l.id,l.name,l.color,l.description,l.created_at FROM labels l JOIN issue_labels il ON il.label_id=l.id JOIN issues i ON i.id=il.issue_id WHERE i.repository_id=$1 AND i.number=$2 ORDER BY lower(l.name),l.id`, repo.ID, number)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	labels := []Label{}
	for rows.Next() {
		var label Label
		if err = rows.Scan(&label.ID, &label.Name, &label.Color, &label.Description, &label.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		labels = append(labels, label)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, labels)
}

func (a *App) addIssueLabel(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanTriage {
		fail(w, 403, "forbidden", "Repository triage permission is required.")
		return
	}
	if !activeRepository(w, repo) {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var in struct {
		LabelID string `json:"label_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	u := a.user(r)
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var label Label
	err = tx.QueryRow(r.Context(), `WITH selected AS (SELECT i.id issue_id,l.id,l.name,l.color,l.description,l.created_at FROM issues i JOIN labels l ON l.repository_id=i.repository_id WHERE i.repository_id=$1 AND i.number=$2 AND l.id=$3), inserted AS (INSERT INTO issue_labels(issue_id,label_id) SELECT issue_id,id FROM selected ON CONFLICT DO NOTHING RETURNING label_id) SELECT s.id,s.name,s.color,s.description,s.created_at FROM selected s`, repo.ID, number, in.LabelID).Scan(&label.ID, &label.Name, &label.Color, &label.Description, &label.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Issue or label not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.label_added',$2)`, u.ID, fmt.Sprintf("%s/%s#%d:%s", repo.Owner, repo.Name, number, label.Name)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, label)
}

func (a *App) removeIssueLabel(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanTriage {
		fail(w, 403, "forbidden", "Repository triage permission is required.")
		return
	}
	if !activeRepository(w, repo) {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	u := a.user(r)
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var name string
	err = tx.QueryRow(r.Context(), `DELETE FROM issue_labels il USING issues i,labels l WHERE il.issue_id=i.id AND il.label_id=l.id AND i.repository_id=$1 AND i.number=$2 AND l.repository_id=$1 AND l.id=$3 RETURNING l.name`, repo.ID, number, r.PathValue("id")).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Issue label not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.label_removed',$2)`, u.ID, fmt.Sprintf("%s/%s#%d:%s", repo.Owner, repo.Name, number, name)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

func (a *App) pulls(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT `+pullColumns+` FROM pull_requests p JOIN users u ON u.id=p.author_id WHERE repository_id=$1 ORDER BY number DESC LIMIT 100`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []Pull{}
	for rows.Next() {
		p, err := scanPull(rows)
		if err != nil {
			serverError(w, err)
			return
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, items)
}

func (a *App) createPull(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, true)
	if repo == nil {
		return
	}
	u := a.user(r)
	var in struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		Base  string `json:"base_branch"`
		Head  string `json:"head_branch"`
		Draft bool   `json:"draft"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validContent(in.Title, in.Body) || in.Base == in.Head {
		fail(w, 422, "validation_failed", "Provide a title and two different branches.")
		return
	}
	base, e1 := a.git.Resolve(r.Context(), repo.ID, in.Base)
	head, e2 := a.git.Resolve(r.Context(), repo.ID, in.Head)
	if e1 != nil || e2 != nil {
		fail(w, 422, "invalid_branch", "Both branches must exist.")
		return
	}
	if _, err := a.git.Run(r.Context(), repo.ID, nil, "merge-base", base, head); err != nil {
		fail(w, 422, "unrelated_history", "These branches have no common history.")
		return
	}
	if _, err := a.git.Run(r.Context(), repo.ID, nil, "merge-base", "--is-ancestor", head, base); err == nil {
		fail(w, 422, "no_changes", "The base branch already contains these commits.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT id FROM repositories WHERE id=$1 FOR UPDATE`, repo.ID); err != nil {
		serverError(w, err)
		return
	}
	p := Pull{ID: auth.ID(), Title: strings.TrimSpace(in.Title), Body: in.Body, State: "open", Author: u.Username, Base: in.Base, Head: in.Head, Draft: in.Draft}
	err = tx.QueryRow(r.Context(), `INSERT INTO pull_requests(id,repository_id,number,author_id,title,body,base_branch,head_branch,draft) SELECT $1,$2,COALESCE(MAX(number),0)+1,$3,$4,$5,$6,$7,$8 FROM pull_requests WHERE repository_id=$2 RETURNING number,created_at`, p.ID, repo.ID, u.ID, p.Title, p.Body, p.Base, p.Head, p.Draft).Scan(&p.Number, &p.CreatedAt)
	if conflict(err) {
		fail(w, 409, "pull_exists", "An open pull request already exists for these branches.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO pull_subscriptions(pull_request_id,user_id) VALUES($1,$2)`, p.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO pull_events(pull_request_id,actor_id,kind,body) VALUES($1,$2,'opened',$3)`, p.ID, u.ID, p.Title); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.opened',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, p.Number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, p)
}

func (a *App) getPull(w http.ResponseWriter, r *http.Request, repo *Repository) *Pull {
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil {
		fail(w, 404, "not_found", "Pull request not found.")
		return nil
	}
	p, err := scanPull(a.db.QueryRow(r.Context(), `SELECT `+pullColumns+` FROM pull_requests p JOIN users u ON u.id=p.author_id WHERE repository_id=$1 AND number=$2`, repo.ID, number))
	if err != nil {
		fail(w, 404, "not_found", "Pull request not found.")
		return nil
	}
	return &p
}

func (a *App) pull(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	base, e1 := a.git.Resolve(r.Context(), repo.ID, p.Base)
	head, e2 := a.git.Resolve(r.Context(), repo.ID, p.Head)
	diff := ""
	diffError := ""
	diffTruncated := false
	mergeable := false
	if e1 == nil && e2 == nil {
		rangeSpec := base + "..." + head
		if p.State == "merged" && p.MergeSHA != nil {
			rangeSpec = *p.MergeSHA + "^1.." + *p.MergeSHA
		}
		output, err := a.git.Run(r.Context(), repo.ID, nil, "diff", "--no-ext-diff", "--no-textconv", "--stat", "--patch", rangeSpec, "--")
		if err != nil {
			diffError = "Diff is unavailable or exceeds the display limit. Inspect the branches with Git."
		} else {
			diff, diffTruncated = pageLines(r, string(output))
		}
		if p.State == "open" {
			_, err = a.git.Run(r.Context(), repo.ID, nil, "merge-tree", "--write-tree", base, head)
			mergeable = err == nil
		}
	} else {
		diffError = "A branch no longer exists."
	}
	u := a.user(r)
	canReview := u != nil && repo.CanWrite && u.Username != p.Author && p.State == "open"
	respond(w, 200, map[string]any{"pull": p, "head_sha": head, "base_sha": base, "diff": diff, "diff_truncated": diffTruncated, "diff_error": diffError, "mergeable": mergeable, "can_review": canReview})
}

func (a *App) updatePull(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanTriage {
		fail(w, 403, "forbidden", "Repository triage permission is required.")
		return
	}
	if !activeRepository(w, repo) {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	var in struct {
		State string `json:"state"`
		Draft *bool  `json:"draft"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.State == "" && in.Draft == nil {
		fail(w, 422, "validation_failed", "Provide a state or draft flag.")
		return
	}
	if in.State != "" && in.State != "open" && in.State != "closed" {
		fail(w, 422, "validation_failed", "State must be open or closed.")
		return
	}
	if p.State == "merged" || p.State == "merging" {
		fail(w, 409, "pull_not_changeable", "A merged or merging pull request cannot be closed or reopened.")
		return
	}
	u := a.user(r)
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if in.State != "" {
		if _, err = tx.Exec(r.Context(), `UPDATE pull_requests SET state=$1 WHERE id=$2`, in.State, p.ID); err != nil {
			serverError(w, err)
			return
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,$2,$3)`, u.ID, "pull."+in.State, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, p.Number)); err != nil {
			serverError(w, err)
			return
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO pull_events(pull_request_id,actor_id,kind) VALUES($1,$2,$3)`, p.ID, u.ID, in.State); err != nil {
			serverError(w, err)
			return
		}
		if p.State != in.State {
			kind := "pull_closed"
			if in.State == "open" {
				kind = "pull_reopened"
			}
			if err = notifyPull(r.Context(), tx, p.ID, u.ID, kind); err != nil {
				serverError(w, err)
				return
			}
		}
		p.State = in.State
	}
	if in.Draft != nil {
		if _, err = tx.Exec(r.Context(), `UPDATE pull_requests SET draft=$1 WHERE id=$2`, *in.Draft, p.ID); err != nil {
			serverError(w, err)
			return
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO pull_events(pull_request_id,actor_id,kind,body) VALUES($1,$2,'draft',$3)`, p.ID, u.ID, strconv.FormatBool(*in.Draft)); err != nil {
			serverError(w, err)
			return
		}
		p.Draft = *in.Draft
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, p)
}

func (a *App) pullComments(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT c.id,c.body,u.username,c.created_at FROM pull_comments c JOIN users u ON u.id=c.author_id WHERE c.pull_request_id=$1 ORDER BY c.created_at,c.id LIMIT 200`, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	comments := []PullComment{}
	for rows.Next() {
		var comment PullComment
		if err = rows.Scan(&comment.ID, &comment.Body, &comment.Author, &comment.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		comments = append(comments, comment)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, comments)
}

func (a *App) createPullComment(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	u := a.requireUser(w, r)
	if u == nil || !activeRepository(w, repo) {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" || len(in.Body) > 10000 {
		fail(w, 422, "validation_failed", "A comment between 1 and 10,000 characters is required.")
		return
	}
	comment := PullComment{ID: auth.ID(), Body: in.Body, Author: u.Username}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	err = tx.QueryRow(r.Context(), `INSERT INTO pull_comments(id,pull_request_id,author_id,body) VALUES($1,$2,$3,$4) RETURNING created_at`, comment.ID, p.ID, u.ID, comment.Body).Scan(&comment.CreatedAt)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO pull_subscriptions(pull_request_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, p.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err = notifyPull(r.Context(), tx, p.ID, u.ID, "pull_comment"); err != nil {
		serverError(w, err)
		return
	}
	if err = a.noteMentions(r.Context(), tx, repo, u, comment.Body, "", p.ID, comment.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO pull_events(pull_request_id,actor_id,kind,body) VALUES($1,$2,'comment',$3)`, p.ID, u.ID, comment.Body); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.commented',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, p.Number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, comment)
}

func (a *App) pullReviews(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	currentHead, _ := a.git.Resolve(r.Context(), repo.ID, p.Head)
	rows, err := a.db.Query(r.Context(), `SELECT rv.id,rv.state,rv.body,u.username,rv.head_sha,rv.dismissed_at IS NOT NULL,rv.dismissal_reason,rv.created_at FROM pull_reviews rv JOIN users u ON u.id=rv.reviewer_id WHERE rv.pull_request_id=$1 ORDER BY rv.created_at,rv.id LIMIT 200`, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	reviews := []PullReview{}
	for rows.Next() {
		var review PullReview
		if err = rows.Scan(&review.ID, &review.State, &review.Body, &review.Reviewer, &review.HeadSHA, &review.Dismissed, &review.DismissalReason, &review.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		review.Stale = currentHead == "" || review.HeadSHA != currentHead
		reviews = append(reviews, review)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, reviews)
}

func (a *App) createPullReview(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, true)
	if repo == nil {
		return
	}
	u := a.user(r)
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	if p.Author == u.Username {
		fail(w, 403, "self_review", "Authors cannot formally review their own unite request.")
		return
	}
	if p.State != "open" {
		fail(w, 409, "not_open", "Only open unite requests can be reviewed.")
		return
	}
	var in struct {
		State   string `json:"state"`
		Body    string `json:"body"`
		HeadSHA string `json:"head_sha"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	if (in.State != "approved" && in.State != "changes_requested" && in.State != "commented") || len(in.Body) > 10000 || len(in.HeadSHA) != 40 {
		fail(w, 422, "validation_failed", "Choose approve, request changes, or comment, with a body up to 10,000 characters.")
		return
	}
	if in.State != "approved" && in.Body == "" {
		fail(w, 422, "review_body_required", "A review comment is required when requesting changes or commenting.")
		return
	}
	currentHead, err := a.git.Resolve(r.Context(), repo.ID, p.Head)
	if err != nil || currentHead != in.HeadSHA {
		fail(w, 409, "stale_review", "The head branch changed. Refresh before reviewing the latest code.")
		return
	}
	review := PullReview{ID: auth.ID(), State: in.State, Body: in.Body, Reviewer: u.Username, HeadSHA: currentHead}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	err = tx.QueryRow(r.Context(), `INSERT INTO pull_reviews(id,pull_request_id,reviewer_id,state,body,head_sha) VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at`, review.ID, p.ID, u.ID, review.State, review.Body, review.HeadSHA).Scan(&review.CreatedAt)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO pull_subscriptions(pull_request_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, p.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err = notifyPull(r.Context(), tx, p.ID, u.ID, "pull_review"); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO pull_events(pull_request_id,actor_id,kind,body) VALUES($1,$2,$3,$4)`, p.ID, u.ID, "review."+review.State, review.Body); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,$2,$3)`, u.ID, "pull.reviewed."+review.State, fmt.Sprintf("%s/%s#%d@%s", repo.Owner, repo.Name, p.Number, currentHead)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, review)
}

func (a *App) mergePull(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, true)
	if repo == nil {
		return
	}
	u := a.user(r)
	var in struct {
		HeadSHA      string `json:"head_sha"`
		BaseSHA      string `json:"base_sha"`
		Method       string `json:"method"`
		DeleteBranch bool   `json:"delete_branch"`
	}
	if !decode(w, r, &in) {
		return
	}
	// A session advisory lock serializes merge attempts across API processes.
	conn, err := a.db.Acquire(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer conn.Release()
	var locked bool
	if err = conn.QueryRow(r.Context(), `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, repo.ID).Scan(&locked); err != nil {
		serverError(w, err)
		return
	}
	if !locked {
		fail(w, 409, "merge_busy", "Another merge is running. Refresh and retry.")
		return
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, repo.ID); err != nil {
			_ = conn.Conn().Close(ctx)
		}
	}()
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	if p.State == "merged" {
		respond(w, 200, p)
		return
	}
	if p.State == "merging" && p.MergeSHA != nil && p.ExpectedBase != nil {
		// Recover an interrupted Git/database handoff without producing a second merge.
		current, e := a.git.Resolve(r.Context(), repo.ID, p.Base)
		if e != nil {
			fail(w, 409, "merge_recovery", "Base branch is unavailable; restore it before retrying.")
			return
		}
		if _, e = a.git.Run(r.Context(), repo.ID, nil, "merge-base", "--is-ancestor", *p.MergeSHA, current); e != nil {
			if current != *p.ExpectedBase {
				fail(w, 409, "merge_recovery", "Base changed during an interrupted merge. Administrative recovery is required.")
				return
			}
			if _, e = a.git.Run(r.Context(), repo.ID, nil, "update-ref", "refs/heads/"+p.Base, *p.MergeSHA, *p.ExpectedBase); e != nil {
				fail(w, 409, "merge_recovery", "The merge could not be resumed. Refresh and retry.")
				return
			}
		}
		if err := a.finishMerge(r.Context(), conn, *p, *repo, *u, "", ""); err != nil {
			serverError(w, err)
			return
		}
		p.State = "merged"
		respond(w, 200, p)
		return
	}
	if p.State != "open" {
		fail(w, 409, "not_open", "This pull request is not open.")
		return
	}
	method := in.Method
	if method == "" {
		method = "merge"
	}
	if method != "merge" && method != "squash" && method != "rebase" {
		fail(w, 422, "validation_failed", "Merge method must be merge, squash, or rebase.")
		return
	}
	if p.Draft {
		fail(w, 409, "draft_pull", "Mark the unite request ready before merging it.")
		return
	}
	base, e1 := a.git.Resolve(r.Context(), repo.ID, p.Base)
	head, e2 := a.git.Resolve(r.Context(), repo.ID, p.Head)
	if e1 != nil || e2 != nil || head != in.HeadSHA || base != in.BaseSHA {
		fail(w, 409, "stale_branches", "A branch changed. Refresh the pull request before merging.")
		return
	}
	if !a.enforceReviewRule(w, r, repo, p, base, head) {
		return
	}
	// Collect commit messages now: after the base moves, base..head is empty
	// for merge commits, and the head branch may be deleted.
	messages, err := a.git.Run(r.Context(), repo.ID, nil, "log", "--max-count=1000", "--format=%s%n%b%n", base+".."+head)
	if err != nil {
		serverError(w, err)
		return
	}
	var sha string
	if method == "rebase" {
		sha, err = a.git.Rebase(r.Context(), repo.ID, base, head, u.DisplayName, u.Username+"@users.gitown.local")
		if err != nil {
			fail(w, 409, "merge_conflict", "The rebase has conflicts. Resolve them locally and push the branch again.")
			return
		}
	} else {
		tree, treeErr := a.git.Run(r.Context(), repo.ID, nil, "merge-tree", "--write-tree", base, head)
		if treeErr != nil {
			fail(w, 409, "merge_conflict", "Resolve merge conflicts locally and push the branch again.")
			return
		}
		treeSHA := strings.SplitN(strings.TrimSpace(string(tree)), "\n", 2)[0]
		message := fmt.Sprintf("Merge pull request #%d: %s", p.Number, p.Title)
		parents := []string{base, head}
		if method == "squash" {
			message = fmt.Sprintf("%s (#%d)", p.Title, p.Number)
			if strings.TrimSpace(p.Body) != "" {
				message += "\n\n" + p.Body
			}
			parents = []string{base}
		}
		sha, err = a.git.Commit(r.Context(), repo.ID, treeSHA, parents, message, u.DisplayName, u.Username+"@users.gitown.local")
	}
	if err != nil {
		serverError(w, err)
		return
	}
	// Persist the intent before touching refs, so a failed final DB write is recoverable.
	_, err = conn.Exec(r.Context(), `UPDATE pull_requests SET state='merging',merge_sha=$1,expected_base_sha=$2,merge_method=$3 WHERE id=$4`, sha, base, method, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	p.MergeSHA = &sha
	p.ExpectedBase = &base
	p.MergeMethod = &method
	if _, err = a.git.Run(r.Context(), repo.ID, nil, "update-ref", "refs/heads/"+p.Base, sha, base); err != nil {
		fail(w, 409, "merge_recovery", "The branch moved or the merge was interrupted. Refresh before retrying.")
		return
	}
	deleted, deleteProblem := false, ""
	if in.DeleteBranch {
		deleted, deleteProblem = a.deleteMergedBranch(r.Context(), repo, p, head)
	}
	if err = a.finishMerge(r.Context(), conn, *p, *repo, *u, method, string(messages)); err != nil {
		serverError(w, err)
		return
	}
	if deleted {
		a.recordRefEvents(r.Context(), repo.ID, u.ID, []refUpdate{{Old: head, New: strings.Repeat("0", 40), Ref: "refs/heads/" + p.Head}}, "api")
	}
	p.State = "merged"
	if in.DeleteBranch {
		p.BranchDeleted = &deleted
		p.BranchDeleteError = deleteProblem
	}
	respond(w, 200, p)
}

// deleteMergedBranch removes the source branch after a merge unless it is the
// default branch, the base, or protected by a rule that forbids deletion.
func (a *App) deleteMergedBranch(ctx context.Context, repo *Repository, p *Pull, head string) (bool, string) {
	if p.Head == p.Base || p.Head == repo.DefaultBranch {
		return false, "The default branch and the base branch are never deleted."
	}
	policies, err := a.branchPolicies(ctx, repo.ID)
	if err != nil {
		return false, "Branch rules could not be checked, so the branch was kept."
	}
	if policy, ok := policies[p.Head]; ok && !policy.AllowDeletion {
		return false, "The branch is protected and its rule does not allow deletion."
	}
	if _, err = a.git.Run(ctx, repo.ID, nil, "update-ref", "-d", "refs/heads/"+p.Head, head); err != nil {
		return false, "The branch changed after the merge, so it was kept."
	}
	return true, ""
}

type beginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

func (a *App) finishMerge(ctx context.Context, db beginner, p Pull, repo Repository, u User, method, messages string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE pull_requests SET state='merged',merged_at=now(),merge_method=COALESCE(NULLIF($2,''),merge_method) WHERE id=$1`, p.ID, method); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO pull_events(pull_request_id,actor_id,kind,body) VALUES($1,$2,'merged',$3)`, p.ID, u.ID, method); err != nil {
		return err
	}
	if err = a.closeReferencedIssues(ctx, tx, &repo, &p, &u, messages); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.merged',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, p.Number)); err != nil {
		return err
	}
	if err = notifyPull(ctx, tx, p.ID, u.ID, "pull_merged"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
