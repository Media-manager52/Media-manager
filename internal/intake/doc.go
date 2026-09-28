// Package intake reconciles Seerr webhook and polling deliveries into catalog
// items, workflows, and verified availability feedback.
//
// Deliveries are deduplicated, identity mismatches fail closed, and Seerr is
// told a title is available only after exact Plex confirmation.
package intake
