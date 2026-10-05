package app

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Aaravkhanal/GITOWN/internal/gitstore"
)

const wikiDirectory = ".gitown/wiki"

var wikiSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)
var wikiAttachmentName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// GitStore caps individual objects at 512 KiB; wiki assets use that same bound.
const maxWikiAttachment = 512 << 10

var wikiAttachmentTypes = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif",
	"image/webp": ".webp", "application/pdf": ".pdf",
}

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

func wikiAttachmentPath(slug, name string) (string, bool) {
	if _, ok := wikiPath(slug); !ok || !wikiAttachmentName.MatchString(name) || path.Base(name) != name {
		return "", false
	}
	return wikiDirectory + "/attachments/" + slug + "/" + name, true
}

func (a *App) wikiAttachments(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, r.Method == http.MethodPost)
	if repo == nil {
		return
	}
	slug := r.PathValue("slug")
	if _, ok := wikiPath(slug); !ok {
		fail(w, 404, "not_found", "Wiki page not found.")
		return
	}
	if r.Method == http.MethodPost {
		var in struct {
			Name          string `json:"name"`
			ContentBase64 string `json:"content_base64"`
			ExpectedHead  string `json:"expected_head"`
		}
		if !decode(w, r, &in) {
			return
		}
		assetPath, ok := wikiAttachmentPath(slug, in.Name)
		if !ok {
			fail(w, 422, "validation_failed", "Use a simple attachment filename without directories.")
			return
		}
		data, err := base64.StdEncoding.DecodeString(in.ContentBase64)
		if err != nil || len(data) == 0 || len(data) > maxWikiAttachment {
			fail(w, 422, "validation_failed", "Attachments must be valid base64 and no larger than 512 KiB.")
			return
		}
		contentType := http.DetectContentType(data)
		if ext, allowed := wikiAttachmentTypes[contentType]; !allowed || !strings.EqualFold(path.Ext(in.Name), ext) {
			fail(w, 422, "unsupported_attachment", "Only PNG, JPEG, GIF, WebP, and PDF attachments with matching filename extensions are supported.")
			return
		}
		if len(in.ExpectedHead) != 40 {
			fail(w, 422, "validation_failed", "Provide the current branch head SHA.")
			return
		}
		if !a.allowBrowserBranchEdit(w, r, repo.ID, repo.DefaultBranch, repo.Role) {
			return
		}
		u := a.user(r)
		sha, err := a.git.CommitFile(r.Context(), repo.ID, repo.DefaultBranch, assetPath, data, "Add wiki attachment "+in.Name, u.DisplayName, u.Username+"@users.gitown.local", in.ExpectedHead)
		if errors.Is(err, gitstore.ErrConflict) {
			fail(w, 409, "branch_changed", "The branch changed. Refresh the wiki page before uploading.")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'wiki.attachment_added',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+slug+":"+in.Name+":"+sha)
		a.finishReceive(r.Context(), repo, u.ID, []refUpdate{{Old: in.ExpectedHead, New: sha, Ref: "refs/heads/" + repo.DefaultBranch}}, "api")
		respond(w, 201, map[string]string{"name": in.Name, "sha": sha, "content_type": contentType})
		return
	}
	assetPath, ok := wikiAttachmentPath(slug, r.PathValue("name"))
	if !ok {
		fail(w, 404, "not_found", "Wiki attachment not found.")
		return
	}
	data, err := a.git.Blob(r.Context(), repo.ID, repo.DefaultBranch, assetPath)
	if errors.Is(err, gitstore.ErrNotFound) {
		fail(w, 404, "not_found", "Wiki attachment not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if len(data) > maxWikiAttachment {
		fail(w, 404, "not_found", "Wiki attachment not found.")
		return
	}
	contentType := http.DetectContentType(data)
	if _, allowed := wikiAttachmentTypes[contentType]; !allowed {
		fail(w, 404, "not_found", "Wiki attachment not found.")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	w.Header().Set("Content-Disposition", `inline; filename="`+r.PathValue("name")+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func (a *App) wikiAttachmentList(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	slug := r.PathValue("slug")
	if _, ok := wikiPath(slug); !ok {
		fail(w, 404, "not_found", "Wiki page not found.")
		return
	}
	tree, err := a.git.Browse(r.Context(), repo.ID, repo.DefaultBranch, wikiDirectory+"/attachments/"+slug)
	if errors.Is(err, gitstore.ErrNotFound) {
		respond(w, 200, map[string]any{"items": []any{}})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	items := []map[string]string{}
	for _, entry := range tree.Entries {
		if len(items) >= 100 {
			break
		}
		assetPath, ok := wikiAttachmentPath(slug, entry.Name)
		if entry.Type != "blob" || !ok {
			continue
		}
		data, blobErr := a.git.Blob(r.Context(), repo.ID, repo.DefaultBranch, assetPath)
		if blobErr != nil {
			continue
		}
		if len(data) > maxWikiAttachment {
			continue
		}
		contentType := http.DetectContentType(data)
		if _, ok = wikiAttachmentTypes[contentType]; ok {
			items = append(items, map[string]string{"name": entry.Name, "content_type": contentType})
		}
	}
	respond(w, 200, map[string]any{"items": items})
}
