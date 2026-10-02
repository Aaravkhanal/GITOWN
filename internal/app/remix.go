package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

type forkLink struct {
	ID, Parent, Root string
}

func (a *App) lookupRepo(ctx context.Context, u *User, owner, name string) (*Repository, error) {
	repo, err := scanRepo(a.db.QueryRow(ctx, `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id WHERE u.username=$1 AND r.name=$2 AND r.deleted_at IS NULL`, owner, name))
	if err != nil {
		return nil, err
	}
	if err = a.decorate(ctx, &repo, u); err != nil {
		return nil, err
	}
	if repo.Visibility != "public" && repo.Role == "" {
		return nil, pgx.ErrNoRows
	}
	return &repo, nil
}

func (a *App) forkLink(ctx context.Context, id string) (forkLink, error) {
	var link forkLink
	var parent, root *string
	err := a.db.QueryRow(ctx, `SELECT id::text,parent_repository_id::text,fork_root_id::text FROM repositories WHERE id=$1`, id).Scan(&link.ID, &parent, &root)
	if parent != nil {
		link.Parent = *parent
	}
	if root != nil {
		link.Root = *root
	}
	return link, err
}

func sameRemixFamily(base, head forkLink) bool {
	if base.Parent == head.ID || head.Parent == base.ID {
		return true
	}
	if base.Root != "" && (base.Root == head.ID || base.Root == head.Root) {
		return true
	}
	if head.Root != "" && (head.Root == base.ID || head.Root == base.Root) {
		return true
	}
	return false
}

func (a *App) remixes(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	var parentOwner, parentName string
	_ = a.db.QueryRow(r.Context(), `SELECT u.username,p.name FROM repositories r JOIN repositories p ON p.id=r.parent_repository_id JOIN users u ON u.id=p.owner_id WHERE r.id=$1 AND p.deleted_at IS NULL`, repo.ID).Scan(&parentOwner, &parentName)
	viewer := ""
	if u := a.user(r); u != nil {
		viewer = u.ID
	}
	rows, err := a.db.Query(r.Context(), `SELECT u.username,c.name,c.visibility,c.created_at FROM repositories c JOIN users u ON u.id=c.owner_id WHERE c.parent_repository_id=$1 AND c.deleted_at IS NULL AND (c.visibility='public' OR c.owner_id::text=$2) ORDER BY c.created_at DESC LIMIT 50`, repo.ID, viewer)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var owner, name, visibility string
		var created any
		if err = rows.Scan(&owner, &name, &visibility, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"owner": owner, "name": name, "visibility": visibility, "created_at": created})
	}
	var parent any
	if parentOwner != "" {
		parent = map[string]string{"owner": parentOwner, "name": parentName}
	}
	respond(w, 200, map[string]any{"remix_of": parent, "remixes": items})
}

func (a *App) createRemix(w http.ResponseWriter, r *http.Request) {
	source := a.access(w, r, false)
	if source == nil {
		return
	}
	if source.Archived {
		fail(w, 409, "repository_archived", "Archived repositories cannot be remixed.")
		return
	}
	u := a.user(r)
	if u == nil {
		fail(w, 401, "authentication_required", "Sign in to remix a repository.")
		return
	}
	var in struct {
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Visibility = strings.ToLower(strings.TrimSpace(in.Visibility))
	if source.Visibility != "public" {
		in.Visibility = "private"
	}
	if !repoSlug.MatchString(in.Name) || strings.HasSuffix(in.Name, ".git") || (in.Visibility != "public" && in.Visibility != "private") {
		fail(w, 422, "validation_failed", "Choose a repository name and public or private visibility.")
		return
	}
	link, err := a.forkLink(r.Context(), source.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	root := link.Root
	if root == "" {
		root = source.ID
	}
	repo := Repository{ID: auth.ID(), OwnerID: u.ID, Owner: u.Username, Name: in.Name, Description: source.Description, Visibility: in.Visibility, DefaultBranch: source.DefaultBranch}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `SELECT id FROM users WHERE id=$1 FOR UPDATE`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	var count int
	if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM repositories WHERE owner_id=$1 AND deleted_at IS NULL`, u.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 100 {
		fail(w, 422, "repository_limit", "This alpha supports up to 100 repositories per account.")
		return
	}
	err = tx.QueryRow(r.Context(), `INSERT INTO repositories(id,owner_id,name,description,visibility,default_branch,parent_repository_id,fork_root_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at,pushed_at`, repo.ID, u.ID, repo.Name, repo.Description, repo.Visibility, repo.DefaultBranch, source.ID, root).Scan(&repo.CreatedAt, &repo.PushedAt)
	if conflict(err) {
		fail(w, 409, "repository_exists", "You already have a repository with that name.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if err = a.git.CloneLocal(r.Context(), repo.ID, source.ID); err != nil {
		serverError(w, err)
		return
	}
	if branch, branchErr := a.git.HeadBranch(r.Context(), repo.ID); branchErr == nil && branch != "" {
		repo.DefaultBranch = branch
		if _, err = tx.Exec(r.Context(), `UPDATE repositories SET default_branch=$1 WHERE id=$2`, branch, repo.ID); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.remixed',$2)`, u.ID, source.Owner+"/"+source.Name+"->"+u.Username+"/"+repo.Name); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		_ = os.RemoveAll(a.git.Path(repo.ID))
		serverError(w, err)
		return
	}
	_ = a.noteRepositoryFacts(r.Context(), &repo)
	if err = a.decorate(r.Context(), &repo, u); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, repo)
}

func parseImportURL(source, raw string) (string, error) {
	hosts := map[string]string{"github": "github.com", "gitlab": "gitlab.com", "bitbucket": "bitbucket.org"}
	host, ok := hosts[source]
	if !ok {
		return "", errors.New("source must be github, gitlab, or bitbucket")
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
		return "", errors.New("import a public https URL with no credentials, query, or port")
	}
	if !strings.EqualFold(parsed.Hostname(), host) {
		return "", errors.New("that URL is not on the selected forge")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(parts[0], "..") || strings.Contains(parts[1], "..") {
		return "", errors.New("use a repository URL like https://" + host + "/owner/name")
	}
	name := strings.TrimSuffix(parts[1], ".git")
	if !repoSlug.MatchString(strings.ToLower(name)) || !repoSlug.MatchString(strings.ToLower(parts[0])) {
		return "", errors.New("the owner and repository names in that URL are not supported")
	}
	return "https://" + host + "/" + parts[0] + "/" + name + ".git", nil
}

func ensurePublicImportHost(ctx context.Context, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	lookup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(lookup, parsed.Hostname())
	if err != nil || len(ips) == 0 {
		return errors.New("the forge address could not be resolved")
	}
	for _, addr := range ips {
		if isDisallowedWebhookIP(addr.IP) {
			return errors.New("that address is not a public forge")
		}
	}
	return nil
}

func (a *App) importRepository(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Source     string `json:"source"`
		URL        string `json:"url"`
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
	}
	if !decode(w, r, &in) {
		return
	}
	remote, err := parseImportURL(strings.ToLower(strings.TrimSpace(in.Source)), in.URL)
	if err != nil {
		fail(w, 422, "validation_failed", err.Error())
		return
	}
	if err = ensurePublicImportHost(r.Context(), remote); err != nil {
		fail(w, 422, "validation_failed", err.Error())
		return
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	in.Visibility = strings.ToLower(strings.TrimSpace(in.Visibility))
	if in.Visibility == "" {
		in.Visibility = "private"
	}
	if !repoSlug.MatchString(in.Name) || (in.Visibility != "public" && in.Visibility != "private") {
		fail(w, 422, "validation_failed", "Choose a repository name and public or private visibility. Imports stay private unless you ask for public.")
		return
	}
	repo := Repository{ID: auth.ID(), OwnerID: u.ID, Owner: u.Username, Name: in.Name, Description: "Imported from " + remote, Visibility: in.Visibility, DefaultBranch: "main"}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	err = tx.QueryRow(r.Context(), `INSERT INTO repositories(id,owner_id,name,description,visibility,source_url) VALUES($1,$2,$3,$4,$5,$6) RETURNING created_at,pushed_at`, repo.ID, u.ID, repo.Name, repo.Description, repo.Visibility, remote).Scan(&repo.CreatedAt, &repo.PushedAt)
	if conflict(err) {
		fail(w, 409, "repository_exists", "You already have a repository with that name.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if err = a.git.CloneHTTPS(r.Context(), repo.ID, remote); err != nil {
		fail(w, 422, "import_failed", "The public repository could not be cloned. Check that it exists and is public.")
		return
	}
	if branch, branchErr := a.git.HeadBranch(r.Context(), repo.ID); branchErr == nil {
		repo.DefaultBranch = branch
		_, _ = tx.Exec(r.Context(), `UPDATE repositories SET default_branch=$1 WHERE id=$2`, branch, repo.ID)
	}
	_ = a.noteRepositoryFacts(r.Context(), &repo)
	if err = a.withinQuota(r.Context(), &repo, 0); err != nil {
		_ = os.RemoveAll(a.git.Path(repo.ID))
		fail(w, 422, "quota_exceeded", "The imported repository exceeds the storage quota.")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.imported',$2)`, u.ID, remote+"->"+u.Username+"/"+repo.Name); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		_ = os.RemoveAll(a.git.Path(repo.ID))
		serverError(w, err)
		return
	}
	if err = a.decorate(r.Context(), &repo, u); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 201, repo)
}
