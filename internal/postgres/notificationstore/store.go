package notificationstore

import "github.com/jackc/pgx/v5/pgxpool"

// Notifications holds this subject's SQL. postgres.Store embeds it, so callers keep
// using the one store.
type Notifications struct{ Pool *pgxpool.Pool }
