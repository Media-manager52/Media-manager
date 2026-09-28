// Package catalog defines media items, external identities, and normalized
// metadata shared across the domain.
//
// The internal media UUID is always the primary key. Provider identities are
// validated per namespace, and metadata keeps provider-separated facts with
// provenance instead of merging them into anonymous values.
package catalog
