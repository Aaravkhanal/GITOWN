package app

import (
	"net/http"
	"regexp"
	"strings"
)

var topicPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

type TopicState struct {
	Topics []string `json:"topics"`
}

func (a *App) topics(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT topic FROM repository_topics WHERE repository_id=$1 ORDER BY topic`, repo.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	state := TopicState{Topics: []string{}}
	for rows.Next() {
		var topic string
		if err = rows.Scan(&topic); err != nil {
			serverError(w, err)
			return
		}
		state.Topics = append(state.Topics, topic)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, state)
}

func (a *App) updateTopics(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	if !repo.CanManage {
		fail(w, 403, "forbidden", "Only the repository owner can change topics.")
		return
	}
	if !activeRepository(w, repo) {
		return
	}
	var in TopicState
	if !decode(w, r, &in) {
		return
	}
	if in.Topics == nil || len(in.Topics) > 10 {
		fail(w, 422, "validation_failed", "Choose up to ten unique topics.")
		return
	}
	seen := map[string]bool{}
	for i, topic := range in.Topics {
		topic = strings.ToLower(strings.TrimSpace(topic))
		if !topicPattern.MatchString(topic) || seen[topic] {
			fail(w, 422, "validation_failed", "Topics must be unique lowercase words up to 32 characters, with hyphens allowed.")
			return
		}
		in.Topics[i] = topic
		seen[topic] = true
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
	if _, err = tx.Exec(r.Context(), `DELETE FROM repository_topics WHERE repository_id=$1`, repo.ID); err != nil {
		serverError(w, err)
		return
	}
	for _, topic := range in.Topics {
		if _, err = tx.Exec(r.Context(), `INSERT INTO repository_topics(repository_id,topic) VALUES($1,$2)`, repo.ID, topic); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'repository.topics_updated',$2)`, repo.OwnerID, repo.Owner+"/"+repo.Name); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	a.topics(w, r)
}
