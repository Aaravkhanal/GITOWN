package app

import (
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
)

const (
	routesMaxQueuedRuns       = 25
	routesMaxArtifactBytes    = 512 << 10
	routesMaxRunArtifactBytes = 4 << 20
	routesMaxCacheObjectBytes = 512 << 10
	routesMaxRepoCacheBytes   = 20 << 20
)

var routeObjectID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var routeSecretName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
var routeCacheKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,119}$`)
var routeRunnerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

func (a *App) routeBlobPath(id string) (string, bool) {
	if !routeObjectID.MatchString(id) {
		return "", false
	}
	return filepath.Join(a.extraRoot(), "route-blobs", id), true
}

func writeRouteBlob(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0600)
}

func decodeRoutePayload(encoded string, limit int) ([]byte, error) {
	if encoded == "" || len(encoded) > (limit*4/3)+8 {
		return nil, errors.New("the file is empty or too large")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 || len(raw) > limit {
		return nil, errors.New("provide base64 content within the size limit")
	}
	return raw, nil
}

func routeResourceCaps(network string, cpu, memory, disk int) string {
	if network != "none" && network != "restricted" {
		return "Network must be none or restricted. Open network access stays off until sandbox review."
	}
	if cpu < 100 || cpu > 8000 || memory < 128 || memory > 8192 || disk < 128 || disk > 10240 {
		return "CPU must be 100–8000 millis, memory 128–8192 MB, and disk 128–10240 MB."
	}
	return ""
}

func (a *App) routeSecrets(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT name,created_at FROM route_secrets WHERE repository_id=$1 ORDER BY name`, repo.ID)
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
	respond(w, 200, map[string]any{"items": items, "execution_enabled": false})
}

func (a *App) putRouteSecret(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	name := r.PathValue("name")
	var in struct {
		Value string `json:"value"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !routeSecretName.MatchString(name) || len(in.Value) == 0 || len(in.Value) > 8192 {
		fail(w, 422, "validation_failed", "Secret names are uppercase identifiers. Values are 1–8192 characters and are never shown again.")
		return
	}
	ciphertext, nonce, err := sealSecret(in.Value)
	if errors.Is(err, errSecretUnconfigured) {
		fail(w, 503, "secret_key_unconfigured", "Set GITOWN_SECRET_KEY before storing secrets.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = a.db.Exec(r.Context(), `INSERT INTO route_secrets(id,repository_id,name,ciphertext,nonce) VALUES($1,$2,$3,$4,$5) ON CONFLICT(repository_id,name) DO UPDATE SET ciphertext=excluded.ciphertext,nonce=excluded.nonce,created_at=now()`, auth.ID(), repo.ID, name, ciphertext, nonce); err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'route.secret_stored',$2)`, a.user(r).ID, repo.Owner+"/"+repo.Name+":"+name)
	respond(w, 200, map[string]any{"name": name, "stored": true})
}

func (a *App) deleteRouteSecret(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	name := r.PathValue("name")
	tag, err := a.db.Exec(r.Context(), `DELETE FROM route_secrets WHERE repository_id=$1 AND name=$2`, repo.ID, name)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Secret not found.")
		return
	}
	respond(w, 200, map[string]bool{"deleted": true})
}

func (a *App) uploadRouteArtifact(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	runID := r.PathValue("id")
	if !routeObjectID.MatchString(runID) {
		fail(w, 404, "not_found", "Run not found.")
		return
	}
	var in struct {
		Name        string `json:"name"`
		Content     string `json:"content_base64"`
		ContentType string `json:"content_type"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !routeCacheKey.MatchString(in.Name) {
		fail(w, 422, "validation_failed", "Name the artifact with letters, numbers, dots, or hyphens.")
		return
	}
	content, err := decodeRoutePayload(in.Content, routesMaxArtifactBytes)
	if err != nil {
		fail(w, 422, "validation_failed", err.Error())
		return
	}
	var status string
	if err = a.db.QueryRow(r.Context(), `SELECT status FROM route_runs WHERE id=$1 AND repository_id=$2`, runID, repo.ID).Scan(&status); err != nil {
		fail(w, 404, "not_found", "Run not found.")
		return
	}
	if status == "cancelled" || status == "expired" {
		fail(w, 409, "run_closed", "Artifacts can only be attached to a queued run.")
		return
	}
	var used int64
	if err = a.db.QueryRow(r.Context(), `SELECT COALESCE(sum(size_bytes),0) FROM route_run_artifacts WHERE run_id=$1`, runID).Scan(&used); err != nil {
		serverError(w, err)
		return
	}
	if used+int64(len(content)) > routesMaxRunArtifactBytes {
		fail(w, 422, "quota_exceeded", "This run's artifact quota is 4 MB.")
		return
	}
	id := auth.ID()
	path, _ := a.routeBlobPath(id)
	if err = writeRouteBlob(path, content); err != nil {
		serverError(w, err)
		return
	}
	contentType := strings.TrimSpace(in.ContentType)
	if contentType == "" || len(contentType) > 80 || strings.ContainsAny(contentType, "\r\n;") {
		contentType = "application/octet-stream"
	}
	if _, err = a.db.Exec(r.Context(), `INSERT INTO route_run_artifacts(id,run_id,name,content_type,size_bytes,storage_path,uploaded_by) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, runID, in.Name, contentType, len(content), path, a.user(r).ID); err != nil {
		_ = os.Remove(path)
		if conflict(err) {
			fail(w, 409, "artifact_exists", "An artifact with that name is already attached to this run.")
			return
		}
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO route_run_logs(id,run_id,message) VALUES($1,$2,$3)`, auth.ID(), runID, "Artifact "+in.Name+" attached. No workflow step produced it; execution is still disabled.")
	respond(w, 201, map[string]any{"id": id, "name": in.Name, "size_bytes": len(content)})
}

func (a *App) downloadRouteArtifact(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	var path, contentType, name string
	err := a.db.QueryRow(r.Context(), `SELECT a.storage_path,a.content_type,a.name FROM route_run_artifacts a JOIN route_runs run ON run.id=a.run_id WHERE a.id=$1 AND run.repository_id=$2`, r.PathValue("artifactId"), repo.ID).Scan(&path, &contentType, &name)
	if err != nil {
		fail(w, 404, "not_found", "Artifact not found.")
		return
	}
	if _, ok := a.routeBlobPath(filepath.Base(path)); !ok || filepath.Dir(path) != filepath.Join(a.extraRoot(), "route-blobs") {
		fail(w, 404, "not_found", "Artifact not found.")
		return
	}
	content, err := os.ReadFile(path)
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.WriteHeader(200)
	_, _ = w.Write(content)
}

func (a *App) routeCaches(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,cache_key,size_bytes,created_at FROM route_caches WHERE repository_id=$1 ORDER BY cache_key`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, key string
		var size int64
		var created time.Time
		if err = rows.Scan(&id, &key, &size, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "key": key, "size_bytes": size, "created_at": created})
	}
	respond(w, 200, map[string]any{"items": items, "quota_bytes": routesMaxRepoCacheBytes})
}

func (a *App) putRouteCache(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	key := r.PathValue("key")
	var in struct {
		Content string `json:"content_base64"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !routeCacheKey.MatchString(key) {
		fail(w, 422, "validation_failed", "Cache keys use letters, numbers, dots, or hyphens.")
		return
	}
	content, err := decodeRoutePayload(in.Content, routesMaxCacheObjectBytes)
	if err != nil {
		fail(w, 422, "validation_failed", err.Error())
		return
	}
	var used int64
	if err = a.db.QueryRow(r.Context(), `SELECT COALESCE(sum(size_bytes),0) FROM route_caches WHERE repository_id=$1 AND cache_key<>$2`, repo.ID, key).Scan(&used); err != nil {
		serverError(w, err)
		return
	}
	if used+int64(len(content)) > routesMaxRepoCacheBytes {
		fail(w, 422, "quota_exceeded", "This repository's route cache quota is 20 MB.")
		return
	}
	id := auth.ID()
	path, _ := a.routeBlobPath(id)
	if err = writeRouteBlob(path, content); err != nil {
		serverError(w, err)
		return
	}
	var previous string
	_ = a.db.QueryRow(r.Context(), `SELECT storage_path FROM route_caches WHERE repository_id=$1 AND cache_key=$2`, repo.ID, key).Scan(&previous)
	if _, err = a.db.Exec(r.Context(), `INSERT INTO route_caches(id,repository_id,cache_key,size_bytes,storage_path) VALUES($1,$2,$3,$4,$5) ON CONFLICT(repository_id,cache_key) DO UPDATE SET size_bytes=excluded.size_bytes,storage_path=excluded.storage_path,created_at=now()`, id, repo.ID, key, len(content), path); err != nil {
		_ = os.Remove(path)
		serverError(w, err)
		return
	}
	if previous != "" && previous != path {
		_ = os.Remove(previous)
	}
	respond(w, 200, map[string]any{"key": key, "size_bytes": len(content)})
}

func (a *App) routeRunners(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,labels,cpu_millis,memory_mb,disk_mb,network,last_seen_at,created_at FROM route_runners WHERE repository_id=$1 ORDER BY name`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{{
		"kind":              "hosted",
		"name":              "gitown-hosted",
		"execution_enabled": false,
		"reason":            "Hosted runners stay offline until workload sandboxing has been reviewed.",
		"network":           "none",
		"isolation":         "untrusted_execution_disabled",
	}}
	for rows.Next() {
		var id, name, network string
		var labels []string
		var cpu, memory, disk int
		var seen *time.Time
		var created time.Time
		if err = rows.Scan(&id, &name, &labels, &cpu, &memory, &disk, &network, &seen, &created); err != nil {
			serverError(w, err)
			return
		}
		online := seen != nil && time.Since(*seen) < 2*time.Minute
		items = append(items, map[string]any{
			"id": id, "kind": "self_hosted", "name": name, "labels": labels,
			"cpu_millis": cpu, "memory_mb": memory, "disk_mb": disk, "network": network,
			"online": online, "execution_enabled": false, "created_at": created,
			"reason": "Self-hosted runners can register and heartbeat. GITOWN does not dispatch step commands.",
		})
	}
	respond(w, 200, map[string]any{"items": items, "execution_enabled": false})
}

func (a *App) createRouteRunner(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	var in struct {
		Name     string   `json:"name"`
		Labels   []string `json:"labels"`
		CPU      int      `json:"cpu_millis"`
		MemoryMB int      `json:"memory_mb"`
		DiskMB   int      `json:"disk_mb"`
		Network  string   `json:"network"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !routeRunnerName.MatchString(in.Name) || len(in.Labels) > 8 {
		fail(w, 422, "validation_failed", "Name the runner with lowercase letters, numbers, and hyphens, and use at most 8 labels.")
		return
	}
	if msg := routeResourceCaps(in.Network, in.CPU, in.MemoryMB, in.DiskMB); msg != "" {
		fail(w, 422, "validation_failed", msg)
		return
	}
	labels := []string{}
	for _, label := range in.Labels {
		label = strings.TrimSpace(label)
		if !routeRunnerName.MatchString(label) {
			fail(w, 422, "validation_failed", "Runner labels use the same shape as runner names.")
			return
		}
		labels = append(labels, label)
	}
	token := auth.Secret("rnr_")
	id := auth.ID()
	if _, err := a.db.Exec(r.Context(), `INSERT INTO route_runners(id,repository_id,name,kind,labels,token_hash,cpu_millis,memory_mb,disk_mb,network) VALUES($1,$2,$3,'self_hosted',$4,$5,$6,$7,$8,$9)`, id, repo.ID, in.Name, labels, auth.Digest(token), in.CPU, in.MemoryMB, in.DiskMB, in.Network); err != nil {
		if conflict(err) {
			fail(w, 409, "runner_exists", "A runner with that name is already registered.")
			return
		}
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]any{"id": id, "name": in.Name, "token": token, "execution_enabled": false})
}

func (a *App) deleteRouteRunner(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	tag, err := a.db.Exec(r.Context(), `DELETE FROM route_runners WHERE id=$1 AND repository_id=$2`, r.PathValue("runnerId"), repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Runner not found.")
		return
	}
	respond(w, 200, map[string]bool{"deleted": true})
}

func (a *App) routeRunnerHeartbeat(w http.ResponseWriter, r *http.Request) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !strings.HasPrefix(raw, "rnr_") || len(raw) > 200 {
		fail(w, 401, "authentication_required", "Use the runner token issued at registration.")
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE route_runners SET last_seen_at=now() WHERE token_hash=$1`, auth.Digest(raw))
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 401, "authentication_required", "Use the runner token issued at registration.")
		return
	}
	respond(w, 200, map[string]any{
		"execution_enabled": false,
		"jobs":              []any{},
		"reason":            "No step is dispatched. Untrusted workflow execution stays off until sandbox review.",
	})
}

func (a *App) appendRouteLog(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	var in struct {
		Message string `json:"message"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Message = strings.TrimSpace(in.Message)
	if in.Message == "" || len(in.Message) > 2000 {
		fail(w, 422, "validation_failed", "Log notes are 1–2000 characters.")
		return
	}
	var exists bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM route_runs WHERE id=$1 AND repository_id=$2)`, r.PathValue("id"), repo.ID).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if !exists {
		fail(w, 404, "not_found", "Run not found.")
		return
	}
	if _, err := a.db.Exec(r.Context(), `INSERT INTO route_run_logs(id,run_id,message) VALUES($1,$2,$3)`, auth.ID(), r.PathValue("id"), in.Message); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]bool{"logged": true})
}
