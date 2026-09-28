// Package newznab implements the Newznab indexing protocol with capability
// profiles, strict XML parsing, scheduler-backed request reservations, and
// pinned redirect/download handling.
//
// Provider values are sanitized before persistence and credentials are never
// logged or stored in observations.
package newznab
