package app

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

func scanPayload(value string) string {
	if containsSecretMarker(value) {
		return "secret"
	}
	lowered := strings.ToLower(value)
	for _, marker := range []string{"xmrig", "stratum+tcp://", "curl | sh", "wget | sh", "invoke-expression"} {
		if strings.Contains(lowered, marker) {
			return "dangerous_pattern"
		}
	}
	return ""
}

func (a *App) writeCrateVersion(r *http.Request, actorID, crateID, name, version, metadata string, content []byte, retention int) (string, int, string, string) {
	if scanPayload(metadata) != "" || scanPayload(string(content)) != "" {
		return "", 422, "secret_detected", "The package payload matches a private-key header, a known token prefix, or a known dangerous pattern. This is a fixed pattern list, not a malware engine."
	}
	if !safeObjectID(crateID) {
		return "", 500, "internal_error", "The operation could not be completed."
	}
	versionID := auth.ID()
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	path := filepath.Join(a.extraRoot(), "crates", versionID)
	if err := writePrivateFile(path, content); err != nil {
		return "", 500, "internal_error", "The operation could not be completed."
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		_ = os.Remove(path)
		return "", 500, "internal_error", "The operation could not be completed."
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), `INSERT INTO crate_versions(id,crate_id,version,metadata,sha256,size_bytes) VALUES($1,$2,$3,$4,$5,$6)`, versionID, crateID, version, metadata, digest, len(content))
	if conflict(err) {
		_ = os.Remove(path)
		return "", 409, "version_exists", "That version is already published."
	}
	if err != nil {
		_ = os.Remove(path)
		return "", 500, "internal_error", "The operation could not be completed."
	}
	rows, err := tx.Query(r.Context(), `SELECT id::text FROM crate_versions WHERE crate_id=$1 AND id<>$2 ORDER BY created_at DESC OFFSET $3`, crateID, versionID, retention-1)
	if err != nil {
		_ = os.Remove(path)
		return "", 500, "internal_error", "The operation could not be completed."
	}
	var stale []string
	for rows.Next() {
		var staleID string
		if err = rows.Scan(&staleID); err != nil {
			rows.Close()
			_ = os.Remove(path)
			return "", 500, "internal_error", "The operation could not be completed."
		}
		stale = append(stale, staleID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		_ = os.Remove(path)
		return "", 500, "internal_error", "The operation could not be completed."
	}
	for _, staleID := range stale {
		if _, err = tx.Exec(r.Context(), `DELETE FROM crate_versions WHERE id=$1`, staleID); err != nil {
			_ = os.Remove(path)
			return "", 500, "internal_error", "The operation could not be completed."
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		_ = os.Remove(path)
		return "", 500, "internal_error", "The operation could not be completed."
	}
	for _, staleID := range stale {
		if safeObjectID(staleID) {
			_ = os.Remove(filepath.Join(a.extraRoot(), "crates", staleID))
		}
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'crate.published',$2)`, actorID, "crate/"+name+"@"+version)
	return digest, 201, "", ""
}

func (a *App) npmPublish(w http.ResponseWriter, r *http.Request) {
	u := a.packageUser(w, r, true)
	if u == nil {
		return
	}
	name := strings.ToLower(r.PathValue("name"))
	if !crateNamePattern.MatchString(name) {
		fail(w, 422, "validation_failed", "Package names use lowercase letters, numbers, dots, underscores, and hyphens.")
		return
	}
	var in struct {
		DistTags map[string]string `json:"dist-tags"`
		Versions map[string]struct {
			Version     string `json:"version"`
			Description string `json:"description"`
		} `json:"versions"`
		Attachments map[string]struct {
			Data string `json:"data"`
		} `json:"_attachments"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	if err := decoder.Decode(&in); err != nil {
		fail(w, 400, "invalid_input", "Invalid npm publish document.")
		return
	}
	version := ""
	if in.DistTags != nil {
		version = in.DistTags["latest"]
	}
	if version == "" {
		for key := range in.Versions {
			version = key
			break
		}
	}
	var attachment string
	for _, item := range in.Attachments {
		attachment = item.Data
		break
	}
	content, err := base64.StdEncoding.DecodeString(attachment)
	if !crateVersionPattern.MatchString(version) || err != nil || len(content) == 0 || len(content) > 524288 {
		fail(w, 422, "validation_failed", "Publish one version with a base64 tarball up to 512 KB.")
		return
	}
	meta, _ := json.Marshal(map[string]string{"name": name, "version": version, "description": in.Versions[version].Description})
	var crateID, ownerID string
	var retention int
	err = a.db.QueryRow(r.Context(), `SELECT id::text,owner_id::text,retention FROM crates WHERE ecosystem='npm' AND name=$1`, name).Scan(&crateID, &ownerID, &retention)
	if errors.Is(err, pgx.ErrNoRows) {
		crateID = auth.ID()
		ownerID = u.ID
		retention = 20
		description := in.Versions[version].Description
		if len(description) > 500 {
			description = description[:500]
		}
		_, err = a.db.Exec(r.Context(), `INSERT INTO crates(id,owner_id,name,ecosystem,description,visibility,retention) VALUES($1,$2,$3,'npm',$4,'public',20)`, crateID, u.ID, name, description)
		if err != nil {
			serverError(w, err)
			return
		}
		_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'crate.created',$2)`, u.ID, "crate/"+name)
	} else if err != nil {
		serverError(w, err)
		return
	} else if ownerID != u.ID {
		fail(w, 403, "forbidden", "Only the package owner can publish this name.")
		return
	}
	digest, status, code, message := a.writeCrateVersion(r, u.ID, crateID, name, version, string(meta), content, retention)
	if status != 201 {
		fail(w, status, code, message)
		return
	}
	respond(w, 201, map[string]any{"ok": true, "name": name, "version": version, "sha256": digest, "registry": "npm"})
}

func (a *App) npmPackument(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(r.PathValue("name"))
	if !crateNamePattern.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	var crateID, visibility, ownerID string
	err := a.db.QueryRow(r.Context(), `SELECT id::text,visibility,owner_id::text FROM crates WHERE ecosystem='npm' AND name=$1`, name).Scan(&crateID, &visibility, &ownerID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if visibility != "public" {
		u := a.packageUser(w, r, false)
		if u == nil {
			return
		}
		if u.ID != ownerID {
			http.NotFound(w, r)
			return
		}
	}
	rows, err := a.db.Query(r.Context(), `SELECT id::text,version,sha256 FROM crate_versions WHERE crate_id=$1 ORDER BY created_at`, crateID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	versions := map[string]any{}
	latest := ""
	for rows.Next() {
		var versionID, version, sum string
		if err = rows.Scan(&versionID, &version, &sum); err != nil {
			serverError(w, err)
			return
		}
		latest = version
		shasum := ""
		if safeObjectID(versionID) {
			if raw, readErr := os.ReadFile(filepath.Join(a.extraRoot(), "crates", versionID)); readErr == nil {
				sum1 := sha1.Sum(raw)
				shasum = hex.EncodeToString(sum1[:])
			}
		}
		versions[version] = map[string]any{
			"name":    name,
			"version": version,
			"dist": map[string]string{
				"tarball": a.absoluteURL(r, "/npm/"+name+"/-/"+name+"-"+version+".tgz"),
				"shasum":  shasum,
			},
			"sha256": sum,
		}
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"name": name, "dist-tags": map[string]string{"latest": latest}, "versions": versions})
}

func (a *App) npmTarball(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(r.PathValue("name"))
	file := r.PathValue("file")
	version, ok := strings.CutPrefix(file, name+"-")
	version, cut := strings.CutSuffix(version, ".tgz")
	if !ok || !cut || !crateNamePattern.MatchString(name) || !crateVersionPattern.MatchString(version) {
		http.NotFound(w, r)
		return
	}
	var crateID, visibility, ownerID, versionID string
	err := a.db.QueryRow(r.Context(), `SELECT c.id::text,c.visibility,c.owner_id::text,v.id::text FROM crates c JOIN crate_versions v ON v.crate_id=c.id WHERE c.ecosystem='npm' AND c.name=$1 AND v.version=$2`, name, version).Scan(&crateID, &visibility, &ownerID, &versionID)
	if err != nil || !safeObjectID(versionID) {
		http.NotFound(w, r)
		return
	}
	if visibility != "public" {
		u := a.packageUser(w, r, false)
		if u == nil || u.ID != ownerID {
			http.NotFound(w, r)
			return
		}
	}
	raw, err := os.ReadFile(filepath.Join(a.extraRoot(), "crates", versionID))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(200)
	_, _ = w.Write(raw)
	_ = crateID
}

func (a *App) ociVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	if a.packageUser(w, r, false) == nil {
		return
	}
	respond(w, 200, map[string]any{})
}

func (a *App) ociStartBlob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	u := a.packageUser(w, r, true)
	if u == nil {
		return
	}
	name := r.PathValue("name")
	if !crateNamePattern.MatchString(name) {
		http.Error(w, "invalid repository name", 422)
		return
	}
	id := auth.ID()
	path := filepath.Join(a.extraRoot(), "oci-uploads", id)
	if err := writePrivateFile(path, nil); err != nil {
		http.Error(w, "upload could not be started", 500)
		return
	}
	_ = u
	w.Header().Set("Location", "/v2/"+name+"/blobs/uploads/"+id)
	w.Header().Set("Docker-Upload-UUID", id)
	w.Header().Set("Range", "0-0")
	w.WriteHeader(202)
}

func (a *App) ociFinishBlob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	u := a.packageUser(w, r, true)
	if u == nil {
		return
	}
	name := r.PathValue("name")
	upload := r.PathValue("uuid")
	digest := r.URL.Query().Get("digest")
	hexDigest, ok := strings.CutPrefix(digest, "sha256:")
	if !crateNamePattern.MatchString(name) || !safeObjectID(upload) || !ok || !lfsOIDPattern.MatchString(hexDigest) {
		http.Error(w, "invalid blob upload", 422)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil || len(body) == 0 || len(body) > 4<<20 {
		http.Error(w, "blob must be between 1 byte and 4 MB", 422)
		return
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != hexDigest {
		http.Error(w, "digest does not match the blob", 400)
		return
	}
	if reason := scanPayload(string(body)); reason != "" {
		http.Error(w, "blob matches a secret or known dangerous pattern", 422)
		return
	}
	dest := filepath.Join(a.extraRoot(), "oci", name, hexDigest)
	if err = writePrivateFile(dest, body); err != nil {
		http.Error(w, "blob could not be stored", 500)
		return
	}
	_, err = a.db.Exec(r.Context(), `INSERT INTO oci_blobs(name,digest,size_bytes,owner_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, name, digest, len(body), u.ID)
	if err != nil {
		_ = os.Remove(dest)
		http.Error(w, "blob could not be stored", 500)
		return
	}
	_ = os.Remove(filepath.Join(a.extraRoot(), "oci-uploads", upload))
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'oci.blob_uploaded',$2)`, u.ID, "oci/"+name+"@"+digest)
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Location", "/v2/"+name+"/blobs/"+digest)
	w.WriteHeader(201)
}

func (a *App) ociPutManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	u := a.packageUser(w, r, true)
	if u == nil {
		return
	}
	name := r.PathValue("name")
	reference := r.PathValue("reference")
	if !crateNamePattern.MatchString(name) || reference == "" || len(reference) > 200 || strings.Contains(reference, "..") || strings.ContainsAny(reference, "\r\n/") {
		http.Error(w, "invalid manifest reference", 422)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || len(body) == 0 || !json.Valid(body) {
		http.Error(w, "manifest must be a JSON document", 422)
		return
	}
	if reason := scanPayload(string(body)); reason != "" {
		http.Error(w, "manifest matches a secret or known dangerous pattern", 422)
		return
	}
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	media := r.Header.Get("Content-Type")
	if media == "" {
		media = "application/vnd.oci.image.manifest.v1+json"
	}
	if len(media) > 200 {
		media = media[:200]
	}
	dest := filepath.Join(a.extraRoot(), "oci", name, hex.EncodeToString(sum[:]))
	if err = writePrivateFile(dest, body); err != nil {
		http.Error(w, "manifest could not be stored", 500)
		return
	}
	_, err = a.db.Exec(r.Context(), `INSERT INTO oci_blobs(name,digest,size_bytes,owner_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, name, digest, len(body), u.ID)
	if err != nil {
		http.Error(w, "manifest could not be stored", 500)
		return
	}
	_, err = a.db.Exec(r.Context(), `INSERT INTO oci_manifests(name,reference,digest,media_type,owner_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT (name, reference) DO UPDATE SET digest=EXCLUDED.digest, media_type=EXCLUDED.media_type, owner_id=EXCLUDED.owner_id`, name, reference, digest, media, u.ID)
	if err != nil {
		http.Error(w, "manifest could not be stored", 500)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'oci.manifest_uploaded',$2)`, u.ID, "oci/"+name+":"+reference)
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Location", "/v2/"+name+"/manifests/"+reference)
	w.WriteHeader(201)
}

func (a *App) ociGetManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	if a.packageUser(w, r, false) == nil {
		return
	}
	name := r.PathValue("name")
	reference := r.PathValue("reference")
	var digest, media string
	err := a.db.QueryRow(r.Context(), `SELECT digest,media_type FROM oci_manifests WHERE name=$1 AND reference=$2`, name, reference).Scan(&digest, &media)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.writeOCIBlob(w, name, digest, media)
}

func (a *App) ociGetBlob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	if a.packageUser(w, r, false) == nil {
		return
	}
	a.writeOCIBlob(w, r.PathValue("name"), r.PathValue("digest"), "application/octet-stream")
}

func (a *App) writeOCIBlob(w http.ResponseWriter, name, digest, media string) {
	hexDigest, ok := strings.CutPrefix(digest, "sha256:")
	if !crateNamePattern.MatchString(name) || !ok || !lfsOIDPattern.MatchString(hexDigest) {
		http.Error(w, "blob not found", 404)
		return
	}
	raw, err := os.ReadFile(filepath.Join(a.extraRoot(), "oci", name, hexDigest))
	if err != nil {
		http.Error(w, "blob not found", 404)
		return
	}
	w.Header().Set("Docker-Content-Digest", digest)
	w.Header().Set("Content-Type", media)
	w.WriteHeader(200)
	_, _ = w.Write(raw)
}
