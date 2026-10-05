package app

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Aaravkhanal/GITOWN/internal/gitstore"
)

const wikiDirectory = ".gitown/wiki"

var wikiSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)

func wikiPath(slug string) (string, bool) {
	if !wikiSlug.MatchString(slug) {
		return "", false
	}
	return wikiDirectory + "/" + slug + ".md", true
}

func (a *App) wikiPages(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	head, err := a.git.Resolve(r.Context(), repo.ID, repo.DefaultBranch)
	if err != nil {
		respond(w, 200, map[string]any{"pages": []string{}, "head_sha": ""})
		return
	}
	tree, err := a.git.Browse(r.Context(), repo.ID, repo.DefaultBranch, wikiDirectory)
	if errors.Is(err, gitstore.ErrNotFound) {
		respond(w, 200, map[string]any{"pages": []string{}, "head_sha": head})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	pages := []string{}
	for _, entry := range tree.Entries {
		if entry.Type != "blob" || !strings.HasSuffix(entry.Name, ".md") {
			continue
		}
		slug := strings.TrimSuffix(entry.Name, ".md")
		if wikiSlug.MatchString(slug) {
			pages = append(pages, slug)
		}
	}
	respond(w, 200, map[string]any{"pages": pages, "head_sha": head})
}

func (a *App) wikiPage(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	path, ok := wikiPath(r.PathValue("slug"))
	if !ok {
		fail(w, 404, "not_found", "Wiki page not found.")
		return
	}
	content, err := a.git.Blob(r.Context(), repo.ID, repo.DefaultBranch, path)
	if errors.Is(err, gitstore.ErrNotFound) {
		fail(w, 404, "not_found", "Wiki page not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if !utf8.Valid(content) {
		fail(w, 422, "invalid_content", "Wiki pages must be UTF-8 Markdown.")
		return
	}
	head, err := a.git.Resolve(r.Context(), repo.ID, repo.DefaultBranch)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"slug": r.PathValue("slug"), "content": string(content), "head_sha": head})
}

func (a *App) saveWikiPage(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, true)
	if repo == nil {
		return
	}
	slug := r.PathValue("slug")
	path, ok := wikiPath(slug)
	if !ok {
		fail(w, 422, "validation_failed", "Use a lowercase page slug with letters, numbers, and hyphens (up to 80 characters).")
		return
	}
	var in struct {
		Content      string `json:"content"`
		ExpectedHead string `json:"expected_head"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Content) > 128<<10 || !utf8.ValidString(in.Content) || len(in.ExpectedHead) != 40 {
		fail(w, 422, "validation_failed", "Provide UTF-8 Markdown up to 128 KiB and the current branch head SHA.")
		return
	}
	if !a.allowBrowserBranchEdit(w, r, repo.ID, repo.DefaultBranch, repo.Role) {
		return
	}
	u := a.user(r)
	sha, err := a.git.CommitFile(r.Context(), repo.ID, repo.DefaultBranch, path, []byte(in.Content), "Update wiki page "+slug, u.DisplayName, u.Username+"@users.gitown.local", in.ExpectedHead)
	if errors.Is(err, gitstore.ErrConflict) {
		fail(w, 409, "branch_changed", "The branch changed. Refresh the wiki page before saving.")
		return
	}
	if errors.Is(err, gitstore.ErrNotFound) {
		fail(w, 422, "invalid_path_or_branch", "The repository's default branch is unavailable.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'wiki.saved',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+slug+":"+sha)
	a.finishReceive(r.Context(), repo, u.ID, []refUpdate{{Old: in.ExpectedHead, New: sha, Ref: "refs/heads/" + repo.DefaultBranch}}, "api")
	respond(w, 201, map[string]string{"slug": slug, "sha": sha})
}
