package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
)

const maxIssueDepth = 8
const maxSubIssues = 100

type SubIssues struct {
	Parent   *LinkedIssue  `json:"parent"`
	Children []LinkedIssue `json:"children"`
	Total    int           `json:"total"`
	Closed   int           `json:"closed"`
}

// issueAncestry resolves the proposed parent issue and returns its ID and the
// number of ancestors above it. It rejects a parent chain that contains
// childID, which would create a cycle.
func issueAncestry(ctx context.Context, tx pgx.Tx, repositoryID string, parentNumber int, childID string) (string, int, error) {
	var parentID string
	var next *string
	err := tx.QueryRow(ctx, `SELECT id,parent_id FROM issues WHERE repository_id=$1 AND number=$2`, repositoryID, parentNumber).Scan(&parentID, &next)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, errors.New("The parent must be an issue in this repository.")
	}
	if err != nil {
		return "", 0, err
	}
	if parentID == childID {
		return "", 0, errors.New("An issue cannot be its own parent.")
	}
	depth := 0
	for next != nil {
		if *next == childID {
			return "", 0, errors.New("That parent is already a sub-issue of this issue.")
		}
		depth++
		if depth > maxIssueDepth {
			break
		}
		var ancestor *string
		if err = tx.QueryRow(ctx, `SELECT parent_id FROM issues WHERE id=$1`, *next).Scan(&ancestor); err != nil {
			return "", 0, err
		}
		next = ancestor
	}
	return parentID, depth + 1, nil
}

func (a *App) subIssues(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var issueID string
	var parent LinkedIssue
	var parentNumber *int
	var parentTitle, parentState *string
	err := a.db.QueryRow(r.Context(), `SELECT i.id,p.number,p.title,p.state FROM issues i LEFT JOIN issues p ON p.id=i.parent_id WHERE i.repository_id=$1 AND i.number=$2`, repo.ID, number).Scan(&issueID, &parentNumber, &parentTitle, &parentState)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	result := SubIssues{Children: []LinkedIssue{}}
	if parentNumber != nil {
		parent = LinkedIssue{Number: *parentNumber, Title: *parentTitle, State: *parentState}
		result.Parent = &parent
	}
	rows, err := a.db.Query(r.Context(), `SELECT number,title,state FROM issues WHERE parent_id=$1 ORDER BY number LIMIT $2`, issueID, maxSubIssues)
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
		result.Children = append(result.Children, item)
		result.Total++
		if item.State == "closed" {
			result.Closed++
		}
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, result)
}

// updateIssueParent attaches an issue to a parent issue or detaches it when
// parent is null.
func (a *App) updateIssueParent(w http.ResponseWriter, r *http.Request) {
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
		Parent *int `json:"parent"`
	}
	if !decode(w, r, &in) {
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	// Serialize hierarchy changes per repository so two concurrent edits
	// cannot each pass the cycle check and together form a loop.
	if _, err = tx.Exec(r.Context(), `SELECT id FROM repositories WHERE id=$1 FOR UPDATE`, repo.ID); err != nil {
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
	var parentID any
	target := "none"
	if in.Parent != nil {
		id, depth, parentErr := issueAncestry(r.Context(), tx, repo.ID, *in.Parent, issueID)
		if parentErr != nil {
			fail(w, 422, "validation_failed", parentErr.Error())
			return
		}
		var below int
		if err = tx.QueryRow(r.Context(), `WITH RECURSIVE tree(id,depth) AS (SELECT id,1 FROM issues WHERE parent_id=$1 UNION ALL SELECT i.id,t.depth+1 FROM issues i JOIN tree t ON i.parent_id=t.id WHERE t.depth<$2) SELECT COALESCE(max(depth),0) FROM tree`, issueID, maxIssueDepth+1).Scan(&below); err != nil {
			serverError(w, err)
			return
		}
		if depth+below >= maxIssueDepth {
			fail(w, 422, "validation_failed", fmt.Sprintf("Sub-issues can be nested up to %d levels.", maxIssueDepth))
			return
		}
		var siblings int
		if err = tx.QueryRow(r.Context(), `SELECT count(*)::int FROM issues WHERE parent_id=$1 AND id<>$2`, id, issueID).Scan(&siblings); err != nil {
			serverError(w, err)
			return
		}
		if siblings >= maxSubIssues {
			fail(w, 422, "validation_failed", fmt.Sprintf("An issue can have up to %d sub-issues.", maxSubIssues))
			return
		}
		parentID = id
		target = fmt.Sprintf("#%d", *in.Parent)
	}
	if _, err = tx.Exec(r.Context(), `UPDATE issues SET parent_id=$1,updated_at=now() WHERE id=$2`, parentID, issueID); err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.parent_updated',$2)`, u.ID, fmt.Sprintf("%s/%s#%d->%s", repo.Owner, repo.Name, number, target)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.subIssues(w, r)
}
