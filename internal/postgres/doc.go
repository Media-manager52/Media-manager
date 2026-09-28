// Package postgres owns all SQL persistence: the pgx pool, embedded
// migrations, and typed Store methods for every domain.
//
// Mutations use optimistic revision checks (ErrConcurrentModification) and
// write audit and outbox events in the same transaction as state changes.
package postgres
