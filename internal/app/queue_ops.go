package app

import (
	"context"
	"net/http"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var queueItemID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var queueErrorEmail = regexp.MustCompile(`(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}`)
var queueErrorURL = regexp.MustCompile(`(?i)https?://[^\s]+`)

func safeQueueError(value string, limit int) string {
	value = queueErrorEmail.ReplaceAllString(value, "[address]")
	value = queueErrorURL.ReplaceAllString(value, "[url]")
	return cleanHeader(value, limit)
}

type queueSummary struct {
	Queue                string         `json:"queue"`
	Counts               map[string]int `json:"counts"`
	OldestPendingSeconds float64        `json:"oldest_pending_seconds"`
}

func (a *App) operatorQueues(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if !isOperator(u) {
		fail(w, 403, "forbidden", "This endpoint is restricted to configured GITOWN operators.")
		return
	}
	summaries, err := a.queueSummaries(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	email, err := a.recentEmailDeadLetters(r)
	if err != nil {
		serverError(w, err)
		return
	}
	webhooks, err := a.recentWebhookDeadLetters(r)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{
		"queues":                     summaries,
		"dead_letters":               map[string]any{"email": email, "webhooks": webhooks},
		"delivery_semantics":         "at_least_once; recovered in-flight requests may have reached their receiver before a worker stopped",
		"workflow_execution_enabled": false,
	})
}

func (a *App) queueSummaries(ctx context.Context) ([]queueSummary, error) {
	type source struct{ name, query string }
	sources := []source{
		{"email", `SELECT status,count(*)::int,COALESCE(max(extract(epoch FROM now()-created_at)) FILTER (WHERE status='pending'),0) FROM email_messages GROUP BY status`},
		{"webhooks", `SELECT status,count(*)::int,COALESCE(max(extract(epoch FROM now()-created_at)) FILTER (WHERE status='pending'),0) FROM webhook_deliveries GROUP BY status`},
		{"routes", `SELECT status,count(*)::int,COALESCE(max(extract(epoch FROM now()-created_at)) FILTER (WHERE status='queued'),0) FROM route_runs GROUP BY status`},
		{"route_jobs", `SELECT status,count(*)::int,COALESCE(max(extract(epoch FROM now()-created_at)) FILTER (WHERE status IN ('ready','blocked')),0) FROM route_jobs GROUP BY status`},
	}
	out := make([]queueSummary, 0, len(sources))
	for _, src := range sources {
		rows, err := a.db.Query(ctx, src.query)
		if err != nil {
			return nil, err
		}
		item := queueSummary{Queue: src.name, Counts: map[string]int{}}
		for rows.Next() {
			var status string
			var count int
			var oldest float64
			if err = rows.Scan(&status, &count, &oldest); err != nil {
				rows.Close()
				return nil, err
			}
			item.Counts[status] = count
			if oldest > item.OldestPendingSeconds {
				item.OldestPendingSeconds = oldest
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (a *App) recentEmailDeadLetters(r *http.Request) ([]map[string]any, error) {
	rows, err := a.db.Query(r.Context(), `SELECT id::text,kind,attempts,last_error,created_at::text FROM email_messages WHERE status='failed' ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, kind, lastError, created string
		var attempts int
		if err = rows.Scan(&id, &kind, &attempts, &lastError, &created); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "kind": kind, "attempts": attempts, "last_error": safeQueueError(lastError, 300), "created_at": created})
	}
	return items, rows.Err()
}

func (a *App) recentWebhookDeadLetters(r *http.Request) ([]map[string]any, error) {
	rows, err := a.db.Query(r.Context(), `SELECT id::text,event,attempts,COALESCE(last_error,''),created_at::text FROM webhook_deliveries WHERE status='failed' ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, event, lastError, created string
		var attempts int
		if err = rows.Scan(&id, &event, &attempts, &lastError, &created); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": id, "event": event, "attempts": attempts, "last_error": safeQueueError(lastError, 500), "created_at": created})
	}
	return items, rows.Err()
}

func (a *App) retryDeadLetter(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if !isOperator(u) {
		fail(w, 403, "forbidden", "This endpoint is restricted to configured GITOWN operators.")
		return
	}
	if !queueItemID.MatchString(r.PathValue("id")) {
		fail(w, 404, "not_found", "Dead-letter item not found.")
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var tag pgconn.CommandTag
	switch r.PathValue("queue") {
	case "email":
		tag, err = tx.Exec(r.Context(), `UPDATE email_messages SET status='pending',attempts=0,next_attempt_at=now(),last_error='',claimed_at=NULL WHERE id=$1 AND status='failed' AND kind NOT IN ('password_reset','email_verification')`, r.PathValue("id"))
	case "webhooks":
		tag, err = tx.Exec(r.Context(), `UPDATE webhook_deliveries SET status='pending',attempts=0,next_attempt_at=now(),last_error='',claimed_at=NULL,response_status=NULL,response_body=NULL,delivered_at=NULL WHERE id=$1 AND status='failed'`, r.PathValue("id"))
	default:
		fail(w, 404, "not_found", "Unknown queue.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 409, "not_dead_lettered", "Only a failed dead-letter item can be retried.")
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'worker.dead_letter_retried',$2)`, u.ID, r.PathValue("queue")+":"+r.PathValue("id")); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"retried": true, "queue": r.PathValue("queue"), "next_attempt_at": time.Now().UTC()})
}
