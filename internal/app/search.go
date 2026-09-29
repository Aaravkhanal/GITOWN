package app

import (
	"net/http"
	"strconv"
	"strings"
)

type RepositorySearch struct {
	Items   []Repository `json:"items"`
	HasMore bool         `json:"has_more"`
}

type BuilderResult struct {
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	Bio          string `json:"bio"`
	Location     string `json:"location"`
	Followers    int    `json:"followers"`
	Repositories int    `json:"repositories"`
}

type BuilderSearch struct {
	Items   []BuilderResult `json:"items"`
	HasMore bool            `json:"has_more"`
}

func (a *App) searchBuilders(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		var err error
		offset, err = strconv.Atoi(raw)
		if err != nil {
			fail(w, 422, "validation_failed", "Offset must be between zero and 1000.")
			return
		}
	}
	if len(q) > 100 || offset < 0 || offset > 1000 {
		fail(w, 422, "validation_failed", "Use a query up to 100 characters and offset from zero to 1000.")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT u.username,u.display_name,u.bio,u.location,
		(SELECT count(*)::int FROM user_follows f WHERE f.followed_id=u.id),
		(SELECT count(*)::int FROM repositories r WHERE r.owner_id=u.id AND r.visibility='public' AND r.deleted_at IS NULL)
		FROM users u WHERE $1='' OR strpos(lower(u.username||' '||u.display_name||' '||u.bio),lower($1))>0
		ORDER BY u.username ASC LIMIT 26 OFFSET $2`, q, offset)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	result := BuilderSearch{Items: []BuilderResult{}}
	for rows.Next() {
		var builder BuilderResult
		if err = rows.Scan(&builder.Username, &builder.DisplayName, &builder.Bio, &builder.Location, &builder.Followers, &builder.Repositories); err != nil {
			serverError(w, err)
			return
		}
		if len(result.Items) == 25 {
			result.HasMore = offset < 1000
			break
		}
		result.Items = append(result.Items, builder)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, result)
}

func (a *App) searchRepositories(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	topic := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("topic")))
	sort := r.URL.Query().Get("sort")
	if sort == "" {
		sort = "recent"
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		var err error
		offset, err = strconv.Atoi(raw)
		if err != nil {
			fail(w, 422, "validation_failed", "Offset must be between zero and 1000.")
			return
		}
	}
	if len(q) > 100 || (topic != "" && !topicPattern.MatchString(topic)) || offset < 0 || offset > 1000 || (sort != "recent" && sort != "name" && sort != "sparks" && sort != "trending") {
		fail(w, 422, "validation_failed", "Use a query up to 100 characters, a valid topic, offset from zero to 1000, and recent, name, sparks, or trending sort.")
		return
	}
	order := "r.created_at DESC,r.id DESC"
	if sort == "name" {
		order = "r.name ASC,r.id ASC"
	} else if sort == "sparks" {
		order = "(SELECT count(*) FROM repository_sparks rs WHERE rs.repository_id=r.id) DESC,r.created_at DESC,r.id DESC"
	} else if sort == "trending" {
		order = "(SELECT count(*) FROM repository_sparks rs WHERE rs.repository_id=r.id AND rs.created_at>=now()-interval '30 days') DESC,r.created_at DESC,r.id DESC"
	}
	rows, err := a.db.Query(r.Context(), `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id
		WHERE r.visibility='public' AND r.deleted_at IS NULL
		AND ($1='' OR strpos(lower(u.username||'/'||r.name||' '||r.description),lower($1))>0)
		AND ($2='' OR EXISTS (SELECT 1 FROM repository_topics rt WHERE rt.repository_id=r.id AND rt.topic=$2))
		ORDER BY `+order+` LIMIT 26 OFFSET $3`, q, topic, offset)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	result := RepositorySearch{Items: []Repository{}}
	u := a.user(r)
	for rows.Next() {
		repo, scanErr := scanRepo(rows)
		if scanErr != nil {
			serverError(w, scanErr)
			return
		}
		if len(result.Items) == 25 {
			result.HasMore = offset < 1000
			break
		}
		if err = a.decorate(r.Context(), &repo, u); err != nil {
			serverError(w, err)
			return
		}
		result.Items = append(result.Items, repo)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, result)
}
