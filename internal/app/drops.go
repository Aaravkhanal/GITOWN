package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

var dropTagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,60}$`)
var assetNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,119}$`)

func validDropTag(tag string) bool {
	return dropTagPattern.MatchString(tag) && !strings.Contains(tag, "..")
}

func (a *App) repositoryTags(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	out, code, err := a.git.Command(r.Context(), 20*1e9, repo.ID, "for-each-ref", "--sort=-creatordate", "--format=%(refname:short)%00%(objectname)%00%(subject)", "refs/tags")
	if err != nil || code != 0 {
		if err != nil {
			serverError(w, err)
			return
		}
		respond(w, 200, map[string]any{"items": []any{}})
		return
	}
	items := []map[string]any{}
	text := strings.TrimSpace(string(out))
	if text == "" {
		respond(w, 200, map[string]any{"items": items})
		return
	}
	// Built once and reused for every tag in this listing: signed is a real,
	// cryptographic result from `git tag -v` against the repository's
	// registered signing keys, not a search for a signature-looking block of
	// text (which a forged or copy-pasted block would satisfy).
	signers, signersErr := a.allowedSignersFile(r.Context())
	if signersErr != nil {
		serverError(w, signersErr)
		return
	}
	defer os.Remove(signers)
	for _, line := range strings.Split(text, "\n") {
		if len(items) == 40 {
			break
		}
		parts := strings.Split(line, "\x00")
		if len(parts) < 2 || parts[0] == "" || len(parts[1]) != 40 {
			continue
		}
		subject := ""
		if len(parts) > 2 {
			subject = parts[2]
		}
		signed, verifyErr := verifyTagAgainstSigners(r.Context(), a.git, repo.ID, parts[0], signers)
		if verifyErr != nil {
			serverError(w, verifyErr)
			return
		}
		items = append(items, map[string]any{"name": parts[0], "sha": parts[1], "subject": subject, "signed": signed})
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) drops(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	query := `SELECT d.tag,d.title,d.draft,d.prerelease,d.created_at,u.username FROM drops d JOIN users u ON u.id=d.author_id WHERE d.repository_id=$1`
	if !repo.CanWrite {
		query += ` AND d.draft=false`
	}
	query += ` ORDER BY d.created_at DESC LIMIT 50`
	rows, err := a.db.Query(r.Context(), query, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	// The listing is already ordered newest-first, so the first row that is
	// neither a draft nor a prerelease is, by construction, the most recent
	// full release — no separate query or semver comparison needed.
	latest := ""
	for rows.Next() {
		var tag, title, author string
		var draft, prerelease bool
		var created any
		if err = rows.Scan(&tag, &title, &draft, &prerelease, &created, &author); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"tag": tag, "title": title, "draft": draft, "prerelease": prerelease, "created_at": created, "author": author})
		if latest == "" && !draft && !prerelease {
			latest = tag
		}
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "latest": latest})
}

func (a *App) createDrop(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, true)
	if repo == nil {
		return
	}
	u := a.user(r)
	var in struct {
		Tag        string `json:"tag"`
		Title      string `json:"title"`
		Body       string `json:"body"`
		Provenance string `json:"provenance"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		CreateTag  bool   `json:"create_tag"`
		Branch     string `json:"branch"`
		Signature  string `json:"signature"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Tag = strings.TrimSpace(in.Tag)
	in.Title = strings.TrimSpace(in.Title)
	in.Body = strings.TrimSpace(in.Body)
	in.Provenance = strings.TrimSpace(in.Provenance)
	in.Signature = strings.TrimSpace(in.Signature)
	if !validDropTag(in.Tag) || in.Title == "" || len(in.Title) > 200 || len(in.Body) > 20000 || len(in.Provenance) > 4000 || len(in.Signature) > 8000 {
		fail(w, 422, "validation_failed", "Use a tag name, a title up to 200 characters, and notes within the size limits.")
		return
	}
	sha, err := a.git.ResolveTag(r.Context(), repo.ID, in.Tag)
	if err != nil && in.CreateTag {
		branch := strings.TrimSpace(in.Branch)
		if branch == "" {
			branch = repo.DefaultBranch
		}
		if branch == "" {
			branch = "main"
		}
		if strings.Contains(branch, "..") || strings.ContainsAny(branch, " \r\n\\") || strings.HasPrefix(branch, "-") {
			fail(w, 422, "validation_failed", "Choose a branch to tag.")
			return
		}
		out, revErr := a.git.Run(r.Context(), repo.ID, nil, "rev-parse", "--verify", "--end-of-options", "refs/heads/"+branch)
		if revErr != nil {
			fail(w, 422, "validation_failed", "The branch to tag does not exist.")
			return
		}
		sha = strings.TrimSpace(string(out))
		if err = a.git.CreateAnnotatedTag(r.Context(), repo.ID, in.Tag, sha, u.DisplayName, u.Username+"@users.gitown.local", in.Title); err != nil {
			fail(w, 422, "validation_failed", "The Git tag could not be created.")
			return
		}
		_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.tag_created',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+in.Tag)
	} else if err != nil {
		fail(w, 422, "validation_failed", "Create the Git tag before publishing a drop, or set create_tag.")
		return
	}
	verified := false
	if in.Signature != "" {
		verified, err = a.verifyProvenance(r.Context(), u.ID, u.Username, in.Provenance, in.Signature)
		if err != nil {
			serverError(w, err)
			return
		}
		if !verified {
			fail(w, 422, "signature_rejected", "The signature does not verify against an SSH signing key registered by this account.")
			return
		}
	}
	if len(in.Body) == 0 {
		lines := a.generatedChangelog(r, repo.ID, in.Tag, sha)
		if len(lines) > 0 {
			in.Body = strings.Join(lines, "\n")
			if len(in.Body) > 20000 {
				in.Body = in.Body[:20000]
			}
		}
	}
	id := auth.ID()
	var created any
	err = a.db.QueryRow(r.Context(), `INSERT INTO drops(id,repository_id,tag,title,body,provenance,draft,prerelease,author_id,signature,provenance_verified) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING created_at`, id, repo.ID, in.Tag, in.Title, in.Body, in.Provenance, in.Draft, in.Prerelease, u.ID, in.Signature, verified).Scan(&created)
	if conflict(err) {
		fail(w, 409, "drop_exists", "A drop for that tag already exists.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'drop.created',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+in.Tag)
	if !in.Draft {
		if err = a.fireWebhook(r.Context(), a.db, repo.ID, repo.DistrictID, "drop.published", webhookDropPayload(repo, u.Username, in.Tag, in.Title)); err != nil {
			serverError(w, err)
			return
		}
	}
	respond(w, 201, map[string]any{"tag": in.Tag, "title": in.Title, "body": in.Body, "provenance": in.Provenance, "draft": in.Draft, "prerelease": in.Prerelease, "sha": sha, "created_at": created, "provenance_verified": verified})
}

// webhookDropPayload is the shared envelope for the drop.published webhook
// event.
func webhookDropPayload(repo *Repository, actor, tag, title string) map[string]any {
	return map[string]any{
		"repository": map[string]string{"owner": repo.Owner, "name": repo.Name},
		"actor":      actor,
		"drop": map[string]string{
			"tag":   tag,
			"title": title,
			"url":   fmt.Sprintf("/repos/%s/%s/drops/%s", repo.Owner, repo.Name, tag),
		},
	}
}

func (a *App) generatedChangelog(r *http.Request, repoID, tag, sha string) []string {
	if len(sha) != 40 {
		return nil
	}
	out, code, err := a.git.Command(r.Context(), 20*1e9, repoID, "for-each-ref", "--sort=-creatordate", "--format=%(refname:short)%00%(objectname)", "refs/tags")
	spec := sha
	if err == nil && code == 0 {
		seen := false
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			name, object, ok := strings.Cut(line, "\x00")
			if !ok {
				continue
			}
			if name == tag {
				seen = true
				continue
			}
			if seen && len(strings.TrimSpace(object)) == 40 {
				spec = strings.TrimSpace(object) + ".." + sha
				break
			}
		}
	}
	lines, err := a.git.Subjects(r.Context(), repoID, spec)
	if err != nil {
		return nil
	}
	return lines
}

func (a *App) loadDrop(w http.ResponseWriter, r *http.Request, write bool) (repo *Repository, id, tag, title, body, provenance string, draft, prerelease bool, ok bool) {
	repo = a.access(w, r, write)
	if repo == nil {
		return nil, "", "", "", "", "", false, false, false
	}
	tag = r.PathValue("tag")
	err := a.db.QueryRow(r.Context(), `SELECT id::text,title,body,provenance,draft,prerelease FROM drops WHERE repository_id=$1 AND tag=$2`, repo.ID, tag).Scan(&id, &title, &body, &provenance, &draft, &prerelease)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && draft && !repo.CanWrite) {
		fail(w, 404, "not_found", "Drop not found.")
		return nil, "", "", "", "", "", false, false, false
	}
	if err != nil {
		serverError(w, err)
		return nil, "", "", "", "", "", false, false, false
	}
	return repo, id, tag, title, body, provenance, draft, prerelease, true
}

func (a *App) drop(w http.ResponseWriter, r *http.Request) {
	repo, _, tag, title, body, provenance, draft, prerelease, ok := a.loadDrop(w, r, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT a.name,a.size_bytes,a.sha256,a.download_count,a.content_type FROM drop_assets a JOIN drops d ON d.id=a.drop_id WHERE d.repository_id=$1 AND d.tag=$2 ORDER BY a.name`, repo.ID, tag)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	assets := []map[string]any{}
	for rows.Next() {
		var name, sum, contentType string
		var size int64
		var downloads int
		if err = rows.Scan(&name, &size, &sum, &downloads, &contentType); err != nil {
			serverError(w, err)
			return
		}
		assets = append(assets, map[string]any{"name": name, "size_bytes": size, "sha256": sum, "download_count": downloads, "content_type": contentType})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	sha, _ := a.git.ResolveTag(r.Context(), repo.ID, tag)
	var verified bool
	_ = a.db.QueryRow(r.Context(), `SELECT provenance_verified FROM drops WHERE repository_id=$1 AND tag=$2`, repo.ID, tag).Scan(&verified)
	// Two distinct, separately-meaningful claims, reported as two fields so
	// neither is mistaken for covering the other: tag_signed is a real
	// cryptographic verification of the Git tag object itself (git tag -v
	// against this account's registered signing keys); provenance_verified
	// is a verification of a client-supplied note about the release
	// artifacts, signed separately by the publisher. A Drop can have either,
	// both, or neither — they do not have to agree.
	tagSigned, tagErr := a.verifiedTagSignature(r.Context(), repo.ID, tag)
	if tagErr != nil {
		serverError(w, tagErr)
		return
	}
	respond(w, 200, map[string]any{"tag": tag, "title": title, "body": body, "provenance": provenance, "provenance_verified": verified, "tag_signed": tagSigned, "draft": draft, "prerelease": prerelease, "sha": sha, "changelog": a.generatedChangelog(r, repo.ID, tag, sha), "assets": assets})
}

func (a *App) updateDrop(w http.ResponseWriter, r *http.Request) {
	repo, id, tag, title, body, provenance, draft, prerelease, ok := a.loadDrop(w, r, true)
	if !ok {
		return
	}
	var in struct {
		Title      *string `json:"title"`
		Body       *string `json:"body"`
		Provenance *string `json:"provenance"`
		Draft      *bool   `json:"draft"`
		Prerelease *bool   `json:"prerelease"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Title != nil {
		title = strings.TrimSpace(*in.Title)
	}
	if in.Body != nil {
		body = strings.TrimSpace(*in.Body)
	}
	if in.Provenance != nil {
		provenance = strings.TrimSpace(*in.Provenance)
	}
	previousDraft := draft
	if in.Draft != nil {
		draft = *in.Draft
	}
	if in.Prerelease != nil {
		prerelease = *in.Prerelease
	}
	if title == "" || len(title) > 200 || len(body) > 20000 || len(provenance) > 4000 {
		fail(w, 422, "validation_failed", "Use a title up to 200 characters and keep notes within the size limits.")
		return
	}
	var verified bool
	if err := a.db.QueryRow(r.Context(), `UPDATE drops SET title=$1,body=$2,provenance=$3,draft=$4,prerelease=$5,provenance_verified=CASE WHEN provenance IS DISTINCT FROM $3 THEN false ELSE provenance_verified END WHERE id=$6 RETURNING provenance_verified`, title, body, provenance, draft, prerelease, id).Scan(&verified); err != nil {
		serverError(w, err)
		return
	}
	u := a.user(r)
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'drop.updated',$2)`, u.ID, repo.Owner+"/"+repo.Name+":"+tag)
	if previousDraft && !draft {
		if err := a.fireWebhook(r.Context(), a.db, repo.ID, repo.DistrictID, "drop.published", webhookDropPayload(repo, u.Username, tag, title)); err != nil {
			serverError(w, err)
			return
		}
	}
	respond(w, 200, map[string]any{"tag": tag, "title": title, "draft": draft, "prerelease": prerelease, "provenance_verified": verified})
}

func (a *App) deleteDrop(w http.ResponseWriter, r *http.Request) {
	repo, id, tag, _, _, _, _, _, ok := a.loadDrop(w, r, true)
	if !ok {
		return
	}
	if _, err := a.db.Exec(r.Context(), `DELETE FROM drops WHERE id=$1`, id); err != nil {
		serverError(w, err)
		return
	}
	_ = os.RemoveAll(filepath.Join(a.extraRoot(), "drops", id))
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'drop.deleted',$2)`, a.user(r).ID, repo.Owner+"/"+repo.Name+":"+tag)
	respond(w, 200, map[string]bool{"removed": true})
}

func (a *App) uploadDropAsset(w http.ResponseWriter, r *http.Request) {
	repo, dropID, tag, _, _, _, _, _, ok := a.loadDrop(w, r, true)
	if !ok {
		return
	}
	var name string
	var content []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/octet-stream") {
		name = strings.TrimSpace(r.URL.Query().Get("name"))
		var err error
		content, err = io.ReadAll(r.Body)
		if err != nil {
			fail(w, 400, "invalid_input", "Could not read the asset.")
			return
		}
	} else {
		var in struct {
			Name          string `json:"name"`
			ContentBase64 string `json:"content_base64"`
		}
		if !decode(w, r, &in) {
			return
		}
		name = strings.TrimSpace(in.Name)
		var err error
		content, err = base64.StdEncoding.DecodeString(in.ContentBase64)
		if err != nil {
			fail(w, 422, "validation_failed", "Asset content must be standard base64.")
			return
		}
	}
	if !assetNamePattern.MatchString(name) || strings.Contains(name, "..") || len(content) == 0 || len(content) > 8<<20 {
		fail(w, 422, "validation_failed", "Use an asset name of letters, numbers, dots, underscores, or hyphens and a file up to 8 MB.")
		return
	}
	var count int
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM drop_assets WHERE drop_id=$1`, dropID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 20 {
		fail(w, 422, "asset_limit", "A drop can have up to 20 assets.")
		return
	}
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	// Sniffed from the actual bytes rather than trusted from a client header,
	// so a download can't be tricked into announcing a type the file isn't.
	contentType := http.DetectContentType(content)
	id := auth.ID()
	if err := writePrivateFile(filepath.Join(a.extraRoot(), "drops", dropID, id), content); err != nil {
		serverError(w, err)
		return
	}
	_, err := a.db.Exec(r.Context(), `INSERT INTO drop_assets(id,drop_id,name,size_bytes,sha256,content_type) VALUES($1,$2,$3,$4,$5,$6)`, id, dropID, name, len(content), digest, contentType)
	if err != nil {
		_ = os.Remove(filepath.Join(a.extraRoot(), "drops", dropID, id))
		if conflict(err) {
			fail(w, 409, "asset_exists", "That asset name is already attached.")
			return
		}
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'drop.asset_added',$2)`, a.user(r).ID, repo.Owner+"/"+repo.Name+":"+tag+":"+name)
	respond(w, 201, map[string]any{"name": name, "size_bytes": len(content), "sha256": digest, "content_type": contentType})
}

func (a *App) downloadDropAsset(w http.ResponseWriter, r *http.Request) {
	_, dropID, _, _, _, _, _, _, ok := a.loadDrop(w, r, false)
	if !ok {
		return
	}
	name := r.PathValue("name")
	var id, digest, contentType string
	var size int64
	err := a.db.QueryRow(r.Context(), `SELECT id::text,sha256,size_bytes,content_type FROM drop_assets WHERE drop_id=$1 AND name=$2`, dropID, name).Scan(&id, &digest, &size, &contentType)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Asset not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	path := filepath.Join(a.extraRoot(), "drops", dropID, id)
	content, err := os.ReadFile(path)
	if err != nil {
		serverError(w, err)
		return
	}
	a.countAssetDownload(r, id)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	// Always served as an attachment, regardless of the sniffed type, so an
	// uploaded HTML or SVG file is downloaded rather than rendered inline by
	// the browser in the app's own origin.
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("ETag", `"`+digest+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// countAssetDownload increments an asset's counter at most once per identity
// (the signed-in user, or the caller's IP when anonymous) per hour, so
// refreshing a download link repeatedly no longer inflates the count
// unboundedly. It counts downloads of private and internal repository
// assets too — previously only public, non-draft assets were counted at all.
func (a *App) countAssetDownload(r *http.Request, assetID string) {
	identity := "ip:" + clientIP(r)
	if u := a.user(r); u != nil {
		identity = "user:" + u.ID
	}
	result, err := a.db.Exec(r.Context(), `INSERT INTO drop_asset_downloads(asset_id,identity,bucket) VALUES($1,$2,date_trunc('hour',now())) ON CONFLICT DO NOTHING`, assetID, identity)
	if err != nil || result.RowsAffected() == 0 {
		return
	}
	_, _ = a.db.Exec(r.Context(), `UPDATE drop_assets SET download_count=download_count+1 WHERE id=$1`, assetID)
}

func (a *App) deleteDropAsset(w http.ResponseWriter, r *http.Request) {
	_, dropID, _, _, _, _, _, _, ok := a.loadDrop(w, r, true)
	if !ok {
		return
	}
	var id string
	err := a.db.QueryRow(r.Context(), `DELETE FROM drop_assets WHERE drop_id=$1 AND name=$2 RETURNING id::text`, dropID, r.PathValue("name")).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Asset not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_ = os.Remove(filepath.Join(a.extraRoot(), "drops", dropID, id))
	respond(w, 200, map[string]bool{"removed": true})
}

func clientIP(r *http.Request) string {
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}
	return r.RemoteAddr
}

func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func safeObjectID(id string) bool {
	return gitstoreID.MatchString(id)
}

var gitstoreID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
