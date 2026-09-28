// Package releases normalizes Newznab observations, infers release-title
// facts, evaluates them against policy rules, and ranks eligible candidates.
//
// Evaluation is pure: it performs no network, database, or filesystem access.
package releases
