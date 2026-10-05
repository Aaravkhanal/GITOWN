package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/Aaravkhanal/GITOWN/internal/auth"
	"github.com/jackc/pgx/v5"
)

func queueEmailVerification(ctx context.Context, tx pgx.Tx, userID, email, origin string) error {
	token := auth.Secret("emailverify_")
	if _, err := tx.Exec(ctx, `DELETE FROM email_verification_tokens WHERE user_id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO email_verification_tokens(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '24 hours')`, auth.Digest(token), userID); err != nil {
		return err
	}
	link := strings.TrimRight(origin, "/") + "/verify-email?token=" + token
	body := "Confirm your email address within 24 hours to verify your GITOWN account:\n\n" + link + "\n\nIf you did not create this account, ignore this message."
	return queueAddressMail(ctx, tx, email, "email_verification", "Verify your GITOWN email", body, "")
}

func (a *App) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Token) > 128 || !strings.HasPrefix(in.Token, "emailverify_") {
		fail(w, 422, "invalid_or_expired_token", "This verification link is invalid or expired. Request a new one.")
		return
	}
	if !a.authLimitFor(w, r, in.Token) {
		return
	}
	defer func() { <-a.passwords }()
	var userID string
	err := a.db.QueryRow(r.Context(), `SELECT user_id FROM email_verification_tokens WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>now()`, auth.Digest(in.Token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, 422, "invalid_or_expired_token", "This verification link is invalid or expired. Request a new one.")
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
	var consumedUserID string
	err = tx.QueryRow(r.Context(), `UPDATE email_verification_tokens SET consumed_at=now() WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at>now() RETURNING user_id`, auth.Digest(in.Token)).Scan(&consumedUserID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && consumedUserID != userID) {
		fail(w, 422, "invalid_or_expired_token", "This verification link is invalid or expired. Request a new one.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `UPDATE users SET email_verified_at=COALESCE(email_verified_at,now()) WHERE id=$1`, userID); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,'account.email_verified','self')`, userID); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"verified": true})
}

func (a *App) emailVerificationStatus(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	var verified bool
	if err := a.db.QueryRow(r.Context(), `SELECT email_verified_at IS NOT NULL FROM users WHERE id=$1`, u.ID).Scan(&verified); err != nil {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]bool{"verified": verified})
}

func (a *App) resendEmailVerification(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if !a.authLimitFor(w, r, u.ID) {
		return
	}
	defer func() { <-a.passwords }()
	var email string
	var verified bool
	if err := a.db.QueryRow(r.Context(), `SELECT email,email_verified_at IS NOT NULL FROM users WHERE id=$1`, u.ID).Scan(&email, &verified); err != nil {
		serverError(w, err)
		return
	}
	if !verified {
		if err := a.sendVerificationEmail(r, u.ID, email); err != nil {
			serverError(w, err)
			return
		}
	}
	respond(w, 200, map[string]string{"message": "If your email still needs verification, a link will be sent shortly."})
}

func (a *App) requestEmailVerification(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if len(in.Email) > 254 {
		in.Email = ""
	}
	if !a.authLimitFor(w, r, in.Email) {
		return
	}
	defer func() { <-a.passwords }()
	var userID string
	var verified bool
	err := a.db.QueryRow(r.Context(), `SELECT id,email_verified_at IS NOT NULL FROM users WHERE email=$1`, in.Email).Scan(&userID, &verified)
	if err == nil && !verified {
		var email string
		if err = a.db.QueryRow(r.Context(), `SELECT email FROM users WHERE id=$1`, userID).Scan(&email); err != nil {
			serverError(w, err)
			return
		}
		if err = a.sendVerificationEmail(r, userID, email); err != nil {
			serverError(w, err)
			return
		}
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	respond(w, 200, map[string]string{"message": "If the account needs verification, a link will be sent shortly."})
}

func (a *App) sendVerificationEmail(r *http.Request, userID, email string) error {
	var recent bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM email_verification_tokens WHERE user_id=$1 AND created_at>now()-interval '1 minute')`, userID).Scan(&recent); err != nil {
		return err
	}
	if recent {
		return nil
	}
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if err = queueEmailVerification(r.Context(), tx, userID, email, a.cfg.Origin); err != nil {
		return err
	}
	if err = tx.Commit(r.Context()); err != nil {
		return err
	}
	return nil
}

func (a *App) emailVerificationRequired() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("GITOWN_REQUIRE_VERIFIED_EMAIL")), "1") || strings.EqualFold(strings.TrimSpace(os.Getenv("GITOWN_REQUIRE_VERIFIED_EMAIL")), "true")
}
