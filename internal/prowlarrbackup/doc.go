// Package prowlarrbackup parses a Prowlarr backup ZIP read-only into disabled
// Newznab candidates while preserving query and grab ceilings.
//
// There is no Prowlarr runtime connection. Owners approve candidates and place
// their keys in secret environment references.
package prowlarrbackup
