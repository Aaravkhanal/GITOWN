package app

import (
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
)

type BoardItem struct {
	IssueID string `json:"issue_id"`
	Number  int    `json:"number"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Author  string `json:"author"`
}

func (a *App) board(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT i.id,i.number,i.title,i.state,
		CASE WHEN i.state='closed' THEN 'done' ELSE COALESCE(NULLIF(bs.status,'done'),'todo') END,
		u.username FROM issues i JOIN users u ON u.id=i.author_id
		LEFT JOIN issue_board_status bs ON bs.issue_id=i.id
		WHERE i.repository_id=$1 ORDER BY i.number DESC LIMIT 100`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []BoardItem{}
	for rows.Next() {
		var item BoardItem
		if err = rows.Scan(&item.IssueID, &item.Number, &item.Title, &item.State, &item.Status, &item.Author); err != nil {
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

func (a *App) updateBoardItem(w http.ResponseWriter, r *http.Request) {
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
		Status string `json:"status"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Status != "todo" && in.Status != "progress" && in.Status != "done" {
		fail(w, 422, "validation_failed", "Board status must be todo, progress, or done.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var issueID, previousState string
	if err = tx.QueryRow(r.Context(), `SELECT id,state FROM issues WHERE repository_id=$1 AND number=$2 FOR UPDATE`, repo.ID, number).Scan(&issueID, &previousState); err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Issue not found.")
		return
	} else if err != nil {
		serverError(w, err)
		return
	}
	state := "open"
	if in.Status == "done" {
		state = "closed"
	}
	if _, err = tx.Exec(r.Context(), `UPDATE issues SET state=$1 WHERE id=$2`, state, issueID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO issue_board_status(issue_id,status) VALUES($1,$2)
		ON CONFLICT (issue_id) DO UPDATE SET status=EXCLUDED.status,updated_at=now()`, issueID, in.Status); err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	if previousState != state {
		kind := "issue_closed"
		if state == "open" {
			kind = "issue_reopened"
		}
		if err = notifyIssue(r.Context(), tx, issueID, u.ID, kind); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.board_status_updated',$2)`, u.ID, fmt.Sprintf("%s/%s#%d:%s", repo.Owner, repo.Name, number, in.Status)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"status": in.Status, "state": state})
}
