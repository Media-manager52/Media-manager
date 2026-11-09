package servarrstore

import "github.com/jackc/pgx/v5/pgxpool"

// ServarrViews holds this subject's SQL. postgres.Store embeds it, so callers keep
// using the one store.
type ServarrViews struct{ Pool *pgxpool.Pool }
