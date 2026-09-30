package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

type District struct {
	ID                        string    `json:"id"`
	Slug                      string    `json:"slug"`
	Name                      string    `json:"name"`
	Description               string    `json:"description"`
	Visibility                string    `json:"visibility"`
	RepoCreation              string    `json:"repo_creation"`
	BasePermission            string    `json:"base_permission"`
	AllowPublic               bool      `json:"allow_public"`
	AllowOutsideCollaborators bool      `json:"allow_outside_collaborators"`
	OwnerID                   string    `json:"-"`
	Owner                     string    `json:"owner"`
	Role                      string    `json:"role,omitempty"`
	CreatedAt                 time.Time `json:"created_at"`
}

var secretNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,40}$`)

var errSecretUnconfigured = errors.New("secret key unconfigured")

func (a *App) districtForCreate(ctx context.Context, tx pgx.Tx, slugName, userID string) (*District, bool, error) {
	if !slug.MatchString(slugName) {
		return nil, true, nil
	}
	var d District
	var member string
	err := tx.QueryRow(ctx, `SELECT d.id::text,d.slug,d.visibility,d.repo_creation,d.owner_id::text,COALESCE((SELECT m.role FROM district_members m WHERE m.district_id=d.id AND m.user_id=$2),'') FROM districts d WHERE d.slug=$1`, slugName, userID).Scan(&d.ID, &d.Slug, &d.Visibility, &d.RepoCreation, &d.OwnerID, &member)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	known := userID == d.OwnerID || member != ""
	if d.Visibility == "private" && !known {
		return nil, true, nil
	}
	if !districtCreateAllowed(userID, d, member) {
		return nil, false, nil
	}
	return &d, true, nil
}

func districtCreateAllowed(userID string, d District, member string) bool {
	if userID == d.OwnerID {
		return true
	}
	switch d.RepoCreation {
	case "member":
		return member == "admin" || member == "member"
	case "admin":
		return member == "admin"
	default:
		return false
	}
}

func (a *App) presentDistrict(ctx context.Context, slugName, userID string) (District, error) {
	var d District
	var member string
	err := a.db.QueryRow(ctx, `SELECT d.id::text,d.slug,d.name,d.description,d.visibility,d.repo_creation,d.base_permission,d.allow_public,d.allow_outside_collaborators,d.owner_id::text,u.username,d.created_at,COALESCE((SELECT m.role FROM district_members m WHERE m.district_id=d.id AND m.user_id::text=$2),'') FROM districts d JOIN users u ON u.id=d.owner_id WHERE d.slug=$1`, slugName, userID).Scan(&d.ID, &d.Slug, &d.Name, &d.Description, &d.Visibility, &d.RepoCreation, &d.BasePermission, &d.AllowPublic, &d.AllowOutsideCollaborators, &d.OwnerID, &d.Owner, &d.CreatedAt, &member)
	if err != nil {
		return District{}, err
	}
	if userID != "" && userID == d.OwnerID {
		d.Role = "owner"
	} else {
		d.Role = member
	}
	return d, nil
}

func (a *App) loadDistrict(w http.ResponseWriter, r *http.Request, admin bool) *District {
	userID := ""
	if u := a.user(r); u != nil {
		userID = u.ID
	}
	d, err := a.presentDistrict(r.Context(), r.PathValue("slug"), userID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "District not found.")
		return nil
	}
	if err != nil {
		serverError(w, err)
		return nil
	}
	if d.Visibility == "private" && d.Role == "" {
		fail(w, 404, "not_found", "District not found.")
		return nil
	}
	if admin && d.Role != "owner" && d.Role != "admin" {
		fail(w, 403, "forbidden", "District administration permission is required.")
		return nil
	}
	return &d
}

func (a *App) districts(w http.ResponseWriter, r *http.Request) {
	userID := ""
	if u := a.user(r); u != nil {
		userID = u.ID
	}
	rows, err := a.db.Query(r.Context(), `SELECT d.id::text,d.slug,d.name,d.description,d.visibility,d.repo_creation,d.base_permission,d.allow_public,d.allow_outside_collaborators,d.owner_id::text,u.username,d.created_at,COALESCE((SELECT m.role FROM district_members m WHERE m.district_id=d.id AND m.user_id::text=$1),'') FROM districts d JOIN users u ON u.id=d.owner_id WHERE d.visibility='public' OR ($1<>'' AND (d.owner_id::text=$1 OR EXISTS (SELECT 1 FROM district_members m WHERE m.district_id=d.id AND m.user_id::text=$1))) ORDER BY d.created_at DESC LIMIT 50`, userID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []District{}
	for rows.Next() {
		var d District
		var member string
		if err = rows.Scan(&d.ID, &d.Slug, &d.Name, &d.Description, &d.Visibility, &d.RepoCreation, &d.BasePermission, &d.AllowPublic, &d.AllowOutsideCollaborators, &d.OwnerID, &d.Owner, &d.CreatedAt, &member); err != nil {
			serverError(w, err)
			return
		}
		if userID != "" && userID == d.OwnerID {
			d.Role = "owner"
		} else {
			d.Role = member
		}
		items = append(items, d)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) createDistrict(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Slug        string `json:"slug"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Visibility  string `json:"visibility"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if in.Visibility == "" {
		in.Visibility = "public"
	}
	if !slug.MatchString(in.Slug) || in.Name == "" || len(in.Name) > 80 || len(in.Description) > 500 || (in.Visibility != "public" && in.Visibility != "private") {
		fail(w, 422, "validation_failed", "Use a district slug, a name up to 80 characters, and public or private visibility.")
		return
	}
	var count int
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM districts WHERE owner_id=$1`, u.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 30 {
		fail(w, 422, "district_limit", "An account can own up to 30 districts.")
		return
	}
	d := District{ID: auth.ID(), Slug: in.Slug, Name: in.Name, Description: in.Description, Visibility: in.Visibility, RepoCreation: "admin", BasePermission: "read", OwnerID: u.ID, Owner: u.Username, Role: "owner"}
	err := a.db.QueryRow(r.Context(), `INSERT INTO districts(id,slug,name,description,visibility,owner_id) VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at`, d.ID, d.Slug, d.Name, d.Description, d.Visibility, u.ID).Scan(&d.CreatedAt)
	if conflict(err) {
		fail(w, 409, "district_exists", "A district with that slug already exists.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'district.created',$2)`, u.ID, "district/"+d.Slug)
	respond(w, 201, d)
}

func (a *App) district(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, false)
	if d == nil {
		return
	}
	respond(w, 200, d)
}

func (a *App) updateDistrict(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, false)
	if d == nil {
		return
	}
	if d.Role != "owner" {
		fail(w, 403, "forbidden", "Only the district owner can change district settings.")
		return
	}
	var in struct {
		Description               *string `json:"description"`
		Visibility                *string `json:"visibility"`
		RepoCreation              *string `json:"repo_creation"`
		BasePermission            *string `json:"base_permission"`
		AllowPublic               *bool   `json:"allow_public"`
		AllowOutsideCollaborators *bool   `json:"allow_outside_collaborators"`
	}
	if !decode(w, r, &in) {
		return
	}
	description := d.Description
	visibility := d.Visibility
	creation := d.RepoCreation
	base := d.BasePermission
	allowPublic := d.AllowPublic
	allowOutside := d.AllowOutsideCollaborators
	if in.Description != nil {
		description = strings.TrimSpace(*in.Description)
	}
	if in.Visibility != nil {
		visibility = *in.Visibility
	}
	if in.RepoCreation != nil {
		creation = *in.RepoCreation
	}
	if in.BasePermission != nil {
		base = *in.BasePermission
	}
	if in.AllowPublic != nil {
		allowPublic = *in.AllowPublic
	}
	if in.AllowOutsideCollaborators != nil {
		allowOutside = *in.AllowOutsideCollaborators
	}
	if len(description) > 500 || (visibility != "public" && visibility != "private") || (creation != "owner" && creation != "admin" && creation != "member") || (base != "none" && base != "read" && base != "triage" && base != "write") {
		fail(w, 422, "validation_failed", "Check the description, visibility, repository creation policy, and base permission.")
		return
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE districts SET description=$1,visibility=$2,repo_creation=$3,base_permission=$4,allow_public=$5,allow_outside_collaborators=$6 WHERE id=$7`, description, visibility, creation, base, allowPublic, allowOutside, d.ID); err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'district.updated',$2)`, d.OwnerID, "district/"+d.Slug)
	updated, err := a.presentDistrict(r.Context(), d.Slug, d.OwnerID)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, updated)
}

func (a *App) districtMembers(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, false)
	if d == nil {
		return
	}
	if d.Visibility == "private" && d.Role == "" {
		fail(w, 404, "not_found", "District not found.")
		return
	}
	if d.Role == "" {
		fail(w, 403, "forbidden", "District membership is required.")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT u.username,u.display_name,m.role,m.created_at FROM district_members m JOIN users u ON u.id=m.user_id WHERE m.district_id=$1 ORDER BY u.username`, d.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var username, display, role string
		var created time.Time
		if err = rows.Scan(&username, &display, &role, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"username": username, "display_name": display, "role": role, "created_at": created})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) addDistrictMember(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, true)
	if d == nil {
		return
	}
	u := a.user(r)
	var in struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	if !slug.MatchString(in.Username) || (in.Role != "admin" && in.Role != "member") {
		fail(w, 422, "validation_failed", "Name an existing account and choose admin or member.")
		return
	}
	if in.Role == "admin" && d.Role != "owner" {
		fail(w, 403, "forbidden", "Only the district owner can grant the admin role.")
		return
	}
	var userID string
	err := a.db.QueryRow(r.Context(), `SELECT id::text FROM users WHERE username=$1`, in.Username).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Account not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if userID == d.OwnerID {
		fail(w, 422, "validation_failed", "The district owner is already an owner and is not added as a member.")
		return
	}
	var count int
	if err = a.db.QueryRow(r.Context(), `SELECT count(*) FROM district_members WHERE district_id=$1`, d.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 100 {
		fail(w, 422, "member_limit", "A district can have up to 100 members.")
		return
	}
	if _, err = a.db.Exec(r.Context(), `INSERT INTO district_members(district_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT (district_id,user_id) DO UPDATE SET role=EXCLUDED.role`, d.ID, userID, in.Role); err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'district.member_changed',$2)`, u.ID, "district/"+d.Slug+"/members/"+in.Username)
	respond(w, 200, map[string]string{"username": in.Username, "role": in.Role})
}

func (a *App) removeDistrictMember(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, true)
	if d == nil {
		return
	}
	username := strings.ToLower(r.PathValue("username"))
	var role, userID string
	err := a.db.QueryRow(r.Context(), `SELECT m.role,m.user_id::text FROM district_members m JOIN users u ON u.id=m.user_id WHERE m.district_id=$1 AND u.username=$2`, d.ID, username).Scan(&role, &userID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "District member not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if role == "admin" && d.Role != "owner" {
		fail(w, 403, "forbidden", "Only the district owner can remove an admin.")
		return
	}
	if _, err = a.db.Exec(r.Context(), `DELETE FROM district_members WHERE district_id=$1 AND user_id=$2`, d.ID, userID); err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'district.member_removed',$2)`, a.user(r).ID, "district/"+d.Slug+"/members/"+username)
	respond(w, 200, map[string]bool{"removed": true})
}

func (a *App) districtCrews(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, false)
	if d == nil {
		return
	}
	if d.Role == "" {
		fail(w, 403, "forbidden", "District membership is required.")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT c.slug,c.name,c.description,c.created_at,(SELECT count(*)::int FROM crew_members m WHERE m.crew_id=c.id) FROM crews c WHERE c.district_id=$1 ORDER BY c.slug`, d.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var crewSlug, name, description string
		var created time.Time
		var count int
		if err = rows.Scan(&crewSlug, &name, &description, &created, &count); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"slug": crewSlug, "name": name, "description": description, "created_at": created, "members": count})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) createCrew(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, true)
	if d == nil {
		return
	}
	var in struct {
		Slug        string `json:"slug"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if !slug.MatchString(in.Slug) || in.Name == "" || len(in.Name) > 80 || len(in.Description) > 300 {
		fail(w, 422, "validation_failed", "Use a crew slug, a name up to 80 characters, and a description up to 300 characters.")
		return
	}
	var count int
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM crews WHERE district_id=$1`, d.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 30 {
		fail(w, 422, "crew_limit", "A district can have up to 30 crews.")
		return
	}
	id := auth.ID()
	var created time.Time
	err := a.db.QueryRow(r.Context(), `INSERT INTO crews(id,district_id,slug,name,description) VALUES($1,$2,$3,$4,$5) RETURNING created_at`, id, d.ID, in.Slug, in.Name, in.Description).Scan(&created)
	if conflict(err) {
		fail(w, 409, "crew_exists", "This district already has a crew with that slug.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'crew.created',$2)`, a.user(r).ID, "district/"+d.Slug+"/crews/"+in.Slug)
	respond(w, 201, map[string]any{"slug": in.Slug, "name": in.Name, "description": in.Description, "created_at": created})
}

func (a *App) addCrewMember(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, true)
	if d == nil {
		return
	}
	var crewID string
	err := a.db.QueryRow(r.Context(), `SELECT id::text FROM crews WHERE district_id=$1 AND slug=$2`, d.ID, r.PathValue("crew")).Scan(&crewID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Crew not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var in struct {
		Username string `json:"username"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	var userID string
	err = a.db.QueryRow(r.Context(), `SELECT id::text FROM users WHERE username=$1`, in.Username).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Account not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if userID != d.OwnerID {
		var memberRole string
		err = a.db.QueryRow(r.Context(), `SELECT role FROM district_members WHERE district_id=$1 AND user_id=$2`, d.ID, userID).Scan(&memberRole)
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 422, "validation_failed", "Add the account to the district before adding it to a crew.")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = a.db.Exec(r.Context(), `INSERT INTO crew_members(crew_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, crewID, userID); err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'crew.member_added',$2)`, a.user(r).ID, "district/"+d.Slug+"/crews/"+r.PathValue("crew")+"/"+in.Username)
	respond(w, 200, map[string]string{"username": in.Username})
}

func (a *App) removeCrewMember(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, true)
	if d == nil {
		return
	}
	tag, err := a.db.Exec(r.Context(), `DELETE FROM crew_members m USING crews c, users u WHERE m.crew_id=c.id AND u.id=m.user_id AND c.district_id=$1 AND c.slug=$2 AND u.username=$3`, d.ID, r.PathValue("crew"), strings.ToLower(r.PathValue("username")))
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Crew member not found.")
		return
	}
	respond(w, 200, map[string]bool{"removed": true})
}

func (a *App) districtRepos(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, false)
	if d == nil {
		return
	}
	userID := ""
	if u := a.user(r); u != nil {
		userID = u.ID
	}
	rows, err := a.db.Query(r.Context(), `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id WHERE r.district_id=$1 AND r.deleted_at IS NULL AND (r.visibility='public' OR $2<>'' AND (r.owner_id::text=$2 OR EXISTS (SELECT 1 FROM repository_members rm WHERE rm.repository_id=r.id AND rm.user_id::text=$2) OR $3 IN ('owner','admin') OR (r.visibility='internal' AND $3<>'') OR ($3='member' AND $4<>'none'))) ORDER BY r.pushed_at DESC LIMIT 100`, d.ID, userID, d.Role, d.BasePermission)
	if err != nil {
		serverError(w, err)
		return
	}
	items, err := a.scanRepoList(r, rows)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) updateRepositoryDistrict(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	var in struct {
		Slug string `json:"slug"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	if in.Slug == "" {
		if repo.Role != "owner" && !a.districtOwnedBy(r.Context(), repo.DistrictID, u.ID) {
			fail(w, 403, "forbidden", "The repository owner or district owner can change the district.")
			return
		}
		if repo.Visibility == "internal" {
			fail(w, 422, "validation_failed", "Change visibility away from internal before removing the district.")
			return
		}
		if _, err := a.db.Exec(r.Context(), `UPDATE repositories SET district_id=NULL WHERE id=$1`, repo.ID); err != nil {
			serverError(w, err)
			return
		}
		_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.district_changed',$2)`, u.ID, repo.Owner+"/"+repo.Name)
		respond(w, 200, map[string]string{"district": ""})
		return
	}
	d, err := a.presentDistrict(r.Context(), in.Slug, u.ID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && d.Visibility == "private" && d.Role == "") {
		fail(w, 404, "not_found", "District not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if repo.Role != "owner" && u.ID != d.OwnerID {
		fail(w, 403, "forbidden", "The repository owner or district owner can change the district.")
		return
	}
	if _, err = a.db.Exec(r.Context(), `UPDATE repositories SET district_id=$1 WHERE id=$2`, d.ID, repo.ID); err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.district_changed',$2)`, u.ID, repo.Owner+"/"+repo.Name)
	respond(w, 200, map[string]string{"district": d.Slug})
}

func (a *App) districtOwnedBy(ctx context.Context, districtID, userID string) bool {
	if districtID == "" || userID == "" {
		return false
	}
	var owner string
	err := a.db.QueryRow(ctx, `SELECT owner_id::text FROM districts WHERE id=$1`, districtID).Scan(&owner)
	return err == nil && owner == userID
}

func (a *App) districtAudit(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, true)
	if d == nil {
		return
	}
	prefix := "district/" + d.Slug
	limit := 100
	if r.URL.Query().Get("download") == "1" {
		limit = 1000
	}
	rows, err := a.db.Query(r.Context(), `SELECT COALESCE(u.username,''),e.action,e.target,e.created_at FROM audit_events e LEFT JOIN users u ON u.id=e.actor_id WHERE e.target=$1 OR e.target LIKE $1 || '/%' OR EXISTS (SELECT 1 FROM repositories r JOIN users owner ON owner.id=r.owner_id WHERE r.district_id=$2 AND (e.target=owner.username||'/'||r.name OR e.target LIKE owner.username||'/'||r.name||':%' OR e.target LIKE owner.username||'/'||r.name||'/%')) ORDER BY e.created_at DESC LIMIT $3`, prefix, d.ID, limit)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	exported := [][]string{}
	for rows.Next() {
		var username, action, target string
		var created time.Time
		if err = rows.Scan(&username, &action, &target, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"actor": username, "action": action, "target": target, "created_at": created})
		exported = append(exported, []string{username, action, target, created.UTC().Format(time.RFC3339)})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	if r.URL.Query().Get("download") == "1" {
		a.writeAuditExport(w, d.Slug, exported)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func secretKey() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv("GITOWN_SECRET_KEY"))
	if raw == "" {
		return nil, errSecretUnconfigured
	}
	sum := sha256.Sum256([]byte(raw))
	return sum[:], nil
}

func sealSecret(plain string) ([]byte, []byte, error) {
	key, err := secretKey()
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return gcm.Seal(nil, nonce, []byte(plain), nil), nonce, nil
}

func openSecret(ciphertext, nonce []byte) (string, error) {
	key, err := secretKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (a *App) districtSecrets(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, true)
	if d == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT name,created_at FROM district_secrets WHERE district_id=$1 ORDER BY name`, d.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var name string
		var created time.Time
		if err = rows.Scan(&name, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"name": name, "created_at": created})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) putDistrictSecret(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, true)
	if d == nil {
		return
	}
	var in struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !secretNamePattern.MatchString(in.Name) || in.Value == "" || len(in.Value) > 8192 {
		fail(w, 422, "validation_failed", "Use a secret name of letters, numbers, dots, underscores, or hyphens and a value up to 8 KB.")
		return
	}
	ciphertext, nonce, err := sealSecret(in.Value)
	if errors.Is(err, errSecretUnconfigured) {
		fail(w, 503, "secret_key_unconfigured", "Set GITOWN_SECRET_KEY before storing district secrets.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var count int
	if err = a.db.QueryRow(r.Context(), `SELECT count(*) FROM district_secrets WHERE district_id=$1`, d.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 50 {
		fail(w, 422, "secret_limit", "A district can store up to 50 secrets.")
		return
	}
	if _, err = a.db.Exec(r.Context(), `INSERT INTO district_secrets(id,district_id,name,ciphertext,nonce) VALUES($1,$2,$3,$4,$5) ON CONFLICT (district_id,name) DO UPDATE SET ciphertext=EXCLUDED.ciphertext,nonce=EXCLUDED.nonce`, auth.ID(), d.ID, in.Name, ciphertext, nonce); err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'district.secret_stored',$2)`, a.user(r).ID, "district/"+d.Slug+"/secrets/"+in.Name)
	respond(w, 201, map[string]string{"name": in.Name})
}

func (a *App) districtSecret(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, true)
	if d == nil {
		return
	}
	name := r.PathValue("name")
	var ciphertext, nonce []byte
	err := a.db.QueryRow(r.Context(), `SELECT ciphertext,nonce FROM district_secrets WHERE district_id=$1 AND name=$2`, d.ID, name).Scan(&ciphertext, &nonce)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Secret not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	value, err := openSecret(ciphertext, nonce)
	if errors.Is(err, errSecretUnconfigured) {
		fail(w, 503, "secret_key_unconfigured", "Set GITOWN_SECRET_KEY before reading district secrets.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'district.secret_read',$2)`, a.user(r).ID, "district/"+d.Slug+"/secrets/"+name)
	respond(w, 200, map[string]string{"name": name, "value": value})
}

func (a *App) deleteDistrictSecret(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, true)
	if d == nil {
		return
	}
	name := r.PathValue("name")
	tag, err := a.db.Exec(r.Context(), `DELETE FROM district_secrets WHERE district_id=$1 AND name=$2`, d.ID, name)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Secret not found.")
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'district.secret_deleted',$2)`, a.user(r).ID, "district/"+d.Slug+"/secrets/"+name)
	respond(w, 200, map[string]bool{"removed": true})
}

func (a *App) pullCrews(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	p := a.getPull(w, r, repo)
	if p == nil {
		return
	}
	available := []map[string]string{}
	if repo.DistrictID != "" {
		rows, err := a.db.Query(r.Context(), `SELECT slug,name FROM crews WHERE district_id=$1 ORDER BY slug LIMIT 100`, repo.DistrictID)
		if err != nil {
			serverError(w, err)
			return
		}
		for rows.Next() {
			var crewSlug, name string
			if err = rows.Scan(&crewSlug, &name); err != nil {
				rows.Close()
				serverError(w, err)
				return
			}
			available = append(available, map[string]string{"slug": crewSlug, "name": name})
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			serverError(w, err)
			return
		}
	}
	rows, err := a.db.Query(r.Context(), `SELECT c.slug FROM pull_crew_requests pcr JOIN crews c ON c.id=pcr.crew_id WHERE pcr.pull_request_id=$1 ORDER BY c.slug`, p.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	requested := []string{}
	for rows.Next() {
		var crewSlug string
		if err = rows.Scan(&crewSlug); err != nil {
			serverError(w, err)
			return
		}
		requested = append(requested, crewSlug)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"district": repo.District, "available": available, "requested": requested})
}

func (a *App) updatePullCrews(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanTriage {
		fail(w, 403, "forbidden", "Repository triage permission is required to request crew reviews.")
		return
	}
	if !activeRepository(w, repo) {
		return
	}
	if repo.DistrictID == "" {
		fail(w, 422, "validation_failed", "Add this repository to a district before requesting a crew review.")
		return
	}
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || number < 1 {
		fail(w, 404, "not_found", "Unite request not found.")
		return
	}
	var in struct {
		Slugs []string `json:"slugs"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Slugs) > 5 {
		fail(w, 422, "validation_failed", "Request at most five crews.")
		return
	}
	seen := map[string]bool{}
	slugs := []string{}
	for _, item := range in.Slugs {
		item = strings.ToLower(strings.TrimSpace(item))
		if !slug.MatchString(item) || seen[item] {
			fail(w, 422, "validation_failed", "Each crew slug must be unique and valid.")
			return
		}
		seen[item] = true
		slugs = append(slugs, item)
	}
	var pullID string
	err = a.db.QueryRow(r.Context(), `SELECT id::text FROM pull_requests WHERE repository_id=$1 AND number=$2 AND state='open'`, repo.ID, number).Scan(&pullID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Open Unite request not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `DELETE FROM pull_crew_requests WHERE pull_request_id=$1`, pullID); err != nil {
		serverError(w, err)
		return
	}
	type notice struct{ userID, crewID string }
	var notices []notice
	for _, crewSlug := range slugs {
		var crewID string
		err = tx.QueryRow(r.Context(), `SELECT id::text FROM crews WHERE district_id=$1 AND slug=$2`, repo.DistrictID, crewSlug).Scan(&crewID)
		if errors.Is(err, pgx.ErrNoRows) {
			fail(w, 422, "validation_failed", "Each crew must belong to this repository's district.")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if _, err = tx.Exec(r.Context(), `INSERT INTO pull_crew_requests(pull_request_id,crew_id,requested_by) VALUES($1,$2,$3)`, pullID, crewID, u.ID); err != nil {
			serverError(w, err)
			return
		}
		rows, queryErr := tx.Query(r.Context(), `SELECT user_id::text FROM crew_members WHERE crew_id=$1`, crewID)
		if queryErr != nil {
			serverError(w, queryErr)
			return
		}
		var members []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				serverError(w, err)
				return
			}
			members = append(members, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			serverError(w, err)
			return
		}
		for _, id := range members {
			notices = append(notices, notice{id, crewID})
		}
	}
	for _, notice := range notices {
		if err = notifyDirect(r.Context(), tx, notice.userID, u.ID, repo.ID, "", pullID, "review_request", "crew-review:"+pullID+":"+notice.crewID+":"+notice.userID); err != nil {
			serverError(w, err)
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"slugs": slugs})
}
