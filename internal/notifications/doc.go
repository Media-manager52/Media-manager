// Package notifications delivers owner-configured webhook notifications for
// selected audit events with durable retries.
//
// Payloads exclude audit detail and secrets; credentials are resolved from
// environment references at delivery time.
package notifications
