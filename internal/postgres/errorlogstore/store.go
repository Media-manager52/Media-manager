package errorlogstore

import "github.com/jackc/pgx/v5/pgxpool"

// ErrorLogs holds this subject's SQL. postgres.Store embeds it, so callers keep
// using the one store.
type ErrorLogs struct{ Pool *pgxpool.Pool }
