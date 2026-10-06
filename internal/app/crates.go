package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

var crateNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,60}$`)
var crateVersionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,40}$`)

// crateEcosystem reads the ecosystem a generic crates-API request applies to.
// The npm and OCI push/pull protocols each have their own fixed routes and
// never call this; it is only for the generic /crates API, which manages
// records for both ecosystems (so an OCI image namespace pushed via /v2 can
// still be listed, described, and have its visibility changed here).
func crateEcosystem(r *http.Request) (string, bool) {
	eco := r.URL.Query().Get("ecosystem")
	if eco == "" {
		eco = "npm"
	}
	return eco, eco == "npm" || eco == "oci"
}

func containsSecretMarker(value string) bool {
	markers := []string{
		"-----BEGIN PRIVATE KEY-----",
		"-----BEGIN RSA PRIVATE KEY-----",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"-----BEGIN EC PRIVATE KEY-----",
		"-----BEGIN DSA PRIVATE KEY-----",
		"ghp_",
		"github_pat_",
		"glpat-",
		"xoxb-",
		"xoxp-",
		"gtn_",
	}
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func (a *App) packageUser(w http.ResponseWriter, r *http.Request, write bool) *User {
	if u := a.sessionUser(r); u != nil {
		return u
	}
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !strings.HasPrefix(raw, "gtn_") || len(raw) > 200 {
		fail(w, 401, "authentication_required", "Sign in or use a package token.")
		return nil
	}
	var u User
	var scope string
	err := a.db.QueryRow(r.Context(), `SELECT u.id::text,u.username,u.display_name,t.scope FROM access_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=$1 AND t.expires_at>now()`, auth.Digest(raw)).Scan(&u.ID, &u.Username, &u.DisplayName, &scope)
	if err != nil {
		fail(w, 401, "authentication_required", "Sign in or use a package token.")
		return nil
	}
	if write && scope != "package:write" {
		fail(w, 403, "forbidden", "package:write scope is required.")
		return nil
	}
	if !write && scope != "package:read" && scope != "package:write" {
		fail(w, 403, "forbidden", "A package scope is required.")
		return nil
	}
	return &u
}

func (a *App) crates(w http.ResponseWriter, r *http.Request) {
	u := a.packageUser(w, r, false)
	if u == nil {
		return
	}
	eco, ok := crateEcosystem(r)
	if !ok {
		fail(w, 422, "validation_failed", "Ecosystem must be npm or oci.")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT c.name,c.description,c.visibility,c.retention,c.created_at,u.username FROM crates c JOIN users u ON u.id=c.owner_id WHERE c.ecosystem=$2 AND (c.visibility='public' OR c.owner_id=$1) ORDER BY c.created_at DESC LIMIT 50`, u.ID, eco)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var name, description, visibility, owner string
		var retention int
		var created any
		if err = rows.Scan(&name, &description, &visibility, &retention, &created, &owner); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"name": name, "description": description, "visibility": visibility, "retention": retention, "created_at": created, "owner": owner, "ecosystem": eco})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "registry": "foundation"})
}

func (a *App) createCrate(w http.ResponseWriter, r *http.Request) {
	u := a.packageUser(w, r, true)
	if u == nil {
		return
	}
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Visibility  string `json:"visibility"`
		Retention   int    `json:"retention"`
		Ecosystem   string `json:"ecosystem"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Description = strings.TrimSpace(in.Description)
	if in.Visibility == "" {
		in.Visibility = "public"
	}
	if in.Retention == 0 {
		in.Retention = 20
	}
	if in.Ecosystem == "" {
		in.Ecosystem = "npm"
	}
	if !crateNamePattern.MatchString(in.Name) || len(in.Description) > 500 || (in.Visibility != "public" && in.Visibility != "private") || in.Retention < 1 || in.Retention > 100 || (in.Ecosystem != "npm" && in.Ecosystem != "oci") {
		fail(w, 422, "validation_failed", "Use a package name, public or private visibility, retention from 1 to 100, and npm or oci as the ecosystem. This is a GITOWN crate record, not an npm registry.")
		return
	}
	id := auth.ID()
	var created any
	err := a.db.QueryRow(r.Context(), `INSERT INTO crates(id,owner_id,name,ecosystem,description,visibility,retention) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`, id, u.ID, in.Name, in.Ecosystem, in.Description, in.Visibility, in.Retention).Scan(&created)
	if conflict(err) {
		fail(w, 409, "crate_exists", "A package with that name already exists in that ecosystem.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'crate.created',$2)`, u.ID, "crate/"+in.Name)
	respond(w, 201, map[string]any{"name": in.Name, "description": in.Description, "visibility": in.Visibility, "retention": in.Retention, "ecosystem": in.Ecosystem, "created_at": created})
}

func (a *App) loadCrate(w http.ResponseWriter, r *http.Request, write bool) (id, ownerID, name, description, visibility string, retention int, ok bool) {
	u := a.packageUser(w, r, write)
	if u == nil {
		return "", "", "", "", "", 0, false
	}
	eco, valid := crateEcosystem(r)
	if !valid {
		fail(w, 422, "validation_failed", "Ecosystem must be npm or oci.")
		return "", "", "", "", "", 0, false
	}
	name = strings.ToLower(r.PathValue("name"))
	err := a.db.QueryRow(r.Context(), `SELECT id::text,owner_id::text,description,visibility,retention FROM crates WHERE ecosystem=$2 AND name=$1`, name, eco).Scan(&id, &ownerID, &description, &visibility, &retention)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && visibility == "private" && ownerID != u.ID) {
		fail(w, 404, "not_found", "Package not found.")
		return "", "", "", "", "", 0, false
	}
	if err != nil {
		serverError(w, err)
		return "", "", "", "", "", 0, false
	}
	if write && ownerID != u.ID {
		fail(w, 403, "forbidden", "Only the package owner can change this record.")
		return "", "", "", "", "", 0, false
	}
	return id, ownerID, name, description, visibility, retention, true
}

func (a *App) crate(w http.ResponseWriter, r *http.Request) {
	id, _, name, description, visibility, retention, ok := a.loadCrate(w, r, false)
	if !ok {
		return
	}
	eco, _ := crateEcosystem(r)
	rows, err := a.db.Query(r.Context(), `SELECT version,metadata,sha256,size_bytes,created_at FROM crate_versions WHERE crate_id=$1 ORDER BY created_at DESC LIMIT 100`, id)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	versions := []map[string]any{}
	for rows.Next() {
		var version, metadata, sum string
		var size int64
		var created any
		if err = rows.Scan(&version, &metadata, &sum, &size, &created); err != nil {
			serverError(w, err)
			return
		}
		versions = append(versions, map[string]any{"version": version, "metadata": metadata, "sha256": sum, "size_bytes": size, "created_at": created})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"name": name, "description": description, "visibility": visibility, "retention": retention, "ecosystem": eco, "versions": versions})
}

func (a *App) updateCrate(w http.ResponseWriter, r *http.Request) {
	id, _, name, description, visibility, retention, ok := a.loadCrate(w, r, true)
	if !ok {
		return
	}
	var in struct {
		Description *string `json:"description"`
		Visibility  *string `json:"visibility"`
		Retention   *int    `json:"retention"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Description != nil {
		description = strings.TrimSpace(*in.Description)
	}
	if in.Visibility != nil {
		visibility = *in.Visibility
	}
	if in.Retention != nil {
		retention = *in.Retention
	}
	if len(description) > 500 || (visibility != "public" && visibility != "private") || retention < 1 || retention > 100 {
		fail(w, 422, "validation_failed", "Use a description up to 500 characters, public or private visibility, and retention from 1 to 100.")
		return
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE crates SET description=$1,visibility=$2,retention=$3 WHERE id=$4`, description, visibility, retention, id); err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'crate.updated',$2)`, a.user(r).ID, "crate/"+name)
	eco, _ := crateEcosystem(r)
	respond(w, 200, map[string]any{"name": name, "description": description, "visibility": visibility, "retention": retention, "ecosystem": eco})
}

func (a *App) deleteCrate(w http.ResponseWriter, r *http.Request) {
	id, ownerID, name, _, _, _, ok := a.loadCrate(w, r, true)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id::text FROM crate_versions WHERE crate_id=$1`, id)
	if err != nil {
		serverError(w, err)
		return
	}
	var files []string
	for rows.Next() {
		var versionID string
		if err = rows.Scan(&versionID); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		if safeObjectID(versionID) {
			files = append(files, filepath.Join(a.extraRoot(), "crates", versionID))
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = a.db.Exec(r.Context(), `DELETE FROM crates WHERE id=$1`, id); err != nil {
		serverError(w, err)
		return
	}
	for _, file := range files {
		_ = os.Remove(file)
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'crate.deleted',$2)`, ownerID, "crate/"+name)
	respond(w, 200, map[string]bool{"removed": true})
}

func (a *App) publishCrateVersion(w http.ResponseWriter, r *http.Request) {
	u := a.user(r)
	crateID, _, name, _, _, retention, ok := a.loadCrate(w, r, true)
	if !ok {
		return
	}
	if u == nil {
		u = a.packageUser(w, r, true)
		if u == nil {
			return
		}
	}
	var in struct {
		Version       string `json:"version"`
		Metadata      string `json:"metadata"`
		ContentBase64 string `json:"content_base64"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Version = strings.TrimSpace(in.Version)
	metadata, metaErr := normalizeCrateMetadata(in.Metadata)
	content, decErr := base64.StdEncoding.DecodeString(in.ContentBase64)
	if !crateVersionPattern.MatchString(in.Version) || metaErr != nil || decErr != nil || len(content) == 0 || len(content) > 524288 {
		fail(w, 422, "validation_failed", "Provide a version, a JSON object of metadata, and a base64 file up to 512 KB.")
		return
	}
	digest, status, code, message := a.writeCrateVersion(r, u.ID, crateID, name, in.Version, metadata, content, retention)
	if status != 201 {
		fail(w, status, code, message)
		return
	}
	respond(w, 201, map[string]any{"name": name, "version": in.Version, "sha256": digest, "size_bytes": len(content)})
}

func normalizeCrateMetadata(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "{}", nil
	}
	if len(raw) > 8000 {
		return "", errors.New("metadata too large")
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(raw), &object); err != nil || object == nil {
		return "", errors.New("metadata must be a JSON object")
	}
	compact, err := json.Marshal(object)
	if err != nil || len(compact) > 8000 {
		return "", errors.New("metadata must be a JSON object")
	}
	return string(compact), nil
}

func (a *App) deleteCrateVersion(w http.ResponseWriter, r *http.Request) {
	crateID, ownerID, name, _, _, _, ok := a.loadCrate(w, r, true)
	if !ok {
		return
	}
	version := r.PathValue("version")
	var id string
	err := a.db.QueryRow(r.Context(), `DELETE FROM crate_versions WHERE crate_id=$1 AND version=$2 RETURNING id::text`, crateID, version).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Package version not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if safeObjectID(id) {
		_ = os.Remove(filepath.Join(a.extraRoot(), "crates", id))
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'crate.version_deleted',$2)`, ownerID, "crate/"+name+"@"+version)
	respond(w, 200, map[string]bool{"removed": true})
}

func (a *App) downloadCrateVersion(w http.ResponseWriter, r *http.Request) {
	crateID, _, _, _, _, _, ok := a.loadCrate(w, r, false)
	if !ok {
		return
	}
	var id, digest string
	err := a.db.QueryRow(r.Context(), `SELECT id::text,sha256 FROM crate_versions WHERE crate_id=$1 AND version=$2`, crateID, r.PathValue("version")).Scan(&id, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Package version not found.")
		return
	}
	if err != nil || !safeObjectID(id) {
		if err != nil {
			serverError(w, err)
		} else {
			serverError(w, errors.New("invalid version id"))
		}
		return
	}
	content, err := os.ReadFile(filepath.Join(a.extraRoot(), "crates", id))
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("ETag", `"`+digest+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}
