package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Delivery tuning lives here so the worker, the SSRF guard, and the
// response-capture limit agree on one set of numbers, matching how mail.go
// centralizes its own outbox constants.
const (
	webhookWorkerInterval = 5 * time.Second
	webhookBatchSize      = 10
	webhookMaxAttempts    = 8
	webhookRequestTimeout = 10 * time.Second
	webhookResponseCap    = 4000
	webhookPerScopeLimit  = 20
)

// webhookEventKinds is the fixed, documented set of events a subscription
// may request. Event names mirror the audit_events action strings already
// used at each firing site (e.g. "issue.opened") so the two logs read the
// same way.
var webhookEventKinds = map[string]bool{
	"push":                true,
	"issue.opened":        true,
	"issue.closed":        true,
	"issue.reopened":      true,
	"issue.commented":     true,
	"pull.opened":         true,
	"pull.closed":         true,
	"pull.reopened":       true,
	"pull.merged":         true,
	"pull.reviewed":       true,
	"pull.commented":      true,
	"drop.published":      true,
	"app.installed":       true,
	"app.uninstalled":     true,
	"route.run_queued":    true,
	"route.run_cancelled": true,
}

// execer is satisfied by both *pgxpool.Pool and pgx.Tx, so fireWebhook can
// enqueue inside a caller's open transaction (the common case: the delivery
// row is committed atomically with whatever triggered it) or directly
// against the pool when there is none, such as after a git push.
type execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

var errPrivateWebhookAddress = errors.New("that address is not allowed for a webhook")

// isDisallowedWebhookIP reports whether ip is loopback, link-local, or in a
// private range, including the 169.254.169.254 cloud metadata address and
// IPv6 unique-local space. It is checked both when a webhook is created and
// again on every delivery attempt (see safeWebhookDialContext), so a DNS
// record that later repoints at an internal address is still refused.
func isDisallowedWebhookIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		return ip4[0] == 10 ||
			(ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) ||
			(ip4[0] == 192 && ip4[1] == 168) ||
			(ip4[0] == 169 && ip4[1] == 254)
	}
	if len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc { // fc00::/7 unique local
		return true
	}
	return false
}

// validWebhookURL checks the URL shape and does a best-effort DNS lookup at
// creation time. It is only the first check: safeWebhookDialContext repeats
// the resolve-then-check on every delivery, since a hostname's DNS answer
// can change after a webhook is saved (DNS rebinding).
func validWebhookURL(ctx context.Context, raw string) error {
	if raw == "" || len(raw) > 2000 {
		return errors.New("enter a webhook URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("webhook URLs must be an https:// address")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if isDisallowedWebhookIP(ip) {
			return errPrivateWebhookAddress
		}
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return nil // delivery re-resolves and will refuse a bad target then
	}
	for _, addr := range ips {
		if isDisallowedWebhookIP(addr.IP) {
			return errPrivateWebhookAddress
		}
	}
	return nil
}

// safeWebhookDialContext resolves the host itself and connects to a
// checked, literal IP address rather than letting net/http resolve and
// dial in one step, so a delivery can never land on a private or
// link-local address no matter what the DNS answer says at send time.
func safeWebhookDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	lastErr := errPrivateWebhookAddress
	for _, addr := range ips {
		if isDisallowedWebhookIP(addr.IP) {
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	return nil, lastErr
}

// newWebhookHTTPClient never follows redirects: a 3xx response is recorded
// as the delivery's outcome rather than chased, which would otherwise let a
// webhook target bounce a request to a private address after the initial
// URL passed validation. New() assigns this to App.webhookClient; tests
// that need a real local receiver substitute their own client (the same
// seam App.mailer already uses for email) rather than weakening the
// dialer every production request goes through.
func newWebhookHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   webhookRequestTimeout,
		Transport: &http.Transport{DialContext: safeWebhookDialContext},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func webhookSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// fireWebhook enqueues one delivery row for every active webhook (scoped to
// this repository, or to its district for a district-wide subscription)
// that is subscribed to kind. It is called with the caller's own open
// transaction wherever one already exists, so the delivery is committed
// atomically with whatever triggered it and never enqueued if that
// transaction later rolls back.
func (a *App) fireWebhook(ctx context.Context, db execer, repositoryID, districtID, kind string, payload map[string]any) error {
	payload["event"] = kind
	payload["fired_at"] = time.Now().UTC().Format(time.RFC3339)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var repoArg, districtArg any
	if repositoryID != "" {
		repoArg = repositoryID
	}
	if districtID != "" {
		districtArg = districtID
	}
	if repoArg == nil && districtArg == nil {
		return nil
	}
	rows, err := db.Query(ctx, `SELECT id FROM webhooks WHERE active AND $1=ANY(events) AND ((repository_id=$2 AND $2 IS NOT NULL) OR (district_id=$3 AND $3 IS NOT NULL))`, kind, repoArg, districtArg)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = db.Exec(ctx, `INSERT INTO webhook_deliveries(id,webhook_id,event,payload) VALUES($1,$2,$3,$4)`, auth.ID(), id, kind, body); err != nil {
			return err
		}
	}
	return nil
}

// startWebhookWorker runs the delivery loop until ctx is cancelled,
// following the exact claim-then-send-outside-any-transaction shape
// deliverMail already uses.
func (a *App) startWebhookWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(webhookWorkerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			for {
				sent, err := a.deliverWebhooks(ctx)
				if err != nil && ctx.Err() == nil {
					slog.Error("webhook delivery failed", "error", err)
				}
				if err != nil || sent < webhookBatchSize {
					break
				}
			}
		}
	}()
}

type queuedWebhookDelivery struct {
	id, webhookID, event string
	payload              []byte
	attempts             int
}

func (a *App) claimWebhookDeliveries(ctx context.Context) ([]queuedWebhookDelivery, error) {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,webhook_id,event,payload,attempts FROM webhook_deliveries
		WHERE status='pending' AND next_attempt_at<=now() ORDER BY next_attempt_at,created_at LIMIT $1 FOR UPDATE SKIP LOCKED`, webhookBatchSize)
	if err != nil {
		return nil, err
	}
	var batch []queuedWebhookDelivery
	for rows.Next() {
		var item queuedWebhookDelivery
		if err = rows.Scan(&item.id, &item.webhookID, &item.event, &item.payload, &item.attempts); err != nil {
			rows.Close()
			return nil, err
		}
		batch = append(batch, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, len(batch))
	for i, item := range batch {
		ids[i] = item.id
	}
	if len(ids) > 0 {
		if _, err = tx.Exec(ctx, `UPDATE webhook_deliveries SET status='sending' WHERE id=ANY($1)`, ids); err != nil {
			return nil, err
		}
	}
	return batch, tx.Commit(ctx)
}

// deliverWebhooks claims one batch of due deliveries, then sends each with
// its own request so a slow or hanging receiver never blocks the others.
func (a *App) deliverWebhooks(ctx context.Context) (int, error) {
	batch, err := a.claimWebhookDeliveries(ctx)
	if err != nil {
		return 0, err
	}
	for _, item := range batch {
		var targetURL, kind string
		var secretCiphertext, secretNonce []byte
		err = a.db.QueryRow(ctx, `SELECT url,secret_ciphertext,secret_nonce,kind FROM webhooks WHERE id=$1`, item.webhookID).Scan(&targetURL, &secretCiphertext, &secretNonce, &kind)
		if errors.Is(err, pgx.ErrNoRows) {
			// The subscription was deleted after this delivery was queued.
			if _, delErr := a.db.Exec(ctx, `UPDATE webhook_deliveries SET status='failed',last_error='webhook was deleted' WHERE id=$1`, item.id); delErr != nil {
				return 0, delErr
			}
			continue
		}
		if err != nil {
			return 0, err
		}
		secret, err := openSecret(secretCiphertext, secretNonce)
		if err != nil {
			return 0, err
		}
		// Slack/Discord targets get their payload reshaped into the body
		// each platform expects; the HMAC signature below covers whatever
		// bytes are actually sent, so this happens before signing.
		body := formatWebhookBody(kind, item.event, item.payload)
		ok, responseStatus, responseBody, sendErr := a.sendWebhook(ctx, targetURL, secret, item.event, body)
		attempts := item.attempts + 1
		// A transport failure (DNS, connect, timeout, the SSRF guard
		// refusing the resolved address) never reached a receiver, so
		// there is no HTTP status or body to record for it.
		var statusArg, bodyArg any
		if sendErr == nil {
			statusArg, bodyArg = responseStatus, responseBody
		}
		if sendErr == nil && ok {
			_, err = a.db.Exec(ctx, `UPDATE webhook_deliveries SET status='success',attempts=$2,response_status=$3,response_body=$4,delivered_at=now(),last_error='' WHERE id=$1`,
				item.id, attempts, statusArg, bodyArg)
		} else {
			failStatus := "pending"
			errText := ""
			if sendErr != nil {
				errText = sendErr.Error()
			} else {
				errText = fmt.Sprintf("receiver returned HTTP %d", responseStatus)
			}
			if attempts >= webhookMaxAttempts {
				failStatus = "failed"
			}
			delay := time.Minute << min(attempts-1, 8)
			delay = min(delay, 6*time.Hour)
			_, err = a.db.Exec(ctx, `UPDATE webhook_deliveries SET status=$2,attempts=$3,response_status=$4,response_body=$5,last_error=$6,next_attempt_at=now()+make_interval(secs => $7) WHERE id=$1`,
				item.id, failStatus, attempts, statusArg, bodyArg, cleanHeader(errText, 500), delay.Seconds())
			slog.Warn("webhook delivery attempt failed", "delivery_id", item.id, "attempts", attempts, "error", errText)
		}
		if err != nil {
			return 0, err
		}
	}
	return len(batch), nil
}

// sendWebhook POSTs one payload and reports whether the receiver answered
// with a 2xx status. It never returns an unsanitized receiver-controlled
// error: sendErr is only set for a transport-level failure (DNS, connect,
// timeout, or the SSRF guard refusing the resolved address).
func (a *App) sendWebhook(ctx context.Context, targetURL, secret, event string, payload []byte) (ok bool, status int, responseBody string, err error) {
	sendCtx, cancel := context.WithTimeout(ctx, webhookRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, targetURL, strings.NewReader(string(payload)))
	if err != nil {
		return false, 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GITOWN-Event", event)
	req.Header.Set("X-GITOWN-Signature", webhookSignature(secret, payload))
	req.Header.Set("User-Agent", "GITOWN-Webhook/1.0")
	resp, err := a.webhookClient.Do(req)
	if err != nil {
		return false, 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, webhookResponseCap))
	return resp.StatusCode >= 200 && resp.StatusCode < 300, resp.StatusCode, string(body), nil
}

// webhookScope names which foreign key column a set of webhook routes are
// bound to, so the same handlers serve both repository-scoped and
// district-wide subscriptions without duplicating the SQL.
type webhookScope struct {
	column string
	id     string
}

func (a *App) repoWebhookScope(w http.ResponseWriter, r *http.Request) (*webhookScope, *User) {
	repo := a.managedRepository(w, r)
	if repo == nil {
		return nil, nil
	}
	return &webhookScope{column: "repository_id", id: repo.ID}, a.user(r)
}

func (a *App) districtWebhookScope(w http.ResponseWriter, r *http.Request) (*webhookScope, *User) {
	d := a.loadDistrict(w, r, true)
	if d == nil {
		return nil, nil
	}
	return &webhookScope{column: "district_id", id: d.ID}, a.user(r)
}

func (a *App) repoWebhooks(w http.ResponseWriter, r *http.Request) {
	scope, _ := a.repoWebhookScope(w, r)
	if scope == nil {
		return
	}
	a.listWebhooks(w, r, scope)
}
func (a *App) createRepoWebhook(w http.ResponseWriter, r *http.Request) {
	scope, u := a.repoWebhookScope(w, r)
	if scope == nil {
		return
	}
	a.createWebhook(w, r, scope, u)
}
func (a *App) updateRepoWebhook(w http.ResponseWriter, r *http.Request) {
	scope, u := a.repoWebhookScope(w, r)
	if scope == nil {
		return
	}
	a.updateWebhook(w, r, scope, u)
}
func (a *App) deleteRepoWebhook(w http.ResponseWriter, r *http.Request) {
	scope, u := a.repoWebhookScope(w, r)
	if scope == nil {
		return
	}
	a.deleteWebhook(w, r, scope, u)
}
func (a *App) repoWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	scope, _ := a.repoWebhookScope(w, r)
	if scope == nil {
		return
	}
	a.webhookDeliveries(w, r, scope)
}
func (a *App) replayRepoWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	scope, u := a.repoWebhookScope(w, r)
	if scope == nil {
		return
	}
	a.replayWebhookDelivery(w, r, scope, u)
}

func (a *App) districtWebhooks(w http.ResponseWriter, r *http.Request) {
	scope, _ := a.districtWebhookScope(w, r)
	if scope == nil {
		return
	}
	a.listWebhooks(w, r, scope)
}
func (a *App) createDistrictWebhook(w http.ResponseWriter, r *http.Request) {
	scope, u := a.districtWebhookScope(w, r)
	if scope == nil {
		return
	}
	a.createWebhook(w, r, scope, u)
}
func (a *App) updateDistrictWebhook(w http.ResponseWriter, r *http.Request) {
	scope, u := a.districtWebhookScope(w, r)
	if scope == nil {
		return
	}
	a.updateWebhook(w, r, scope, u)
}
func (a *App) deleteDistrictWebhook(w http.ResponseWriter, r *http.Request) {
	scope, u := a.districtWebhookScope(w, r)
	if scope == nil {
		return
	}
	a.deleteWebhook(w, r, scope, u)
}
func (a *App) districtWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	scope, _ := a.districtWebhookScope(w, r)
	if scope == nil {
		return
	}
	a.webhookDeliveries(w, r, scope)
}
func (a *App) replayDistrictWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	scope, u := a.districtWebhookScope(w, r)
	if scope == nil {
		return
	}
	a.replayWebhookDelivery(w, r, scope, u)
}

func (a *App) listWebhooks(w http.ResponseWriter, r *http.Request, scope *webhookScope) {
	rows, err := a.db.Query(r.Context(), `SELECT id,url,events,active,kind,created_at FROM webhooks WHERE `+scope.column+`=$1 ORDER BY created_at DESC`, scope.id)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, hookURL, kind string
		var events []string
		var active bool
		var created time.Time
		if err = rows.Scan(&id, &hookURL, &events, &active, &kind, &created); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "url": hookURL, "events": events, "active": active, "kind": kind, "created_at": created})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func normalizeWebhookKind(kind string) string {
	switch kind {
	case "slack", "discord":
		return kind
	default:
		return "generic"
	}
}

// formatWebhookBody reshapes the generic event JSON into the payload shape
// each chat platform actually expects, immediately before signing and
// sending — the HMAC signature covers whatever bytes are put on the wire,
// so reshaping happens here rather than at enqueue time. A generic
// (non-chat) webhook is passed through untouched.
func formatWebhookBody(kind, event string, raw []byte) []byte {
	if kind != "slack" && kind != "discord" {
		return raw
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return raw
	}
	summary := fmt.Sprintf("GITOWN: `%s` event", event)
	if repo, ok := generic["repository"].(string); ok && repo != "" {
		summary = fmt.Sprintf("GITOWN: `%s` on %s", event, repo)
	}
	var body map[string]any
	if kind == "slack" {
		body = map[string]any{"text": summary}
	} else {
		body = map[string]any{"content": summary}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return raw
	}
	return encoded
}

func normalizeWebhookEvents(w http.ResponseWriter, raw []string) ([]string, bool) {
	if len(raw) == 0 || len(raw) > 20 {
		fail(w, 422, "validation_failed", "Choose between one and twenty events to subscribe to.")
		return nil, false
	}
	seen := map[string]bool{}
	events := make([]string, 0, len(raw))
	for _, kind := range raw {
		if !webhookEventKinds[kind] {
			fail(w, 422, "validation_failed", "Unknown event: "+kind)
			return nil, false
		}
		if !seen[kind] {
			seen[kind] = true
			events = append(events, kind)
		}
	}
	return events, true
}

func (a *App) createWebhook(w http.ResponseWriter, r *http.Request, scope *webhookScope, u *User) {
	var in struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
		Kind   string   `json:"kind"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.URL = strings.TrimSpace(in.URL)
	in.Kind = normalizeWebhookKind(in.Kind)
	if err := validWebhookURL(r.Context(), in.URL); err != nil {
		fail(w, 422, "validation_failed", err.Error())
		return
	}
	events, ok := normalizeWebhookEvents(w, in.Events)
	if !ok {
		return
	}
	var count int
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM webhooks WHERE `+scope.column+`=$1`, scope.id).Scan(&count); err != nil {
		serverError(w, err)
		return
	}
	if count >= webhookPerScopeLimit {
		fail(w, 422, "webhook_limit", fmt.Sprintf("Up to %d webhooks are allowed here.", webhookPerScopeLimit))
		return
	}
	secret := auth.Secret("whsec_")
	ciphertext, nonce, err := sealSecret(secret)
	if errors.Is(err, errSecretUnconfigured) {
		fail(w, 503, "secret_key_unconfigured", "Set GITOWN_SECRET_KEY before creating webhooks.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	id := auth.ID()
	var repoArg, districtArg any
	if scope.column == "repository_id" {
		repoArg = scope.id
	} else {
		districtArg = scope.id
	}
	var created time.Time
	if err = a.db.QueryRow(r.Context(), `INSERT INTO webhooks(id,repository_id,district_id,url,secret_ciphertext,secret_nonce,events,created_by,kind) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at`,
		id, repoArg, districtArg, in.URL, ciphertext, nonce, events, u.ID, in.Kind).Scan(&created); err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'webhook.created',$2)`, u.ID, in.URL)
	// The secret is only ever shown here, at creation (or on regeneration
	// through updateWebhook) — every later read omits it, matching how
	// access tokens and district secrets behave in this codebase.
	respond(w, 201, map[string]any{"id": id, "url": in.URL, "events": events, "active": true, "kind": in.Kind, "created_at": created, "secret": secret})
}

func (a *App) updateWebhook(w http.ResponseWriter, r *http.Request, scope *webhookScope, u *User) {
	var in struct {
		URL              *string  `json:"url"`
		Events           []string `json:"events"`
		Active           *bool    `json:"active"`
		RegenerateSecret bool     `json:"regenerate_secret"`
	}
	if !decode(w, r, &in) {
		return
	}
	var hookURL string
	var events []string
	var active bool
	if err := a.db.QueryRow(r.Context(), `SELECT url,events,active FROM webhooks WHERE id=$1 AND `+scope.column+`=$2`, r.PathValue("id"), scope.id).Scan(&hookURL, &events, &active); errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Webhook not found.")
		return
	} else if err != nil {
		serverError(w, err)
		return
	}
	if in.URL != nil {
		hookURL = strings.TrimSpace(*in.URL)
		if err := validWebhookURL(r.Context(), hookURL); err != nil {
			fail(w, 422, "validation_failed", err.Error())
			return
		}
	}
	if in.Events != nil {
		normalized, ok := normalizeWebhookEvents(w, in.Events)
		if !ok {
			return
		}
		events = normalized
	}
	if in.Active != nil {
		active = *in.Active
	}
	var newSecret string
	var ciphertext, nonce []byte
	if in.RegenerateSecret {
		newSecret = auth.Secret("whsec_")
		sealed, sealedNonce, err := sealSecret(newSecret)
		if errors.Is(err, errSecretUnconfigured) {
			fail(w, 503, "secret_key_unconfigured", "Set GITOWN_SECRET_KEY before regenerating a webhook secret.")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		ciphertext, nonce = sealed, sealedNonce
	}
	var err error
	if in.RegenerateSecret {
		_, err = a.db.Exec(r.Context(), `UPDATE webhooks SET url=$1,events=$2,active=$3,secret_ciphertext=$4,secret_nonce=$5 WHERE id=$6`,
			hookURL, events, active, ciphertext, nonce, r.PathValue("id"))
	} else {
		_, err = a.db.Exec(r.Context(), `UPDATE webhooks SET url=$1,events=$2,active=$3 WHERE id=$4`, hookURL, events, active, r.PathValue("id"))
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'webhook.updated',$2)`, u.ID, hookURL)
	out := map[string]any{"id": r.PathValue("id"), "url": hookURL, "events": events, "active": active}
	if newSecret != "" {
		out["secret"] = newSecret
	}
	respond(w, 200, out)
}

func (a *App) deleteWebhook(w http.ResponseWriter, r *http.Request, scope *webhookScope, u *User) {
	tag, err := a.db.Exec(r.Context(), `DELETE FROM webhooks WHERE id=$1 AND `+scope.column+`=$2`, r.PathValue("id"), scope.id)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		fail(w, 404, "not_found", "Webhook not found.")
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'webhook.deleted',$2)`, u.ID, r.PathValue("id"))
	respond(w, 200, map[string]bool{"removed": true})
}

func (a *App) webhookDeliveries(w http.ResponseWriter, r *http.Request, scope *webhookScope) {
	var owns bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM webhooks WHERE id=$1 AND `+scope.column+`=$2)`, r.PathValue("id"), scope.id).Scan(&owns); err != nil {
		serverError(w, err)
		return
	}
	if !owns {
		fail(w, 404, "not_found", "Webhook not found.")
		return
	}
	page, perPage, ok := pageParams(w, r, 30, 100)
	if !ok {
		return
	}
	var total int
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM webhook_deliveries WHERE webhook_id=$1`, r.PathValue("id")).Scan(&total); err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,event,status,attempts,response_status,response_body,last_error,created_at,delivered_at
		FROM webhook_deliveries WHERE webhook_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, r.PathValue("id"), perPage, (page-1)*perPage)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, event, status string
		var attempts int
		var responseStatus *int
		var responseBody, lastError *string
		var created time.Time
		var delivered *time.Time
		if err = rows.Scan(&id, &event, &status, &attempts, &responseStatus, &responseBody, &lastError, &created, &delivered); err != nil {
			serverError(w, err)
			return
		}
		items = append(items, map[string]any{
			"id": id, "event": event, "status": status, "attempts": attempts,
			"response_status": responseStatus, "response_body": responseBody, "last_error": lastError,
			"created_at": created, "delivered_at": delivered,
		})
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	respond(w, 200, map[string]any{"items": items})
}

func (a *App) replayWebhookDelivery(w http.ResponseWriter, r *http.Request, scope *webhookScope, u *User) {
	var webhookID, event string
	var payload []byte
	err := a.db.QueryRow(r.Context(), `SELECT d.webhook_id,d.event,d.payload FROM webhook_deliveries d JOIN webhooks h ON h.id=d.webhook_id
		WHERE d.id=$1 AND h.`+scope.column+`=$2`, r.PathValue("deliveryId"), scope.id).Scan(&webhookID, &event, &payload)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "Delivery not found.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	id := auth.ID()
	var created time.Time
	if err = a.db.QueryRow(r.Context(), `INSERT INTO webhook_deliveries(id,webhook_id,event,payload) VALUES($1,$2,$3,$4) RETURNING created_at`, id, webhookID, event, payload).Scan(&created); err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'webhook.replayed',$2)`, u.ID, r.PathValue("deliveryId"))
	respond(w, 201, map[string]any{"id": id, "status": "pending", "created_at": created})
}
