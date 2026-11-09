package commissioningstore

import "github.com/jackc/pgx/v5/pgxpool"

// Commissioning holds this subject's SQL. postgres.Store embeds it, so callers keep
// using the one store.
type Commissioning struct{ Pool *pgxpool.Pool }
