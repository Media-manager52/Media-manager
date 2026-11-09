// Package dbcore holds what every storage subject shares. It imports no other
// storage package, so a subject can use it without depending on the others.
package dbcore

import (
	"errors"
	"time"
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
