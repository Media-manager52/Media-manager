// Package backups creates durable, checksummed PostgreSQL backups encrypted
// with chunked AES-256-GCM and verifies them by restoring into an isolated
// temporary database.
package backups
