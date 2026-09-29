package app

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
)

type LinkedIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
}

type IssueDependencies struct {
	BlockedBy []LinkedIssue `json:"blocked_by"`
	Blocks    []LinkedIssue `json:"blocks"`
}

func (a *App) issueDependencies(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var issueID string
	if err := a.db.QueryRow(r.Context(), `SELECT id FROM issues WHERE repository_id=$1 AND number=$2`, repo.ID, number).Scan(&issueID); err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Issue not found.")
		return
	} else if err != nil {
		serverError(w, err)
		return
	}
	state := IssueDependencies{BlockedBy: []LinkedIssue{}, Blocks: []LinkedIssue{}}
	rows, err := a.db.Query(r.Context(), `SELECT i.number,i.title,i.state FROM issue_dependencies d JOIN issues i ON i.id=d.blocker_id WHERE d.issue_id=$1 ORDER BY i.number`, issueID)
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var item LinkedIssue
		if err = rows.Scan(&item.Number, &item.Title, &item.State); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		state.BlockedBy = append(state.BlockedBy, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		serverError(w, err)
		return
	}
	rows, err = a.db.Query(r.Context(), `SELECT i.number,i.title,i.state FROM issue_dependencies d JOIN issues i ON i.id=d.issue_id WHERE d.blocker_id=$1 ORDER BY i.number`, issueID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var item LinkedIssue
		if err = rows.Scan(&item.Number, &item.Title, &item.State); err != nil {
			serverError(w, err)
			return
		}
		state.Blocks = append(state.Blocks, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, state)
}

func (a *App) updateIssueDependencies(w http.ResponseWriter, r *http.Request) {
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
		BlockedBy []int `json:"blocked_by"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.BlockedBy == nil || len(in.BlockedBy) > 20 {
		fail(w, 422, "validation_failed", "Choose up to 20 distinct blocker issues.")
		return
	}
	seen := map[int]bool{}
	for _, blocker := range in.BlockedBy {
		if blocker <= 0 || blocker == number || seen[blocker] {
			fail(w, 422, "validation_failed", "Choose distinct blocker issues in this repository, excluding the issue itself.")
			return
		}
		seen[blocker] = true
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
	var issueID, issueState string
	if err = tx.QueryRow(r.Context(), `SELECT id,state FROM issues WHERE repository_id=$1 AND number=$2`, repo.ID, number).Scan(&issueID, &issueState); err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Issue not found.")
		return
	} else if err != nil {
		serverError(w, err)
		return
	}
	blockerIDs := make([]string, 0, len(in.BlockedBy))
	for _, blockerNumber := range in.BlockedBy {
		var blockerID, blockerState string
		if err = tx.QueryRow(r.Context(), `SELECT id,state FROM issues WHERE repository_id=$1 AND number=$2`, repo.ID, blockerNumber).Scan(&blockerID, &blockerState); err == pgx.ErrNoRows {
			fail(w, 422, "validation_failed", "Blocker issue not found in this repository.")
			return
		} else if err != nil {
			serverError(w, err)
			return
		}
		if issueState == "closed" && blockerState == "open" {
			fail(w, 409, "open_blockers", "Reopen this issue before adding an open blocker.")
			return
		}
		var cyclic bool
		if err = tx.QueryRow(r.Context(), `WITH RECURSIVE chain(id) AS (
			SELECT blocker_id FROM issue_dependencies WHERE issue_id=$1
			UNION SELECT d.blocker_id FROM issue_dependencies d JOIN chain c ON d.issue_id=c.id
		) SELECT EXISTS(SELECT 1 FROM chain WHERE id=$2)`, blockerID, issueID).Scan(&cyclic); err != nil {
			serverError(w, err)
			return
		}
		if cyclic {
			fail(w, 409, "dependency_cycle", "This dependency would create a cycle.")
			return
		}
		blockerIDs = append(blockerIDs, blockerID)
	}
	if _, err = tx.Exec(r.Context(), `DELETE FROM issue_dependencies WHERE issue_id=$1`, issueID); err != nil {
		serverError(w, err)
		return
	}
	for _, blockerID := range blockerIDs {
		if _, err = tx.Exec(r.Context(), `INSERT INTO issue_dependencies(repository_id,issue_id,blocker_id) VALUES($1,$2,$3)`, repo.ID, issueID, blockerID); err != nil {
			serverError(w, err)
			return
		}
	}
	u := a.user(r)
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.dependencies_updated',$2)`, u.ID, repo.Owner+"/"+repo.Name+"#"+r.PathValue("number")); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.issueDependencies(w, r)
}

func hasOpenBlockers(ctx context.Context, tx pgx.Tx, issueID string) (bool, error) {
	var blocked bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_dependencies d JOIN issues blocker ON blocker.id=d.blocker_id WHERE d.issue_id=$1 AND blocker.state='open')`, issueID).Scan(&blocked)
	return blocked, err
}
