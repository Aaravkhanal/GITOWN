package app

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

func (a *App) editIssueComment(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil || !activeRepository(w, repo) {
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
	var previous, authorID, issueID string
	err = tx.QueryRow(r.Context(), `SELECT c.body,c.author_id,c.issue_id FROM issue_comments c JOIN issues i ON i.id=c.issue_id WHERE c.id=$1 AND i.repository_id=$2 AND i.number=$3 FOR UPDATE OF c`, r.PathValue("id"), repo.ID, number).Scan(&previous, &authorID, &issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Comment not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if authorID != u.ID {
		fail(w, 403, "forbidden", "Only the comment author can edit it.")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO issue_comment_revisions(id,comment_id,body) VALUES($1,$2,$3)`, auth.ID(), r.PathValue("id"), previous); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE issue_comments SET body=$1,updated_at=now() WHERE id=$2`, in.Body, r.PathValue("id")); err != nil {
		serverError(w, err)
		return
	}
	if err = a.recordReferences(r.Context(), tx, repo, u.ID, "issue_comment", r.PathValue("id"), issueID, "", in.Body); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.comment_edited',$2)`, u.ID, repo.Owner+"/"+repo.Name+"#"+strconv.Itoa(number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"id": r.PathValue("id"), "body": in.Body})
}

func (a *App) issueCommentHistory(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT rv.body,rv.edited_at FROM issue_comment_revisions rv JOIN issue_comments c ON c.id=rv.comment_id JOIN issues i ON i.id=c.issue_id WHERE c.id=$1 AND i.repository_id=$2 AND i.number=$3 ORDER BY rv.edited_at`, r.PathValue("id"), repo.ID, number)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var body string
		var edited time.Time
		if err = rows.Scan(&body, &edited); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"body": body, "edited_at": edited})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	if len(items) == 0 {
		var exists bool
		_ = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM issue_comments c JOIN issues i ON i.id=c.issue_id WHERE c.id=$1 AND i.repository_id=$2 AND i.number=$3)`, r.PathValue("id"), repo.ID, number).Scan(&exists)
		if !exists {
			fail(w, 404, "not_found", "Comment not found.")
			return
		}
	}
	respond(w, 200, items)
}

func (a *App) savedSearches(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,query,created_at FROM saved_searches WHERE user_id=$1 ORDER BY name LIMIT 20`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, query string
		var created time.Time
		if err = rows.Scan(&id, &name, &query, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "query": query, "created_at": created})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, items)
}

func (a *App) createSavedSearch(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Name  string `json:"name"`
		Query string `json:"query"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Query = strings.TrimSpace(in.Query)
	if in.Name == "" || len(in.Name) > 80 || in.Query == "" || len(in.Query) > 200 {
		fail(w, 422, "validation_failed", "Use a name up to 80 characters and a query up to 200 characters.")
		return
	}
	var count int
	if err := a.db.QueryRow(r.Context(), `SELECT count(*)::int FROM saved_searches WHERE user_id=$1`, u.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 20 {
		fail(w, 422, "validation_failed", "Save up to 20 searches.")
		return
	}
	id := auth.ID()
	var created time.Time
	err := a.db.QueryRow(r.Context(), `INSERT INTO saved_searches(id,user_id,name,query) VALUES($1,$2,$3,$4) RETURNING created_at`, id, u.ID, in.Name, in.Query).Scan(&created)
	if conflict(err) {
		fail(w, 409, "search_exists", "You already saved a search with that name.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]any{"id": id, "name": in.Name, "query": in.Query, "created_at": created})
}

func (a *App) deleteSavedSearch(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	result, err := a.db.Exec(r.Context(), `DELETE FROM saved_searches WHERE id=$1 AND user_id=$2`, r.PathValue("id"), u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Saved search not found.")
		return
	}
	respond(w, 200, map[string]bool{"deleted": true})
}

func (a *App) updatePresentation(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil || !activeRepository(w, repo) {
		return
	}
	var in struct {
		Homepage string `json:"homepage"`
		Stack    string `json:"stack"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Homepage = strings.TrimSpace(in.Homepage)
	in.Stack = strings.TrimSpace(in.Stack)
	if len(in.Homepage) > 300 || len(in.Stack) > 200 || (in.Homepage != "" && !validProfileWebsite(in.Homepage)) {
		fail(w, 422, "validation_failed", "Use an optional HTTPS demo link up to 300 characters and a tech stack up to 200 characters.")
		return
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE repositories SET homepage=$1,stack=$2 WHERE id=$3`, in.Homepage, in.Stack, repo.ID); err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.presentation',$2)`, u.ID, repo.Owner+"/"+repo.Name)
	respond(w, 200, map[string]string{"homepage": in.Homepage, "stack": in.Stack})
}
