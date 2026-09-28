// Package platform implements the durable outbox worker engine.
//
// It claims events from PostgreSQL with leases, renews leases via heartbeats
// while handlers run, applies bounded retries or paced deferrals, and persists
// only safe error codes.
package platform
