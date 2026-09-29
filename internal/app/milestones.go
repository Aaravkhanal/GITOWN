package app

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

type Milestone struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	State        string    `json:"state"`
	DueDate      *string   `json:"due_date"`
	OpenIssues   int       `json:"open_issues"`
	ClosedIssues int       `json:"closed_issues"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const milestoneColumns = `m.id,m.title,m.description,m.state,m.due_date::text,
	count(i.id) FILTER (WHERE i.state='open')::int,
	count(i.id) FILTER (WHERE i.state='closed')::int,m.created_at,m.updated_at`

var milestoneIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func scanMilestone(row scanner) (Milestone, error) {
	var m Milestone
	err := row.Scan(&m.ID, &m.Title, &m.Description, &m.State, &m.DueDate, &m.OpenIssues, &m.ClosedIssues, &m.CreatedAt, &m.UpdatedAt)
	return m, err
}

func (a *App) milestones(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT `+milestoneColumns+` FROM milestones m LEFT JOIN issues i ON i.milestone_id=m.id
		WHERE m.repository_id=$1 GROUP BY m.id ORDER BY m.created_at DESC LIMIT 100`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []Milestone{}
	for rows.Next() {
		m, err := scanMilestone(rows)
		if err != nil {
			serverError(w, err)
			return
		}
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, items)
}

func validMilestone(title, description, state, dueDate string) bool {
	if strings.TrimSpace(title) == "" || len(title) > 200 || len(description) > 10000 || (state != "open" && state != "closed") {
		return false
	}
	if dueDate == "" {
		return true
	}
	parsed, err := time.Parse("2006-01-02", dueDate)
	return err == nil && parsed.Format("2006-01-02") == dueDate
}

func (a *App) createMilestone(w http.ResponseWriter, r *http.Request) {
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
	var in struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		DueDate     string `json:"due_date"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if !validMilestone(in.Title, in.Description, "open", in.DueDate) {
		fail(w, 422, "validation_failed", "Provide a title up to 200 characters, description up to 10,000 characters, and optional YYYY-MM-DD due date.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	u := a.user(r)
	id := auth.ID()
	_, err = tx.Exec(r.Context(), `INSERT INTO milestones(id,repository_id,title,description,due_date,created_by)
		VALUES($1,$2,$3,$4,NULLIF($5,'')::date,$6)`, id, repo.ID, in.Title, in.Description, in.DueDate, u.ID)
	if conflict(err) {
		fail(w, 409, "already_exists", "A milestone with this title already exists.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'milestone.created',$2)`, u.ID, fmt.Sprintf("%s/%s:%s", repo.Owner, repo.Name, in.Title)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	m, err := a.milestoneByID(r, repo.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, m)
}

func (a *App) milestoneByID(r *http.Request, repoID, id string) (Milestone, error) {
	return scanMilestone(a.db.QueryRow(r.Context(), `SELECT `+milestoneColumns+` FROM milestones m LEFT JOIN issues i ON i.milestone_id=m.id
		WHERE m.repository_id=$1 AND m.id=$2 GROUP BY m.id`, repoID, id))
}

func (a *App) updateMilestone(w http.ResponseWriter, r *http.Request) {
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
	id := r.PathValue("id")
	if !milestoneIDPattern.MatchString(id) {
		fail(w, 404, "not_found", "Milestone not found.")
		return
	}
	var in struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		State       string `json:"state"`
		DueDate     string `json:"due_date"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if !validMilestone(in.Title, in.Description, in.State, in.DueDate) {
		fail(w, 422, "validation_failed", "Provide a title, description, open or closed state, and optional YYYY-MM-DD due date.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	result, err := tx.Exec(r.Context(), `UPDATE milestones SET title=$1,description=$2,state=$3,due_date=NULLIF($4,'')::date,updated_at=now()
		WHERE repository_id=$5 AND id=$6`, in.Title, in.Description, in.State, in.DueDate, repo.ID, id)
	if conflict(err) {
		fail(w, 409, "already_exists", "A milestone with this title already exists.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Milestone not found.")
		return
	}
	u := a.user(r)
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'milestone.updated',$2)`, u.ID, fmt.Sprintf("%s/%s:%s", repo.Owner, repo.Name, id)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	m, err := a.milestoneByID(r, repo.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, m)
}

func (a *App) issueMilestone(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	number, ok := issueNumber(w, r)
	if !ok {
		return
	}
	var id *string
	err := a.db.QueryRow(r.Context(), `SELECT milestone_id::text FROM issues WHERE repository_id=$1 AND number=$2`, repo.ID, number).Scan(&id)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if id == nil {
		respond(w, 200, map[string]any{"milestone": nil})
		return
	}
	m, err := a.milestoneByID(r, repo.ID, *id)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"milestone": m})
}

func (a *App) updateIssueMilestone(w http.ResponseWriter, r *http.Request) {
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
		MilestoneID *string `json:"milestone_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.MilestoneID != nil && !milestoneIDPattern.MatchString(*in.MilestoneID) {
		fail(w, 422, "validation_failed", "Choose a milestone in this repository.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var issueID string
	err = tx.QueryRow(r.Context(), `SELECT id FROM issues WHERE repository_id=$1 AND number=$2 FOR UPDATE`, repo.ID, number).Scan(&issueID)
	if err == pgx.ErrNoRows {
		fail(w, 404, "not_found", "Issue not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if in.MilestoneID != nil {
		var exists bool
		if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM milestones WHERE id=$1 AND repository_id=$2)`, *in.MilestoneID, repo.ID).Scan(&exists); err != nil {
			serverError(w, err)
			return
		}
		if !exists {
			fail(w, 422, "validation_failed", "Choose a milestone in this repository.")
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `UPDATE issues SET milestone_id=$1 WHERE id=$2`, in.MilestoneID, issueID); err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'issue.milestone_updated',$2)`, u.ID, fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, number)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.issueMilestone(w, r)
}
