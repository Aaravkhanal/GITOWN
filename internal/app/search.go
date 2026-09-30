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
	Username            string `json:"username"`
	DisplayName         string `json:"display_name"`
	Bio                 string `json:"bio"`
	Location            string `json:"location"`
	Skills              string `json:"skills"`
	Availability        string `json:"availability"`
	Followers           int    `json:"followers"`
	Repositories        int    `json:"repositories"`
	OpenToCollaborators bool   `json:"open_to_collaborators"`
}

type BuilderSearch struct {
	Items   []BuilderResult `json:"items"`
	HasMore bool            `json:"has_more"`
}

type WorkResult struct {
	Kind       string `json:"kind"`
	Owner      string `json:"owner"`
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	Title      string `json:"title"`
	Preview    string `json:"preview"`
	State      string `json:"state"`
}

type WorkSearch struct {
	Items   []WorkResult `json:"items"`
	HasMore bool         `json:"has_more"`
}

func (a *App) searchWork(w http.ResponseWriter, r *http.Request) {
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
	rows, err := a.db.Query(r.Context(), `SELECT kind,owner,repository,number,title,left(body,160),state FROM (
		SELECT 'issue' AS kind,u.username AS owner,r.name AS repository,i.number,i.title,i.body,i.state,i.created_at,i.id::text AS id
		FROM issues i JOIN repositories r ON r.id=i.repository_id JOIN users u ON u.id=r.owner_id
		WHERE r.visibility='public' AND r.deleted_at IS NULL
		UNION ALL
		SELECT 'unite' AS kind,u.username AS owner,r.name AS repository,p.number,p.title,p.body,p.state,p.created_at,p.id::text AS id
		FROM pull_requests p JOIN repositories r ON r.id=p.repository_id JOIN users u ON u.id=r.owner_id
		WHERE r.visibility='public' AND r.deleted_at IS NULL
	) work WHERE $1='' OR strpos(lower(owner||'/'||repository||' '||title||' '||body),lower($1))>0
	ORDER BY created_at DESC,id DESC LIMIT 26 OFFSET $2`, q, offset)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	result := WorkSearch{Items: []WorkResult{}}
	for rows.Next() {
		var item WorkResult
		if err = rows.Scan(&item.Kind, &item.Owner, &item.Repository, &item.Number, &item.Title, &item.Preview, &item.State); err != nil {
			serverError(w, err)
			return
		}
		if len(result.Items) == 25 {
			result.HasMore = offset < 1000
			break
		}
		result.Items = append(result.Items, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, result)
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
	available := r.URL.Query().Get("available")
	if available != "" && available != "1" {
		fail(w, 422, "validation_failed", "Available must be 1 when it is set.")
		return
	}
	skill := strings.TrimSpace(r.URL.Query().Get("skill"))
	location := strings.TrimSpace(r.URL.Query().Get("location"))
	if len(q) > 100 || offset < 0 || offset > 1000 || len(skill) > 60 || len(location) > 100 {
		fail(w, 422, "validation_failed", "Use a query up to 100 characters, skill up to 60, location up to 100, and offset from zero to 1000.")
		return
	}
	// The query matches names, bio, skills, location, and availability so
	// people can find collaborators by what they know and when they can help.
	rows, err := a.db.Query(r.Context(), `SELECT u.username,u.display_name,u.bio,u.location,u.skills,u.availability,
		(SELECT count(*)::int FROM user_follows f WHERE f.followed_id=u.id),
		(SELECT count(*)::int FROM repositories r WHERE r.owner_id=u.id AND r.visibility='public' AND r.deleted_at IS NULL),
		u.open_to_collaborators
		FROM users u WHERE ($1='' OR strpos(lower(u.username||' '||u.display_name||' '||u.bio||' '||u.skills||' '||u.location||' '||u.availability),lower($1))>0)
		AND ($3=false OR u.open_to_collaborators)
		AND ($4='' OR EXISTS (SELECT 1 FROM regexp_split_to_table(lower(u.skills), '\s*[,;\n]\s*') tag WHERE btrim(tag)=lower($4)))
		AND ($5='' OR strpos(lower(u.location),lower($5))>0)
		ORDER BY u.username ASC LIMIT 26 OFFSET $2`, q, offset, available == "1", skill, location)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	result := BuilderSearch{Items: []BuilderResult{}}
	for rows.Next() {
		var builder BuilderResult
		if err = rows.Scan(&builder.Username, &builder.DisplayName, &builder.Bio, &builder.Location, &builder.Skills, &builder.Availability, &builder.Followers, &builder.Repositories, &builder.OpenToCollaborators); err != nil {
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

func validLanguageFilter(value string) bool {
	if len(value) > 40 {
		return false
	}
	for _, r := range value {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && r != ' ' && r != '+' && r != '#' {
			return false
		}
	}
	return value != ""
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
	language := strings.TrimSpace(r.URL.Query().Get("language"))
	beginner := r.URL.Query().Get("beginner")
	if beginner != "" && beginner != "1" {
		fail(w, 422, "validation_failed", "Beginner filter must be 1 or omitted.")
		return
	}
	if len(q) > 100 || (topic != "" && !topicPattern.MatchString(topic)) || (language != "" && !validLanguageFilter(language)) || offset < 0 || offset > 1000 || (sort != "recent" && sort != "name" && sort != "sparks" && sort != "trending" && sort != "updated") {
		fail(w, 422, "validation_failed", "Use a query up to 100 characters, a valid topic, offset from zero to 1000, and recent, updated, name, sparks, or trending sort.")
		return
	}
	order := "r.created_at DESC,r.id DESC"
	if sort == "name" {
		order = "r.name ASC,r.id ASC"
	} else if sort == "sparks" {
		order = "(SELECT count(*) FROM repository_sparks rs WHERE rs.repository_id=r.id) DESC,r.created_at DESC,r.id DESC"
	} else if sort == "trending" {
		order = "(SELECT count(*) FROM repository_sparks rs WHERE rs.repository_id=r.id AND rs.created_at>=now()-interval '30 days') DESC,r.created_at DESC,r.id DESC"
	} else if sort == "updated" {
		order = "r.pushed_at DESC,r.id DESC"
	}
	rows, err := a.db.Query(r.Context(), `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id
		WHERE r.visibility='public' AND r.deleted_at IS NULL
		AND ($1='' OR strpos(lower(u.username||'/'||r.name||' '||r.description),lower($1))>0)
		AND ($2='' OR EXISTS (SELECT 1 FROM repository_topics rt WHERE rt.repository_id=r.id AND rt.topic=$2))
		AND ($4='' OR lower(r.language)=lower($4))
		AND (NOT $5 OR EXISTS (SELECT 1 FROM repository_topics rt WHERE rt.repository_id=r.id AND rt.topic='beginner-friendly'))
		ORDER BY `+order+` LIMIT 26 OFFSET $3`, q, topic, offset, language, beginner == "1")
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	result := RepositorySearch{Items: []Repository{}}
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
		result.Items = append(result.Items, repo)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	if err = a.decorateAll(r.Context(), result.Items, a.user(r)); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, result)
}
