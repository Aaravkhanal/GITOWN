package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
)

func (a *App) signingKeys(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id::text,title,fingerprint,created_at FROM signing_keys WHERE user_id=$1 ORDER BY created_at DESC`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, title, fingerprint string
		var created any
		if err = rows.Scan(&id, &title, &fingerprint, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "title": title, "fingerprint": fingerprint, "created_at": created})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) createSigningKey(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Title     string `json:"title"`
		PublicKey string `json:"public_key"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	fingerprint, err := parseSSHPublicKey(in.PublicKey)
	if err != nil || in.Title == "" || len(in.Title) > 80 {
		fail(w, 422, "validation_failed", "Provide a title and an SSH public key.")
		return
	}
	var count int
	if err = a.db.QueryRow(r.Context(), `SELECT count(*) FROM signing_keys WHERE user_id=$1`, u.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 10 {
		fail(w, 422, "key_limit", "An account can register up to 10 signing keys.")
		return
	}
	id := auth.ID()
	var created any
	err = a.db.QueryRow(r.Context(), `INSERT INTO signing_keys(id,user_id,title,public_key,fingerprint) VALUES($1,$2,$3,$4,$5) RETURNING created_at`, id, u.ID, in.Title, strings.TrimSpace(in.PublicKey), fingerprint).Scan(&created)
	if conflict(err) {
		fail(w, 409, "key_exists", "That signing key is already registered.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'signing_key.created',$2)`, u.ID, fingerprint)
	a.notifySecurityChange(r, u.ID, "account.signing_key_added", in.Title, "A commit-signing key titled '"+in.Title+"' was added to your GITOWN account. If this was not you, remove it and review your account security.")
	respond(w, 201, map[string]any{"id": id, "title": in.Title, "fingerprint": fingerprint, "created_at": created})
}

func (a *App) deleteSigningKey(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	result, err := a.db.Exec(r.Context(), `DELETE FROM signing_keys WHERE id=$1 AND user_id=$2`, r.PathValue("id"), u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Signing key not found.")
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'signing_key.deleted',$2)`, u.ID, r.PathValue("id"))
	a.notifySecurityChange(r, u.ID, "account.signing_key_removed", r.PathValue("id"), "A commit-signing key was removed from your GITOWN account.")
	respond(w, 200, map[string]bool{"removed": true})
}

func (a *App) verifyProvenance(ctx context.Context, userID, username, message, signature string) (bool, error) {
	if strings.TrimSpace(signature) == "" || strings.TrimSpace(message) == "" {
		return false, nil
	}
	rows, err := a.db.Query(ctx, `SELECT public_key FROM signing_keys WHERE user_id=$1`, userID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return false, err
		}
		keys = append(keys, key)
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	for _, key := range keys {
		ok, verifyErr := verifySSHSignature(username, key, message, signature)
		if verifyErr != nil {
			continue
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func verifySSHSignature(username, publicKey, message, signature string) (bool, error) {
	if strings.ContainsAny(username, "\r\n") || strings.ContainsAny(publicKey, "\r\n") {
		return false, os.ErrInvalid
	}
	dir, err := os.MkdirTemp("", "gitown-sign-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dir)
	allowed := filepath.Join(dir, "allowed")
	sigPath := filepath.Join(dir, "message.sig")
	body := username + ` namespaces="git" ` + publicKey + "\n"
	if err = os.WriteFile(allowed, []byte(body), 0600); err != nil {
		return false, err
	}
	if err = os.WriteFile(sigPath, []byte(signature), 0600); err != nil {
		return false, err
	}
	cmd := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowed, "-I", username, "-n", "git", "-s", sigPath)
	cmd.Stdin = strings.NewReader(message)
	err = cmd.Run()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return false, nil
	}
	return false, err
}
