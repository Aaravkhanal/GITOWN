package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/Aaravkhanal/GITOWN/internal/gitstore"
)

// Git traffic is deliberately separate from browser session authentication.
// Both the advertisement and the RPC re-check the token and repository policy.
func (a *App) gitHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/info/lfs/") {
		a.gitLFS(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/git/"), "/")
	if len(parts) < 3 || len(parts) > 4 || !slug.MatchString(parts[0]) || !strings.HasSuffix(parts[1], ".git") {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimSuffix(parts[1], ".git")
	if !repoSlug.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	suffix := strings.Join(parts[2:], "/")
	service := r.URL.Query().Get("service")
	allowed := (r.Method == "GET" && suffix == "info/refs" && (service == "git-upload-pack" || service == "git-receive-pack")) || (r.Method == "POST" && (suffix == "git-upload-pack" || suffix == "git-receive-pack"))
	if !allowed {
		http.NotFound(w, r)
		return
	}
	write := suffix == "git-receive-pack" || service == "git-receive-pack"
	repo, user, ok := a.authorizeGit(w, r, parts[0], name, write)
	if !ok {
		return
	}
	requestBody := io.Reader(http.MaxBytesReader(w, r.Body, 100<<20))
	var updates []refUpdate
	if write && r.Method == "POST" {
		protected, err := a.blockedPushBranches(r.Context(), repo.ID, repo.Role)
		if err != nil {
			serverError(w, err)
			return
		}
		raw, err := io.ReadAll(requestBody)
		if err != nil {
			http.Error(w, "Could not read Git receive request.", 400)
			return
		}
		replay, blocked, parsed, inspectErr := inspectReceiveCommands(bytes.NewReader(raw), r.Header.Get("Content-Encoding") == "gzip", protected)
		if inspectErr != nil {
			if len(protected) > 0 {
				http.Error(w, "Could not validate Git receive request.", 400)
				return
			}
			requestBody = bytes.NewReader(raw)
		} else if blocked != "" {
			message := "Direct pushes to this branch are disabled."
			if protected[blocked] == "unite" {
				message = "This branch requires a Unite request; direct pushes are disabled."
			} else if protected[blocked] == "restricted" {
				message = "Only the owner or a maintainer can push to this branch."
			}
			http.Error(w, message, 403)
			return
		} else {
			requestBody = replay
			updates = parsed
		}
		if err = a.withinQuota(r.Context(), repo, int64(len(raw))); err != nil {
			if errors.Is(err, errStorageQuota) {
				http.Error(w, "Repository or account storage quota would be exceeded.", 413)
				return
			}
			serverError(w, err)
			return
		}
	}
	select {
	case a.transports <- struct{}{}:
		defer func() { <-a.transports }()
	default:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "Git server busy; retry shortly.", 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, a.git.Backend)
	remoteIP, _, _ := net.SplitHostPort(r.RemoteAddr)
	env := append(gitstore.Environment(), "GIT_PROJECT_ROOT="+a.git.Root, "GIT_HTTP_EXPORT_ALL=1", "PATH_INFO=/"+repo.ID+".git/"+suffix, "REQUEST_METHOD="+r.Method, "QUERY_STRING="+r.URL.RawQuery, "CONTENT_TYPE="+r.Header.Get("Content-Type"), "SERVER_PROTOCOL=HTTP/1.1", "GATEWAY_INTERFACE=CGI/1.1", "SERVER_NAME=gitown", "REMOTE_ADDR="+remoteIP)
	if user != nil {
		env = append(env, "REMOTE_USER="+user.Username)
	}
	if r.ContentLength >= 0 {
		env = append(env, "CONTENT_LENGTH="+strconv.FormatInt(r.ContentLength, 10))
	}
	if protocol := r.Header.Get("Git-Protocol"); protocol == "version=2" {
		env = append(env, "GIT_PROTOCOL=version=2")
	}
	if encoding := r.Header.Get("Content-Encoding"); encoding == "gzip" {
		env = append(env, "HTTP_CONTENT_ENCODING=gzip")
	}
	cmd.Env = env
	cmd.Stdin = requestBody
	cmd.WaitDelay = time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		serverError(w, err)
		return
	}
	if err = cmd.Start(); err != nil {
		serverError(w, err)
		return
	}
	reader := bufio.NewReader(stdout)
	// Git's CGI headers are trusted output from the system Git binary.
	headers, err := textproto.NewReader(reader).ReadMIMEHeader()
	if err != nil {
		cancel()
		_ = cmd.Wait()
		http.Error(w, "Git transport failed.", 502)
		return
	}
	status := 200
	if v := headers.Get("Status"); v != "" {
		fields := strings.Fields(v)
		if len(fields) > 0 {
			if parsed, e := strconv.Atoi(fields[0]); e == nil {
				status = parsed
			}
		}
	}
	for _, key := range []string{"Content-Type", "Cache-Control", "Pragma", "Expires"} {
		if v := headers.Get(key); v != "" {
			w.Header().Set(key, v)
		}
	}
	w.WriteHeader(status)
	_, copyErr := io.Copy(w, reader)
	if copyErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	// receive-pack may return HTTP 200 even if it rejects refs. Record transport
	// completion, never falsely claim that every proposed ref was accepted.
	if write && r.Method == "POST" && waitErr == nil && copyErr == nil && user != nil {
		auditCtx, auditCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer auditCancel()
		a.finishReceive(auditCtx, repo, user.ID, updates, "https")
	}
}

func (a *App) authorizeGit(w http.ResponseWriter, r *http.Request, owner, name string, write bool) (*Repository, *User, bool) {
	repo, err := scanRepo(a.db.QueryRow(r.Context(), `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id WHERE u.username=$1 AND r.name=$2 AND r.deleted_at IS NULL`, owner, name))
	var u User
	var scope string
	username, token, provided := r.BasicAuth()
	authenticated := false
	if provided && strings.HasPrefix(token, "gtn_") && len(token) < 128 {
		tokenErr := a.db.QueryRow(r.Context(), `SELECT u.id,u.username,u.display_name,t.scope FROM access_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=$1 AND t.expires_at>now() AND u.username=$2`, auth.Digest(token), username).Scan(&u.ID, &u.Username, &u.DisplayName, &scope)
		authenticated = tokenErr == nil
	}
	if err == nil && authenticated {
		if decErr := a.decorate(r.Context(), &repo, &u); decErr != nil {
			serverError(w, decErr)
			return nil, nil, false
		}
	}
	hidden := err != nil || repo.Visibility != "public"
	if (provided && !authenticated) || (!authenticated && (write || hidden)) {
		w.Header().Set("WWW-Authenticate", `Basic realm="GITOWN (use a personal access token)"`)
		http.Error(w, "A valid personal access token is required.", 401)
		return nil, nil, false
	}
	if err != nil || (repo.Visibility != "public" && repo.Role == "") {
		http.NotFound(w, r)
		return nil, nil, false
	}
	if write && repo.Archived {
		http.Error(w, "This repository is archived and read-only.", 403)
		return nil, nil, false
	}
	if write && (!repo.CanWrite || scope != "repo:write") {
		http.Error(w, "Repository write permission and repo:write scope are required.", 403)
		return nil, nil, false
	}
	if !authenticated {
		return &repo, nil, true
	}
	return &repo, &u, true
}
