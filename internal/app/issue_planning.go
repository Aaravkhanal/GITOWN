package app

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

func (a *App) updateIssuePlanning(w http.ResponseWriter, r *http.Request) {
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
		Pinned         *bool   `json:"pinned"`
		Priority       *string `json:"priority"`
		Estimate       *int    `json:"estimate"`
		ClearEstimate  bool    `json:"clear_estimate"`
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
	if in.Estimate != nil && (*in.Estimate < 0 || *in.Estimate > 100 || in.ClearEstimate) {
		fail(w, 422, "validation_failed", "Estimate must be between 0 and 100.")
		return
	}
	if in.Iteration != nil && len(strings.TrimSpace(*in.Iteration)) > 40 {
		fail(w, 422, "validation_failed", "Iteration names can be up to 40 characters.")
		return
	}
	if in.DueDate != nil && *in.DueDate != "" {
		if _, err := time.Parse("2006-01-02", *in.DueDate); err != nil {
			fail(w, 422, "validation_failed", "Due date must be YYYY-MM-DD.")
			return
		}
	}
	if in.DuplicateOf != nil && (*in.DuplicateOf == number || in.ClearDuplicate) {
		fail(w, 422, "validation_failed", "An issue cannot be a duplicate of itself.")
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
	var issueID, state, reason string
	var duplicateOf *string
	err = tx.QueryRow(r.Context(), `SELECT id,state,state_reason,duplicate_of FROM issues WHERE repository_id=$1 AND number=$2 FOR UPDATE`, repo.ID, number).Scan(&issueID, &state, &reason, &duplicateOf)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	var due any
	if in.DueDate != nil && *in.DueDate != "" {
		due = *in.DueDate
	}
	var iteration *string
	if in.Iteration != nil {
		trimmed := strings.TrimSpace(*in.Iteration)
		iteration = &trimmed
	}
	if _, err = tx.Exec(r.Context(), `UPDATE issues SET
		pinned=COALESCE($2,pinned),
		priority=COALESCE($3,priority),
		estimate=CASE WHEN $4 THEN NULL ELSE COALESCE($5,estimate) END,
		due_date=CASE WHEN $6 THEN $7::date ELSE due_date END,
		iteration=COALESCE($8,iteration),
		updated_at=now() WHERE id=$1`,
		issueID, in.Pinned, in.Priority, in.ClearEstimate, in.Estimate, in.DueDate != nil, due, iteration); err != nil {
		serverError(w, err)
		return
	}
	if in.ClearDuplicate && duplicateOf != nil {
		if _, err = tx.Exec(r.Context(), `UPDATE issues SET duplicate_of=NULL WHERE id=$1`, issueID); err != nil {
			serverError(w, err)
			return
		}
		// Closing as a duplicate was the only reason this issue was closed,
		// so clearing the mark reopens it.
		if state == "closed" && reason == "duplicate" {
			if err = a.setIssueState(r, tx, repo, issueID, u.ID, "open", ""); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	if in.DuplicateOf != nil {
		var targetID string
		err = tx.QueryRow(r.Context(), `SELECT id FROM issues WHERE repository_id=$1 AND number=$2`, repo.ID, *in.DuplicateOf).Scan(&targetID)
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 422, "validation_failed", "The duplicate target must be an issue in this repository.")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		cycle, cycleErr := duplicateCycle(r, tx, targetID, issueID)
		if cycleErr != nil {
			serverError(w, cycleErr)
			return
		}
		if cycle {
			fail(w, 422, "validation_failed", "That issue is already marked as a duplicate of this one.")
			return
		}
		if _, err = tx.Exec(r.Context(), `UPDATE issues SET duplicate_of=$1 WHERE id=$2`, targetID, issueID); err != nil {
			serverError(w, err)
			return
		}
		if err = a.setIssueState(r, tx, repo, issueID, u.ID, "closed", "duplicate"); err != nil {
			serverError(w, err)
			return
		}
	}
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

// duplicateCycle reports whether following duplicate_of links from targetID
// reaches issueID.
func duplicateCycle(r *http.Request, tx pgx.Tx, targetID, issueID string) (bool, error) {
	next := &targetID
	for steps := 0; next != nil && steps < 50; steps++ {
		if *next == issueID {
			return true, nil
		}
		var following *string
		if err := tx.QueryRow(r.Context(), `SELECT duplicate_of FROM issues WHERE id=$1`, *next).Scan(&following); err != nil {
			return false, err
		}
		next = following
	}
	return false, nil
}

// setIssueState changes an issue's state inside tx and keeps the board and
// subscribers in sync. It does not check blockers.
func (a *App) setIssueState(r *http.Request, tx pgx.Tx, repo *Repository, issueID, actorID, state, reason string) error {
	var previous string
	if err := tx.QueryRow(r.Context(), `SELECT state FROM issues WHERE id=$1`, issueID).Scan(&previous); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), `UPDATE issues SET state=$1,state_reason=$2,updated_at=now() WHERE id=$3`, state, reason, issueID); err != nil {
		return err
	}
	if err := syncIssueBoard(r.Context(), tx, repo.ID, issueID, state == "closed"); err != nil {
		return err
	}
	if previous == state {
		return nil
	}
	kind := "issue_closed"
	if state == "open" {
		kind = "issue_reopened"
	}
	return notifyIssue(r.Context(), tx, issueID, actorID, kind)
}

// transferIssue moves an issue to another repository the actor can triage.
// Repository-specific data (labels, milestone, board state, links) is
// dropped, and the old number keeps pointing at the new location.
func (a *App) transferIssue(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanWrite {
		fail(w, 403, "forbidden", "Repository write permission is required to transfer issues.")
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
		Repository string `json:"repository"`
	}
	if !decode(w, r, &in) {
		return
	}
	targetOwner, targetName, found := strings.Cut(strings.ToLower(strings.TrimSpace(in.Repository)), "/")
	if !found {
		targetOwner, targetName = repo.Owner, targetOwner
	}
	if !slug.MatchString(targetOwner) || !repoSlug.MatchString(targetName) || (targetOwner == repo.Owner && targetName == repo.Name) {
		fail(w, 422, "validation_failed", "Choose another repository as owner/name.")
		return
	}
	u := a.user(r)
	target, err := scanRepo(a.db.QueryRow(r.Context(), `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id WHERE u.username=$1 AND r.name=$2 AND r.deleted_at IS NULL`, targetOwner, targetName))
	if err == nil {
		err = a.decorate(r.Context(), &target, u)
	}
	if err != nil || !target.CanTriage || target.Archived {
		fail(w, 422, "validation_failed", "Choose an active repository where you can triage issues.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	// Lock both repositories in a stable order so concurrent transfers and
	// issue creation cannot race on issue numbers or deadlock.
	if _, err = tx.Exec(r.Context(), `SELECT id FROM repositories WHERE id IN ($1,$2) ORDER BY id FOR UPDATE`, repo.ID, target.ID); err != nil {
		serverError(w, err)
		return
	}
	var issueID string
	err = tx.QueryRow(r.Context(), `SELECT id FROM issues WHERE repository_id=$1 AND number=$2 FOR UPDATE`, repo.ID, number).Scan(&issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var newNumber int
	if err = tx.QueryRow(r.Context(), `UPDATE issues SET repository_id=$1,number=(SELECT COALESCE(MAX(number),0)+1 FROM issues WHERE repository_id=$1),
		milestone_id=NULL,duplicate_of=NULL,parent_id=NULL,pinned=false,updated_at=now() WHERE id=$2 RETURNING number`, target.ID, issueID).Scan(&newNumber); err != nil {
		serverError(w, err)
		return
	}
	cleanup := []string{
		`DELETE FROM issue_labels WHERE issue_id=$1`,
		`DELETE FROM issue_dependencies WHERE issue_id=$1 OR blocker_id=$1`,
		`DELETE FROM issue_board_status WHERE issue_id=$1`,
		`DELETE FROM board_field_values WHERE issue_id=$1`,
		`DELETE FROM pull_issue_links WHERE issue_id=$1`,
		`DELETE FROM issue_references WHERE target_issue_id=$1 OR source_issue_id=$1`,
		`UPDATE issues SET duplicate_of=NULL WHERE duplicate_of=$1`,
		`UPDATE issues SET parent_id=NULL WHERE parent_id=$1`,
		`DELETE FROM issue_transfers WHERE issue_id=$1`,
	}
	for _, statement := range cleanup {
		if _, err = tx.Exec(r.Context(), statement, issueID); err != nil {
			serverError(w, err)
			return
		}
	}
	// Assignees keep their assignment only if they can work in the target.
	if _, err = tx.Exec(r.Context(), `DELETE FROM issue_assignees WHERE issue_id=$1 AND user_id NOT IN (
		SELECT owner_id FROM repositories WHERE id=$2 UNION SELECT user_id FROM repository_members WHERE repository_id=$2)`, issueID, target.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO issue_transfers(source_repository_id,source_number,issue_id) VALUES($1,$2,$3)
		ON CONFLICT (source_repository_id,source_number) DO UPDATE SET issue_id=EXCLUDED.issue_id,created_at=now()`, repo.ID, number, issueID); err != nil {
		serverError(w, err)
		return
	}
	note := fmt.Sprintf("Transferred from %s/%s#%d by @%s.", repo.Owner, repo.Name, number, u.Username)
	if _, err = tx.Exec(r.Context(), `INSERT INTO issue_comments(id,issue_id,author_id,body) VALUES($1,$2,$3,$4)`, auth.ID(), issueID, u.ID, note); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.transferred',$2)`, u.ID, fmt.Sprintf("%s/%s#%d->%s/%s#%d", repo.Owner, repo.Name, number, target.Owner, target.Name, newNumber)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"owner": target.Owner, "repository": target.Name, "number": newNumber})
}
