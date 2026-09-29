package app

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/jackc/pgx/v5"
)

type IssueAssignees struct {
	Assigned  []PublicUser `json:"assigned"`
	Available []PublicUser `json:"available"`
}

type PublicUser struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

func (a *App) issueAssignees(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	result, ok := a.readIssueAssignees(w, r, repo, number)
	if ok {
		respond(w, 200, result)
	}
}

func (a *App) readIssueAssignees(w http.ResponseWriter, r *http.Request, repo *Repository, number int) (IssueAssignees, bool) {
	var exists bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM issues WHERE repository_id=$1 AND number=$2)`, repo.ID, number).Scan(&exists); err != nil {
		serverError(w, err)
		return IssueAssignees{}, false
	}
	if !exists {
		fail(w, 404, "not_found", "Issue not found.")
		return IssueAssignees{}, false
	}
	rows, err := a.db.Query(r.Context(), `WITH eligible AS (
		SELECT u.id,u.username,u.display_name FROM repositories r JOIN users u ON u.id=r.owner_id WHERE r.id=$1
		UNION
		SELECT u.id,u.username,u.display_name FROM repository_members rm JOIN users u ON u.id=rm.user_id WHERE rm.repository_id=$1
	) SELECT e.username,e.display_name,(ia.user_id IS NOT NULL) FROM eligible e
	LEFT JOIN issues i ON i.repository_id=$1 AND i.number=$2
	LEFT JOIN issue_assignees ia ON ia.issue_id=i.id AND ia.user_id=e.id
	ORDER BY e.username`, repo.ID, number)
	if err != nil {
		serverError(w, err)
		return IssueAssignees{}, false
	}
	defer rows.Close()
	result := IssueAssignees{Assigned: []PublicUser{}, Available: []PublicUser{}}
	for rows.Next() {
		var user PublicUser
		var assigned bool
		if err = rows.Scan(&user.Username, &user.DisplayName, &assigned); err != nil {
			serverError(w, err)
			return IssueAssignees{}, false
		}
		if assigned {
			result.Assigned = append(result.Assigned, user)
		} else {
			result.Available = append(result.Available, user)
		}
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return IssueAssignees{}, false
	}
	return result, true
}

func (a *App) updateIssueAssignees(w http.ResponseWriter, r *http.Request) {
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
		Usernames []string `json:"usernames"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Usernames) > 10 {
		fail(w, 422, "validation_failed", "An issue can have at most 10 assignees.")
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
	var issueID string
	if err = tx.QueryRow(r.Context(), `SELECT id FROM issues WHERE repository_id=$1 AND number=$2 FOR UPDATE`, repo.ID, number).Scan(&issueID); err != nil {
		if err == pgx.ErrNoRows {
			fail(w, 404, "not_found", "Issue not found.")
		} else {
			serverError(w, err)
		}
		return
	}
	previous := map[string]bool{}
	prows, err := tx.Query(r.Context(), `SELECT user_id::text FROM issue_assignees WHERE issue_id=$1`, issueID)
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
	if _, err = tx.Exec(r.Context(), `DELETE FROM issue_assignees WHERE issue_id=$1`, issueID); err != nil {
		serverError(w, err)
		return
	}
	for username := range unique {
		result, insertErr := tx.Exec(r.Context(), `INSERT INTO issue_assignees(issue_id,user_id)
			SELECT $1,u.id FROM users u WHERE u.username=$2 AND (
				u.id=(SELECT owner_id FROM repositories WHERE id=$3) OR
				EXISTS(SELECT 1 FROM repository_members rm WHERE rm.repository_id=$3 AND rm.user_id=u.id)
			)`, issueID, username, repo.ID)
		if insertErr != nil {
			serverError(w, insertErr)
			return
		}
		if result.RowsAffected() != 1 {
			fail(w, 422, "validation_failed", "Assignees must be repository members.")
			return
		}
	}
	names := append([]string(nil), in.Usernames...)
	sort.Strings(names)
	u := a.user(r)
	crows, err := tx.Query(r.Context(), `SELECT user_id::text FROM issue_assignees WHERE issue_id=$1`, issueID)
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
		if err = notifyDirect(r.Context(), tx, id, u.ID, repo.ID, issueID, "", "assignment", "assignment:issue:"+issueID+":"+id); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.assignees_updated',$2)`, u.ID, fmt.Sprintf("%s/%s#%d [%s]", repo.Owner, repo.Name, number, joinNames(names))); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	result, ok := a.readIssueAssignees(w, r, repo, number)
	if ok {
		respond(w, 200, result)
	}
}

func joinNames(names []string) string {
	result := ""
	for index, name := range names {
		if index > 0 {
			result += ","
		}
		result += name
	}
	return result
}
