// Package importlists parses owner-supplied IMDb CSV exports into review-only
// snapshots with strict identity, year, and duplicate bounds.
//
// Parsing never creates media, starts searches, or applies monitoring.
package importlists
