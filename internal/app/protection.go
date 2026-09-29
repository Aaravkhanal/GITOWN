package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

func (a *App) directPushProtected(ctx context.Context, repoID, branch string) (bool, error) {
	var protected bool
	err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM repository_branch_rules WHERE repository_id=$1 AND branch=$2 AND require_unite)`, repoID, branch).Scan(&protected)
	return protected, err
}

func (a *App) allowBrowserBranchEdit(w http.ResponseWriter, r *http.Request, repoID, branch string) bool {
	protected, err := a.directPushProtected(r.Context(), repoID, branch)
	if err != nil {
		serverError(w, err)
		return false
	}
	if protected {
		fail(w, 409, "branch_requires_unite", "This branch requires a Unite request. Edit a feature branch and merge it instead.")
		return false
	}
	return true
}

type BranchRule struct {
	Branch                string    `json:"branch"`
	RequiredApprovals     int       `json:"required_approvals"`
	BlockChangesRequested bool      `json:"block_changes_requested"`
	RequireUnite          bool      `json:"require_unite"`
	UpdatedAt             time.Time `json:"updated_at"`
}

func (a *App) branchRule(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	branch := r.URL.Query().Get("branch")
	if branch == "" {
		fail(w, 422, "validation_failed", "Branch is required.")
		return
	}
	if _, err := a.git.Resolve(r.Context(), repo.ID, branch); err != nil {
		fail(w, 404, "not_found", "Branch not found.")
		return
	}
	rule := BranchRule{Branch: branch, BlockChangesRequested: true}
	err := a.db.QueryRow(r.Context(), `SELECT required_approvals,block_changes_requested,require_unite,updated_at FROM repository_branch_rules WHERE repository_id=$1 AND branch=$2`, repo.ID, branch).Scan(&rule.RequiredApprovals, &rule.BlockChangesRequested, &rule.RequireUnite, &rule.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		respond(w, 200, rule)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, rule)
}

func (a *App) updateBranchRule(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil || !activeRepository(w, repo) {
		return
	}
	branch := r.URL.Query().Get("branch")
	if branch == "" {
		fail(w, 422, "validation_failed", "Branch is required.")
		return
	}
	if _, err := a.git.Resolve(r.Context(), repo.ID, branch); err != nil {
		fail(w, 404, "not_found", "Branch not found.")
		return
	}
	var in struct {
		RequiredApprovals     int  `json:"required_approvals"`
		BlockChangesRequested bool `json:"block_changes_requested"`
		RequireUnite          bool `json:"require_unite"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.RequiredApprovals < 0 || in.RequiredApprovals > 10 {
		fail(w, 422, "validation_failed", "Required approvals must be between 0 and 10.")
		return
	}
	rule := BranchRule{Branch: branch, RequiredApprovals: in.RequiredApprovals, BlockChangesRequested: in.BlockChangesRequested, RequireUnite: in.RequireUnite}
	err := a.db.QueryRow(r.Context(), `INSERT INTO repository_branch_rules(repository_id,branch,required_approvals,block_changes_requested,require_unite) VALUES($1,$2,$3,$4,$5) ON CONFLICT(repository_id,branch) DO UPDATE SET required_approvals=excluded.required_approvals,block_changes_requested=excluded.block_changes_requested,require_unite=excluded.require_unite,updated_at=now() RETURNING updated_at`, repo.ID, branch, rule.RequiredApprovals, rule.BlockChangesRequested, rule.RequireUnite).Scan(&rule.UpdatedAt)
	if err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'branch_rule.updated',$2)`, u.ID, fmt.Sprintf("%s/%s:%s approvals=%d block_changes=%t require_unite=%t", repo.Owner, repo.Name, branch, rule.RequiredApprovals, rule.BlockChangesRequested, rule.RequireUnite))
	respond(w, 200, rule)
}

func (a *App) enforceReviewRule(w http.ResponseWriter, r *http.Request, repo *Repository, p *Pull, headSHA string) bool {
	var approvals int
	var changesRequested bool
	var required int
	var blockChanges bool
	err := a.db.QueryRow(r.Context(), `SELECT required_approvals,block_changes_requested FROM repository_branch_rules WHERE repository_id=$1 AND branch=$2`, repo.ID, p.Base).Scan(&required, &blockChanges)
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	if err != nil {
		serverError(w, err)
		return false
	}
	rows, err := a.db.Query(r.Context(), `SELECT DISTINCT ON (reviewer_id) state FROM pull_reviews WHERE pull_request_id=$1 AND head_sha=$2 ORDER BY reviewer_id,created_at DESC,id DESC`, p.ID, headSHA)
	if err != nil {
		serverError(w, err)
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		if err = rows.Scan(&state); err != nil {
			serverError(w, err)
			return false
		}
		approvals += boolInt(state == "approved")
		changesRequested = changesRequested || state == "changes_requested"
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return false
	}
	if blockChanges && changesRequested {
		fail(w, 409, "changes_requested", "Resolve the current change request before merging.")
		return false
	}
	if approvals < required {
		fail(w, 409, "approvals_required", fmt.Sprintf("This branch requires %d fresh approval%s; %d received.", required, plural(required), approvals))
		return false
	}
	return true
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func plural(value int) string {
	if value == 1 {
		return ""
	}
	return "s"
}
