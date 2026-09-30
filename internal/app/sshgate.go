package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/Aaravkhanal/GITOWN/internal/gitstore"
	"github.com/jackc/pgx/v5"
)

// SSHShell trusts the fingerprint argument. sshd ForceCommand must bind that
// argument to the key that authenticated. Anyone who can execute this binary
// and knows a stored fingerprint can act as that key, so run it as a dedicated user.
func (a *App) SSHShell(fingerprint string) error {
	return a.SSHSession(context.Background(), fingerprint, os.Getenv("SSH_ORIGINAL_COMMAND"), os.Stdin, os.Stdout, os.Stderr)
}

func ParseGitSSHCommand(command string) (string, string, string, error) {
	if command == "" || strings.ContainsAny(command, "\n\r;&|$`<>") {
		return "", "", "", errors.New("unsupported command")
	}
	fields := strings.Fields(command)
	if len(fields) != 2 {
		return "", "", "", errors.New("unsupported command")
	}
	service := fields[0]
	if service != "git-upload-pack" && service != "git-receive-pack" {
		return "", "", "", errors.New("unsupported command")
	}
	target := strings.Trim(fields[1], `"'`)
	target = strings.TrimPrefix(target, "/")
	target = strings.TrimSuffix(target, ".git")
	owner, name, ok := strings.Cut(target, "/")
	if !ok || strings.Contains(name, "/") || !slug.MatchString(owner) || !repoSlug.MatchString(name) || strings.Contains(name, "..") {
		return "", "", "", errors.New("unsupported command")
	}
	return service, owner, name, nil
}

func parseSSHPublicKey(public string) (string, error) {
	public = strings.TrimSpace(public)
	if public == "" || len(public) > 16384 || strings.ContainsAny(public, "\n\r\"\\") {
		return "", errors.New("invalid public key")
	}
	fields := strings.Fields(public)
	if len(fields) < 2 {
		return "", errors.New("invalid public key")
	}
	switch fields[0] {
	case "ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com":
	default:
		return "", errors.New("unsupported public key algorithm")
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || len(blob) < 16 || len(blob) > 8192 {
		return "", errors.New("invalid public key")
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]), nil
}

func authorizedKeysLine(fingerprint, public string) string {
	return fmt.Sprintf("command=%q,restrict %s", os.Args[0]+" ssh-shell "+fingerprint, strings.TrimSpace(public))
}

func (a *App) sshKeys(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id::text,title,fingerprint,created_at FROM ssh_keys WHERE user_id=$1 ORDER BY created_at DESC`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, title, fingerprint string
		var created time.Time
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

func (a *App) createSSHKey(w http.ResponseWriter, r *http.Request) {
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
	if in.Title == "" || len(in.Title) > 80 || err != nil {
		fail(w, 422, "validation_failed", "Provide a title and an ssh-ed25519, ssh-rsa, or ecdsa public key.")
		return
	}
	if err = a.ensureFingerprintAvailable(r.Context(), fingerprint); err != nil {
		if errors.Is(err, errFingerprintUsed) {
			fail(w, 409, "key_exists", "That public key is already registered.")
			return
		}
		serverError(w, err)
		return
	}
	var count int
	if err = a.db.QueryRow(r.Context(), `SELECT count(*) FROM ssh_keys WHERE user_id=$1`, u.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 10 {
		fail(w, 422, "key_limit", "An account can store up to 10 SSH keys.")
		return
	}
	id := auth.ID()
	var created time.Time
	err = a.db.QueryRow(r.Context(), `INSERT INTO ssh_keys(id,user_id,title,public_key,fingerprint) VALUES($1,$2,$3,$4,$5) RETURNING created_at`, id, u.ID, in.Title, strings.TrimSpace(in.PublicKey), fingerprint).Scan(&created)
	if conflict(err) {
		fail(w, 409, "key_exists", "That public key is already registered.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'ssh_key.created',$2)`, u.ID, fingerprint)
	respond(w, 201, map[string]any{"id": id, "title": in.Title, "fingerprint": fingerprint, "created_at": created, "authorized_keys": authorizedKeysLine(fingerprint, in.PublicKey)})
}

func (a *App) deleteSSHKey(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	tag, err := a.db.Exec(r.Context(), `DELETE FROM ssh_keys WHERE id=$1 AND user_id=$2`, r.PathValue("id"), u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "SSH key not found.")
		return
	}
	respond(w, 200, map[string]bool{"removed": true})
}

func (a *App) deployKeys(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id::text,title,fingerprint,write,created_at FROM deploy_keys WHERE repository_id=$1 ORDER BY created_at DESC`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, title, fingerprint string
		var write bool
		var created time.Time
		if err = rows.Scan(&id, &title, &fingerprint, &write, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "title": title, "fingerprint": fingerprint, "write": write, "created_at": created})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) createDeployKey(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	var in struct {
		Title     string `json:"title"`
		PublicKey string `json:"public_key"`
		Write     bool   `json:"write"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	fingerprint, err := parseSSHPublicKey(in.PublicKey)
	if in.Title == "" || len(in.Title) > 80 || err != nil {
		fail(w, 422, "validation_failed", "Provide a title and an ssh-ed25519, ssh-rsa, or ecdsa public key.")
		return
	}
	if err = a.ensureFingerprintAvailable(r.Context(), fingerprint); err != nil {
		if errors.Is(err, errFingerprintUsed) {
			fail(w, 409, "key_exists", "That public key is already registered.")
			return
		}
		serverError(w, err)
		return
	}
	var count int
	if err = a.db.QueryRow(r.Context(), `SELECT count(*) FROM deploy_keys WHERE repository_id=$1`, repo.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 10 {
		fail(w, 422, "key_limit", "A repository can store up to 10 deploy keys.")
		return
	}
	id := auth.ID()
	var created time.Time
	err = a.db.QueryRow(r.Context(), `INSERT INTO deploy_keys(id,repository_id,title,public_key,fingerprint,write) VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at`, id, repo.ID, in.Title, strings.TrimSpace(in.PublicKey), fingerprint, in.Write).Scan(&created)
	if conflict(err) {
		fail(w, 409, "key_exists", "That public key is already registered.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'deploy_key.created',$2)`, a.user(r).ID, repo.Owner+"/"+repo.Name+":"+fingerprint)
	respond(w, 201, map[string]any{"id": id, "title": in.Title, "fingerprint": fingerprint, "write": in.Write, "created_at": created, "authorized_keys": authorizedKeysLine(fingerprint, in.PublicKey)})
}

func (a *App) deleteDeployKey(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	tag, err := a.db.Exec(r.Context(), `DELETE FROM deploy_keys WHERE id=$1 AND repository_id=$2`, r.PathValue("id"), repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Deploy key not found.")
		return
	}
	respond(w, 200, map[string]bool{"removed": true})
}

var errFingerprintUsed = errors.New("fingerprint already registered")

func (a *App) ensureFingerprintAvailable(ctx context.Context, fingerprint string) error {
	var used bool
	err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ssh_keys WHERE fingerprint=$1) OR EXISTS(SELECT 1 FROM deploy_keys WHERE fingerprint=$1)`, fingerprint).Scan(&used)
	if err != nil {
		return err
	}
	if used {
		return errFingerprintUsed
	}
	return nil
}

type sshGrant struct {
	repo       Repository
	actorID    string
	role       string
	canReceive bool
}

func (a *App) grantSSH(ctx context.Context, fingerprint, owner, name, service string) (sshGrant, error) {
	var user User
	err := a.db.QueryRow(ctx, `SELECT u.id::text,u.username,u.display_name FROM ssh_keys k JOIN users u ON u.id=k.user_id WHERE k.fingerprint=$1`, fingerprint).Scan(&user.ID, &user.Username, &user.DisplayName)
	if err == nil {
		repo, scanErr := scanRepo(a.db.QueryRow(ctx, `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id WHERE u.username=$1 AND r.name=$2 AND r.deleted_at IS NULL`, owner, name))
		if scanErr != nil {
			return sshGrant{}, errors.New("repository not found")
		}
		if err = a.decorate(ctx, &repo, &user); err != nil {
			return sshGrant{}, err
		}
		if repo.Visibility != "public" && repo.Role == "" {
			return sshGrant{}, errors.New("repository not found")
		}
		grant := sshGrant{repo: repo, actorID: user.ID, role: repo.Role, canReceive: repo.CanWrite && !repo.Archived}
		if service == "git-receive-pack" && repo.Archived {
			return grant, errors.New("repository is archived")
		}
		if service == "git-receive-pack" && !repo.CanWrite {
			return grant, errors.New("this key cannot push")
		}
		return grant, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sshGrant{}, err
	}
	rows, err := a.db.Query(ctx, `SELECT repository_id::text, write FROM deploy_keys WHERE fingerprint=$1 LIMIT 2`, fingerprint)
	if err != nil {
		return sshGrant{}, err
	}
	defer rows.Close()
	var repoID string
	var write bool
	count := 0
	for rows.Next() {
		count++
		if err = rows.Scan(&repoID, &write); err != nil {
			return sshGrant{}, err
		}
	}
	if err = rows.Err(); err != nil {
		return sshGrant{}, err
	}
	if count != 1 {
		return sshGrant{}, errors.New("repository not found")
	}
	repo, scanErr := scanRepo(a.db.QueryRow(ctx, `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id WHERE r.id=$1 AND r.deleted_at IS NULL`, repoID))
	if scanErr != nil || repo.Owner != owner || repo.Name != name {
		return sshGrant{}, errors.New("repository not found")
	}
	grant := sshGrant{repo: repo, canReceive: write && !repo.Archived}
	if service == "git-receive-pack" && repo.Archived {
		return grant, errors.New("repository is archived")
	}
	if service == "git-receive-pack" && !write {
		return grant, errors.New("this key cannot push")
	}
	return grant, nil
}

func (a *App) SSHSession(ctx context.Context, fingerprint, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	service, owner, name, err := ParseGitSSHCommand(command)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return err
	}
	if strings.TrimSpace(fingerprint) == "" {
		fmt.Fprintln(stderr, "repository not found")
		return errors.New("repository not found")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	grant, err := a.grantSSH(ctx, fingerprint, owner, name, service)
	if err != nil {
		if err.Error() == "repository not found" || err.Error() == "this key cannot push" || err.Error() == "repository is archived" {
			fmt.Fprintln(stderr, err.Error())
			return err
		}
		fmt.Fprintln(stderr, "repository not found")
		return err
	}
	select {
	case a.transports <- struct{}{}:
		defer func() { <-a.transports }()
	default:
		fmt.Fprintln(stderr, "git server busy")
		return errors.New("git server busy")
	}
	if service != "git-receive-pack" {
		cmd := exec.CommandContext(ctx, a.git.Binary, "upload-pack", a.git.Path(grant.repo.ID))
		cmd.Env = gitstore.Environment()
		cmd.Stdin = stdin
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.WaitDelay = time.Second
		return cmd.Run()
	}
	// receive-pack speaks first (the ref advertisement), so both directions
	// stream. The command list is inspected as it arrives for an early, clear
	// refusal; the pre-receive hook enforces every rule before refs move.
	env, cleanup, err := a.receivePolicyEnv(ctx, &grant.repo, grant.role)
	if err != nil {
		fmt.Fprintln(stderr, "could not validate Git receive request")
		return err
	}
	defer cleanup()
	protected, err := a.blockedPushBranches(ctx, grant.repo.ID, grant.role)
	if err != nil {
		fmt.Fprintln(stderr, "could not validate Git receive request")
		return err
	}
	cmd := exec.CommandContext(ctx, a.git.Binary, "receive-pack", a.git.Path(grant.repo.ID))
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	replay, blocked, updates, inspectErr := inspectReceiveCommands(io.LimitReader(stdin, 100<<20), false, protected)
	if inspectErr == nil && blocked != "" {
		message := directWriteMessage(protected[blocked])
		fmt.Fprintln(stderr, message)
		_ = input.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return errors.New(message)
	}
	if inspectErr != nil {
		updates = nil
	}
	_, copyErr := io.Copy(input, replay)
	_ = input.Close()
	if err = cmd.Wait(); err != nil {
		return err
	}
	if copyErr != nil {
		return copyErr
	}
	updates = a.appliedUpdates(ctx, grant.repo.ID, updates)
	if quotaErr := a.enforceUnpackedQuota(ctx, &grant.repo, updates); errors.Is(quotaErr, errStorageQuota) {
		fmt.Fprintln(stderr, "repository or account storage quota would be exceeded")
		return errStorageQuota
	}
	a.finishReceive(ctx, &grant.repo, grant.actorID, updates, "ssh")
	return nil
}
