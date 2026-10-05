package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
)

var errMergeQueue = errors.New("This repository's merge queue is active. Merge the request at the front of the queue first.")

func (a *App) resolvePullHead(ctx context.Context, repo *Repository, p *Pull) (string, error) {
	source := repo.ID
	if p.HeadRepositoryID != "" {
		source = p.HeadRepositoryID
	}
	head, err := a.git.Resolve(ctx, source, p.Head)
	if err != nil {
		return "", err
	}
	if source != repo.ID {
		if err = a.git.CopyCommit(ctx, repo.ID, source, head); err != nil {
			return "", err
		}
	}
	return head, nil
}

func (a *App) mergeQueueBlocks(ctx context.Context, repoID, pullID string) error {
	var front string
	err := a.db.QueryRow(ctx, `SELECT pull_request_id::text FROM merge_queue WHERE repository_id=$1 AND status='waiting' ORDER BY position, created_at LIMIT 1`, repoID).Scan(&front)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if front != pullID {
		return errMergeQueue
	}
	return nil
}

func (a *App) settleMergeQueue(ctx context.Context, pullID string) {
	_, _ = a.db.Exec(ctx, `UPDATE merge_queue SET status='merged' WHERE pull_request_id=$1 AND status='waiting'`, pullID)
}

func (a *App) mergeQueue(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT q.id,q.position,q.status,p.number,p.title FROM merge_queue q JOIN pull_requests p ON p.id=q.pull_request_id WHERE q.repository_id=$1 AND q.status='waiting' ORDER BY q.position`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, status, title string
		var position, number int
		if err = rows.Scan(&id, &position, &status, &number, &title); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "position": position, "status": status, "number": number, "title": title})
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) enqueueMerge(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, true)
	if repo == nil {
		return
	}
	var in struct {
		Number int `json:"number"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Number < 1 {
		fail(w, 422, "validation_failed", "Name the unite request number to enqueue.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT id FROM repositories WHERE id=$1 FOR UPDATE`, repo.ID); err != nil {
		serverError(w, err)
		return
	}
	var pullID, state string
	var draft bool
	err = tx.QueryRow(r.Context(), `SELECT id::text,state,draft FROM pull_requests WHERE repository_id=$1 AND number=$2`, repo.ID, in.Number).Scan(&pullID, &state, &draft)
	if err != nil {
		fail(w, 404, "not_found", "Unite request not found.")
		return
	}
	if state != "open" || draft {
		fail(w, 409, "not_ready", "Only an open, non-draft unite request can enter the merge queue.")
		return
	}
	var position int
	if err = tx.QueryRow(r.Context(), `SELECT COALESCE(MAX(position),0)+1 FROM merge_queue WHERE repository_id=$1`, repo.ID).Scan(&position); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO merge_queue(id,repository_id,pull_request_id,position) VALUES($1,$2,$3,$4)`, auth.ID(), repo.ID, pullID, position); err != nil {
		if conflict(err) {
			fail(w, 409, "already_queued", "That unite request is already waiting in the queue.")
			return
		}
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]any{"number": in.Number, "position": position})
}

func (a *App) dequeueMerge(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, true)
	if repo == nil {
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE merge_queue SET status='dequeued' WHERE repository_id=$1 AND pull_request_id=(SELECT id FROM pull_requests WHERE repository_id=$1 AND number=$2) AND status='waiting'`, repo.ID, r.PathValue("number"))
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "That unite request is not waiting in the queue.")
		return
	}
	respond(w, 200, map[string]bool{"dequeued": true})
}

func (a *App) discussions(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := a.db.Query(r.Context(), `SELECT d.number,d.title,d.category,d.state,u.username,d.created_at FROM discussions d JOIN users u ON u.id=d.author_id WHERE d.repository_id=$1 ORDER BY d.number DESC LIMIT 100`, repo.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var number int
			var title, category, state, author string
			var created time.Time
			if err = rows.Scan(&number, &title, &category, &state, &author, &created); err != nil {
				serverError(w, err)
				return
			}
			items = append(items, map[string]any{"number": number, "title": title, "category": category, "state": state, "author": author, "created_at": created})
		}
		respond(w, 200, map[string]any{"items": items})
		return
	}
	if !repo.CanComment {
		fail(w, 403, "forbidden", "Sign in to start a Town Hall discussion.")
		return
	}
	u := a.user(r)
	var in struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Category string `json:"category"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validContent(in.Title, in.Body) {
		fail(w, 422, "validation_failed", "Give the discussion a title and a body up to 20,000 characters.")
		return
	}
	if in.Category == "" {
		in.Category = "general"
	}
	if in.Category != "general" && in.Category != "ideas" && in.Category != "announcements" && in.Category != "q-and-a" {
		fail(w, 422, "validation_failed", "Category must be general, ideas, announcements, or q-and-a.")
		return
	}
	var number int
	var created time.Time
	err := a.db.QueryRow(r.Context(), `INSERT INTO discussions(id,repository_id,number,author_id,title,body,category) SELECT $1,$2,COALESCE(MAX(number),0)+1,$3,$4,$5,$6 FROM discussions WHERE repository_id=$2 RETURNING number,created_at`, auth.ID(), repo.ID, u.ID, strings.TrimSpace(in.Title), in.Body, in.Category).Scan(&number, &created)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]any{"number": number, "title": strings.TrimSpace(in.Title), "category": in.Category, "created_at": created})
}

func (a *App) discussion(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	var id, title, body, category, state, author string
	var created time.Time
	err := a.db.QueryRow(r.Context(), `SELECT d.id::text,d.title,d.body,d.category,d.state,u.username,d.created_at FROM discussions d JOIN users u ON u.id=d.author_id WHERE d.repository_id=$1 AND d.number=$2`, repo.ID, r.PathValue("number")).Scan(&id, &title, &body, &category, &state, &author, &created)
	if err != nil {
		fail(w, 404, "not_found", "Discussion not found.")
		return
	}
	if r.Method == http.MethodPatch {
		if !repo.CanTriage {
			fail(w, 403, "forbidden", "Repository triage permission is required.")
			return
		}
		var in struct {
			State string `json:"state"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.State != "open" && in.State != "closed" {
			fail(w, 422, "validation_failed", "State must be open or closed.")
			return
		}
		if _, err = a.db.Exec(r.Context(), `UPDATE discussions SET state=$1 WHERE id=$2`, in.State, id); err != nil {
			serverError(w, err)
			return
		}
		state = in.State
	}
	rows, err := a.db.Query(r.Context(), `SELECT c.id::text,c.body,u.username,c.created_at FROM discussion_comments c JOIN users u ON u.id=c.author_id WHERE c.discussion_id=$1 ORDER BY c.created_at`, id)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	comments := []map[string]any{}
	for rows.Next() {
		var cid, cbody, cauthor string
		var ccreated time.Time
		if err = rows.Scan(&cid, &cbody, &cauthor, &ccreated); err != nil {
			serverError(w, err)
			return
		}
		comments = append(comments, map[string]any{"id": cid, "body": cbody, "author": cauthor, "created_at": ccreated})
	}
	respond(w, 200, map[string]any{"number": r.PathValue("number"), "title": title, "body": body, "category": category, "state": state, "author": author, "created_at": created, "comments": comments})
}

func (a *App) discussionComment(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanComment {
		fail(w, 403, "forbidden", "Sign in to comment.")
		return
	}
	var id, state string
	if err := a.db.QueryRow(r.Context(), `SELECT id::text,state FROM discussions WHERE repository_id=$1 AND number=$2`, repo.ID, r.PathValue("number")).Scan(&id, &state); err != nil {
		fail(w, 404, "not_found", "Discussion not found.")
		return
	}
	if state != "open" {
		fail(w, 409, "discussion_closed", "Reopen the discussion before commenting.")
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Body) == "" || len(in.Body) > 20000 {
		fail(w, 422, "validation_failed", "Write a comment up to 20,000 characters.")
		return
	}
	if _, err := a.db.Exec(r.Context(), `INSERT INTO discussion_comments(id,discussion_id,author_id,body) VALUES($1,$2,$3,$4)`, auth.ID(), id, a.user(r).ID, in.Body); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]bool{"created": true})
}

func (a *App) snippets(w http.ResponseWriter, r *http.Request) {
	u := a.user(r)
	if r.Method == http.MethodGet {
		viewer := ""
		if u != nil {
			viewer = u.ID
		}
		mine := r.URL.Query().Get("mine") == "1"
		query := `SELECT s.id::text,s.title,s.filename,s.visibility,u.username,s.updated_at FROM snippets s JOIN users u ON u.id=s.owner_id WHERE s.visibility='public' OR s.owner_id::text=$1 ORDER BY s.updated_at DESC LIMIT 50`
		if mine {
			if u == nil {
				fail(w, 401, "authentication_required", "Sign in to see your snippets.")
				return
			}
			query = `SELECT s.id::text,s.title,s.filename,s.visibility,u.username,s.updated_at FROM snippets s JOIN users u ON u.id=s.owner_id WHERE s.owner_id::text=$1 ORDER BY s.updated_at DESC LIMIT 50`
		}
		rows, err := a.db.Query(r.Context(), query, viewer)
		if err != nil {
			serverError(w, err)
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, title, filename, visibility, owner string
			var updated time.Time
			if err = rows.Scan(&id, &title, &filename, &visibility, &owner, &updated); err != nil {
				serverError(w, err)
				return
			}
			items = append(items, map[string]any{"id": id, "title": title, "filename": filename, "visibility": visibility, "owner": owner, "updated_at": updated})
		}
		respond(w, 200, map[string]any{"items": items})
		return
	}
	if u == nil {
		fail(w, 401, "authentication_required", "Sign in to save a snippet.")
		return
	}
	var in struct {
		Title      string `json:"title"`
		Filename   string `json:"filename"`
		Content    string `json:"content"`
		Visibility string `json:"visibility"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Filename = strings.TrimSpace(in.Filename)
	if in.Visibility == "" {
		in.Visibility = "public"
	}
	if in.Title == "" || len(in.Title) > 120 || !routeCacheKey.MatchString(in.Filename) || len(in.Content) > 65536 || (in.Visibility != "public" && in.Visibility != "private") {
		fail(w, 422, "validation_failed", "Give the snippet a title, a simple filename, content up to 64 KB, and public or private visibility.")
		return
	}
	id := auth.ID()
	if _, err := a.db.Exec(r.Context(), `INSERT INTO snippets(id,owner_id,title,filename,content,visibility) VALUES($1,$2,$3,$4,$5,$6)`, id, u.ID, in.Title, in.Filename, in.Content, in.Visibility); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]string{"id": id})
}

func (a *App) snippet(w http.ResponseWriter, r *http.Request) {
	u := a.user(r)
	var ownerID, title, filename, content, visibility, owner string
	err := a.db.QueryRow(r.Context(), `SELECT s.owner_id::text,s.title,s.filename,s.content,s.visibility,u.username FROM snippets s JOIN users u ON u.id=s.owner_id WHERE s.id=$1`, r.PathValue("id")).Scan(&ownerID, &title, &filename, &content, &visibility, &owner)
	if err != nil || (visibility != "public" && (u == nil || u.ID != ownerID)) {
		fail(w, 404, "not_found", "Snippet not found.")
		return
	}
	if r.Method == http.MethodDelete {
		if u == nil || u.ID != ownerID {
			fail(w, 403, "forbidden", "Only the snippet owner can delete it.")
			return
		}
		_, _ = a.db.Exec(r.Context(), `DELETE FROM snippets WHERE id=$1`, r.PathValue("id"))
		respond(w, 200, map[string]bool{"deleted": true})
		return
	}
	respond(w, 200, map[string]any{"id": r.PathValue("id"), "title": title, "filename": filename, "content": content, "visibility": visibility, "owner": owner})
}

type devFile struct {
	Name  string `yaml:"name"`
	Notes string `yaml:"notes"`
	Image string `yaml:"image"`
}

func parseDevFile(data []byte) (*devFile, error) {
	if len(data) == 0 || len(data) > 16*1024 {
		return nil, errors.New("the dev environment file must be under 16 KB")
	}
	var file devFile
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil {
		return nil, errors.New("the dev environment file has an unsupported field; commands are not accepted")
	}
	file.Name = strings.TrimSpace(file.Name)
	file.Notes = strings.TrimSpace(file.Notes)
	file.Image = strings.TrimSpace(file.Image)
	if file.Name == "" || len(file.Name) > 80 || len(file.Notes) > 1000 || len(file.Image) > 200 {
		return nil, errors.New("name the environment and keep the image name as a display label")
	}
	return &file, nil
}

func (a *App) devEnvironments(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if r.Method == http.MethodGet {
		rows, err := a.db.Query(r.Context(), `SELECT id::text,ref,sha,image,status,created_at FROM dev_environments WHERE repository_id=$1 ORDER BY created_at DESC LIMIT 20`, repo.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, ref, sha, image, status string
			var created time.Time
			if err = rows.Scan(&id, &ref, &sha, &image, &status, &created); err != nil {
				serverError(w, err)
				return
			}
			items = append(items, map[string]any{"id": id, "ref": ref, "sha": sha, "image": image, "status": status, "execution_enabled": false, "created_at": created})
		}
		respond(w, 200, map[string]any{"items": items, "execution_enabled": false})
		return
	}
	if !repo.CanWrite {
		fail(w, 403, "forbidden", "Repository write permission is required.")
		return
	}
	data, err := a.git.Blob(r.Context(), repo.ID, repo.DefaultBranch, ".gitown/dev.yml")
	if err != nil {
		fail(w, 422, "validation_failed", "Add .gitown/dev.yml on the default branch before requesting an environment.")
		return
	}
	file, err := parseDevFile(data)
	if err != nil {
		fail(w, 422, "validation_failed", err.Error())
		return
	}
	sha, err := a.git.Resolve(r.Context(), repo.ID, repo.DefaultBranch)
	if err != nil {
		serverError(w, err)
		return
	}
	id := auth.ID()
	if _, err = a.db.Exec(r.Context(), `INSERT INTO dev_environments(id,repository_id,actor_id,ref,sha,image,status) VALUES($1,$2,$3,$4,$5,$6,'pending_sandbox_review')`, id, repo.ID, a.user(r).ID, repo.DefaultBranch, sha, file.Image); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]any{"id": id, "status": "pending_sandbox_review", "image": file.Image, "execution_enabled": false})
}

func (a *App) cancelDevEnvironment(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, true)
	if repo == nil {
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE dev_environments SET status='cancelled' WHERE id=$1 AND repository_id=$2 AND status='pending_sandbox_review'`, r.PathValue("id"), repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Environment not found.")
		return
	}
	respond(w, 200, map[string]string{"status": "cancelled"})
}

type codeRule struct {
	Pattern string
	Owners  []string
}

func parseCodeOwners(text string) []codeRule {
	var rules []codeRule
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var owners []string
		for _, field := range fields[1:] {
			name := strings.TrimPrefix(field, "@")
			if slug.MatchString(name) {
				owners = append(owners, name)
			}
		}
		if len(owners) > 0 {
			rules = append(rules, codeRule{Pattern: fields[0], Owners: owners})
		}
	}
	return rules
}

func codeOwnersForPath(rules []codeRule, path string) []string {
	var owners []string
	for _, rule := range rules {
		if codePatternMatches(rule.Pattern, path) {
			owners = rule.Owners
		}
	}
	return owners
}

func codePatternMatches(pattern, path string) bool {
	if pattern == "*" {
		return true
	}
	pattern = strings.TrimPrefix(pattern, "/")
	if strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(path, strings.TrimSuffix(pattern, "/")) || strings.HasPrefix(path, pattern)
	}
	if strings.HasSuffix(pattern, "/**") {
		return strings.HasPrefix(path, strings.TrimSuffix(pattern, "/**")+"/") || path == strings.TrimSuffix(pattern, "/**")
	}
	return path == pattern
}

func (a *App) codeOwners(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	text := a.readCodeOwners(r.Context(), repo)
	rules := parseCodeOwners(text)
	items := []map[string]any{}
	for _, rule := range rules {
		items = append(items, map[string]any{"pattern": rule.Pattern, "owners": rule.Owners})
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) readCodeOwners(ctx context.Context, repo *Repository) string {
	for _, path := range []string{".gitown/CODEOWNERS", "CODEOWNERS"} {
		data, err := a.git.Blob(ctx, repo.ID, repo.DefaultBranch, path)
		if err == nil {
			return string(data)
		}
	}
	return ""
}

func (a *App) requestCodeOwners(ctx context.Context, tx pgx.Tx, repo *Repository, actor *User, pullID, author, base, head string) error {
	text := a.readCodeOwners(ctx, repo)
	if text == "" {
		return nil
	}
	out, err := a.git.Run(ctx, repo.ID, nil, "diff", "--name-only", base+"..."+head)
	if err != nil {
		return nil
	}
	wanted := map[string]bool{}
	for _, path := range strings.Split(string(out), "\n") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		for _, name := range codeOwnersForPath(parseCodeOwners(text), path) {
			if name != author {
				wanted[name] = true
			}
		}
	}
	for name := range wanted {
		var reviewerID string
		err = tx.QueryRow(ctx, `SELECT u.id FROM users u WHERE u.username=$1 AND (u.id=$2 OR EXISTS(SELECT 1 FROM repository_members rm WHERE rm.repository_id=$3 AND rm.user_id=u.id AND rm.role IN ('write','maintain')))`, name, repo.OwnerID, repo.ID).Scan(&reviewerID)
		if err != nil {
			continue
		}
		if _, err = tx.Exec(ctx, `INSERT INTO pull_review_requests(pull_request_id,reviewer_id,requested_by) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, pullID, reviewerID, actor.ID); err != nil {
			return err
		}
	}
	return nil
}

func secretMarker(line string) string {
	markers := []string{
		"-----BEGIN PRIVATE KEY-----",
		"-----BEGIN RSA PRIVATE KEY-----",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"-----BEGIN EC PRIVATE KEY-----",
		"-----BEGIN DSA PRIVATE KEY-----",
		"ghp_", "github_pat_", "glpat-", "xoxb-", "xoxp-", "gtn_",
	}
	for _, marker := range markers {
		if strings.Contains(line, marker) {
			return marker
		}
	}
	return ""
}

func parseGoMod(text string) [][2]string {
	var deps [][2]string
	block := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "//", 2)[0])
		if line == "require (" {
			block = true
			continue
		}
		if block && line == ")" {
			block = false
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "require" {
			deps = append(deps, [2]string{fields[1], fields[2]})
		} else if block && len(fields) >= 2 {
			deps = append(deps, [2]string{fields[0], fields[1]})
		}
	}
	return deps
}

func parseRequirements(text string) [][2]string {
	var deps [][2]string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		name, version, ok := strings.Cut(line, "==")
		if ok && name != "" && version != "" && !strings.ContainsAny(name, " <>") {
			deps = append(deps, [2]string{name, version})
		}
	}
	return deps
}

func parsePackageJSON(text string) [][2]string {
	var doc struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal([]byte(text), &doc) != nil {
		return nil
	}
	var deps [][2]string
	for name, version := range doc.Dependencies {
		deps = append(deps, [2]string{name, version})
	}
	for name, version := range doc.DevDependencies {
		deps = append(deps, [2]string{name, version})
	}
	return deps
}

func (a *App) dependencyGraph(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT manifest,ecosystem,package_name,version FROM dependency_edges WHERE repository_id=$1 ORDER BY manifest,package_name`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var manifest, ecosystem, name, version string
		if err = rows.Scan(&manifest, &ecosystem, &name, &version); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]string{"manifest": manifest, "ecosystem": ecosystem, "name": name, "version": version})
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) secretFindings(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil || !repo.CanWrite {
		if repo != nil {
			fail(w, 403, "forbidden", "Repository write permission is required to view secret findings.")
		}
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id::text,path,line_number,marker,state FROM secret_findings WHERE repository_id=$1 ORDER BY path,line_number`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, path, marker, state string
		var line int
		if err = rows.Scan(&id, &path, &line, &marker, &state); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "path": path, "line": line, "marker": marker, "state": state})
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) dismissSecretFinding(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE secret_findings SET state='dismissed' WHERE id=$1 AND repository_id=$2`, r.PathValue("id"), repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Finding not found.")
		return
	}
	respond(w, 200, map[string]string{"state": "dismissed"})
}

func (a *App) advisories(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if r.Method == http.MethodGet {
		query := `SELECT id::text,code,severity,summary,package_name,ecosystem,patched_version,state,created_at FROM security_advisories WHERE repository_id=$1 AND state='published' ORDER BY created_at DESC`
		if repo.CanMaintain {
			query = `SELECT id::text,code,severity,summary,package_name,ecosystem,patched_version,state,created_at FROM security_advisories WHERE repository_id=$1 ORDER BY created_at DESC`
		}
		rows, err := a.db.Query(r.Context(), query, repo.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, code, severity, summary, pkg, ecosystem, patched, state string
			var created time.Time
			if err = rows.Scan(&id, &code, &severity, &summary, &pkg, &ecosystem, &patched, &state, &created); err != nil {
				serverError(w, err)
				return
			}
			items = append(items, map[string]any{"id": id, "code": code, "severity": severity, "summary": summary, "package_name": pkg, "ecosystem": ecosystem, "patched_version": patched, "state": state, "created_at": created})
		}
		respond(w, 200, map[string]any{"items": items})
		return
	}
	if !repo.CanMaintain {
		fail(w, 403, "forbidden", "Repository maintain permission is required.")
		return
	}
	var in struct {
		Severity  string `json:"severity"`
		Summary   string `json:"summary"`
		Package   string `json:"package_name"`
		Ecosystem string `json:"ecosystem"`
		Patched   string `json:"patched_version"`
		State     string `json:"state"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Summary = strings.TrimSpace(in.Summary)
	in.Package = strings.TrimSpace(in.Package)
	if in.State == "" {
		in.State = "draft"
	}
	if (in.Severity != "low" && in.Severity != "medium" && in.Severity != "high" && in.Severity != "critical") || in.Summary == "" || len(in.Summary) > 300 || in.Package == "" || len(in.Package) > 200 || (in.Ecosystem != "go" && in.Ecosystem != "npm" && in.Ecosystem != "pypi") || len(in.Patched) > 80 || (in.State != "draft" && in.State != "published") {
		fail(w, 422, "validation_failed", "Provide a severity, summary, package, ecosystem (go, npm, or pypi), and draft or published state.")
		return
	}
	code := "GOWN-" + strings.ToUpper(strings.ReplaceAll(auth.ID(), "-", "")[:8])
	id := auth.ID()
	if _, err := a.db.Exec(r.Context(), `INSERT INTO security_advisories(id,repository_id,author_id,code,severity,summary,package_name,ecosystem,patched_version,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, repo.ID, a.user(r).ID, code, in.Severity, in.Summary, in.Package, in.Ecosystem, in.Patched, in.State); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]string{"id": id, "code": code, "state": in.State})
}

func (a *App) vulnerabilityAlerts(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil || !repo.CanWrite {
		if repo != nil {
			fail(w, 403, "forbidden", "Repository write permission is required.")
		}
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT a.id::text,s.code,a.package_name,a.installed_version,s.patched_version,a.manifest,a.state FROM vulnerability_alerts a JOIN security_advisories s ON s.id=a.advisory_id WHERE a.repository_id=$1 ORDER BY a.package_name`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, code, pkg, installed, patched, manifest, state string
		if err = rows.Scan(&id, &code, &pkg, &installed, &patched, &manifest, &state); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "code": code, "package_name": pkg, "installed_version": installed, "patched_version": patched, "manifest": manifest, "state": state})
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) dismissVulnerability(w http.ResponseWriter, r *http.Request) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE vulnerability_alerts SET state='dismissed' WHERE id=$1 AND repository_id=$2`, r.PathValue("id"), repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Alert not found.")
		return
	}
	respond(w, 200, map[string]string{"state": "dismissed"})
}

func (a *App) scanSupplyChain(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, true)
	if repo == nil {
		return
	}
	findings, alerts, issues, err := a.refreshSupplyChain(r.Context(), repo, a.user(r))
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"secret_findings": findings, "vulnerability_alerts": alerts, "dependency_issues": issues})
}

func (a *App) refreshSupplyChain(ctx context.Context, repo *Repository, actor *User) (int, int, int, error) {
	sha, err := a.git.Resolve(ctx, repo.ID, repo.DefaultBranch)
	if err != nil {
		return 0, 0, 0, nil
	}
	findings := 0
	if out, lsErr := a.git.Run(ctx, repo.ID, nil, "ls-tree", "-r", "--name-only", repo.DefaultBranch); lsErr == nil {
		if _, err = a.db.Exec(ctx, `DELETE FROM secret_findings WHERE repository_id=$1 AND state='open'`, repo.ID); err != nil {
			return 0, 0, 0, err
		}
		count := 0
		for _, path := range strings.Split(string(out), "\n") {
			path = strings.TrimSpace(path)
			if path == "" || count > 200 {
				continue
			}
			lower := strings.ToLower(path)
			if strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".woff") {
				continue
			}
			count++
			data, blobErr := a.git.Blob(ctx, repo.ID, repo.DefaultBranch, path)
			if blobErr != nil || len(data) > 256*1024 {
				continue
			}
			for number, line := range strings.Split(string(data), "\n") {
				marker := secretMarker(line)
				if marker == "" || number > 4000 {
					continue
				}
				tag, insertErr := a.db.Exec(ctx, `INSERT INTO secret_findings(id,repository_id,path,line_number,marker,sha) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, auth.ID(), repo.ID, path, number+1, marker, sha)
				if insertErr != nil {
					return 0, 0, 0, insertErr
				}
				findings += int(tag.RowsAffected())
			}
		}
	}
	type edge struct{ manifest, ecosystem, name, version string }
	var edges []edge
	if data, blobErr := a.git.Blob(ctx, repo.ID, repo.DefaultBranch, "go.mod"); blobErr == nil {
		for _, dep := range parseGoMod(string(data)) {
			edges = append(edges, edge{"go.mod", "go", dep[0], dep[1]})
		}
	}
	if data, blobErr := a.git.Blob(ctx, repo.ID, repo.DefaultBranch, "package.json"); blobErr == nil {
		for _, dep := range parsePackageJSON(string(data)) {
			edges = append(edges, edge{"package.json", "npm", dep[0], dep[1]})
		}
	}
	if data, blobErr := a.git.Blob(ctx, repo.ID, repo.DefaultBranch, "requirements.txt"); blobErr == nil {
		for _, dep := range parseRequirements(string(data)) {
			edges = append(edges, edge{"requirements.txt", "pypi", dep[0], dep[1]})
		}
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `DELETE FROM dependency_edges WHERE repository_id=$1`, repo.ID); err != nil {
		return 0, 0, 0, err
	}
	for _, edge := range edges {
		if len(edge.name) > 200 || len(edge.version) > 80 {
			continue
		}
		if _, err = tx.Exec(ctx, `INSERT INTO dependency_edges(id,repository_id,manifest,ecosystem,package_name,version) VALUES($1,$2,$3,$4,$5,$6)`, auth.ID(), repo.ID, edge.manifest, edge.ecosystem, edge.name, edge.version); err != nil {
			return 0, 0, 0, err
		}
	}
	alerts := 0
	issues := 0
	if actor != nil {
		rows, err := tx.Query(ctx, `SELECT s.id::text,s.code,s.package_name,s.patched_version,e.version,e.manifest FROM dependency_edges e JOIN security_advisories s ON s.package_name=e.package_name AND s.ecosystem=e.ecosystem AND s.state='published' WHERE e.repository_id=$1 AND (s.repository_id=$1 OR EXISTS(SELECT 1 FROM repositories r WHERE r.id=s.repository_id AND r.visibility='public' AND r.deleted_at IS NULL))`, repo.ID)
		if err != nil {
			return 0, 0, 0, err
		}
		type hit struct{ advisory, code, pkg, patched, installed, manifest string }
		var hits []hit
		for rows.Next() {
			var item hit
			if err = rows.Scan(&item.advisory, &item.code, &item.pkg, &item.patched, &item.installed, &item.manifest); err != nil {
				rows.Close()
				return 0, 0, 0, err
			}
			hits = append(hits, item)
		}
		rows.Close()
		for _, hit := range hits {
			tag, err := tx.Exec(ctx, `INSERT INTO vulnerability_alerts(id,repository_id,advisory_id,manifest,package_name,installed_version) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, auth.ID(), repo.ID, hit.advisory, hit.manifest, hit.pkg, hit.installed)
			if err != nil {
				return 0, 0, 0, err
			}
			alerts += int(tag.RowsAffected())
			if hit.patched == "" || hit.patched == hit.installed || issues >= 5 {
				continue
			}
			title := "Update dependency " + hit.pkg
			var exists bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issues WHERE repository_id=$1 AND title=$2 AND state='open')`, repo.ID, title).Scan(&exists); err != nil {
				return 0, 0, 0, err
			}
			if exists {
				continue
			}
			issueID := auth.ID()
			body := hit.code + " affects " + hit.pkg + " " + hit.installed + " in " + hit.manifest + ". The patched version recorded on the advisory is " + hit.patched + ". This note was opened by the dependency update check. It does not change any file."
			if _, err = tx.Exec(ctx, `INSERT INTO issues(id,repository_id,number,author_id,title,body) SELECT $1,$2,COALESCE(MAX(number),0)+1,$3,$4,$5 FROM issues WHERE repository_id=$2`, issueID, repo.ID, repo.OwnerID, title, body); err != nil {
				return 0, 0, 0, err
			}
			owner := &User{ID: repo.OwnerID, Username: repo.Owner, DisplayName: repo.Owner}
			if err = a.AnnounceIssue(ctx, tx, repo, owner, issueID, body); err != nil {
				return 0, 0, 0, err
			}
			issues++
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, 0, 0, err
	}
	return findings, alerts, issues, nil
}

func (a *App) sponsorships(w http.ResponseWriter, r *http.Request) {
	var targetID, username string
	if err := a.db.QueryRow(r.Context(), `SELECT id::text,username FROM users WHERE username=$1`, strings.ToLower(r.PathValue("username"))).Scan(&targetID, &username); err != nil {
		fail(w, 404, "not_found", "Builder not found.")
		return
	}
	if r.Method == http.MethodGet {
		viewer := ""
		if u := a.user(r); u != nil {
			viewer = u.ID
		}
		rows, err := a.db.Query(r.Context(), `SELECT u.username,s.amount_cents,s.message,s.created_at FROM sponsorships s JOIN users u ON u.id=s.sponsor_id WHERE s.target_user_id=$1 AND (s.public OR s.sponsor_id::text=$2 OR s.target_user_id::text=$2) ORDER BY s.created_at DESC`, targetID, viewer)
		if err != nil {
			serverError(w, err)
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var sponsor, message string
			var amount int
			var created time.Time
			if err = rows.Scan(&sponsor, &amount, &message, &created); err != nil {
				serverError(w, err)
				return
			}
			items = append(items, map[string]any{"sponsor": sponsor, "amount_cents": amount, "message": message, "created_at": created, "charges": false})
		}
		respond(w, 200, map[string]any{"items": items, "charges": false})
		return
	}
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if u.ID == targetID {
		fail(w, 422, "validation_failed", "A builder cannot sponsor their own account.")
		return
	}
	var in struct {
		Amount  int    `json:"amount_cents"`
		Message string `json:"message"`
		Public  *bool  `json:"public"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Amount < 0 || in.Amount > 100000000 || len(in.Message) > 280 {
		fail(w, 422, "validation_failed", "Record a pledge amount and a message up to 280 characters. GITOWN does not charge a card.")
		return
	}
	public := true
	if in.Public != nil {
		public = *in.Public
	}
	if _, err := a.db.Exec(r.Context(), `INSERT INTO sponsorships(id,sponsor_id,target_user_id,amount_cents,message,public) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(sponsor_id,target_user_id) DO UPDATE SET amount_cents=excluded.amount_cents,message=excluded.message,public=excluded.public`, auth.ID(), u.ID, targetID, in.Amount, strings.TrimSpace(in.Message), public); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]any{"sponsor": u.Username, "target": username, "amount_cents": in.Amount, "charges": false})
}

func (a *App) registerDevice(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Name     string `json:"name"`
		Platform string `json:"platform"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 || (in.Platform != "ios" && in.Platform != "android" && in.Platform != "web") {
		fail(w, 422, "validation_failed", "Name the device and choose ios, android, or web.")
		return
	}
	token := auth.Secret("mob_")
	if _, err := a.db.Exec(r.Context(), `INSERT INTO mobile_devices(id,user_id,name,platform,token_hash) VALUES($1,$2,$3,$4,$5)`, auth.ID(), u.ID, in.Name, in.Platform, auth.Digest(token)); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, map[string]any{"name": in.Name, "platform": in.Platform, "token": token, "delivery": "pull"})
}

func (a *App) mobileFeed(w http.ResponseWriter, r *http.Request) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !strings.HasPrefix(raw, "mob_") || len(raw) > 200 {
		fail(w, 401, "authentication_required", "Use the device token from registration.")
		return
	}
	var userID, deviceID string
	err := a.db.QueryRow(r.Context(), `SELECT id::text,user_id::text FROM mobile_devices WHERE token_hash=$1`, auth.Digest(raw)).Scan(&deviceID, &userID)
	if err != nil {
		fail(w, 401, "authentication_required", "Use the device token from registration.")
		return
	}
	_, _ = a.db.Exec(r.Context(), `UPDATE mobile_devices SET last_seen_at=now() WHERE id=$1`, deviceID)
	rows, err := a.db.Query(r.Context(), `SELECT id,kind,excerpt,created_at FROM notifications WHERE recipient_id=$1 AND read_at IS NULL ORDER BY id DESC LIMIT 50`, userID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var kind, excerpt string
		var created time.Time
		if err = rows.Scan(&id, &kind, &excerpt, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "kind": kind, "excerpt": excerpt, "created_at": created})
	}
	respond(w, 200, map[string]any{"items": items, "delivery": "pull"})
}

func (a *App) serveShowcase(w http.ResponseWriter, r *http.Request) {
	repo, err := scanRepo(a.db.QueryRow(r.Context(), `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id WHERE u.username=$1 AND r.name=$2 AND r.deleted_at IS NULL`, r.PathValue("owner"), r.PathValue("repo")))
	if err != nil || repo.Visibility != "public" || repo.Archived {
		http.NotFound(w, r)
		return
	}
	rel := strings.Trim(r.PathValue("path"), "/")
	if rel == "" {
		rel = "index.html"
	}
	if strings.Contains(rel, "..") || strings.ContainsAny(rel, "\\\x00") {
		http.NotFound(w, r)
		return
	}
	ref := repo.DefaultBranch
	path := ".gitown/showcase/" + rel
	if _, resolveErr := a.git.Resolve(r.Context(), repo.ID, "showcase"); resolveErr == nil {
		ref = "showcase"
		path = rel
	}
	content, err := a.git.Blob(r.Context(), repo.ID, ref, path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	kind := showcaseType(rel)
	if kind == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(200)
	_, _ = w.Write(content)
}

func showcaseType(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(lower, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(lower, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".md"):
		return "text/plain; charset=utf-8"
	case strings.HasSuffix(lower, ".json"):
		return "application/json"
	case strings.HasSuffix(lower, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".ico"):
		return "image/x-icon"
	default:
		return ""
	}
}

func (a *App) wikiSearch(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > 80 || strings.HasPrefix(query, "-") || strings.ContainsAny(query, "\x00\r\n") {
		fail(w, 422, "validation_failed", "Enter a wiki search of up to 80 characters.")
		return
	}
	out, code, err := a.git.Command(r.Context(), 20*time.Second, repo.ID, "grep", "-n", "-I", "-F", "--max-count=20", "-e", query, repo.DefaultBranch, "--", ".gitown/wiki")
	if err != nil {
		serverError(w, err)
		return
	}
	items := []map[string]any{}
	if code == 0 {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if prefix := repo.DefaultBranch + ":"; strings.HasPrefix(line, prefix) {
				line = strings.TrimPrefix(line, prefix)
			}
			parts := strings.SplitN(line, ":", 3)
			if len(parts) != 3 || !strings.HasPrefix(parts[0], ".gitown/wiki/") || !strings.HasSuffix(parts[0], ".md") {
				continue
			}
			slug := strings.TrimSuffix(strings.TrimPrefix(parts[0], ".gitown/wiki/"), ".md")
			items = append(items, map[string]any{"slug": slug, "line": parts[1], "text": parts[2]})
		}
	}
	respond(w, 200, map[string]any{"items": items})
}
