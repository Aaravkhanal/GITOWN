package app

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

func (a *App) pullAssignees(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `WITH eligible AS (
		SELECT u.id,u.username,u.display_name FROM repositories r JOIN users u ON u.id=r.owner_id WHERE r.id=$1
		UNION
		SELECT u.id,u.username,u.display_name FROM repository_members rm JOIN users u ON u.id=rm.user_id WHERE rm.repository_id=$1
	) SELECT e.username,e.display_name,(pa.user_id IS NOT NULL) FROM eligible e
	LEFT JOIN pull_assignees pa ON pa.pull_request_id=$2 AND pa.user_id=e.id
	ORDER BY e.username`, repo.ID, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	result := IssueAssignees{Assigned: []PublicUser{}, Available: []PublicUser{}}
	for rows.Next() {
		var user PublicUser
		var assigned bool
		if err = rows.Scan(&user.Username, &user.DisplayName, &assigned); err != nil {
			serverError(w, err)
			return
		}
		if assigned {
			result.Assigned = append(result.Assigned, user)
		} else {
			result.Available = append(result.Available, user)
		}
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, result)
}

func (a *App) updatePullAssignees(w http.ResponseWriter, r *http.Request) {
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
		Usernames []string `json:"usernames"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Usernames) > 10 {
		fail(w, 422, "validation_failed", "A Unite request can have at most 10 assignees.")
		return
	}
	unique := map[string]bool{}
	for _, username := range in.Usernames {
		if !slug.MatchString(username) || unique[username] {
			fail(w, 422, "validation_failed", "Assignees must be unique repository members.")
			return
		}
		unique[username] = true
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	previous := map[string]bool{}
	prows, err := tx.Query(r.Context(), `SELECT user_id::text FROM pull_assignees WHERE pull_request_id=$1`, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	for prows.Next() {
		var id string
		if err = prows.Scan(&id); err != nil {
			prows.Close()
			serverError(w, err)
			return
		}
		previous[id] = true
	}
	prows.Close()
	if err = prows.Err(); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM pull_assignees WHERE pull_request_id=$1`, p.ID); err != nil {
		serverError(w, err)
		return
	}
	for username := range unique {
		result, insertErr := tx.Exec(r.Context(), `INSERT INTO pull_assignees(pull_request_id,user_id)
			SELECT $1,u.id FROM users u WHERE u.username=$2 AND (
				u.id=(SELECT owner_id FROM repositories WHERE id=$3) OR
				EXISTS(SELECT 1 FROM repository_members rm WHERE rm.repository_id=$3 AND rm.user_id=u.id)
			)`, p.ID, username, repo.ID)
		if insertErr != nil {
			serverError(w, insertErr)
			return
		}
		if result.RowsAffected() != 1 {
			fail(w, 422, "validation_failed", "Assignees must be repository members.")
			return
		}
	}
	u := a.user(r)
	crows, err := tx.Query(r.Context(), `SELECT user_id::text FROM pull_assignees WHERE pull_request_id=$1`, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	var fresh []string
	for crows.Next() {
		var id string
		if err = crows.Scan(&id); err != nil {
			crows.Close()
			serverError(w, err)
			return
		}
		if !previous[id] {
			fresh = append(fresh, id)
		}
	}
	crows.Close()
	if err = crows.Err(); err != nil {
		serverError(w, err)
		return
	}
	for _, id := range fresh {
		if err = notifyDirect(r.Context(), tx, id, u.ID, repo.ID, "", p.ID, "assignment", "assignment:pull:"+p.ID+":"+id); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.assignees_updated',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, p.Number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.pullAssignees(w, r)
}

func (a *App) pullLabels(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT l.id,l.name,l.color,l.description,l.created_at FROM pull_labels pl JOIN labels l ON l.id=pl.label_id WHERE pl.pull_request_id=$1 ORDER BY l.name`, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []Label{}
	for rows.Next() {
		var label Label
		if err = rows.Scan(&label.ID, &label.Name, &label.Color, &label.Description, &label.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, label)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, items)
}

func (a *App) updatePullLabels(w http.ResponseWriter, r *http.Request) {
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
		LabelIDs []string `json:"label_ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.LabelIDs) > 20 {
		fail(w, 422, "validation_failed", "Choose up to 20 labels.")
		return
	}
	seen := map[string]bool{}
	for _, id := range in.LabelIDs {
		if !milestoneIDPattern.MatchString(id) || seen[id] {
			fail(w, 422, "validation_failed", "Choose distinct labels from this repository.")
			return
		}
		seen[id] = true
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `DELETE FROM pull_labels WHERE pull_request_id=$1`, p.ID); err != nil {
		serverError(w, err)
		return
	}
	for id := range seen {
		result, insertErr := tx.Exec(r.Context(), `INSERT INTO pull_labels(pull_request_id,label_id) SELECT $1,id FROM labels WHERE id=$2 AND repository_id=$3`, p.ID, id, repo.ID)
		if insertErr != nil {
			serverError(w, insertErr)
			return
		}
		if result.RowsAffected() != 1 {
			fail(w, 422, "validation_failed", "Choose labels from this repository.")
			return
		}
	}
	u := a.user(r)
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.labels_updated',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, p.Number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.pullLabels(w, r)
}

type PullLink struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Closes bool   `json:"closes"`
}

func (a *App) pullLinks(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT i.number,i.title,i.state,l.closes FROM pull_issue_links l JOIN issues i ON i.id=l.issue_id WHERE l.pull_request_id=$1 ORDER BY i.number`, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []PullLink{}
	for rows.Next() {
		var item PullLink
		if err = rows.Scan(&item.Number, &item.Title, &item.State, &item.Closes); err != nil {
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

func (a *App) updatePullLinks(w http.ResponseWriter, r *http.Request) {
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
		Links []struct {
			Number int  `json:"number"`
			Closes bool `json:"closes"`
		} `json:"links"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Links) > 20 {
		fail(w, 422, "validation_failed", "Link up to 20 issues.")
		return
	}
	seen := map[int]bool{}
	for _, link := range in.Links {
		if link.Number < 1 || seen[link.Number] {
			fail(w, 422, "validation_failed", "Link distinct issues in this repository.")
			return
		}
		seen[link.Number] = true
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `DELETE FROM pull_issue_links WHERE pull_request_id=$1`, p.ID); err != nil {
		serverError(w, err)
		return
	}
	for _, link := range in.Links {
		result, insertErr := tx.Exec(r.Context(), `INSERT INTO pull_issue_links(pull_request_id,issue_id,closes) SELECT $1,id,$2 FROM issues WHERE repository_id=$3 AND number=$4`, p.ID, link.Closes, repo.ID, link.Number)
		if insertErr != nil {
			serverError(w, insertErr)
			return
		}
		if result.RowsAffected() != 1 {
			fail(w, 422, "validation_failed", "Link issues from this repository.")
			return
		}
	}
	u := a.user(r)
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.links_updated',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, p.Number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.pullLinks(w, r)
}

func (a *App) createThreadReply(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanComment {
		fail(w, 403, "forbidden", "Sign in to reply.")
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
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" || len(in.Body) > 10000 {
		fail(w, 422, "validation_failed", "A reply between 1 and 10,000 characters is required.")
		return
	}
	u := a.user(r)
	id := auth.ID()
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var threadID string
	err = tx.QueryRow(r.Context(), `SELECT id FROM pull_review_threads WHERE id=$1 AND pull_request_id=$2`, r.PathValue("id"), p.ID).Scan(&threadID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Conversation not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO pull_thread_replies(id,thread_id,author_id,body) VALUES($1,$2,$3,$4)`, id, threadID, u.ID, in.Body); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO pull_subscriptions(pull_request_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, p.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err = a.noteMentions(r.Context(), tx, repo, u, in.Body, "", p.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if err = notifyPullEvent(r.Context(), tx, p.ID, u.ID, "pull_comment", "", in.Body); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.thread_replied',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, p.Number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]string{"id": id, "body": in.Body, "author": u.Username})
}

func (a *App) editPullComment(w http.ResponseWriter, r *http.Request) {
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
	err = tx.QueryRow(r.Context(), `SELECT body,author_id FROM pull_comments WHERE id=$1 AND pull_request_id=$2 FOR UPDATE`, r.PathValue("id"), p.ID).Scan(&previous, &authorID)
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
	if _, err = tx.Exec(r.Context(), `INSERT INTO pull_comment_revisions(id,comment_id,body) VALUES($1,$2,$3)`, auth.ID(), r.PathValue("id"), previous); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE pull_comments SET body=$1,updated_at=now() WHERE id=$2`, in.Body, r.PathValue("id")); err != nil {
		serverError(w, err)
		return
	}
	if err = a.noteMentions(r.Context(), tx, repo, u, in.Body, "", p.ID, r.PathValue("id")); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'pull.comment_edited',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, p.Number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"id": r.PathValue("id"), "body": in.Body})
}

func (a *App) pullCommentHistory(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT body,edited_at FROM pull_comment_revisions WHERE comment_id=$1 AND EXISTS(SELECT 1 FROM pull_comments c WHERE c.id=$1 AND c.pull_request_id=$2) ORDER BY edited_at`, r.PathValue("id"), p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var body string
		var edited any
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
		_ = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM pull_comments WHERE id=$1 AND pull_request_id=$2)`, r.PathValue("id"), p.ID).Scan(&exists)
		if !exists {
			fail(w, 404, "not_found", "Comment not found.")
			return
		}
	}
	respond(w, 200, items)
}
