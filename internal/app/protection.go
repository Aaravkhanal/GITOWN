package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (a *App) blockedPushBranches(ctx context.Context, repositoryID, role string) (map[string]string, error) {
	rows, err := a.db.Query(ctx, `SELECT branch,require_unite,restrict_push FROM repository_branch_rules WHERE repository_id=$1 AND (require_unite OR restrict_push)`, repositoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	privileged := role == "owner" || role == "maintain"
	branches := map[string]string{}
	for rows.Next() {
		var branch string
		var unite, restrict bool
		if err = rows.Scan(&branch, &unite, &restrict); err != nil {
			return nil, err
		}
		if unite {
			branches[branch] = "unite"
		} else if restrict && !privileged {
			branches[branch] = "restricted"
		}
	}
	return branches, rows.Err()
}

func (a *App) allowBrowserBranchEdit(w http.ResponseWriter, r *http.Request, repoID, branch, role string) bool {
	var unite, restrict bool
	err := a.db.QueryRow(r.Context(), `SELECT require_unite,restrict_push FROM repository_branch_rules WHERE repository_id=$1 AND branch=$2`, repoID, branch).Scan(&unite, &restrict)
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	if err != nil {
		serverError(w, err)
		return false
	}
	if unite {
		fail(w, 409, "branch_requires_unite", "This branch requires a Unite request. Edit a feature branch and merge it instead.")
		return false
	}
	if restrict && role != "owner" && role != "maintain" {
		fail(w, 403, "push_restricted", "Only the owner or a maintainer can push to this branch.")
		return false
	}
	return true
}

type BranchRule struct {
	Branch                    string    `json:"branch"`
	RequiredApprovals         int       `json:"required_approvals"`
	BlockChangesRequested     bool      `json:"block_changes_requested"`
	RequireUnite              bool      `json:"require_unite"`
	RequireResolved           bool      `json:"require_resolved"`
	RequireUpToDate           bool      `json:"require_up_to_date"`
	RestrictPush              bool      `json:"restrict_push"`
	RequireSigned             bool      `json:"require_signed"`
	RequireMaintainerApproval bool      `json:"require_maintainer_approval"`
	RequiredChecks            []string  `json:"required_checks"`
	UpdatedAt                 time.Time `json:"updated_at"`
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
	rule := BranchRule{Branch: branch, BlockChangesRequested: true, RequiredChecks: []string{}}
	err := a.db.QueryRow(r.Context(), `SELECT required_approvals,block_changes_requested,require_unite,require_resolved,require_up_to_date,restrict_push,require_signed,require_maintainer_approval,required_checks,updated_at FROM repository_branch_rules WHERE repository_id=$1 AND branch=$2`, repo.ID, branch).Scan(&rule.RequiredApprovals, &rule.BlockChangesRequested, &rule.RequireUnite, &rule.RequireResolved, &rule.RequireUpToDate, &rule.RestrictPush, &rule.RequireSigned, &rule.RequireMaintainerApproval, &rule.RequiredChecks, &rule.UpdatedAt)
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
		RequiredApprovals         int      `json:"required_approvals"`
		BlockChangesRequested     bool     `json:"block_changes_requested"`
		RequireUnite              bool     `json:"require_unite"`
		RequireResolved           bool     `json:"require_resolved"`
		RequireUpToDate           bool     `json:"require_up_to_date"`
		RestrictPush              bool     `json:"restrict_push"`
		RequireSigned             bool     `json:"require_signed"`
		RequireMaintainerApproval bool     `json:"require_maintainer_approval"`
		RequiredChecks            []string `json:"required_checks"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.RequiredChecks == nil {
		in.RequiredChecks = []string{}
	}
	if in.RequiredApprovals < 0 || in.RequiredApprovals > 10 || len(in.RequiredChecks) > 8 || !validChecks(in.RequiredChecks) {
		fail(w, 422, "validation_failed", "Required approvals must be between 0 and 10, with up to 8 check contexts.")
		return
	}
	rule := BranchRule{Branch: branch, RequiredApprovals: in.RequiredApprovals, BlockChangesRequested: in.BlockChangesRequested, RequireUnite: in.RequireUnite, RequireResolved: in.RequireResolved, RequireUpToDate: in.RequireUpToDate, RestrictPush: in.RestrictPush, RequireSigned: in.RequireSigned, RequireMaintainerApproval: in.RequireMaintainerApproval, RequiredChecks: in.RequiredChecks}
	err := a.db.QueryRow(r.Context(), `INSERT INTO repository_branch_rules(repository_id,branch,required_approvals,block_changes_requested,require_unite,require_resolved,require_up_to_date,restrict_push,require_signed,require_maintainer_approval,required_checks) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(repository_id,branch) DO UPDATE SET required_approvals=excluded.required_approvals,block_changes_requested=excluded.block_changes_requested,require_unite=excluded.require_unite,require_resolved=excluded.require_resolved,require_up_to_date=excluded.require_up_to_date,restrict_push=excluded.restrict_push,require_signed=excluded.require_signed,require_maintainer_approval=excluded.require_maintainer_approval,required_checks=excluded.required_checks,updated_at=now() RETURNING updated_at`, repo.ID, branch, rule.RequiredApprovals, rule.BlockChangesRequested, rule.RequireUnite, rule.RequireResolved, rule.RequireUpToDate, rule.RestrictPush, rule.RequireSigned, rule.RequireMaintainerApproval, rule.RequiredChecks).Scan(&rule.UpdatedAt)
	if err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'branch_rule.updated',$2)`, u.ID, fmt.Sprintf("%s/%s:%s approvals=%d block_changes=%t require_unite=%t resolved=%t up_to_date=%t restrict_push=%t signed=%t maintainer=%t", repo.Owner, repo.Name, branch, rule.RequiredApprovals, rule.BlockChangesRequested, rule.RequireUnite, rule.RequireResolved, rule.RequireUpToDate, rule.RestrictPush, rule.RequireSigned, rule.RequireMaintainerApproval))
	respond(w, 200, rule)
}

func validChecks(checks []string) bool {
	seen := map[string]bool{}
	for _, check := range checks {
		if seen[check] || !checkContext.MatchString(check) {
			return false
		}
		seen[check] = true
	}
	return true
}

var checkContext = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,39}$`)

func (a *App) enforceReviewRule(w http.ResponseWriter, r *http.Request, repo *Repository, p *Pull, baseSHA, headSHA string) bool {
	var approvals int
	var changesRequested bool
	var maintainerApproved bool
	var required int
	var blockChanges, requireResolved, requireUpToDate, requireSigned, requireMaintainer bool
	var checks []string
	err := a.db.QueryRow(r.Context(), `SELECT required_approvals,block_changes_requested,require_resolved,require_up_to_date,require_signed,require_maintainer_approval,required_checks FROM repository_branch_rules WHERE repository_id=$1 AND branch=$2`, repo.ID, p.Base).Scan(&required, &blockChanges, &requireResolved, &requireUpToDate, &requireSigned, &requireMaintainer, &checks)
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	if err != nil {
		serverError(w, err)
		return false
	}
	rows, err := a.db.Query(r.Context(), `SELECT DISTINCT ON (rv.reviewer_id) rv.state,u.id=$3 OR EXISTS(SELECT 1 FROM repository_members rm WHERE rm.repository_id=$4 AND rm.user_id=u.id AND rm.role='maintain') FROM pull_reviews rv JOIN users u ON u.id=rv.reviewer_id WHERE rv.pull_request_id=$1 AND rv.head_sha=$2 AND rv.dismissed_at IS NULL ORDER BY rv.reviewer_id,rv.created_at DESC,rv.id DESC`, p.ID, headSHA, repo.OwnerID, repo.ID)
	if err != nil {
		serverError(w, err)
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var maintainer bool
		if err = rows.Scan(&state, &maintainer); err != nil {
			serverError(w, err)
			return false
		}
		approvals += boolInt(state == "approved")
		maintainerApproved = maintainerApproved || (state == "approved" && maintainer)
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
	if requireMaintainer && !maintainerApproved {
		fail(w, 409, "maintainer_approval_required", "An owner or maintainer must approve the latest commit.")
		return false
	}
	if requireResolved {
		var open bool
		if err = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM pull_review_threads WHERE pull_request_id=$1 AND commit_sha=$2 AND resolved_at IS NULL)`, p.ID, headSHA).Scan(&open); err != nil {
			serverError(w, err)
			return false
		}
		if open {
			fail(w, 409, "conversations_unresolved", "Resolve every conversation on the latest commit before merging.")
			return false
		}
	}
	if requireUpToDate {
		if _, err = a.git.Run(r.Context(), repo.ID, nil, "merge-base", "--is-ancestor", baseSHA, headSHA); err != nil {
			fail(w, 409, "branch_out_of_date", "Update this branch with the latest base commits before merging.")
			return false
		}
	}
	if requireSigned {
		output, gitErr := a.git.Run(r.Context(), repo.ID, nil, "log", "--format=%G?", baseSHA+".."+headSHA)
		if gitErr != nil {
			serverError(w, gitErr)
			return false
		}
		for _, mark := range strings.Fields(string(output)) {
			if mark == "N" || mark == "B" {
				fail(w, 409, "signature_required", "Every commit on this unite request must be signed.")
				return false
			}
		}
	}
	if len(checks) > 0 {
		rows, err = a.db.Query(r.Context(), `SELECT context,state FROM commit_statuses WHERE repository_id=$1 AND sha=$2 AND context = ANY($3)`, repo.ID, headSHA, checks)
		if err != nil {
			serverError(w, err)
			return false
		}
		defer rows.Close()
		passed := map[string]bool{}
		for rows.Next() {
			var contextName, state string
			if err = rows.Scan(&contextName, &state); err != nil {
				serverError(w, err)
				return false
			}
			passed[contextName] = state == "success"
		}
		if err = rows.Err(); err != nil {
			serverError(w, err)
			return false
		}
		for _, check := range checks {
			if !passed[check] {
				fail(w, 409, "checks_required", "Required checks must succeed on the latest commit.")
				return false
			}
		}
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
