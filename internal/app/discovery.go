package app

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
)

type CodeMatch struct {
	Owner      string `json:"owner"`
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Snippet    string `json:"snippet"`
}

func (a *App) searchCode(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 || len(q) > 80 || strings.HasPrefix(q, "-") || strings.ContainsAny(q, "\x00\r\n") {
		fail(w, 422, "validation_failed", "Use a fixed code query between 2 and 80 characters.")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT r.id,u.username,r.name,r.default_branch FROM repositories r JOIN users u ON u.id=r.owner_id WHERE r.visibility='public' AND r.deleted_at IS NULL ORDER BY r.pushed_at DESC,r.id DESC LIMIT 12`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type candidate struct{ id, owner, name, branch string }
	var repos []candidate
	for rows.Next() {
		var item candidate
		if err = rows.Scan(&item.id, &item.owner, &item.name, &item.branch); err != nil {
			serverError(w, err)
			return
		}
		repos = append(repos, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	matches := []CodeMatch{}
	for _, repo := range repos {
		if repo.branch == "" {
			continue
		}
		text, grepErr := a.git.Grep(r.Context(), repo.id, repo.branch, q)
		if grepErr != nil || text == "" {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
			path, number, snippet, ok := parseGrepLine(line)
			if !ok {
				continue
			}
			matches = append(matches, CodeMatch{Owner: repo.owner, Repository: repo.name, Path: path, Line: number, Snippet: snippet})
			if len(matches) == 40 {
				respond(w, 200, map[string]any{"items": matches, "limited": true})
				return
			}
		}
	}
	respond(w, 200, map[string]any{"items": matches, "limited": false})
}

func parseGrepLine(line string) (string, int, string, bool) {
	parts := strings.SplitN(line, ":", 4)
	if len(parts) == 4 {
		if number, err := strconv.Atoi(parts[2]); err == nil && number > 0 {
			snippet := parts[3]
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			return parts[1], number, snippet, true
		}
	}
	if len(parts) < 3 {
		return "", 0, "", false
	}
	number, err := strconv.Atoi(parts[1])
	if err != nil || number < 1 {
		return "", 0, "", false
	}
	snippet := parts[2]
	if len(snippet) > 200 {
		snippet = snippet[:200]
	}
	return parts[0], number, snippet, true
}

func (a *App) searchTasks(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	names := []string{"help wanted", "good first task", "good first issue"}
	if kind == "help" {
		names = []string{"help wanted"}
	} else if kind == "first" {
		names = []string{"good first task", "good first issue"}
	} else if kind != "" {
		fail(w, 422, "validation_failed", "Kind must be help, first, or empty.")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT u.username,r.name,i.number,i.title,l.name FROM issues i
		JOIN repositories r ON r.id=i.repository_id JOIN users u ON u.id=r.owner_id
		JOIN issue_labels il ON il.issue_id=i.id JOIN labels l ON l.id=il.label_id
		WHERE r.visibility='public' AND r.deleted_at IS NULL AND i.state='open' AND lower(l.name)=ANY($1)
		ORDER BY i.created_at DESC,i.id DESC LIMIT 25`, names)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var owner, name, title, label string
		var number int
		if err = rows.Scan(&owner, &name, &number, &title, &label); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"owner": owner, "repository": name, "number": number, "title": title, "label": label})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) recommendations(w http.ResponseWriter, r *http.Request) {
	u := a.user(r)
	if u == nil {
		a.writeTrending(w, r)
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id
		WHERE r.visibility='public' AND r.deleted_at IS NULL AND r.owner_id<>$1
		AND NOT EXISTS (SELECT 1 FROM repository_sparks rs WHERE rs.repository_id=r.id AND rs.user_id=$1)
		AND EXISTS (
			SELECT 1 FROM repository_topics rt WHERE rt.repository_id=r.id AND rt.topic IN (
				SELECT t2.topic FROM repository_topics t2 JOIN repositories mine ON mine.id=t2.repository_id
				WHERE mine.deleted_at IS NULL AND (mine.owner_id=$1 OR EXISTS (SELECT 1 FROM repository_sparks s WHERE s.repository_id=mine.id AND s.user_id=$1))
			)
		)
		ORDER BY (SELECT count(*) FROM repository_sparks rs WHERE rs.repository_id=r.id) DESC, r.pushed_at DESC LIMIT 12`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	items, err := a.scanRepoList(r, rows)
	if err != nil {
		serverError(w, err)
		return
	}
	if len(items) == 0 {
		a.writeTrending(w, r)
		return
	}
	respond(w, 200, map[string]any{"items": items, "personalized": true})
}

func (a *App) writeTrending(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id
		WHERE r.visibility='public' AND r.deleted_at IS NULL
		ORDER BY (SELECT count(*) FROM repository_sparks rs WHERE rs.repository_id=r.id AND rs.created_at>=now()-interval '30 days') DESC, r.pushed_at DESC LIMIT 12`)
	if err != nil {
		serverError(w, err)
		return
	}
	items, err := a.scanRepoList(r, rows)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "personalized": false})
}

func (a *App) collections(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `SELECT c.id,u.username,c.slug,c.title,c.description,c.created_at,
		(SELECT count(*)::int FROM collection_items ci JOIN repositories r ON r.id=ci.repository_id WHERE ci.collection_id=c.id AND r.visibility='public' AND r.deleted_at IS NULL)
		FROM collections c JOIN users u ON u.id=c.owner_id
		ORDER BY (SELECT count(*) FROM collection_items ci WHERE ci.collection_id=c.id) DESC, c.created_at DESC LIMIT 20`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, owner, slug, title, description string
		var createdAt time.Time
		var count int
		if err = rows.Scan(&id, &owner, &slug, &title, &description, &createdAt, &count); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "owner": owner, "slug": slug, "title": title, "description": description, "created_at": createdAt, "repositories": count})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) createCollection(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		Slug        string `json:"slug"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	if !slug.MatchString(in.Slug) || in.Title == "" || len(in.Title) > 80 || len(in.Description) > 300 {
		fail(w, 422, "validation_failed", "Use a collection slug, a title up to 80 characters, and a description up to 300 characters.")
		return
	}
	var count int
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM collections WHERE owner_id=$1`, u.ID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 30 {
		fail(w, 422, "collection_limit", "An account can have up to 30 collections.")
		return
	}
	id := auth.ID()
	var created any
	err := a.db.QueryRow(r.Context(), `INSERT INTO collections(id,owner_id,slug,title,description) VALUES($1,$2,$3,$4,$5) RETURNING created_at`, id, u.ID, in.Slug, in.Title, in.Description).Scan(&created)
	if conflict(err) {
		fail(w, 409, "collection_exists", "You already have a collection with that slug.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'collection.created',$2)`, u.ID, u.Username+"/"+in.Slug)
	respond(w, 201, map[string]any{"id": id, "owner": u.Username, "slug": in.Slug, "title": in.Title, "description": in.Description, "created_at": created})
}

func (a *App) collection(w http.ResponseWriter, r *http.Request) {
	owner := r.PathValue("owner")
	name := r.PathValue("slug")
	var id, title, description string
	var created time.Time
	err := a.db.QueryRow(r.Context(), `SELECT c.id,c.title,c.description,c.created_at FROM collections c JOIN users u ON u.id=c.owner_id WHERE u.username=$1 AND c.slug=$2`, owner, name).Scan(&id, &title, &description, &created)
	if err != nil {
		fail(w, 404, "not_found", "Collection not found.")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT `+repoColumns+` FROM collection_items ci JOIN repositories r ON r.id=ci.repository_id JOIN users u ON u.id=r.owner_id WHERE ci.collection_id=$1 AND r.visibility='public' AND r.deleted_at IS NULL ORDER BY ci.position, r.name`, id)
	if err != nil {
		serverError(w, err)
		return
	}
	items, err := a.scanRepoList(r, rows)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"owner": owner, "slug": name, "title": title, "description": description, "created_at": created, "items": items})
}

func (a *App) addCollectionItem(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var collectionID string
	err := a.db.QueryRow(r.Context(), `SELECT c.id FROM collections c JOIN users u ON u.id=c.owner_id WHERE u.username=$1 AND c.slug=$2 AND c.owner_id=$3`, r.PathValue("owner"), r.PathValue("slug"), u.ID).Scan(&collectionID)
	if err != nil {
		fail(w, 404, "not_found", "Collection not found.")
		return
	}
	var in struct {
		Repository string `json:"repository"`
	}
	if !decode(w, r, &in) {
		return
	}
	owner, name, ok := strings.Cut(strings.TrimSpace(in.Repository), "/")
	if !ok || !slug.MatchString(owner) || !repoSlug.MatchString(name) {
		fail(w, 422, "validation_failed", "Name a public repository as owner/name.")
		return
	}
	var repoID string
	err = a.db.QueryRow(r.Context(), `SELECT r.id FROM repositories r JOIN users u ON u.id=r.owner_id WHERE u.username=$1 AND r.name=$2 AND r.visibility='public' AND r.deleted_at IS NULL`, owner, name).Scan(&repoID)
	if err != nil {
		fail(w, 404, "not_found", "Public repository not found.")
		return
	}
	var count int
	if err = a.db.QueryRow(r.Context(), `SELECT count(*) FROM collection_items WHERE collection_id=$1`, collectionID).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= 20 {
		fail(w, 422, "collection_limit", "A collection can hold up to 20 repositories.")
		return
	}
	if _, err = a.db.Exec(r.Context(), `INSERT INTO collection_items(collection_id,repository_id,position) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, collectionID, repoID, count); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"repository": owner + "/" + name})
}

func (a *App) removeCollectionItem(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	tag, err := a.db.Exec(r.Context(), `DELETE FROM collection_items ci USING collections c, users owner, repositories r, users ru
		WHERE c.id=ci.collection_id AND owner.id=c.owner_id AND r.id=ci.repository_id AND ru.id=r.owner_id
		AND owner.username=$1 AND c.slug=$2 AND c.owner_id=$3 AND ru.username=$4 AND r.name=$5`,
		r.PathValue("owner"), r.PathValue("slug"), u.ID, r.PathValue("repoOwner"), r.PathValue("repoName"))
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Collection item not found.")
		return
	}
	respond(w, 200, map[string]bool{"removed": true})
}
