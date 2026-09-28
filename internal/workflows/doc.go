// Package workflows defines the durable workflow state machine and acquisition
// scope variants (default, season:NN, season:NN:episode:NN).
//
// The allowed transition table is the single source of legal state edges.
// Callers persist transitions in the same transaction as the state change and
// never guess at an unknown variant.
package workflows
