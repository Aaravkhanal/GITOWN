package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

// Delivery tuning lives here so the outbox, digests, and unsubscribe links
// agree on one set of limits.
const (
	mailWorkerInterval  = 5 * time.Second
	mailBatchSize       = 10
	mailMaxAttempts     = 8
	mailSendTimeout     = 45 * time.Second
	digestFlushCount    = 20
	digestItemLimit     = 200
	unsubscribeLifetime = 90 * 24 * time.Hour
)

var errSMTPDisabled = errors.New("SMTP is not configured")

// outgoingMail is one fully rendered message handed to the mail transport.
type outgoingMail struct {
	To             string
	Subject        string
	Body           string
	MessageID      string
	UnsubscribeURL string
	OneClickURL    string
}

func cleanHeader(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(value)), " ")
	if len(value) > limit {
		value = strings.TrimSpace(value[:limit])
	}
	return value
}

// queueUserMail enqueues mail for an account, honoring its delivery
// preference. It never sends inside the caller's transaction.
func queueUserMail(ctx context.Context, tx pgx.Tx, userID, kind, subject, body string, dedupe any) error {
	return queueUserMailLink(ctx, tx, userID, kind, subject, body, "", dedupe)
}

func queueUserMailLink(ctx context.Context, tx pgx.Tx, userID, kind, subject, body, linkPath string, dedupe any) error {
	var email, preference string
	if err := tx.QueryRow(ctx, `SELECT email,email_notifications FROM users WHERE id=$1`, userID).Scan(&email, &preference); err != nil {
		return err
	}
	if preference == "off" {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO email_messages(id,user_id,recipient_email,kind,subject,body,digest,dedupe_key,link_path)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (user_id, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`,
		auth.ID(), userID, email, kind, cleanHeader(subject, 200), body, preference == "digest", dedupe, linkPath)
	return err
}

// queueAddressMail enqueues transactional mail for an address that may not
// belong to an account yet, such as an email-only repository invitation.
func queueAddressMail(ctx context.Context, tx pgx.Tx, email, kind, subject, body, dedupe string) error {
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return fmt.Errorf("invalid recipient address")
	}
	var key any
	if dedupe != "" {
		key = dedupe
	}
	_, err = tx.Exec(ctx, `INSERT INTO email_messages(id,user_id,recipient_email,kind,subject,body,dedupe_key)
		VALUES($1,NULL,$2,$3,$4,$5,$6)
		ON CONFLICT (recipient_email, dedupe_key) WHERE user_id IS NULL AND dedupe_key IS NOT NULL DO NOTHING`,
		auth.ID(), email, kind, cleanHeader(subject, 200), body, key)
	return err
}

// StartWorkers runs every background job (mail delivery, repository
// maintenance) until ctx is cancelled.
func (a *App) StartWorkers(ctx context.Context) {
	a.startMaintenanceSweep(ctx)
	a.startWebhookWorker(ctx)
	a.startRoutesWorker(ctx)
	go func() {
		ticker := time.NewTicker(mailWorkerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if err := a.bundleDigests(ctx); err != nil && ctx.Err() == nil {
				slog.Error("email digest bundling failed", "error", err)
			}
			if err := a.recoverStaleMailClaims(ctx); err != nil && ctx.Err() == nil {
				slog.Error("outbox lease recovery failed", "error", err)
			}
			for {
				sent, err := a.deliverMail(ctx)
				if err != nil && ctx.Err() == nil {
					slog.Error("email delivery failed", "error", err)
				}
				if err != nil || sent < mailBatchSize {
					break
				}
			}
		}
	}()
}

type queuedMail struct {
	id, kind, to, subject, body, link string
	userID                            *string
	attempts                          int
}

// claimMail marks one batch of due messages "sending" and commits right
// away, so the row lock (and the pooled connection) is held only for the
// claim itself, never across the network calls that follow.
func (a *App) claimMail(ctx context.Context) ([]queuedMail, error) {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,kind,user_id::text,recipient_email,subject,body,link_path,attempts
		FROM email_messages WHERE status='pending' AND NOT digest AND next_attempt_at<=now()
		ORDER BY next_attempt_at,created_at LIMIT $1 FOR UPDATE SKIP LOCKED`, mailBatchSize)
	if err != nil {
		return nil, err
	}
	var batch []queuedMail
	for rows.Next() {
		var item queuedMail
		if err = rows.Scan(&item.id, &item.kind, &item.userID, &item.to, &item.subject, &item.body, &item.link, &item.attempts); err != nil {
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
		if _, err = tx.Exec(ctx, `UPDATE email_messages SET status='sending',claimed_at=now() WHERE id=ANY($1)`, ids); err != nil {
			return nil, err
		}
	}
	return batch, tx.Commit(ctx)
}

// deliverMail claims one batch of due messages, then sends each one with
// its own short-lived connection so a slow or stuck SMTP conversation never
// holds a pooled connection or blocks other claimants.
func (a *App) deliverMail(ctx context.Context) (int, error) {
	batch, err := a.claimMail(ctx)
	if err != nil {
		return 0, err
	}
	for _, item := range batch {
		if a.mailer == nil {
			if _, err = a.db.Exec(ctx, `UPDATE email_messages SET status='suppressed',last_error=$2,claimed_at=NULL,body=CASE WHEN kind IN ('password_reset','email_verification') THEN '' ELSE body END WHERE id=$1`, item.id, errSMTPDisabled.Error()); err != nil {
				return 0, err
			}
			a.workers.mailAttempts.Add(1)
			continue
		}
		message := outgoingMail{To: item.to, Subject: item.subject, Body: item.body, MessageID: item.id}
		if item.link != "" {
			message.Body += "\n\nOpen in GITOWN: " + a.cfg.Origin + item.link
		}
		var tokenHash any
		if item.userID != nil {
			token := auth.Secret("unsub_")
			tokenHash = auth.Digest(token)
			message.UnsubscribeURL = a.cfg.Origin + "/unsubscribe?token=" + url.QueryEscape(token)
			message.OneClickURL = a.cfg.Origin + "/api/v1/email/unsubscribe?token=" + url.QueryEscape(token)
			message.Body += "\n\nStop GITOWN email: " + message.UnsubscribeURL
		}
		sendCtx, cancel := context.WithTimeout(ctx, mailSendTimeout)
		sendErr := a.mailer(sendCtx, message)
		cancel()
		if sendErr == nil {
			_, err = a.db.Exec(ctx, `UPDATE email_messages SET status='sent',sent_at=now(),attempts=attempts+1,last_error='',claimed_at=NULL,body=CASE WHEN kind IN ('password_reset','email_verification') THEN '' ELSE body END,
				unsubscribe_token=$2,unsubscribe_expires_at=CASE WHEN $2::text IS NULL THEN NULL ELSE now()+make_interval(secs => $3) END WHERE id=$1`,
				item.id, tokenHash, unsubscribeLifetime.Seconds())
		} else {
			attempts := item.attempts + 1
			status := "pending"
			if attempts >= mailMaxAttempts {
				status = "failed"
			}
			// Back off 1, 2, 4 ... minutes, capped at six hours.
			delay := time.Minute << min(attempts-1, 9)
			delay = min(delay, 6*time.Hour)
			_, err = a.db.Exec(ctx, `UPDATE email_messages SET status=$2,attempts=$3,last_error=$4,next_attempt_at=now()+make_interval(secs => $5),claimed_at=NULL,body=CASE WHEN kind IN ('password_reset','email_verification') AND $2='failed' THEN '' ELSE body END WHERE id=$1`,
				item.id, status, attempts, cleanHeader(sendErr.Error(), 300), delay.Seconds())
			slog.Warn("email delivery attempt failed", "message_id", item.id, "attempts", attempts, "error", sendErr)
		}
		if err != nil {
			return 0, err
		}
		a.workers.mailAttempts.Add(1)
		if sendErr == nil {
			a.workers.mailSuccess.Add(1)
		} else {
			a.workers.mailFailures.Add(1)
		}
	}
	return len(batch), nil
}

// bundleDigests combines each digest reader's queued updates into one
// message once the oldest item has waited a full interval or enough items
// have piled up.
func (a *App) bundleDigests(ctx context.Context) error {
	interval := a.cfg.DigestInterval
	if interval <= 0 {
		interval = time.Hour
	}
	rows, err := a.db.Query(ctx, `SELECT user_id::text FROM email_messages WHERE status='pending' AND digest AND user_id IS NOT NULL
		GROUP BY user_id HAVING count(*)>=$1 OR min(created_at)<=now()-make_interval(secs => $2) LIMIT 100`, digestFlushCount, interval.Seconds())
	if err != nil {
		return err
	}
	var users []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		users = append(users, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, userID := range users {
		if err = a.bundleDigest(ctx, userID); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) bundleDigest(ctx context.Context, userID string) error {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var email, preference string
	if err = tx.QueryRow(ctx, `SELECT email,email_notifications FROM users WHERE id=$1`, userID).Scan(&email, &preference); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id,subject,link_path FROM email_messages WHERE user_id=$1 AND status='pending' AND digest
		ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED`, userID, digestItemLimit)
	if err != nil {
		return err
	}
	var ids []string
	var body strings.Builder
	for rows.Next() {
		var id, subject, link string
		if err = rows.Scan(&id, &subject, &link); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
		body.WriteString("- " + subject)
		if link != "" {
			body.WriteString("\n  " + a.cfg.Origin + link)
		}
		body.WriteString("\n")
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return tx.Commit(ctx)
	}
	switch preference {
	case "off":
		_, err = tx.Exec(ctx, `UPDATE email_messages SET status='suppressed',last_error='recipient turned email off' WHERE id=ANY($1)`, ids)
	case "immediate":
		_, err = tx.Exec(ctx, `UPDATE email_messages SET digest=false,next_attempt_at=now() WHERE id=ANY($1)`, ids)
	default:
		digestID := auth.ID()
		noun := "updates"
		if len(ids) == 1 {
			noun = "update"
		}
		subject := fmt.Sprintf("GITOWN digest: %d %s", len(ids), noun)
		text := "Here is what happened since your last GITOWN digest:\n\n" + body.String()
		if _, err = tx.Exec(ctx, `INSERT INTO email_messages(id,user_id,recipient_email,kind,subject,body,link_path) VALUES($1,$2,$3,'digest',$4,$5,'/inbox')`,
			digestID, userID, email, subject, text); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE email_messages SET status='bundled',digest_id=$2 WHERE id=ANY($1)`, ids, digestID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// buildMessage renders an RFC 5322 message with a UTF-8 quoted-printable
// body. Header values are stripped of line breaks to prevent injection.
func buildMessage(from string, m outgoingMail, host string, now time.Time) ([]byte, error) {
	var out bytes.Buffer
	header := func(name, value string) {
		out.WriteString(name + ": " + value + "\r\n")
	}
	header("From", cleanHeader(from, 300))
	header("To", cleanHeader(m.To, 300))
	header("Subject", mime.QEncoding.Encode("utf-8", cleanHeader(m.Subject, 200)))
	header("Date", now.UTC().Format(time.RFC1123Z))
	header("Message-ID", "<"+cleanHeader(m.MessageID, 80)+"@"+cleanHeader(host, 200)+">")
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	header("Auto-Submitted", "auto-generated")
	if m.OneClickURL != "" {
		header("List-Unsubscribe", "<"+cleanHeader(m.OneClickURL, 600)+">")
		header("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
	}
	out.WriteString("\r\n")
	writer := quotedprintable.NewWriter(&out)
	if _, err := writer.Write([]byte(m.Body)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// sendSMTP delivers one message with explicit deadlines, upgrading to TLS
// whenever the server offers STARTTLS.
func (a *App) sendSMTP(ctx context.Context, m outgoingMail) error {
	host, _, err := net.SplitHostPort(a.cfg.SMTPAddr)
	if err != nil {
		return err
	}
	origin, _ := url.Parse(a.cfg.Origin)
	messageHost := "gitown.local"
	if origin != nil && origin.Hostname() != "" {
		messageHost = origin.Hostname()
	}
	message, err := buildMessage(a.cfg.SMTPFrom, m, messageHost, time.Now())
	if err != nil {
		return err
	}
	dialer := net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", a.cfg.SMTPAddr)
	if err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(mailSendTimeout)
	}
	_ = conn.SetDeadline(deadline)
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err = client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if a.cfg.SMTPUser != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("SMTP server does not offer authentication")
		}
		if err = client.Auth(smtp.PlainAuth("", a.cfg.SMTPUser, a.cfg.SMTPPassword, host)); err != nil {
			return err
		}
	}
	sender, err := mail.ParseAddress(a.cfg.SMTPFrom)
	if err != nil {
		return err
	}
	if err = client.Mail(sender.Address); err != nil {
		return err
	}
	if err = client.Rcpt(m.To); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = writer.Write(message); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (a *App) emailPreference(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var mode string
	if err := a.db.QueryRow(r.Context(), `SELECT email_notifications FROM users WHERE id=$1`, u.ID).Scan(&mode); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]any{"email_notifications": mode, "smtp_configured": a.mailer != nil})
}

func (a *App) updateEmailPreference(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var in struct {
		EmailNotifications string `json:"email_notifications"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.EmailNotifications != "immediate" && in.EmailNotifications != "digest" && in.EmailNotifications != "off" {
		fail(w, 422, "validation_failed", "Email delivery must be immediate, digest, or off.")
		return
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE users SET email_notifications=$1 WHERE id=$2`, in.EmailNotifications, u.ID); err != nil {
		serverError(w, err)
		return
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'account.email_notifications',$2)`, u.ID, u.Username+":"+in.EmailNotifications)
	respond(w, 200, map[string]any{"email_notifications": in.EmailNotifications, "smtp_configured": a.mailer != nil})
}

func maskEmail(email string) string {
	name, domain, ok := strings.Cut(email, "@")
	if !ok || name == "" {
		return "your address"
	}
	return name[:1] + strings.Repeat("•", min(len(name)-1, 6)) + "@" + domain
}

func (a *App) unsubscribeTarget(r *http.Request, token string) (string, string, string, error) {
	var userID, email, mode string
	err := a.db.QueryRow(r.Context(), `SELECT u.id,u.email,u.email_notifications FROM email_messages m JOIN users u ON u.id=m.user_id
		WHERE m.unsubscribe_token=$1 AND m.unsubscribe_expires_at>now()`, auth.Digest(token)).Scan(&userID, &email, &mode)
	return userID, email, mode, err
}

// unsubscribeEmail only reports what a link would change; GET requests
// never mutate so mail scanners that prefetch links cannot unsubscribe.
func (a *App) unsubscribeEmail(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" || len(token) > 200 {
		fail(w, 422, "validation_failed", "An unsubscribe token is required.")
		return
	}
	_, email, mode, err := a.unsubscribeTarget(r, token)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "This unsubscribe link is invalid or has expired.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"email": maskEmail(email), "email_notifications": mode})
}

// confirmUnsubscribe accepts the web confirmation (JSON) and RFC 8058
// one-click requests from mail clients (form body, token in the URL).
func (a *App) confirmUnsubscribe(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		var in struct {
			Token string `json:"token"`
		}
		if !decode(w, r, &in) {
			return
		}
		token = in.Token
	}
	if token == "" || len(token) > 200 {
		fail(w, 422, "validation_failed", "An unsubscribe token is required.")
		return
	}
	userID, email, _, err := a.unsubscribeTarget(r, token)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 404, "not_found", "This unsubscribe link is invalid or has expired.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `UPDATE users SET email_notifications='off' WHERE id=$1`, userID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE email_messages SET status='suppressed',last_error='recipient unsubscribed' WHERE user_id=$1 AND status='pending'`, userID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'account.email_unsubscribed',$2)`, userID, maskEmail(email)); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"email": maskEmail(email), "email_notifications": "off"})
}
