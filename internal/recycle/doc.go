// Package recycle executes the planned move, restore, and purge steps for
// retained links and adopted real files.
//
// Filesystem operations are containment-checked and fingerprint-bound; purge
// never runs without the required retention and owner confirmation.
package recycle
