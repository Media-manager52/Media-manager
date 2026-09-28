// Package indexers owns Newznab pacing, rolling query/grab budgets, durable
// reservations, cooldowns, and the shared per-indexer scheduler lane.
//
// Runtime search and commissioning share this scheduler. State transitions
// are pure and persisted through the Store interfaces.
package indexers
