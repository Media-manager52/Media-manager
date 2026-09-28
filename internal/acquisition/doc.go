// Package acquisition runs the worker steps that search commissioned
// indexers, select a release, retrieve and submit its NZB to AltMount,
// reconcile the job, and verify the completed read-only output.
//
// Search planning and release evaluation are pure helpers; external calls go
// through internal/adapters and state changes through internal/postgres.
package acquisition
