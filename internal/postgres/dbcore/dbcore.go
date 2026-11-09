// Package dbcore holds what every storage subject shares. It imports no other
// storage package, so a subject can use it without depending on the others.
package dbcore

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrConcurrentModification = errors.New("concurrent modification")
	ErrNotFound               = errors.New("record not found")
)

// ActionRefusal is a guard refusal an owner or agent can act on: a stable
// snake_case code, the human message, and the next step. The HTTP layer
// returns it as 409 {"error": {"code", "message", "hint"}}.
type ActionRefusal struct {
	Code    string
	Message string
	Hint    string
}

func (refusal ActionRefusal) Error() string { return refusal.Message }

// Refuse builds an ActionRefusal.
func Refuse(code, message, hint string) error {
	return ActionRefusal{Code: code, Message: message, Hint: hint}
}

// RowScanner is a single pgx row or the current row of a result set.
type RowScanner interface{ Scan(...any) error }

// NullableTime stores the zero time as SQL NULL.
func NullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

// Integration is one configured external service.
type Integration struct {
	ID        uuid.UUID       `json:"id"`
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	Enabled   bool            `json:"enabled"`
	Default   bool            `json:"is_default"`
	Config    json.RawMessage `json:"config"`
	SecretRef string          `json:"secret_ref,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// LoadIntegration reads one integration, or ErrNotFound.
func LoadIntegration(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (Integration, error) {
	var item Integration
	var idText string
	var config []byte
	err := pool.QueryRow(ctx, `SELECT id::text,kind,name,enabled,is_default,config,COALESCE(secret_ref,''),created_at,updated_at FROM integration_instances WHERE id=$1`, id.String()).Scan(&idText, &item.Kind, &item.Name, &item.Enabled, &item.Default, &config, &item.SecretRef, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrNotFound
	}
	if err != nil {
		return Integration{}, err
	}
	item.ID, _ = uuid.Parse(idText)
	item.Config = append(json.RawMessage(nil), config...)
	return item, nil
}
