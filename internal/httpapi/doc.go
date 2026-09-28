// Package httpapi exposes the owner-authenticated HTTP API, health endpoints,
// and the authenticated Seerr webhook over the store and domain services.
//
// Handlers stay thin: authenticate, validate, call the store or queue an
// outbox event, then return the JSON envelope. routes() is the full surface.
package httpapi
