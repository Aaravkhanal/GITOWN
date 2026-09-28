package app

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/Aaravkhanal/GITOWN/internal/gitstore"
)

type Repository struct {
	ID            string    `json:"id"`
	OwnerID       string    `json:"-"`
	Owner         string    `json:"owner"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Visibility    string    `json:"visibility"`
	DefaultBranch string    `json:"default_branch"`
	CreatedAt     time.Time `json:"created_at"`
	CanWrite      bool      `json:"can_write"`
	CloneURL      string    `json:"clone_url"`
}

const repoColumns = `r.id,r.owner_id,u.username,r.name,r.description,r.visibility,r.default_branch,r.created_at`

type scanner interface{ Scan(...any) error }

func scanRepo(row scanner) (Repository, error) {
	var r Repository
	err := row.Scan(&r.ID, &r.OwnerID, &r.Owner, &r.Name, &r.Description, &r.Visibility, &r.DefaultBranch, &r.CreatedAt)
	return r, err
}
func (a *App) decorate(repo *Repository, u *User) {
	repo.CanWrite = u != nil && u.ID == repo.OwnerID
	repo.CloneURL = a.cfg.GitURL + "/" + repo.Owner + "/" + repo.Name + ".git"
}

func (a *App) access(w http.ResponseWriter, r *http.Request, write bool) *Repository {
	u := a.user(r)
	repo, err := scanRepo(a.db.QueryRow(r.Context(), `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id WHERE u.username=$1 AND r.name=$2`, r.PathValue("owner"), r.PathValue("repo")))
	if err != nil || (repo.Visibility == "private" && (u == nil || u.ID != repo.OwnerID)) {
		fail(w, 404, "not_found", "Repository not found.")
		return nil
	}
	a.decorate(&repo, u)
	if write && !repo.CanWrite {
		fail(w, 403, "forbidden", "Only the repository owner can make this change.")
		return nil
	}
	return &repo
}

func (a *App) repositories(w http.ResponseWriter, r *http.Request) {
	u := a.user(r)
	id := ""
	if u != nil {
		id = u.ID
	}
	query := `SELECT ` + repoColumns + ` FROM repositories r JOIN users u ON u.id=r.owner_id WHERE (r.visibility='public' OR r.owner_id::text=$1)`
	if r.URL.Query().Get("mine") == "true" {
		query += ` AND r.owner_id::text=$1`
	}
	query += ` ORDER BY r.created_at DESC LIMIT 100`
	rows, err := a.db.Query(r.Context(), query, id)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	repos := []Repository{}
	for rows.Next() {
		repo, err := scanRepo(rows)
		if err != nil {
			serverError(w, err)
			return
		}
		a.decorate(&repo, u)
		repos = append(repos, repo)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, repos)
}

func (a *App) createRepository(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Visibility  string `json:"visibility"`
		Readme      bool   `json:"readme"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Description = strings.TrimSpace(in.Description)
	if !repoSlug.MatchString(in.Name) || strings.HasSuffix(in.Name, ".git") || strings.Contains(in.Name, "..") || len(in.Description) > 500 || (in.Visibility != "private" && in.Visibility != "public") {
		fail(w, 422, "validation_failed", "Use a repository name with letters, numbers, dots, hyphens or underscores and select a visibility.")
		return
	}
	repo := Repository{ID: auth.ID(), OwnerID: u.ID, Owner: u.Username, Name: in.Name, Description: in.Description, Visibility: in.Visibility, DefaultBranch: "main"}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT id FROM users WHERE id=$1 FOR UPDATE`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	var count int
	if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM repositories WHERE owner_id=$1`, u.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 100 {
		fail(w, 422, "repository_limit", "This alpha supports up to 100 repositories per account.")
		return
	}
	err = tx.QueryRow(r.Context(), `INSERT INTO repositories(id,owner_id,name,description,visibility) VALUES($1,$2,$3,$4,$5) RETURNING created_at`, repo.ID, u.ID, repo.Name, repo.Description, repo.Visibility).Scan(&repo.CreatedAt)
	if conflict(err) {
		fail(w, 409, "repository_exists", "You already have a repository with that name.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	// The opaque directory is created before the row is committed; interrupted
	// creations can leave an unreferenced directory, never a readable partial repo.
	if err = a.git.Init(r.Context(), repo.ID, repo.Name, u.DisplayName, u.Username+"@users.gitown.local", in.Readme); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.created',$2)`, u.ID, u.Username+"/"+repo.Name); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.decorate(&repo, u)
	respond(w, 201, repo)
}

func (a *App) repository(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if _, err := os.Stat(a.git.Path(repo.ID)); err != nil {
		fail(w, 503, "storage_unavailable", "Repository storage is unavailable.")
		return
	}
	branches, err := a.git.Branches(r.Context(), repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"repository": repo, "branches": branches})
}

func (a *App) updateRepository(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, true)
	if repo == nil {
		return
	}
	var in struct {
		Description string `json:"description"`
		Visibility  string `json:"visibility"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Description = strings.TrimSpace(in.Description)
	if len(in.Description) > 500 || (in.Visibility != "private" && in.Visibility != "public") {
		fail(w, 422, "validation_failed", "Use a description up to 500 characters and select public or private visibility.")
		return
	}
	if _, err := a.db.Exec(
		r.Context(),
		`UPDATE repositories SET description=$1, visibility=$2 WHERE id=$3`,
		in.Description,
		in.Visibility,
		repo.ID,
	); err != nil {
		serverError(w, err)
		return
	}
	updated, err := scanRepo(a.db.QueryRow(r.Context(), `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id WHERE r.id=$1`, repo.ID))
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.updated',$2)`, updated.OwnerID, updated.Owner+"/"+updated.Name); err != nil {
		serverError(w, err)
		return
	}
	a.decorate(&updated, a.user(r))
	respond(w, 200, updated)
}
func (a *App) tree(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	branch := r.URL.Query().Get("ref")
	if branch == "" {
		branch = repo.DefaultBranch
	}
	result, err := a.git.Browse(r.Context(), repo.ID, branch, r.URL.Query().Get("path"))
	if errors.Is(err, gitstore.ErrNotFound) {
		fail(w, 404, "not_found", "Branch or file not found.")
		return
	}
	if errors.Is(err, gitstore.ErrTooLarge) {
		fail(w, 413, "too_large", "This file is too large to display. Clone the repository to view it.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, result)
}
func (a *App) commits(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	branch := r.URL.Query().Get("ref")
	if branch == "" {
		branch = repo.DefaultBranch
	}
	result, err := a.git.Commits(r.Context(), repo.ID, branch)
	if errors.Is(err, gitstore.ErrNotFound) {
		respond(w, 200, []gitstore.Commit{})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, result)
}
