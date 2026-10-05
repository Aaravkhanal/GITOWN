package app

import (
	"context"
	"log/slog"
	"time"
)

// Maintenance tuning lives here so the manual trigger and the periodic
// sweep agree on one set of limits.
const (
	maintenanceWorkerInterval = time.Hour
	maintenanceStaleAfter     = 14 * 24 * time.Hour
	maintenanceSweepBatch     = 3
)

// runMaintenance repacks one repository's Git storage and records when it
// last ran, so both the manual "Run maintenance now" action and the
// scheduled sweep leave the same trail.
func (a *App) runMaintenance(ctx context.Context, repo *Repository) error {
	select {
	case a.transports <- struct{}{}:
		defer func() { <-a.transports }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := a.git.Maintain(ctx, repo.ID); err != nil {
		return err
	}
	now := time.Now()
	if _, err := a.db.Exec(ctx, `UPDATE repositories SET last_maintained_at=$1 WHERE id=$2`, now, repo.ID); err != nil {
		return err
	}
	repo.LastMaintainedAt = &now
	return a.noteRepositoryFacts(ctx, repo)
}

// startMaintenanceSweep periodically garbage-collects repositories that
// haven't been maintained in a while, a handful at a time so one large
// repository can't monopolize the shared transport concurrency budget that
// live clones and pushes also draw from. It runs until ctx is cancelled.
func (a *App) startMaintenanceSweep(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(maintenanceWorkerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if err := a.sweepMaintenance(ctx); err != nil && ctx.Err() == nil {
				slog.Error("repository maintenance sweep failed", "error", err)
			}
			if err := a.sweepAuthAbuse(ctx); err != nil && ctx.Err() == nil {
				slog.Error("authentication abuse cleanup failed", "error", err)
			}
		}
	}()
}

func (a *App) sweepAuthAbuse(ctx context.Context) error {
	if _, err := a.db.Exec(ctx, `DELETE FROM auth_rate_limit_buckets WHERE updated_at<now()-interval '1 day'`); err != nil {
		return err
	}
	_, err := a.db.Exec(ctx, `DELETE FROM auth_abuse_events WHERE created_at<now()-interval '7 days'`)
	return err
}

func (a *App) sweepMaintenance(ctx context.Context) error {
	rows, err := a.db.Query(ctx, `SELECT `+repoColumns+` FROM repositories r JOIN users u ON u.id=r.owner_id
		WHERE r.deleted_at IS NULL AND r.archived_at IS NULL
		AND (r.last_maintained_at IS NULL OR r.last_maintained_at < $1)
		ORDER BY r.last_maintained_at ASC NULLS FIRST LIMIT $2`, time.Now().Add(-maintenanceStaleAfter), maintenanceSweepBatch)
	if err != nil {
		return err
	}
	var due []Repository
	for rows.Next() {
		repo, scanErr := scanRepo(rows)
		if scanErr != nil {
			rows.Close()
			return scanErr
		}
		due = append(due, repo)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for i := range due {
		if ctx.Err() != nil {
			return nil
		}
		if err = a.runMaintenance(ctx, &due[i]); err != nil {
			slog.Error("repository maintenance failed", "repository", due[i].Owner+"/"+due[i].Name, "error", err)
		}
	}
	return nil
}
