package app

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var orphanStorageID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

type storageFinding struct {
	ID     string `json:"repository_id"`
	State  string `json:"state"`
	Path   string `json:"path"`
	Action string `json:"safe_action"`
}

// reconcileOrphanStorage inventories repo directories against database
// metadata. Dry-run is the default. Orphans can only be moved into a
// recoverable quarantine; this endpoint never deletes Git data.
func (a *App) reconcileOrphanStorage(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if !isOperator(u) {
		fail(w, 403, "forbidden", "This endpoint is restricted to configured GITOWN operators.")
		return
	}
	var in struct {
		Action string `json:"action"`
		ID     string `json:"repository_id"`
	}
	if r.Body != nil && r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	if in.Action == "" || in.Action == "dry_run" {
		items, err := a.scanOrphanStorage(r)
		if err != nil {
			serverError(w, err)
			return
		}
		respond(w, 200, map[string]any{"dry_run": true, "items": items, "note": "No files were changed. Quarantined repositories remain recoverable."})
		return
	}
	if !orphanStorageID.MatchString(in.ID) || (in.Action != "quarantine" && in.Action != "restore") {
		fail(w, 422, "validation_failed", "Choose dry_run, quarantine, or restore and provide a valid repository_id for a change.")
		return
	}
	var exists bool
	err := a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM repositories WHERE id=$1)`, in.ID).Scan(&exists)
	if err != nil {
		serverError(w, err)
		return
	}
	if exists {
		fail(w, 409, "repository_registered", "Registered repositories cannot be moved by orphan reconciliation.")
		return
	}
	active := a.git.Path(in.ID)
	quarantineRoot := filepath.Join(a.extraRoot(), "orphan-quarantine")
	if err = os.MkdirAll(quarantineRoot, 0700); err != nil {
		serverError(w, err)
		return
	}
	quarantined := filepath.Join(quarantineRoot, in.ID+".git")
	from, to := active, quarantined
	if in.Action == "restore" {
		from, to = quarantined, active
	}
	info, err := os.Lstat(from)
	if errors.Is(err, os.ErrNotExist) {
		fail(w, 404, "storage_not_found", "The requested orphan storage directory was not found at the expected recovery location.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		fail(w, 422, "unsafe_storage", "Only real Git directories can be reconciled.")
		return
	}
	if _, err = os.Lstat(to); err == nil {
		fail(w, 409, "storage_exists", "The destination already exists; no data was changed.")
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		serverError(w, err)
		return
	}
	if err = os.Rename(from, to); err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,$2,$3)`, u.ID, "storage.orphan_"+in.Action, in.ID)
	respond(w, 200, map[string]any{"dry_run": false, "repository_id": in.ID, "action": in.Action, "recoverable": true})
}

func (a *App) scanOrphanStorage(r *http.Request) ([]storageFinding, error) {
	entries, err := os.ReadDir(a.git.Root)
	if err != nil {
		return nil, err
	}
	items := make([]storageFinding, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".git") || !orphanStorageID.MatchString(strings.TrimSuffix(name, ".git")) || !entry.IsDir() {
			continue
		}
		id := strings.TrimSuffix(name, ".git")
		var registered bool
		if err = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM repositories WHERE id=$1)`, id).Scan(&registered); err != nil {
			return nil, err
		}
		if !registered {
			items = append(items, storageFinding{ID: id, State: "orphan", Path: name, Action: "quarantine (recoverable; never delete)"})
		}
	}
	quarantine := filepath.Join(a.extraRoot(), "orphan-quarantine")
	qentries, err := os.ReadDir(quarantine)
	if errors.Is(err, os.ErrNotExist) {
		return items, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range qentries {
		name := entry.Name()
		id := strings.TrimSuffix(name, ".git")
		if !strings.HasSuffix(name, ".git") || !orphanStorageID.MatchString(id) || !entry.IsDir() {
			continue
		}
		var registered bool
		if err = a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM repositories WHERE id=$1)`, id).Scan(&registered); err != nil {
			return nil, err
		}
		if !registered {
			items = append(items, storageFinding{ID: id, State: "quarantined", Path: "_gitown/orphan-quarantine/" + name, Action: "restore"})
		}
	}
	return items, nil
}
