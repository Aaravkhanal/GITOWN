package app

// TEMPORARY stub for the Phase 3 branch. The Phase 5 mail work provides the
// real queueAddressMail in mail.go; delete this file when merging that work.

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func queueAddressMail(ctx context.Context, tx pgx.Tx, email, kind, subject, body, dedupe string) error {
	return nil
}
