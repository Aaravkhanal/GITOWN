package app

import (
	"context"
	"encoding/csv"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

var errDistrictPolicy = errors.New("district policy rejected the change")

type rowQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func isOperator(u *User) bool {
	if u == nil {
		return false
	}
	for _, name := range strings.Split(os.Getenv("GITOWN_OPERATORS"), ",") {
		if strings.EqualFold(strings.TrimSpace(name), u.Username) {
			return true
		}
	}
	return false
}

func districtAllowsPublic(ctx context.Context, q rowQuery, districtID, visibility string) error {
	if districtID == "" || visibility != "public" {
		return nil
	}
	var allow bool
	if err := q.QueryRow(ctx, `SELECT allow_public FROM districts WHERE id=$1`, districtID).Scan(&allow); err != nil {
		return err
	}
	if !allow {
		return errDistrictPolicy
	}
	return nil
}

func (a *App) outsideCollaboratorAllowed(ctx context.Context, districtID, username string) (bool, error) {
	if districtID == "" {
		return true, nil
	}
	var allow, inside bool
	err := a.db.QueryRow(ctx, `SELECT d.allow_outside_collaborators,
		d.owner_id=(SELECT id FROM users WHERE username=$2)
		OR EXISTS (SELECT 1 FROM district_members m JOIN users u ON u.id=m.user_id WHERE m.district_id=d.id AND u.username=$2)
		FROM districts d WHERE d.id=$1`, districtID, username).Scan(&allow, &inside)
	if err != nil {
		return false, err
	}
	return allow || inside, nil
}

func (a *App) topicCatalog(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(r.Context(), `SELECT t.topic, count(*)::int FROM repository_topics t JOIN repositories r ON r.id=t.repository_id WHERE r.visibility='public' AND r.deleted_at IS NULL GROUP BY t.topic ORDER BY count(*) DESC, t.topic LIMIT 50`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var topic string
		var count int
		if err = rows.Scan(&topic, &count); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"topic": topic, "repositories": count})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) topicPage(w http.ResponseWriter, r *http.Request) {
	topic := strings.ToLower(r.PathValue("topic"))
	if !topicPattern.MatchString(topic) {
		fail(w, 404, "not_found", "Topic not found.")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT `+repoColumns+` FROM repository_topics t JOIN repositories r ON r.id=t.repository_id JOIN users u ON u.id=r.owner_id WHERE t.topic=$1 AND r.visibility='public' AND r.deleted_at IS NULL ORDER BY r.pushed_at DESC, r.id DESC LIMIT 30`, topic)
	if err != nil {
		serverError(w, err)
		return
	}
	items, err := a.scanRepoList(r, rows)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"topic": topic, "items": items})
}

func (a *App) contributions(w http.ResponseWriter, r *http.Request) {
	username := strings.ToLower(r.PathValue("username"))
	if !slug.MatchString(username) {
		fail(w, 404, "not_found", "Builder not found.")
		return
	}
	var userID string
	err := a.db.QueryRow(r.Context(), `SELECT id::text FROM users WHERE username=$1`, username).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Builder not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT day, count(*)::int FROM (
		SELECT to_char(i.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day FROM issues i JOIN repositories r ON r.id=i.repository_id WHERE i.author_id=$1 AND r.visibility='public' AND r.deleted_at IS NULL AND i.created_at>now()-interval '180 days'
		UNION ALL
		SELECT to_char(c.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') FROM issue_comments c JOIN issues i ON i.id=c.issue_id JOIN repositories r ON r.id=i.repository_id WHERE c.author_id=$1 AND r.visibility='public' AND r.deleted_at IS NULL AND c.created_at>now()-interval '180 days'
		UNION ALL
		SELECT to_char(e.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') FROM ref_events e JOIN repositories r ON r.id=e.repository_id WHERE e.actor_id=$1 AND e.new_sha<>repeat('0',40) AND r.visibility='public' AND r.deleted_at IS NULL AND e.created_at>now()-interval '180 days'
		UNION ALL
		SELECT to_char(rv.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') FROM pull_reviews rv JOIN pull_requests p ON p.id=rv.pull_request_id JOIN repositories r ON r.id=p.repository_id WHERE rv.reviewer_id=$1 AND p.author_id<>$1 AND r.visibility='public' AND r.deleted_at IS NULL AND rv.created_at>now()-interval '180 days'
		UNION ALL
		SELECT to_char(c.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') FROM pull_comments c JOIN pull_requests p ON p.id=c.pull_request_id JOIN repositories r ON r.id=p.repository_id WHERE c.author_id=$1 AND r.visibility='public' AND r.deleted_at IS NULL AND c.created_at>now()-interval '180 days'
		UNION ALL
		SELECT to_char(COALESCE(p.merged_at, p.created_at) AT TIME ZONE 'UTC', 'YYYY-MM-DD') FROM pull_requests p JOIN repositories r ON r.id=p.repository_id WHERE p.author_id=$1 AND p.state='merged' AND r.visibility='public' AND r.deleted_at IS NULL AND COALESCE(p.merged_at, p.created_at)>now()-interval '180 days'
	) events GROUP BY day ORDER BY day`, userID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	days := []map[string]any{}
	total := 0
	for rows.Next() {
		var day string
		var count int
		if err = rows.Scan(&day, &count); err != nil {
			serverError(w, err)
			return
		}
		total += count
		days = append(days, map[string]any{"date": day, "count": count})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"username": username, "total": total, "days": days})
}

func (a *App) featureCollection(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if !isOperator(u) {
		fail(w, 403, "forbidden", "Set GITOWN_OPERATORS to the username that may feature a collection.")
		return
	}
	var in struct {
		Featured bool `json:"featured"`
	}
	if !decode(w, r, &in) {
		return
	}
	result, err := a.db.Exec(r.Context(), `UPDATE collections c SET featured=$1 FROM users u WHERE u.id=c.owner_id AND u.username=$2 AND c.slug=$3`, in.Featured, r.PathValue("owner"), r.PathValue("slug"))
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Collection not found.")
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'collection.featured',$2)`, u.ID, r.PathValue("owner")+"/"+r.PathValue("slug"))
	respond(w, 200, map[string]any{"owner": r.PathValue("owner"), "slug": r.PathValue("slug"), "featured": in.Featured})
}

func (a *App) districtUsage(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, true)
	if d == nil {
		return
	}
	var repos, members, secrets, openInvoices int
	var storage int64
	err := a.db.QueryRow(r.Context(), `SELECT
		(SELECT count(*)::int FROM repositories WHERE district_id=$1 AND deleted_at IS NULL),
		(SELECT COALESCE(sum(size_bytes),0) FROM repositories WHERE district_id=$1 AND deleted_at IS NULL),
		(SELECT count(*)::int FROM district_members WHERE district_id=$1),
		(SELECT count(*)::int FROM district_secrets WHERE district_id=$1),
		(SELECT count(*)::int FROM district_invoices WHERE district_id=$1 AND status='open')`, d.ID).Scan(&repos, &storage, &members, &secrets, &openInvoices)
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"repositories": repos, "storage_bytes": storage, "members": members, "secrets": secrets, "open_invoices": openInvoices, "charges": false})
}

func (a *App) districtInvoices(w http.ResponseWriter, r *http.Request) {
	d := a.loadDistrict(w, r, true)
	if d == nil {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id::text,period,amount_cents,status,created_at FROM district_invoices WHERE district_id=$1 ORDER BY period DESC`, d.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, period, status string
		var amount int
		var created time.Time
		if err = rows.Scan(&id, &period, &amount, &status, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "period": period, "amount_cents": amount, "status": status, "created_at": created})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items, "charges": false})
}

func (a *App) createDistrictInvoice(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if !isOperator(u) {
		fail(w, 403, "forbidden", "Set GITOWN_OPERATORS to the username that may record an invoice.")
		return
	}
	d := a.loadDistrict(w, r, false)
	if d == nil {
		return
	}
	var in struct {
		Period      string `json:"period"`
		AmountCents int    `json:"amount_cents"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Period = strings.TrimSpace(in.Period)
	validPeriod := len(in.Period) == 7 && in.Period[4] == '-'
	if validPeriod {
		for i := 0; i < len(in.Period); i++ {
			if i == 4 {
				continue
			}
			if in.Period[i] < '0' || in.Period[i] > '9' {
				validPeriod = false
			}
		}
	}
	if !validPeriod || in.AmountCents < 0 || in.AmountCents > 100000000 {
		fail(w, 422, "validation_failed", "Use a YYYY-MM period and a non-negative amount in cents.")
		return
	}
	id := auth.ID()
	var created time.Time
	err := a.db.QueryRow(r.Context(), `INSERT INTO district_invoices(id,district_id,period,amount_cents,status) VALUES($1,$2,$3,$4,'open') RETURNING created_at`, id, d.ID, in.Period, in.AmountCents).Scan(&created)
	if conflict(err) {
		fail(w, 409, "invoice_exists", "That billing period already has an invoice.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'district.invoice_created',$2)`, u.ID, "district/"+d.Slug+"/"+in.Period)
	respond(w, 201, map[string]any{"id": id, "period": in.Period, "amount_cents": in.AmountCents, "status": "open", "created_at": created, "charges": false})
}

func (a *App) payDistrictInvoice(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if !isOperator(u) {
		fail(w, 403, "forbidden", "Set GITOWN_OPERATORS to the username that may mark an invoice paid.")
		return
	}
	d := a.loadDistrict(w, r, false)
	if d == nil {
		return
	}
	result, err := a.db.Exec(r.Context(), `UPDATE district_invoices SET status='paid' WHERE id=$1 AND district_id=$2 AND status='open'`, r.PathValue("id"), d.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Open invoice not found.")
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'district.invoice_paid',$2)`, u.ID, "district/"+d.Slug+"/"+r.PathValue("id"))
	respond(w, 200, map[string]any{"id": r.PathValue("id"), "status": "paid", "charges": false})
}

func (a *App) writeAuditExport(w http.ResponseWriter, slug string, items [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+slug+`-audit.csv"`)
	w.WriteHeader(200)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"actor", "action", "target", "created_at"})
	_ = writer.WriteAll(items)
}
