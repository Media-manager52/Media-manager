package backupstore

import "github.com/jackc/pgx/v5/pgxpool"

// Backups holds this subject's SQL. postgres.Store embeds it, so callers keep
// using the one store.
type Backups struct{ Pool *pgxpool.Pool }
