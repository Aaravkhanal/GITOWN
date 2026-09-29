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
	if len(q) > 100 || (topic != "" && !topicPattern.MatchString(topic)) || offset < 0 || offset > 1000 || (sort != "recent" && sort != "name" && sort != "sparks") {
		fail(w, 422, "validation_failed", "Use a query up to 100 characters, a valid topic, offset from zero to 1000, and recent, name, or sparks sort.")
		return
	}
	order := "r.created_at DESC,r.id DESC"
	if sort == "name" {
		order = "r.name ASC,r.id ASC"
	} else if sort == "sparks" {
		order = "(SELECT count(*) FROM repository_sparks rs WHERE rs.repository_id=r.id) DESC,r.created_at DESC,r.id DESC"
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
