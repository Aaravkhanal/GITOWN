package app

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type RepositoryMember struct {
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

func validMemberRole(role string) bool {
	return role == "read" || role == "triage" || role == "write" || role == "maintain"
}

// Repository capabilities by role:
//
//	read      clone, view, comment
//	triage    + manage issues, labels, assignees, reviewers, resolve conversations
//	write     + push, open and merge unite requests
//	maintain  + branch rules, issue templates, milestones, dismiss reviews, protected pushes
//	manage    owner or district admin: access, visibility, transfer, deletion
type RepositoryPermissions struct {
	Role     string `json:"role"`
	Read     bool   `json:"read"`
	Triage   bool   `json:"triage"`
	Write    bool   `json:"write"`
	Maintain bool   `json:"maintain"`
	Manage   bool   `json:"manage"`
}

func (a *App) repositoryPermissions(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	respond(w, 200, RepositoryPermissions{Role: repo.Role, Read: true, Triage: repo.CanTriage, Write: repo.CanWrite, Maintain: repo.CanMaintain, Manage: repo.CanManage})
}

// maintainedRepository admits owners, maintainers, and district admins.
func (a *App) maintainedRepository(w http.ResponseWriter, r *http.Request) *Repository {
	repo := a.access(w, r, false)
	if repo != nil && !repo.CanMaintain {
		fail(w, 403, "forbidden", "Repository maintain permission is required.")
		return nil
	}
	return repo
}

func (a *App) managedRepository(w http.ResponseWriter, r *http.Request) *Repository {
	repo := a.access(w, r, false)
	if repo != nil && !repo.CanManage {
		fail(w, 403, "forbidden", "Repository management permission is required.")
		return nil
	}
	return repo
}

func (a *App) members(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT u.username,u.display_name,rm.role,rm.created_at FROM repository_members rm JOIN users u ON u.id=rm.user_id WHERE rm.repository_id=$1 ORDER BY u.username`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	members := []RepositoryMember{}
	for rows.Next() {
		var member RepositoryMember
		if err = rows.Scan(&member.Username, &member.DisplayName, &member.Role, &member.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		members = append(members, member)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, members)
}

func (a *App) updateMember(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validMemberRole(in.Role) {
		fail(w, 422, "validation_failed", "Choose read, triage, write, or maintain.")
		return
	}
	username := strings.ToLower(r.PathValue("username"))
	var member RepositoryMember
	err := a.db.QueryRow(r.Context(), `UPDATE repository_members rm SET role=$1 FROM users u WHERE rm.repository_id=$2 AND rm.user_id=u.id AND u.username=$3 RETURNING u.username,u.display_name,rm.role,rm.created_at`, in.Role, repo.ID, username).Scan(&member.Username, &member.DisplayName, &member.Role, &member.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "collaborator_not_found", "Collaborator not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.member_updated',$2)`, a.user(r).ID, repo.Owner+"/"+repo.Name+":"+member.Username+":"+member.Role)
	respond(w, 200, member)
}

func (a *App) removeMember(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	username := strings.ToLower(r.PathValue("username"))
	result, err := a.db.Exec(r.Context(), `DELETE FROM repository_members rm USING users u WHERE rm.repository_id=$1 AND rm.user_id=u.id AND u.username=$2`, repo.ID, username)
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		fail(w, 404, "collaborator_not_found", "Collaborator not found.")
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.member_removed',$2)`, a.user(r).ID, repo.Owner+"/"+repo.Name+":"+username)
	respond(w, 200, map[string]bool{"removed": true})
}
