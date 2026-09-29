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

func (a *App) addMember(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	var in struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if !slug.MatchString(in.Username) || !validMemberRole(in.Role) || in.Username == repo.Owner {
		fail(w, 422, "validation_failed", "Choose another GITOWN user and a valid collaborator role.")
		return
	}
	var member RepositoryMember
	err := a.db.QueryRow(r.Context(), `WITH selected_user AS (SELECT id,username,display_name FROM users WHERE username=$2), inserted AS (INSERT INTO repository_members(repository_id,user_id,role) SELECT $1,id,$3 FROM selected_user RETURNING user_id,role,created_at) SELECT u.username,u.display_name,i.role,i.created_at FROM inserted i JOIN selected_user u ON u.id=i.user_id`, repo.ID, in.Username, in.Role).Scan(&member.Username, &member.DisplayName, &member.Role, &member.CreatedAt)
	if conflict(err) {
		fail(w, 409, "collaborator_exists", "That user is already a collaborator.")
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "user_not_found", "No GITOWN user has that username.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.member_added',$2)`, repo.OwnerID, repo.Owner+"/"+repo.Name+":"+member.Username+":"+member.Role)
	respond(w, 201, member)
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
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.member_updated',$2)`, repo.OwnerID, repo.Owner+"/"+repo.Name+":"+member.Username+":"+member.Role)
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
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.member_removed',$2)`, repo.OwnerID, repo.Owner+"/"+repo.Name+":"+username)
	respond(w, 200, map[string]bool{"removed": true})
}
