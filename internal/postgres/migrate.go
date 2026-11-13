package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/postgres/migrations"
)

// CurrentSchemaVersion is the schema this build expects.
const CurrentSchemaVersion = migrations.CurrentSchemaVersion

// Migrate brings the database up to CurrentSchemaVersion.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error { return migrations.Migrate(ctx, pool) }

// SchemaVersion reads the version the database is at.
func SchemaVersion(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	return migrations.SchemaVersion(ctx, pool)
}
