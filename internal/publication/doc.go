// Package publication turns verified output into contained Plex-safe symlinks
// and confirms the result against Plex exactly.
//
// Nothing becomes the current generation until Plex confirms the verified
// identity and every expected path; displaced generations are removed only
// afterwards.
package publication
