package importliststore

import "github.com/jackc/pgx/v5/pgxpool"

// ImportLists holds this subject's SQL. postgres.Store embeds it, so callers keep
// using the one store.
type ImportLists struct{ Pool *pgxpool.Pool }
