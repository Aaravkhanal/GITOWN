package app

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

func (a *App) updateIssuePlanning(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil || !repo.CanTriage || !activeRepository(w, repo) {
		if repo != nil && !repo.CanTriage {
			fail(w, 403, "forbidden", "Repository triage permission is required.")
		}
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var in struct {
		Pinned         *bool   `json:"pinned"`
		Priority       *string `json:"priority"`
		Estimate       *int    `json:"estimate"`
		DueDate        *string `json:"due_date"`
		Iteration      *string `json:"iteration"`
		DuplicateOf    *int    `json:"duplicate_of"`
		ClearDuplicate bool    `json:"clear_duplicate"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Priority != nil && *in.Priority != "none" && *in.Priority != "low" && *in.Priority != "medium" && *in.Priority != "high" && *in.Priority != "urgent" {
		fail(w, 422, "validation_failed", "Priority must be none, low, medium, high, or urgent.")
		return
	}
	if in.Estimate != nil && (*in.Estimate < 0 || *in.Estimate > 100) {
		fail(w, 422, "validation_failed", "Estimate must be between 0 and 100.")
		return
	}
	if in.Iteration != nil && len(*in.Iteration) > 40 {
		fail(w, 422, "validation_failed", "Iteration names can be up to 40 characters.")
		return
	}
	if in.DueDate != nil && *in.DueDate != "" {
		if _, err := time.Parse("2006-01-02", *in.DueDate); err != nil {
			fail(w, 422, "validation_failed", "Due date must be YYYY-MM-DD.")
			return
		}
	}
	if in.DuplicateOf != nil && *in.DuplicateOf == number {
		fail(w, 422, "validation_failed", "An issue cannot be a duplicate of itself.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var issueID string
	if err = tx.QueryRow(r.Context(), `SELECT id FROM issues WHERE repository_id=$1 AND number=$2 FOR UPDATE`, repo.ID, number).Scan(&issueID); err != nil {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	if in.Pinned != nil {
		if _, err = tx.Exec(r.Context(), `UPDATE issues SET pinned=$1 WHERE id=$2`, *in.Pinned, issueID); err != nil {
			serverError(w, err)
			return
		}
	}
	if in.Priority != nil {
		if _, err = tx.Exec(r.Context(), `UPDATE issues SET priority=$1 WHERE id=$2`, *in.Priority, issueID); err != nil {
			serverError(w, err)
			return
		}
	}
	if in.Estimate != nil {
		if _, err = tx.Exec(r.Context(), `UPDATE issues SET estimate=$1 WHERE id=$2`, *in.Estimate, issueID); err != nil {
			serverError(w, err)
			return
		}
	}
	if in.DueDate != nil {
		var due any
		if *in.DueDate != "" {
			due = *in.DueDate
		}
		if _, err = tx.Exec(r.Context(), `UPDATE issues SET due_date=$1 WHERE id=$2`, due, issueID); err != nil {
			serverError(w, err)
			return
		}
	}
	if in.Iteration != nil {
		if _, err = tx.Exec(r.Context(), `UPDATE issues SET iteration=$1 WHERE id=$2`, strings.TrimSpace(*in.Iteration), issueID); err != nil {
			serverError(w, err)
			return
		}
	}
	if in.ClearDuplicate {
		if _, err = tx.Exec(r.Context(), `UPDATE issues SET duplicate_of=NULL WHERE id=$1`, issueID); err != nil {
			serverError(w, err)
			return
		}
	}
	if in.DuplicateOf != nil {
		var duplicateID string
		err = tx.QueryRow(r.Context(), `SELECT id FROM issues WHERE repository_id=$1 AND number=$2`, repo.ID, *in.DuplicateOf).Scan(&duplicateID)
		if err != nil {
			fail(w, 422, "validation_failed", "The duplicate target must be an issue in this repository.")
			return
		}
		if _, err = tx.Exec(r.Context(), `UPDATE issues SET duplicate_of=$1 WHERE id=$2`, duplicateID, issueID); err != nil {
			serverError(w, err)
			return
		}
	}
	u := a.user(r)
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.planning_updated',$2)`, u.ID, repo.Owner+"/"+repo.Name+"#"+strconv.Itoa(number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"ok": true})
}

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
	var previous, authorID string
	err = tx.QueryRow(r.Context(), `SELECT c.body,c.author_id FROM issue_comments c JOIN issues i ON i.id=c.issue_id WHERE c.id=$1 AND i.repository_id=$2 AND i.number=$3 FOR UPDATE`, r.PathValue("id"), repo.ID, number).Scan(&previous, &authorID)
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
	if err = a.noteIssueCommentMentions(r.Context(), tx, repo, u, r.PathValue("id"), in.Body); err != nil {
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

func (a *App) issueReferences(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var body string
	err := a.db.QueryRow(r.Context(), `SELECT body FROM issues WHERE repository_id=$1 AND number=$2`, repo.ID, number).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT c.body FROM issue_comments c JOIN issues i ON i.id=c.issue_id WHERE i.repository_id=$1 AND i.number=$2`, repo.ID, number)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	text := body
	for rows.Next() {
		var comment string
		if err = rows.Scan(&comment); err != nil {
			serverError(w, err)
			return
		}
		text += "\n" + comment
	}
	seen := map[int]bool{}
	numbers := []int{}
	for _, match := range regexpReference.FindAllStringSubmatch(text, 50) {
		n, _ := strconv.Atoi(match[1])
		if n > 0 && n != number && !seen[n] {
			seen[n] = true
			numbers = append(numbers, n)
		}
	}
	items := []map[string]any{}
	for _, n := range numbers {
		var title, state string
		err = a.db.QueryRow(r.Context(), `SELECT title,state FROM issues WHERE repository_id=$1 AND number=$2`, repo.ID, n).Scan(&title, &state)
		if err == nil {
			items = append(items, map[string]any{"number": n, "title": title, "state": state})
		}
	}
	respond(w, 200, items)
}

var regexpReference = regexp.MustCompile(`(?:^|[^A-Za-z0-9])#([1-9][0-9]{0,8})`)

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

func (a *App) transferIssue(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil || !activeRepository(w, repo) {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var in struct {
		Repository string `json:"repository"`
	}
	if !decode(w, r, &in) || !repoSlug.MatchString(in.Repository) || in.Repository == repo.Name {
		fail(w, 422, "validation_failed", "Choose another repository you own.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var targetID string
	err = tx.QueryRow(r.Context(), `SELECT id FROM repositories WHERE owner_id=$1 AND name=$2 AND deleted_at IS NULL AND archived_at IS NULL`, repo.OwnerID, in.Repository).Scan(&targetID)
	if err != nil {
		fail(w, 422, "validation_failed", "Choose an active repository you own.")
		return
	}
	var issueID string
	err = tx.QueryRow(r.Context(), `SELECT id FROM issues WHERE repository_id=$1 AND number=$2 FOR UPDATE`, repo.ID, number).Scan(&issueID)
	if err != nil {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	var newNumber int
	if err = tx.QueryRow(r.Context(), `UPDATE issues SET repository_id=$1,number=(SELECT COALESCE(MAX(number),0)+1 FROM issues WHERE repository_id=$1),milestone_id=NULL,duplicate_of=NULL WHERE id=$2 RETURNING number`, targetID, issueID).Scan(&newNumber); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM issue_labels WHERE issue_id=$1`, issueID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM issue_dependencies WHERE issue_id=$1 OR blocker_id=$1`, issueID); err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.transferred',$2)`, u.ID, repo.Owner+"/"+repo.Name+"#"+strconv.Itoa(number)+"->"+in.Repository+"#"+strconv.Itoa(newNumber)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"repository": in.Repository, "number": newNumber})
}

func (a *App) updatePresentation(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil || !activeRepository(w, repo) {
		return
	}
	var in struct {
		Homepage    string        `json:"homepage"`
		Stack       string        `json:"stack"`
		Screenshots *[]Screenshot `json:"screenshots"`
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
	if in.Screenshots != nil {
		if len(*in.Screenshots) > 6 {
			fail(w, 422, "validation_failed", "Add up to six screenshots.")
			return
		}
		for index, shot := range *in.Screenshots {
			shot.URL = strings.TrimSpace(shot.URL)
			shot.Caption = strings.TrimSpace(shot.Caption)
			if shot.URL == "" || len(shot.URL) > 300 || !validProfileWebsite(shot.URL) || len(shot.Caption) > 140 {
				fail(w, 422, "validation_failed", "Each screenshot needs an HTTPS image address and a caption up to 140 characters.")
				return
			}
			(*in.Screenshots)[index] = shot
		}
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `UPDATE repositories SET homepage=$1,stack=$2 WHERE id=$3`, in.Homepage, in.Stack, repo.ID); err != nil {
		serverError(w, err)
		return
	}
	if in.Screenshots != nil {
		if _, err = tx.Exec(r.Context(), `DELETE FROM repository_screenshots WHERE repository_id=$1`, repo.ID); err != nil {
			serverError(w, err)
			return
		}
		for index, shot := range *in.Screenshots {
			if _, err = tx.Exec(r.Context(), `INSERT INTO repository_screenshots(repository_id,position,url,caption) VALUES($1,$2,$3,$4)`, repo.ID, index+1, shot.URL, shot.Caption); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	u := a.user(r)
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.presentation',$2)`, u.ID, repo.Owner+"/"+repo.Name); err != nil {
		serverError(w, err)
		return
	}
	shots, err := a.repositoryScreenshots(r.Context(), tx, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"homepage": in.Homepage, "stack": in.Stack, "screenshots": shots})
}
