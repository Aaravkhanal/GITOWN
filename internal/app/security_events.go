package app

import (
	"log/slog"
	"net/http"
)

// notifySecurityChange records and queues a notice after a security-sensitive
// credential change has committed. Notification failure is logged rather
// than turning a completed credential mutation into an apparent API failure.
func (a *App) notifySecurityChange(r *http.Request, userID, action, subject, body string) {
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		slog.Error("security notice transaction failed", "action", action, "error", err)
		return
	}
	defer tx.Rollback(r.Context())
	if err = a.queueSecurityNotice(r, tx, userID, subject, body); err != nil {
		slog.Error("security notice queue failed", "action", action, "error", err)
		return
	}
	if _, err = tx.Exec(r.Context(), `INSERT INTO audit_events(actor_id,action,target) VALUES($1,$2,'self')`, userID, action); err != nil {
		slog.Error("security notice audit failed", "action", action, "error", err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		slog.Error("security notice commit failed", "action", action, "error", err)
	}
}
