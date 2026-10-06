package migrations

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.sql
var files embed.FS

// Apply serializes startup migration attempts across application instances.
func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(71269351)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	entries, err := files.ReadDir(".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		var applied bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, entry.Name()).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := files.ReadFile(entry.Name())
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", entry.Name(), err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, entry.Name()); err != nil {
			return err
		}
	}
	// Migration 044 adds this column. For databases upgraded from earlier
	// releases, a blank checksum is adopted once from the embedded migration
	// file; later changes to an applied migration fail startup rather than
	// silently diverging between instances.
	for _, entry := range entries {
		body, err := files.ReadFile(entry.Name())
		if err != nil {
			return err
		}
		digest := sha256.Sum256(body)
		checksum := hex.EncodeToString(digest[:])
		var existing string
		if err = tx.QueryRow(ctx, `SELECT COALESCE(checksum,'') FROM schema_migrations WHERE version=$1`, entry.Name()).Scan(&existing); err != nil {
			return err
		}
		if existing == "" {
			if _, err = tx.Exec(ctx, `UPDATE schema_migrations SET checksum=$1 WHERE version=$2`, checksum, entry.Name()); err != nil {
				return err
			}
		} else if existing != checksum {
			return fmt.Errorf("migration checksum mismatch for %s; restore the immutable migration file or create a new forward migration", entry.Name())
		}
	}
	return tx.Commit(ctx)
}
