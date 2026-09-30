package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
)

var lfsOIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (a *App) gitLFS(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/git/")
	owner, after, ok := strings.Cut(rest, "/")
	if !ok || !slug.MatchString(owner) {
		http.NotFound(w, r)
		return
	}
	repoPart, lfsPath, ok := strings.Cut(after, "/")
	if !ok || !strings.HasSuffix(repoPart, ".git") || !strings.HasPrefix(lfsPath, "info/lfs/") {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimSuffix(repoPart, ".git")
	if !repoSlug.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	op := strings.TrimPrefix(lfsPath, "info/lfs/")
	if r.Method == "POST" && op == "objects/batch" {
		a.lfsBatch(w, r, owner, name)
		return
	}
	if op == "locks" || op == "locks/verify" || strings.HasPrefix(op, "locks/") {
		a.lfsLocks(w, r, owner, name, op)
		return
	}
	oid, isObject := strings.CutPrefix(op, "objects/")
	if isObject && lfsOIDPattern.MatchString(oid) && !strings.Contains(oid, "/") && (r.Method == "PUT" || r.Method == "GET") {
		a.lfsObject(w, r, owner, name, oid)
		return
	}
	http.NotFound(w, r)
}

func (a *App) lfsBatch(w http.ResponseWriter, r *http.Request, owner, name string) {
	repo, user, ok := a.authorizeGit(w, r, owner, name, false)
	if !ok {
		return
	}
	var in struct {
		Operation string `json:"operation"`
		Objects   []struct {
			OID  string `json:"oid"`
			Size int64  `json:"size"`
		} `json:"objects"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := decoder.Decode(&in); err != nil {
		http.Error(w, "Invalid Git LFS batch request.", 400)
		return
	}
	if (in.Operation != "upload" && in.Operation != "download") || len(in.Objects) == 0 || len(in.Objects) > 20 {
		http.Error(w, "Git LFS batch supports upload or download of up to 20 objects.", 422)
		return
	}
	if in.Operation == "upload" && (user == nil || !repo.CanWrite || a.basicTokenScope(r) != "repo:write") {
		http.Error(w, "Repository write permission and repo:write scope are required.", 403)
		return
	}
	type action struct {
		Href string `json:"href"`
	}
	type object struct {
		OID     string            `json:"oid"`
		Size    int64             `json:"size"`
		Actions map[string]action `json:"actions,omitempty"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	objects := []object{}
	for _, item := range in.Objects {
		entry := object{OID: item.OID, Size: item.Size}
		if !lfsOIDPattern.MatchString(item.OID) || item.Size < 0 || item.Size > 20<<20 {
			entry.Error = &struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}{422, "OID must be 64 hex characters and size must be at most 20 MB."}
			objects = append(objects, entry)
			continue
		}
		path := a.lfsFile(repo.ID, item.OID)
		_, statErr := os.Stat(path)
		present := statErr == nil
		href := a.absoluteURL(r, "/git/"+owner+"/"+name+".git/info/lfs/objects/"+item.OID)
		if in.Operation == "upload" && !present {
			entry.Actions = map[string]action{"upload": {Href: href}}
		}
		if in.Operation == "download" {
			if !present {
				entry.Error = &struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				}{404, "Object not found."}
			} else {
				entry.Actions = map[string]action{"download": {Href: href}}
			}
		}
		objects = append(objects, entry)
	}
	w.Header().Set("Content-Type", "application/vnd.git-lfs+json")
	w.WriteHeader(200)
	_ = json.NewEncoder(w).Encode(map[string]any{"transfer": "basic", "objects": objects})
}

func (a *App) lfsObject(w http.ResponseWriter, r *http.Request, owner, name, oid string) {
	write := r.Method == "PUT"
	repo, _, ok := a.authorizeGit(w, r, owner, name, write)
	if !ok {
		return
	}
	if !safeObjectID(repo.ID) {
		http.Error(w, "Repository storage is unavailable.", 503)
		return
	}
	path := a.lfsFile(repo.ID, oid)
	if r.Method == "GET" {
		content, err := os.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		_, _ = w.Write(content)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 20<<20))
	if err != nil || len(body) == 0 || len(body) > 20<<20 {
		http.Error(w, "Git LFS object must be at most 20 MB.", 413)
		return
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != oid {
		http.Error(w, "Git LFS object digest does not match the OID.", 422)
		return
	}
	if err = a.withinQuota(r.Context(), repo, int64(len(body))); err != nil {
		if errors.Is(err, errStorageQuota) {
			http.Error(w, "Repository or account storage quota would be exceeded.", 413)
			return
		}
		serverError(w, err)
		return
	}
	if err = writePrivateFile(path, body); err != nil {
		serverError(w, err)
		return
	}
	_ = a.noteRepositoryFacts(r.Context(), repo)
	w.WriteHeader(200)
}

func (a *App) lfsFile(repoID, oid string) string {
	return filepath.Join(a.extraRoot(), "lfs", repoID, oid)
}

func (a *App) absoluteURL(r *http.Request, path string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + path
}

func (a *App) basicTokenScope(r *http.Request) string {
	_, token, ok := r.BasicAuth()
	if !ok || !strings.HasPrefix(token, "gtn_") || len(token) >= 128 {
		return ""
	}
	var scope string
	if err := a.db.QueryRow(r.Context(), `SELECT scope FROM access_tokens WHERE token_hash=$1 AND expires_at>now()`, auth.Digest(token)).Scan(&scope); err != nil {
		return ""
	}
	return scope
}

var lfsPathPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)

func validLFSPath(path string) bool {
	return lfsPathPattern.MatchString(path) && !strings.Contains(path, "..") && !strings.Contains(path, "//")
}

func (a *App) lfsLocks(w http.ResponseWriter, r *http.Request, owner, name, op string) {
	write := r.Method == "POST" && op != "locks/verify" && !strings.HasPrefix(op, "locks?")
	if op == "locks" && r.Method == "GET" {
		write = false
	}
	repo, user, ok := a.authorizeGit(w, r, owner, name, write && op != "locks/verify")
	if !ok {
		return
	}
	if op == "locks/verify" {
		repo, user, ok = a.authorizeGit(w, r, owner, name, false)
		if !ok {
			return
		}
	}
	w.Header().Set("Content-Type", "application/vnd.git-lfs+json")
	switch {
	case r.Method == "POST" && op == "locks":
		if user == nil || !repo.CanWrite || a.basicTokenScope(r) != "repo:write" {
			http.Error(w, "Repository write permission and repo:write scope are required.", 403)
			return
		}
		var in struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil || !validLFSPath(in.Path) {
			http.Error(w, "Provide a lock path.", 422)
			return
		}
		id := auth.ID()
		var created time.Time
		err := a.db.QueryRow(r.Context(), `INSERT INTO lfs_locks(id,repository_id,path,owner_id) VALUES($1,$2,$3,$4) RETURNING created_at`, id, repo.ID, in.Path, user.ID).Scan(&created)
		if conflict(err) {
			http.Error(w, "That path is already locked.", 409)
			return
		}
		if err != nil {
			http.Error(w, "The lock could not be created.", 500)
			return
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"lock": lfsLockJSON(id, in.Path, created, user.Username)})
	case r.Method == "GET" && op == "locks":
		path := r.URL.Query().Get("path")
		query := `SELECT l.id::text,l.path,l.created_at,u.username FROM lfs_locks l JOIN users u ON u.id=l.owner_id WHERE l.repository_id=$1`
		args := []any{repo.ID}
		if path != "" {
			query += ` AND l.path=$2`
			args = append(args, path)
		}
		query += ` ORDER BY l.created_at`
		rows, err := a.db.Query(r.Context(), query, args...)
		if err != nil {
			http.Error(w, "Locks could not be listed.", 500)
			return
		}
		defer rows.Close()
		locks := []any{}
		for rows.Next() {
			var id, lockPath, username string
			var created time.Time
			if err = rows.Scan(&id, &lockPath, &created, &username); err != nil {
				http.Error(w, "Locks could not be listed.", 500)
				return
			}
			locks = append(locks, lfsLockJSON(id, lockPath, created, username))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"locks": locks})
	case r.Method == "POST" && op == "locks/verify":
		if user == nil {
			http.Error(w, "Authentication is required.", 401)
			return
		}
		rows, err := a.db.Query(r.Context(), `SELECT l.id::text,l.path,l.created_at,u.username,l.owner_id::text FROM lfs_locks l JOIN users u ON u.id=l.owner_id WHERE l.repository_id=$1 ORDER BY l.created_at`, repo.ID)
		if err != nil {
			http.Error(w, "Locks could not be listed.", 500)
			return
		}
		defer rows.Close()
		ours := []any{}
		theirs := []any{}
		for rows.Next() {
			var id, lockPath, username, ownerID string
			var created time.Time
			if err = rows.Scan(&id, &lockPath, &created, &username, &ownerID); err != nil {
				http.Error(w, "Locks could not be listed.", 500)
				return
			}
			item := lfsLockJSON(id, lockPath, created, username)
			if ownerID == user.ID {
				ours = append(ours, item)
			} else {
				theirs = append(theirs, item)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ours": ours, "theirs": theirs, "next_cursor": ""})
	case r.Method == "POST" && strings.HasPrefix(op, "locks/") && strings.HasSuffix(op, "/unlock"):
		if user == nil || a.basicTokenScope(r) != "repo:write" {
			http.Error(w, "Repository write permission and repo:write scope are required.", 403)
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(op, "locks/"), "/unlock")
		if !safeObjectID(id) {
			http.NotFound(w, r)
			return
		}
		var in struct {
			Force bool `json:"force"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in)
		var ownerID, lockPath string
		var created time.Time
		err := a.db.QueryRow(r.Context(), `SELECT owner_id::text,path,created_at FROM lfs_locks WHERE id=$1 AND repository_id=$2`, id, repo.ID).Scan(&ownerID, &lockPath, &created)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if ownerID != user.ID && !in.Force {
			http.Error(w, "Only the lock owner can unlock this path.", 403)
			return
		}
		if _, err = a.db.Exec(r.Context(), `DELETE FROM lfs_locks WHERE id=$1`, id); err != nil {
			http.Error(w, "The lock could not be removed.", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"lock": lfsLockJSON(id, lockPath, created, user.Username)})
	default:
		http.NotFound(w, r)
	}
}

func lfsLockJSON(id, path string, created time.Time, username string) map[string]any {
	return map[string]any{"id": id, "path": path, "locked_at": created.UTC().Format(time.RFC3339), "owner": map[string]string{"name": username}}
}
