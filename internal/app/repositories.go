package app

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/Aaravkhanal/GITOWN/internal/gitstore"
	"github.com/jackc/pgx/v5"
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
	CanTriage     bool      `json:"can_triage"`
	CanManage     bool      `json:"can_manage"`
	CanComment    bool      `json:"can_comment"`
	Role          string    `json:"role,omitempty"`
	Archived      bool      `json:"archived"`
	CloneURL      string    `json:"clone_url"`
	Homepage      string    `json:"homepage"`
	Stack         string    `json:"stack"`
	Language      string    `json:"language"`
	PushedAt      time.Time `json:"pushed_at"`
	SizeBytes     int64     `json:"size_bytes"`
	DistrictID    string    `json:"-"`
	District      string    `json:"district,omitempty"`
	SSHCloneURL   string    `json:"ssh_clone_url,omitempty"`
}

const repoColumns = `r.id,r.owner_id,u.username,r.name,r.description,r.visibility,r.default_branch,r.created_at,(r.archived_at IS NOT NULL),r.homepage,r.stack,r.language,r.pushed_at,r.size_bytes,COALESCE(r.district_id::text,''),COALESCE((SELECT d.slug FROM districts d WHERE d.id=r.district_id),'')`

type scanner interface{ Scan(...any) error }

func scanRepo(row scanner) (Repository, error) {
	var r Repository
	err := row.Scan(&r.ID, &r.OwnerID, &r.Owner, &r.Name, &r.Description, &r.Visibility, &r.DefaultBranch, &r.CreatedAt, &r.Archived, &r.Homepage, &r.Stack, &r.Language, &r.PushedAt, &r.SizeBytes, &r.DistrictID, &r.District)
	return r, err
}

func (a *App) scanRepoList(r *http.Request, rows pgx.Rows) ([]Repository, error) {
	defer rows.Close()
	items := []Repository{}
	viewer := a.user(r)
	for rows.Next() {
		repo, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		if err = a.decorate(r.Context(), &repo, viewer); err != nil {
			return nil, err
		}
		items = append(items, repo)
	}
	return items, rows.Err()
}
func (a *App) decorate(ctx context.Context, repo *Repository, u *User) error {
	if u != nil && u.ID == repo.OwnerID {
		repo.Role = "owner"
	} else if u != nil {
		err := a.db.QueryRow(ctx, `SELECT role FROM repository_members WHERE repository_id=$1 AND user_id=$2`, repo.ID, u.ID).Scan(&repo.Role)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	manage := repo.Role == "owner"
	if u != nil && repo.DistrictID != "" {
		var districtOwner, base, memberRole string
		err := a.db.QueryRow(ctx, `SELECT d.owner_id::text,d.base_permission,COALESCE((SELECT m.role FROM district_members m WHERE m.district_id=d.id AND m.user_id=$2),'') FROM districts d WHERE d.id=$1`, repo.DistrictID, u.ID).Scan(&districtOwner, &base, &memberRole)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && (u.ID == districtOwner || memberRole == "admin") {
			if repo.Role != "owner" {
				repo.Role = higherRole(repo.Role, "maintain")
			}
			manage = true
		} else if err == nil && memberRole == "member" {
			if base == "read" || base == "triage" || base == "write" {
				repo.Role = higherRole(repo.Role, base)
			}
			if repo.Visibility == "internal" && repo.Role == "" {
				repo.Role = "read"
			}
		}
	}
	repo.CanWrite = repo.Role == "owner" || repo.Role == "maintain" || repo.Role == "write"
	repo.CanTriage = repo.CanWrite || repo.Role == "triage"
	repo.CanManage = manage || repo.Role == "owner"
	repo.CanComment = u != nil && !repo.Archived
	repo.CloneURL = a.cfg.GitURL + "/" + repo.Owner + "/" + repo.Name + ".git"
	if host := strings.TrimSpace(os.Getenv("GITOWN_SSH_HOST")); host != "" && !strings.ContainsAny(host, " \t\r\n") {
		repo.SSHCloneURL = "git@" + host + ":" + repo.Owner + "/" + repo.Name + ".git"
	}
	return nil
}

func (a *App) access(w http.ResponseWriter, r *http.Request, write bool) *Repository {
	u := a.user(r)
	repo, err := scanRepo(a.db.QueryRow(r.Context(), `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id WHERE u.username=$1 AND r.name=$2 AND r.deleted_at IS NULL`, r.PathValue("owner"), r.PathValue("repo")))
	if err != nil {
		fail(w, 404, "not_found", "Repository not found.")
		return nil
	}
	if err = a.decorate(r.Context(), &repo, u); err != nil {
		serverError(w, err)
		return nil
	}
	if repo.Visibility != "public" && repo.Role == "" {
		fail(w, 404, "not_found", "Repository not found.")
		return nil
	}
	if write && !repo.CanWrite {
		fail(w, 403, "forbidden", "Repository write permission is required.")
		return nil
	}
	if write && repo.Archived {
		fail(w, 409, "repository_archived", "Unarchive this repository before making changes.")
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
	query := `SELECT ` + repoColumns + ` FROM repositories r JOIN users u ON u.id=r.owner_id WHERE r.deleted_at IS NULL AND (r.visibility='public' OR r.owner_id::text=$1 OR EXISTS (SELECT 1 FROM repository_members rm WHERE rm.repository_id=r.id AND rm.user_id::text=$1) OR EXISTS (SELECT 1 FROM districts d WHERE d.id=r.district_id AND $1<>'' AND (d.owner_id::text=$1 OR EXISTS (SELECT 1 FROM district_members dm WHERE dm.district_id=d.id AND dm.user_id::text=$1 AND dm.role='admin') OR (EXISTS (SELECT 1 FROM district_members dm WHERE dm.district_id=d.id AND dm.user_id::text=$1) AND (r.visibility='internal' OR d.base_permission<>'none')))))`
	if r.URL.Query().Get("mine") == "true" {
		query += ` AND (r.owner_id::text=$1 OR EXISTS (SELECT 1 FROM repository_members rm WHERE rm.repository_id=r.id AND rm.user_id::text=$1) OR EXISTS (SELECT 1 FROM districts d WHERE d.id=r.district_id AND (d.owner_id::text=$1 OR EXISTS (SELECT 1 FROM district_members dm WHERE dm.district_id=d.id AND dm.user_id::text=$1))))`
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
		if err = a.decorate(r.Context(), &repo, u); err != nil {
			serverError(w, err)
			return
		}
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
		District    string `json:"district"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Description = strings.TrimSpace(in.Description)
	in.District = strings.ToLower(strings.TrimSpace(in.District))
	if !repoSlug.MatchString(in.Name) || strings.HasSuffix(in.Name, ".git") || strings.Contains(in.Name, "..") || len(in.Description) > 500 {
		fail(w, 422, "validation_failed", "Use a repository name with letters, numbers, dots, hyphens or underscores and select a visibility.")
		return
	}
	repo := Repository{ID: auth.ID(), OwnerID: u.ID, Owner: u.Username, Name: in.Name, Description: in.Description, Visibility: in.Visibility, DefaultBranch: "main", District: in.District}
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
	if in.District != "" {
		district, allowed, err := a.districtForCreate(r.Context(), tx, in.District, u.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		if district == nil {
			if allowed {
				fail(w, 404, "not_found", "District not found.")
			} else {
				fail(w, 403, "forbidden", "You cannot create a repository in this district.")
			}
			return
		}
		repo.DistrictID = district.ID
		repo.District = district.Slug
	}
	if repo.Visibility != "public" && repo.Visibility != "private" && !(repo.Visibility == "internal" && repo.DistrictID != "") {
		fail(w, 422, "validation_failed", "Visibility must be public, private, or internal inside a district.")
		return
	}
	var districtValue any
	if repo.DistrictID != "" {
		districtValue = repo.DistrictID
	}
	err = tx.QueryRow(r.Context(), `INSERT INTO repositories(id,owner_id,name,description,visibility,district_id) VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at,pushed_at`, repo.ID, u.ID, repo.Name, repo.Description, repo.Visibility, districtValue).Scan(&repo.CreatedAt, &repo.PushedAt)
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
	_ = a.noteRepositoryFacts(r.Context(), &repo)
	if err = a.decorate(r.Context(), &repo, u); err != nil {
		serverError(w, err)
		return
	}
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
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanManage {
		fail(w, 403, "forbidden", "Repository management permission is required.")
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
	if len(in.Description) > 500 || (in.Visibility != "public" && in.Visibility != "private" && !(in.Visibility == "internal" && repo.DistrictID != "")) {
		fail(w, 422, "validation_failed", "Use a description up to 500 characters and select public, private, or internal visibility.")
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
	if err = a.decorate(r.Context(), &updated, a.user(r)); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, updated)
}

func (a *App) renameRepository(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	if !repoSlug.MatchString(in.Name) || strings.HasSuffix(in.Name, ".git") || strings.Contains(in.Name, "..") {
		fail(w, 422, "validation_failed", "Use a repository name with letters, numbers, dots, hyphens or underscores.")
		return
	}
	if in.Name == repo.Name {
		respond(w, 200, repo)
		return
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE repositories SET name=$1 WHERE id=$2`, in.Name, repo.ID); conflict(err) {
		fail(w, 409, "repository_exists", "You already have a repository with that name.")
		return
	} else if err != nil {
		serverError(w, err)
		return
	}
	oldTarget := repo.Owner + "/" + repo.Name
	repo.Name = in.Name
	repo.CloneURL = a.cfg.GitURL + "/" + repo.Owner + "/" + repo.Name + ".git"
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.renamed',$2)`, repo.OwnerID, oldTarget+"->"+repo.Owner+"/"+repo.Name)
	respond(w, 200, repo)
}

func (a *App) setRepositoryArchived(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	archived := r.PathValue("action") == "archive"
	if !archived && r.PathValue("action") != "unarchive" {
		fail(w, 404, "not_found", "Action not found.")
		return
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE repositories SET archived_at=CASE WHEN $1 THEN now() ELSE NULL END WHERE id=$2`, archived, repo.ID); err != nil {
		serverError(w, err)
		return
	}
	repo.Archived = archived
	action := "repository.unarchived"
	if archived {
		action = "repository.archived"
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,$2,$3)`, repo.OwnerID, action, repo.Owner+"/"+repo.Name)
	respond(w, 200, repo)
}

type DeletedRepository struct {
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	Name       string    `json:"name"`
	DeletedAt  time.Time `json:"deleted_at"`
	PurgeAfter time.Time `json:"purge_after"`
}

func (a *App) deleteRepository(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	var in struct {
		Confirmation string `json:"confirmation"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Confirmation != repo.Name {
		fail(w, 422, "confirmation_failed", "Type the repository name exactly to schedule deletion.")
		return
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE repositories SET deleted_at=now(),purge_after=now()+interval '30 days' WHERE id=$1`, repo.ID); err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.deletion_scheduled',$2)`, repo.OwnerID, repo.Owner+"/"+repo.Name)
	respond(w, 200, map[string]any{"deleted": true, "recoverable_days": 30})
}

func (a *App) deletedRepositories(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT r.id,u.username,r.name,r.deleted_at,r.purge_after FROM repositories r JOIN users u ON u.id=r.owner_id WHERE r.owner_id=$1 AND r.deleted_at IS NOT NULL ORDER BY r.deleted_at DESC`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []DeletedRepository{}
	for rows.Next() {
		var item DeletedRepository
		if err = rows.Scan(&item.ID, &item.Owner, &item.Name, &item.DeletedAt, &item.PurgeAfter); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, item)
	}
	respond(w, 200, items)
}

func (a *App) restoreRepository(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var owner, name string
	err := a.db.QueryRow(r.Context(), `UPDATE repositories r SET deleted_at=NULL,purge_after=NULL WHERE id=$1 AND owner_id=$2 AND deleted_at IS NOT NULL RETURNING (SELECT username FROM users WHERE id=r.owner_id),r.name`, r.PathValue("id"), u.ID).Scan(&owner, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Deleted repository not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.restored',$2)`, u.ID, owner+"/"+name)
	respond(w, 200, map[string]string{"owner": owner, "name": name})
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

func (a *App) raw(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	branch := r.URL.Query().Get("ref")
	if branch == "" {
		branch = repo.DefaultBranch
	}
	path := r.URL.Query().Get("path")
	content, err := a.git.Blob(r.Context(), repo.ID, branch, path)
	if errors.Is(err, gitstore.ErrNotFound) {
		fail(w, 404, "not_found", "Branch or file not found.")
		return
	}
	if errors.Is(err, gitstore.ErrTooLarge) {
		fail(w, 413, "too_large", "This file is too large for a browser download. Clone the repository instead.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `inline; filename="`+strings.ReplaceAll(filepath.Base(path), `"`, "")+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (a *App) updateContent(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, true)
	if repo == nil {
		return
	}
	u := a.user(r)
	var in struct {
		Branch       string `json:"branch"`
		Path         string `json:"path"`
		Content      string `json:"content"`
		Message      string `json:"message"`
		ExpectedHead string `json:"expected_head"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Branch = strings.TrimSpace(in.Branch)
	in.Path = strings.TrimSpace(in.Path)
	in.Message = strings.TrimSpace(in.Message)
	if in.Branch == "" || in.Path == "" || in.Message == "" || len(in.Message) > 200 || len(in.ExpectedHead) != 40 || len(in.Content) > gitstore.MaxBlob {
		fail(w, 422, "validation_failed", "Provide a branch, safe file path, expected head SHA, content up to 512 KiB, and a commit message up to 200 characters.")
		return
	}
	if !a.allowBrowserBranchEdit(w, r, repo.ID, in.Branch, repo.Role) {
		return
	}
	sha, err := a.git.CommitFile(r.Context(), repo.ID, in.Branch, in.Path, []byte(in.Content), in.Message, u.DisplayName, u.Username+"@users.gitown.local", in.ExpectedHead)
	if errors.Is(err, gitstore.ErrConflict) {
		fail(w, 409, "branch_changed", "The branch changed while you were editing. Refresh the file and apply your changes again.")
		return
	}
	if errors.Is(err, gitstore.ErrNotFound) {
		fail(w, 422, "invalid_path_or_branch", "The branch or file path is invalid.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.web_commit',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+in.Branch+":"+in.Path+":"+sha)
	_ = a.noteRepositoryFacts(r.Context(), repo)
	a.recordRefEvents(r.Context(), repo.ID, u.ID, []refUpdate{{Old: in.ExpectedHead, New: sha, Ref: "refs/heads/" + in.Branch}}, "api")
	respond(w, 201, map[string]string{"sha": sha, "branch": in.Branch, "path": in.Path})
}

func (a *App) deleteContent(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, true)
	if repo == nil {
		return
	}
	u := a.user(r)
	var in struct {
		Branch       string `json:"branch"`
		Path         string `json:"path"`
		Message      string `json:"message"`
		ExpectedHead string `json:"expected_head"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Branch = strings.TrimSpace(in.Branch)
	in.Path = strings.TrimSpace(in.Path)
	in.Message = strings.TrimSpace(in.Message)
	if in.Branch == "" || in.Path == "" || in.Message == "" || len(in.Message) > 200 || len(in.ExpectedHead) != 40 {
		fail(w, 422, "validation_failed", "Provide a branch, safe file path, expected head SHA, and a commit message up to 200 characters.")
		return
	}
	if !a.allowBrowserBranchEdit(w, r, repo.ID, in.Branch, repo.Role) {
		return
	}
	sha, err := a.git.DeleteFile(r.Context(), repo.ID, in.Branch, in.Path, in.Message, u.DisplayName, u.Username+"@users.gitown.local", in.ExpectedHead)
	if errors.Is(err, gitstore.ErrConflict) {
		fail(w, 409, "branch_changed", "The branch changed before the file could be deleted. Refresh and try again.")
		return
	}
	if errors.Is(err, gitstore.ErrNotFound) {
		fail(w, 422, "invalid_path_or_branch", "The branch or file path is invalid.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.web_delete',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+in.Branch+":"+in.Path+":"+sha)
	_ = a.noteRepositoryFacts(r.Context(), repo)
	a.recordRefEvents(r.Context(), repo.ID, u.ID, []refUpdate{{Old: in.ExpectedHead, New: sha, Ref: "refs/heads/" + in.Branch}}, "api")
	respond(w, 200, map[string]string{"sha": sha, "branch": in.Branch, "path": in.Path})
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
	skip := 0
	if raw := r.URL.Query().Get("skip"); raw != "" {
		var convErr error
		skip, convErr = strconv.Atoi(raw)
		if convErr != nil || skip < 0 || skip > 5000 {
			fail(w, 422, "validation_failed", "Skip must be between zero and 5000.")
			return
		}
	}
	var result []gitstore.Commit
	var err error
	if path := r.URL.Query().Get("path"); path != "" {
		result, err = a.git.FileCommits(r.Context(), repo.ID, branch, path)
	} else {
		result, err = a.git.History(r.Context(), repo.ID, branch, skip)
	}
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
