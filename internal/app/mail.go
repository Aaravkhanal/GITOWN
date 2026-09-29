package app

import (
	"context"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"time"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

func queueNotificationMail(ctx context.Context, tx pgx.Tx, userIDs []string, kind, dedupe string) error {
	var key any
	if dedupe != "" {
		key = kind + ":" + dedupe
	}
	for _, id := range userIDs {
		if err := queueUserMail(ctx, tx, id, kind, "GITOWN "+strings.ReplaceAll(kind, "_", " "), "Open your GITOWN inbox to read this update.", key); err != nil {
			return err
		}
	}
	return nil
}

func queueUserMail(ctx context.Context, tx pgx.Tx, userID, kind, subject, body string, dedupe any) error {
	token := auth.Secret("unsub_")
	message := body + "\n\nUnsubscribe by opening GITOWN with this token: " + token
	var email string
	var preference string
	err := tx.QueryRow(ctx, `SELECT email,email_notifications FROM users WHERE id=$1`, userID).Scan(&email, &preference)
	if err != nil {
		return err
	}
	if preference == "off" {
		return nil
	}
	digest := preference == "digest"
	var sent any
	if !digest && os.Getenv("GITOWN_SMTP_ADDR") == "" {
		sent = time.Now()
	}
	_, err = tx.Exec(ctx, `INSERT INTO email_messages(id,user_id,recipient_email,kind,subject,body,unsubscribe_token,digest,dedupe_key,sent_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (user_id, dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`,
		auth.ID(), userID, email, kind, subject, message, auth.Digest(token), digest, dedupe, sent)
	if err != nil || digest {
		if err == nil {
			return flushDigest(ctx, tx, userID)
		}
		return err
	}
	if os.Getenv("GITOWN_SMTP_ADDR") != "" {
		if sendErr := sendSMTP(email, subject, message); sendErr == nil {
			_, err = tx.Exec(ctx, `UPDATE email_messages SET sent_at=now() WHERE user_id=$1 AND dedupe_key=$2 AND sent_at IS NULL`, userID, dedupe)
		}
	}
	return err
}

func flushDigest(ctx context.Context, tx pgx.Tx, userID string) error {
	var count int
	var stale bool
	if err := tx.QueryRow(ctx, `SELECT count(*)::int, COALESCE(bool_or(created_at < now() - interval '15 minutes'), false) FROM email_messages WHERE user_id=$1 AND digest AND sent_at IS NULL`, userID).Scan(&count, &stale); err != nil {
		return err
	}
	if count < 5 && !stale {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE email_messages SET sent_at=now() WHERE user_id=$1 AND digest AND sent_at IS NULL`, userID)
	return err
}

func sendSMTP(to, subject, body string) error {
	addr := os.Getenv("GITOWN_SMTP_ADDR")
	from := os.Getenv("GITOWN_SMTP_FROM")
	if addr == "" || from == "" {
		return nil
	}
	var authClient smtp.Auth
	if user := os.Getenv("GITOWN_SMTP_USER"); user != "" {
		host := addr
		if parts := strings.Split(addr, ":"); len(parts) > 0 {
			host = parts[0]
		}
		authClient = smtp.PlainAuth("", user, os.Getenv("GITOWN_SMTP_PASSWORD"), host)
	}
	message := "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + subject + "\r\n\r\n" + body
	return smtp.SendMail(addr, authClient, from, []string{to}, []byte(message))
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
	respond(w, 200, map[string]string{"email_notifications": mode})
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
	respond(w, 200, map[string]string{"email_notifications": in.EmailNotifications})
}

func (a *App) unsubscribeEmail(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		fail(w, 422, "validation_failed", "An unsubscribe token is required.")
		return
	}
	var userID string
	err := a.db.QueryRow(r.Context(), `SELECT user_id FROM email_messages WHERE unsubscribe_token=$1`, auth.Digest(token)).Scan(&userID)
	if err != nil {
		fail(w, 404, "not_found", "Unsubscribe link was not found.")
		return
	}
	if _, err = a.db.Exec(r.Context(), `UPDATE users SET email_notifications='off' WHERE id=$1`, userID); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"email_notifications": "off"})
}
