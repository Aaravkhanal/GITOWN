package app

import (
	"context"
	"time"
)

const (
	mailWorkerClaimLease    = 10 * time.Minute
	webhookWorkerClaimLease = 3 * time.Minute
)

// recoverStaleMailClaims requeues claims left behind by a terminated worker.
// Since a crash can happen after the SMTP server accepted a message but
// before GITOWN recorded success, this is intentionally at-least-once.
func (a *App) recoverStaleMailClaims(ctx context.Context) error {
	tag, err := a.db.Exec(ctx, `UPDATE email_messages
		SET status=CASE WHEN attempts+1 >= $1 THEN 'failed' ELSE 'pending' END,
		    attempts=attempts+1, claimed_at=NULL,
		    next_attempt_at=now()+interval '1 minute',
		    last_error='worker lease expired; delivery outcome is unknown',
		    body=CASE WHEN attempts+1 >= $1 AND kind IN ('password_reset','email_verification') THEN '' ELSE body END
		WHERE status='sending' AND claimed_at < now()-make_interval(secs => $2)`, mailMaxAttempts, mailWorkerClaimLease.Seconds())
	if err != nil {
		return err
	}
	a.workers.recoveredLeases.Add(uint64(tag.RowsAffected()))
	return nil
}

func (a *App) recoverStaleWebhookClaims(ctx context.Context) error {
	tag, err := a.db.Exec(ctx, `UPDATE webhook_deliveries
		SET status=CASE WHEN attempts+1 >= $1 THEN 'failed' ELSE 'pending' END,
		    attempts=attempts+1, claimed_at=NULL,
		    next_attempt_at=now()+interval '1 minute',
		    last_error='worker lease expired; receiver outcome is unknown'
		WHERE status='sending' AND claimed_at < now()-make_interval(secs => $2)`, webhookMaxAttempts, webhookWorkerClaimLease.Seconds())
	if err != nil {
		return err
	}
	a.workers.recoveredLeases.Add(uint64(tag.RowsAffected()))
	return nil
}
