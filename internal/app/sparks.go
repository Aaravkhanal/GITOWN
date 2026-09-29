package app

import (
	"net/http"
)

type SparkState struct {
	Count   int  `json:"count"`
	Sparked bool `json:"sparked"`
}

func (a *App) spark(w http.ResponseWriter, r *http.Request) {
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	u := a.user(r)
	userID := ""
	if u != nil {
		userID = u.ID
	}
	var state SparkState
	err := a.db.QueryRow(r.Context(), `SELECT count(*)::int, COALESCE(bool_or(user_id::text=$2),false) FROM repository_sparks WHERE repository_id=$1`, repo.ID, userID).Scan(&state.Count, &state.Sparked)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, state)
}

func (a *App) updateSpark(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	repo := a.access(w, r, false)
	if repo == nil {
		return
	}
	var in struct {
		Sparked *bool `json:"sparked"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Sparked == nil {
		fail(w, 422, "validation_failed", "Choose whether to Spark this repository.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if *in.Sparked {
		var recent int
		if err = tx.QueryRow(r.Context(), `SELECT count(*) FROM audit_events WHERE actor_id=$1 AND action='repository.sparked' AND created_at>now()-interval '1 hour'`, u.ID).Scan(&recent); err != nil {
			serverError(w, err)
			return
		}
		if recent >= 20 {
			fail(w, 429, "spark_limited", "Spark at most 20 repositories per hour.")
			return
		}
		_, err = tx.Exec(r.Context(), `INSERT INTO repository_sparks(repository_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, repo.ID, u.ID)
	} else {
		_, err = tx.Exec(r.Context(), `DELETE FROM repository_sparks WHERE repository_id=$1 AND user_id=$2`, repo.ID, u.ID)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var state SparkState
	err = tx.QueryRow(r.Context(), `SELECT count(*)::int, COALESCE(bool_or(user_id=$2),false) FROM repository_sparks WHERE repository_id=$1`, repo.ID, u.ID).Scan(&state.Count, &state.Sparked)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,$2,$3)`, u.ID, map[bool]string{true: "repository.sparked", false: "repository.unsparked"}[*in.Sparked], repo.Owner+"/"+repo.Name); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, state)
}
