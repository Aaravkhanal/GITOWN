package app

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

// crossReference matches #42 and owner/repository#42 in Markdown text.
var crossReference = regexp.MustCompile(`(?:^|[^A-Za-z0-9_/#.-])(?:([a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?)/([a-z0-9](?:[a-z0-9._-]{0,98}[a-z0-9])?))?#([1-9][0-9]{0,8})\b`)

type referenceTarget struct {
	Owner      string
	Repository string
	Number     int
}

func parseReferences(text string) []referenceTarget {
	seen := map[referenceTarget]bool{}
	items := []referenceTarget{}
	for _, match := range crossReference.FindAllStringSubmatch(strings.ToLower(text), 50) {
		n, err := strconv.Atoi(match[3])
		if err != nil {
			continue
		}
		target := referenceTarget{Owner: match[1], Repository: match[2], Number: n}
		if !seen[target] {
			seen[target] = true
			items = append(items, target)
		}
	}
	return items
}

// recordReferences replaces the stored references made by one source (an
// issue body, a comment, a unite request body) with those in text.
func (a *App) recordReferences(ctx context.Context, tx pgx.Tx, repo *Repository, actorID, sourceKind, sourceID, sourceIssueID, sourcePullID, text string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM issue_references WHERE source_kind=$1 AND source_id=$2`, sourceKind, sourceID); err != nil {
		return err
	}
	var issueRef, pullRef any
	if sourceIssueID != "" {
		issueRef = sourceIssueID
	}
	if sourcePullID != "" {
		pullRef = sourcePullID
	}
	for _, target := range parseReferences(text) {
		repositoryID := repo.ID
		if target.Owner != "" && (target.Owner != repo.Owner || target.Repository != repo.Name) {
			err := tx.QueryRow(ctx, `SELECT r.id FROM repositories r JOIN users u ON u.id=r.owner_id WHERE u.username=$1 AND r.name=$2 AND r.deleted_at IS NULL`, target.Owner, target.Repository).Scan(&repositoryID)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
		}
		var targetID string
		err := tx.QueryRow(ctx, `SELECT id FROM issues WHERE repository_id=$1 AND number=$2`, repositoryID, target.Number).Scan(&targetID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if targetID == sourceIssueID {
			continue
		}
		if _, err = tx.Exec(ctx, `INSERT INTO issue_references(id,target_issue_id,source_kind,source_id,source_repository_id,source_issue_id,source_pull_id,actor_id)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (source_kind,source_id,target_issue_id) DO NOTHING`,
			auth.ID(), targetID, sourceKind, sourceID, repo.ID, issueRef, pullRef, actorID); err != nil {
			return err
		}
	}
	return nil
}

type IssueReference struct {
	Kind       string `json:"kind"`
	Owner      string `json:"owner"`
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	Title      string `json:"title"`
	State      string `json:"state"`
}

type IssueReferences struct {
	Mentions     []IssueReference `json:"mentions"`
	ReferencedBy []IssueReference `json:"referenced_by"`
}

// issueReferences lists the issues this issue mentions and the issues and
// unite requests that mention it. Items in repositories the viewer cannot
// read are omitted.
func (a *App) issueReferences(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var issueID string
	err := a.db.QueryRow(r.Context(), `SELECT id FROM issues WHERE repository_id=$1 AND number=$2`, repo.ID, number).Scan(&issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	result := IssueReferences{Mentions: []IssueReference{}, ReferencedBy: []IssueReference{}}
	readable := map[string]bool{repo.ID: true}
	canRead := func(repositoryID, visibility string) bool {
		if visibility == "public" {
			return true
		}
		if _, known := readable[repositoryID]; !known {
			readable[repositoryID] = a.canReadRepository(r, repositoryID)
		}
		return readable[repositoryID]
	}
	rows, err := a.db.Query(r.Context(), `SELECT DISTINCT ON (t.id) tr.id,tr.visibility,o.username,tr.name,t.number,t.title,t.state
		FROM issue_references x JOIN issues t ON t.id=x.target_issue_id JOIN repositories tr ON tr.id=t.repository_id JOIN users o ON o.id=tr.owner_id
		WHERE x.source_issue_id=$1 AND tr.deleted_at IS NULL ORDER BY t.id LIMIT 100`, issueID)
	if err != nil {
		serverError(w, err)
		return
	}
	type row struct {
		repositoryID, visibility string
		item                     IssueReference
	}
	var mentions []row
	for rows.Next() {
		var item row
		item.item.Kind = "issue"
		if err = rows.Scan(&item.repositoryID, &item.visibility, &item.item.Owner, &item.item.Repository, &item.item.Number, &item.item.Title, &item.item.State); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		mentions = append(mentions, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	rows, err = a.db.Query(r.Context(), `SELECT DISTINCT ON (COALESCE(x.source_issue_id,x.source_pull_id)) sr.id,sr.visibility,o.username,sr.name,
		CASE WHEN x.source_issue_id IS NULL THEN 'pull' ELSE 'issue' END,COALESCE(si.number,sp.number),COALESCE(si.title,sp.title),COALESCE(si.state,sp.state)
		FROM issue_references x JOIN repositories sr ON sr.id=x.source_repository_id JOIN users o ON o.id=sr.owner_id
		LEFT JOIN issues si ON si.id=x.source_issue_id LEFT JOIN pull_requests sp ON sp.id=x.source_pull_id
		WHERE x.target_issue_id=$1 AND sr.deleted_at IS NULL AND (si.repository_id IS NULL OR si.repository_id=sr.id)
		ORDER BY COALESCE(x.source_issue_id,x.source_pull_id) LIMIT 100`, issueID)
	if err != nil {
		serverError(w, err)
		return
	}
	var sources []row
	for rows.Next() {
		var item row
		if err = rows.Scan(&item.repositoryID, &item.visibility, &item.item.Owner, &item.item.Repository, &item.item.Kind, &item.item.Number, &item.item.Title, &item.item.State); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		sources = append(sources, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	for _, item := range mentions {
		if canRead(item.repositoryID, item.visibility) {
			result.Mentions = append(result.Mentions, item.item)
		}
	}
	for _, item := range sources {
		if canRead(item.repositoryID, item.visibility) {
			result.ReferencedBy = append(result.ReferencedBy, item.item)
		}
	}
	respond(w, 200, result)
}
