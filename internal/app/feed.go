package app

import (
	"net/http"
	"strings"
	"time"
)

type FeedEvent struct {
	Kind       string    `json:"kind"`
	Actor      string    `json:"actor"`
	Owner      string    `json:"owner"`
	Repository string    `json:"repository"`
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	CreatedAt  time.Time `json:"created_at"`
}

func (a *App) feed(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `WITH followed AS (
		SELECT followed_id FROM user_follows WHERE follower_id=$1
	), events AS (
		SELECT 'repository_created' AS kind,r.owner_id AS actor_id,r.id AS repository_id,0 AS number,r.name AS title,r.created_at
		FROM repositories r JOIN followed f ON f.followed_id=r.owner_id
		UNION ALL
		SELECT 'repository_sparked',s.user_id,s.repository_id,0,r.name,s.created_at
		FROM repository_sparks s JOIN followed f ON f.followed_id=s.user_id JOIN repositories r ON r.id=s.repository_id
		UNION ALL
		SELECT 'issue_opened',i.author_id,i.repository_id,i.number,i.title,i.created_at
		FROM issues i JOIN followed f ON f.followed_id=i.author_id
		UNION ALL
		SELECT 'unite_opened',p.author_id,p.repository_id,p.number,p.title,p.created_at
		FROM pull_requests p JOIN followed f ON f.followed_id=p.author_id
	)
	SELECT e.kind,actor.username,owner.username,repo.name,e.number,e.title,e.created_at
	FROM events e JOIN repositories repo ON repo.id=e.repository_id
	JOIN users actor ON actor.id=e.actor_id JOIN users owner ON owner.id=repo.owner_id
	WHERE `+strings.ReplaceAll(visibleRepoPredicate(2), "r.", "repo.")+` AND repo.deleted_at IS NULL
	ORDER BY e.created_at DESC LIMIT 100`, u.ID, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	events := []FeedEvent{}
	for rows.Next() {
		var event FeedEvent
		if err = rows.Scan(&event.Kind, &event.Actor, &event.Owner, &event.Repository, &event.Number, &event.Title, &event.CreatedAt); err != nil {
			serverError(w, err)
			return
		}
		events = append(events, event)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, events)
}
