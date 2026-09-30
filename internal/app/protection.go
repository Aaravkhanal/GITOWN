package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// branchPolicy is the direct-write part of a branch rule. Browser edits, API
// merges, HTTPS pushes, and SSH pushes all consult it through the helpers
// below, so a rule means the same thing on every path.
type branchPolicy struct {
	Branch         string
	RequireUnite   bool
	RestrictPush   bool
	RequireSigned  bool
	AllowForcePush bool
	AllowDeletion  bool
}

func privilegedRole(role string) bool {
	return role == "owner" || role == "maintain"
}

// directWriteBlock explains why role may not update the branch directly.
// Merging a unite request is not a direct write, so callers merging pass
// merging=true and only the push restriction applies.
func (p branchPolicy) directWriteBlock(role string, merging bool) string {
	if p.RequireUnite && !merging {
		return "unite"
	}
	if p.RestrictPush && !privilegedRole(role) {
		return "restricted"
	}
	return ""
}

func directWriteMessage(block string) string {
	if block == "unite" {
		return "This branch requires a Unite request; direct pushes are disabled."
	}
	return "Only the owner or a maintainer can push to this branch."
}

func (a *App) branchPolicies(ctx context.Context, repositoryID string) (map[string]branchPolicy, error) {
	rows, err := a.db.Query(ctx, `SELECT branch,require_unite,restrict_push,require_signed,allow_force_push,allow_deletion FROM repository_branch_rules WHERE repository_id=$1`, repositoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	policies := map[string]branchPolicy{}
	for rows.Next() {
		var p branchPolicy
		if err = rows.Scan(&p.Branch, &p.RequireUnite, &p.RestrictPush, &p.RequireSigned, &p.AllowForcePush, &p.AllowDeletion); err != nil {
			return nil, err
		}
		policies[p.Branch] = p
	}
	return policies, rows.Err()
}

func (a *App) blockedPushBranches(ctx context.Context, repositoryID, role string) (map[string]string, error) {
	policies, err := a.branchPolicies(ctx, repositoryID)
	if err != nil {
		return nil, err
	}
	branches := map[string]string{}
	for branch, policy := range policies {
		if block := policy.directWriteBlock(role, false); block != "" {
			branches[branch] = block
		}
	}
	return branches, nil
}

// receivePolicyEnv returns the environment for git receive-pack. It enables
// GITOWN's pre-receive hook and hands it this push's rules: which refs the
// pusher may not update directly, which refs refuse force pushes, deletions,
// or unsigned commits, and how many bytes the push may add. The cleanup
// function removes the temporary allowed-signers file.
func (a *App) receivePolicyEnv(ctx context.Context, repo *Repository, role string) ([]string, func(), error) {
	policies, err := a.branchPolicies(ctx, repo.ID)
	if err != nil {
		return nil, func() {}, err
	}
	var unite, restricted, noForce, noDelete, signed []string
	for branch, policy := range policies {
		ref := "refs/heads/" + branch
		switch policy.directWriteBlock(role, false) {
		case "unite":
			unite = append(unite, ref)
		case "restricted":
			restricted = append(restricted, ref)
		}
		if !policy.AllowForcePush {
			noForce = append(noForce, ref)
		}
		if !policy.AllowDeletion {
			noDelete = append(noDelete, ref)
		}
		if policy.RequireSigned {
			signed = append(signed, ref)
		}
	}
	defaultBranch := repo.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	noDelete = append(noDelete, "refs/heads/"+defaultBranch)
	remaining, err := a.remainingQuota(ctx, repo)
	if err != nil {
		return nil, func() {}, err
	}
	env := append(a.git.ReceiveEnvironment(),
		"GITOWN_BLOCK_UNITE="+strings.Join(unite, " "),
		"GITOWN_BLOCK_RESTRICTED="+strings.Join(restricted, " "),
		"GITOWN_NO_FORCE="+strings.Join(noForce, " "),
		"GITOWN_NO_DELETE="+strings.Join(noDelete, " "),
		"GITOWN_SIGNED="+strings.Join(signed, " "),
		"GITOWN_QUOTA_REMAINING="+strconv.FormatInt(remaining, 10),
	)
	cleanup := func() {}
	if len(signed) > 0 {
		path, signersErr := a.allowedSignersFile(ctx)
		if signersErr != nil {
			return nil, func() {}, signersErr
		}
		env = append(env, "GITOWN_ALLOWED_SIGNERS="+path)
		cleanup = func() { _ = os.Remove(path) }
	}
	return env, cleanup, nil
}

// allowedSignersFile writes every registered SSH signing key in the format Git
// uses to verify SSH commit signatures. A commit counts as signed only when Git
// reports a good signature (%G? == "G") from one of these keys.
func (a *App) allowedSignersFile(ctx context.Context) (string, error) {
	rows, err := a.db.Query(ctx, `SELECT u.email,k.public_key FROM signing_keys k JOIN users u ON u.id=k.user_id ORDER BY k.created_at`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var body strings.Builder
	for rows.Next() {
		var email, key string
		if err = rows.Scan(&email, &key); err != nil {
			return "", err
		}
		if strings.ContainsAny(email, " \t\r\n\"") || strings.ContainsAny(key, "\r\n") {
			continue
		}
		body.WriteString(email + ` namespaces="git" ` + strings.TrimSpace(key) + "\n")
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	file, err := os.CreateTemp("", "gitown-signers-")
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err = file.WriteString(body.String()); err != nil {
		_ = os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

// verifiedSignatures reports whether every commit in the range carries a good
// signature from a registered signing key.
func (a *App) verifiedSignatures(ctx context.Context, repoID, revisionRange string) (bool, error) {
	signers, err := a.allowedSignersFile(ctx)
	if err != nil {
		return false, err
	}
	defer os.Remove(signers)
	output, err := a.git.Run(ctx, repoID, nil, "-c", "gpg.ssh.allowedSignersFile="+signers, "log", "--format=%G?", revisionRange)
	if err != nil {
		return false, err
	}
	for _, mark := range strings.Fields(string(output)) {
		if mark != "G" {
			return false, nil
		}
	}
	return true, nil
}

func (a *App) allowBrowserBranchEdit(w http.ResponseWriter, r *http.Request, repoID, branch, role string) bool {
	policies, err := a.branchPolicies(r.Context(), repoID)
	if err != nil {
		serverError(w, err)
		return false
	}
	policy, ok := policies[branch]
	if !ok {
		return true
	}
	switch policy.directWriteBlock(role, false) {
	case "unite":
		fail(w, 409, "branch_requires_unite", "This branch requires a Unite request. Edit a feature branch and merge it instead.")
		return false
	case "restricted":
		fail(w, 403, "push_restricted", "Only the owner or a maintainer can push to this branch.")
		return false
	}
	if policy.RequireSigned {
		fail(w, 409, "signature_required", "This branch accepts only signed commits. Browser edits are unsigned; edit another branch and open a Unite request.")
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
	RequiredReviewers         []string  `json:"required_reviewers"`
	AllowForcePush            bool      `json:"allow_force_push"`
	AllowDeletion             bool      `json:"allow_deletion"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

const branchRuleColumns = `required_approvals,block_changes_requested,require_unite,require_resolved,require_up_to_date,restrict_push,require_signed,require_maintainer_approval,required_checks,required_reviewers,allow_force_push,allow_deletion,updated_at`

func (rule *BranchRule) scanTargets() []any {
	return []any{&rule.RequiredApprovals, &rule.BlockChangesRequested, &rule.RequireUnite, &rule.RequireResolved, &rule.RequireUpToDate, &rule.RestrictPush, &rule.RequireSigned, &rule.RequireMaintainerApproval, &rule.RequiredChecks, &rule.RequiredReviewers, &rule.AllowForcePush, &rule.AllowDeletion, &rule.UpdatedAt}
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
	rule := BranchRule{Branch: branch, BlockChangesRequested: true, RequiredChecks: []string{}, RequiredReviewers: []string{}}
	err := a.db.QueryRow(r.Context(), `SELECT `+branchRuleColumns+` FROM repository_branch_rules WHERE repository_id=$1 AND branch=$2`, repo.ID, branch).Scan(rule.scanTargets()...)
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
	repo := a.maintainedRepository(w, r)
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
		RequiredReviewers         []string `json:"required_reviewers"`
		AllowForcePush            bool     `json:"allow_force_push"`
		AllowDeletion             bool     `json:"allow_deletion"`
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
	reviewers, message := a.normalizeRequiredReviewers(r.Context(), repo, in.RequiredReviewers)
	if message != "" {
		fail(w, 422, "validation_failed", message)
		return
	}
	rule := BranchRule{Branch: branch, RequiredApprovals: in.RequiredApprovals, BlockChangesRequested: in.BlockChangesRequested, RequireUnite: in.RequireUnite, RequireResolved: in.RequireResolved, RequireUpToDate: in.RequireUpToDate, RestrictPush: in.RestrictPush, RequireSigned: in.RequireSigned, RequireMaintainerApproval: in.RequireMaintainerApproval, RequiredChecks: in.RequiredChecks, RequiredReviewers: reviewers, AllowForcePush: in.AllowForcePush, AllowDeletion: in.AllowDeletion}
	err := a.db.QueryRow(r.Context(), `INSERT INTO repository_branch_rules(repository_id,branch,required_approvals,block_changes_requested,require_unite,require_resolved,require_up_to_date,restrict_push,require_signed,require_maintainer_approval,required_checks,required_reviewers,allow_force_push,allow_deletion) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT(repository_id,branch) DO UPDATE SET required_approvals=excluded.required_approvals,block_changes_requested=excluded.block_changes_requested,require_unite=excluded.require_unite,require_resolved=excluded.require_resolved,require_up_to_date=excluded.require_up_to_date,restrict_push=excluded.restrict_push,require_signed=excluded.require_signed,require_maintainer_approval=excluded.require_maintainer_approval,required_checks=excluded.required_checks,required_reviewers=excluded.required_reviewers,allow_force_push=excluded.allow_force_push,allow_deletion=excluded.allow_deletion,updated_at=now() RETURNING updated_at`, repo.ID, branch, rule.RequiredApprovals, rule.BlockChangesRequested, rule.RequireUnite, rule.RequireResolved, rule.RequireUpToDate, rule.RestrictPush, rule.RequireSigned, rule.RequireMaintainerApproval, rule.RequiredChecks, rule.RequiredReviewers, rule.AllowForcePush, rule.AllowDeletion).Scan(&rule.UpdatedAt)
	if err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'branch_rule.updated',$2)`, u.ID, fmt.Sprintf("%s/%s:%s approvals=%d block_changes=%t require_unite=%t resolved=%t up_to_date=%t restrict_push=%t signed=%t maintainer=%t reviewers=%s force_push=%t deletion=%t", repo.Owner, repo.Name, branch, rule.RequiredApprovals, rule.BlockChangesRequested, rule.RequireUnite, rule.RequireResolved, rule.RequireUpToDate, rule.RestrictPush, rule.RequireSigned, rule.RequireMaintainerApproval, strings.Join(rule.RequiredReviewers, ","), rule.AllowForcePush, rule.AllowDeletion))
	respond(w, 200, rule)
}

// normalizeRequiredReviewers accepts usernames of people who can review and
// crew:<slug> entries naming a crew in the repository's district.
func (a *App) normalizeRequiredReviewers(ctx context.Context, repo *Repository, entries []string) ([]string, string) {
	result := []string{}
	if len(entries) > 8 {
		return nil, "List up to 8 required reviewers or crews."
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		entry = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(entry), "@")))
		if entry == "" || seen[entry] {
			continue
		}
		seen[entry] = true
		if crewSlug, ok := strings.CutPrefix(entry, "crew:"); ok {
			if repo.DistrictID == "" || !slug.MatchString(crewSlug) {
				return nil, "Crew reviewers are available for district repositories; use crew:<slug>."
			}
			var exists bool
			if err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM crews WHERE district_id=$1 AND slug=$2)`, repo.DistrictID, crewSlug).Scan(&exists); err != nil || !exists {
				return nil, "Crew " + crewSlug + " does not exist in this repository's district."
			}
		} else {
			if !slug.MatchString(entry) {
				return nil, "Required reviewers must be usernames or crew:<slug>."
			}
			var eligible bool
			if err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u WHERE u.username=$1 AND (u.id=$2 OR EXISTS(SELECT 1 FROM repository_members rm WHERE rm.repository_id=$3 AND rm.user_id=u.id AND rm.role IN ('write','maintain'))))`, entry, repo.OwnerID, repo.ID).Scan(&eligible); err != nil || !eligible {
				return nil, "@" + entry + " must be the owner or a collaborator with write or maintain access."
			}
		}
		result = append(result, entry)
	}
	return result, ""
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

// enforceReviewRule applies the base branch's rule to a merge by merger.
func (a *App) enforceReviewRule(w http.ResponseWriter, r *http.Request, repo *Repository, p *Pull, baseSHA, headSHA string) bool {
	var rule BranchRule
	err := a.db.QueryRow(r.Context(), `SELECT `+branchRuleColumns+` FROM repository_branch_rules WHERE repository_id=$1 AND branch=$2`, repo.ID, p.Base).Scan(rule.scanTargets()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	if err != nil {
		serverError(w, err)
		return false
	}
	policy := branchPolicy{RequireUnite: rule.RequireUnite, RestrictPush: rule.RestrictPush}
	if block := policy.directWriteBlock(repo.Role, true); block != "" {
		fail(w, 403, "push_restricted", "Only the owner or a maintainer can merge into this branch.")
		return false
	}
	approvals := 0
	changesRequested := false
	maintainerApproved := false
	approvers := map[string]bool{}
	rows, err := a.db.Query(r.Context(), `SELECT DISTINCT ON (rv.reviewer_id) rv.state,u.username,u.id=$3 OR EXISTS(SELECT 1 FROM repository_members rm WHERE rm.repository_id=$4 AND rm.user_id=u.id AND rm.role='maintain') FROM pull_reviews rv JOIN users u ON u.id=rv.reviewer_id WHERE rv.pull_request_id=$1 AND rv.head_sha=$2 AND rv.dismissed_at IS NULL ORDER BY rv.reviewer_id,rv.created_at DESC,rv.id DESC`, p.ID, headSHA, repo.OwnerID, repo.ID)
	if err != nil {
		serverError(w, err)
		return false
	}
	for rows.Next() {
		var state, reviewer string
		var maintainer bool
		if err = rows.Scan(&state, &reviewer, &maintainer); err != nil {
			rows.Close()
			serverError(w, err)
			return false
		}
		if state == "approved" {
			approvals++
			approvers[reviewer] = true
			maintainerApproved = maintainerApproved || maintainer
		}
		changesRequested = changesRequested || state == "changes_requested"
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return false
	}
	if rule.BlockChangesRequested && changesRequested {
		fail(w, 409, "changes_requested", "Resolve the current change request before merging.")
		return false
	}
	if approvals < rule.RequiredApprovals {
		fail(w, 409, "approvals_required", fmt.Sprintf("This branch requires %d fresh approval%s; %d received.", rule.RequiredApprovals, plural(rule.RequiredApprovals), approvals))
		return false
	}
	if rule.RequireMaintainerApproval && !maintainerApproved {
		fail(w, 409, "maintainer_approval_required", "An owner or maintainer must approve the latest commit.")
		return false
	}
	for _, required := range rule.RequiredReviewers {
		satisfied := false
		if crewSlug, ok := strings.CutPrefix(required, "crew:"); ok {
			names := []string{}
			for name := range approvers {
				names = append(names, name)
			}
			if err = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM crew_members cm JOIN crews c ON c.id=cm.crew_id JOIN users u ON u.id=cm.user_id WHERE c.district_id::text=$1 AND c.slug=$2 AND u.username=ANY($3))`, repo.DistrictID, crewSlug, names).Scan(&satisfied); err != nil {
				serverError(w, err)
				return false
			}
		} else {
			satisfied = approvers[required]
		}
		if !satisfied {
			who := "@" + required
			if strings.HasPrefix(required, "crew:") {
				who = "a member of crew " + strings.TrimPrefix(required, "crew:")
			}
			fail(w, 409, "required_reviewer", "This branch needs an approval of the latest commit from "+who+".")
			return false
		}
	}
	if rule.RequireResolved {
		var open bool
		if err = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM pull_review_threads WHERE pull_request_id=$1 AND resolved_at IS NULL)`, p.ID).Scan(&open); err != nil {
			serverError(w, err)
			return false
		}
		if open {
			fail(w, 409, "conversations_unresolved", "Resolve every review conversation before merging.")
			return false
		}
	}
	if rule.RequireUpToDate {
		if _, err = a.git.Run(r.Context(), repo.ID, nil, "merge-base", "--is-ancestor", baseSHA, headSHA); err != nil {
			fail(w, 409, "branch_out_of_date", "Update this branch with the latest base commits before merging.")
			return false
		}
	}
	if rule.RequireSigned {
		verified, verifyErr := a.verifiedSignatures(r.Context(), repo.ID, baseSHA+".."+headSHA)
		if verifyErr != nil {
			serverError(w, verifyErr)
			return false
		}
		if !verified {
			fail(w, 409, "signature_required", "Every commit on this unite request must carry a verified signature from a registered signing key.")
			return false
		}
	}
	if len(rule.RequiredChecks) > 0 {
		rows, err = a.db.Query(r.Context(), `SELECT context,state FROM commit_statuses WHERE repository_id=$1 AND sha=$2 AND context = ANY($3)`, repo.ID, headSHA, rule.RequiredChecks)
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
		for _, check := range rule.RequiredChecks {
			if !passed[check] {
				fail(w, 409, "checks_required", "Required checks must succeed on the latest commit: "+check+".")
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
