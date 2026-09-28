// Package library performs containment-checked filesystem operations and
// renders Plex-safe media names.
//
// It is the only package allowed to create, move, or remove symlinks and
// regular files in configured roots. Every operation validates relative paths,
// expected link targets, and file fingerprints.
package library
