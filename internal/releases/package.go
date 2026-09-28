package releases

import (
	"regexp"
	"strings"
)

// PackageKind classifies whether an NZB package can become a playable output.
type PackageKind string

const (
	PackagePlayable    PackageKind = "playable"
	PackageUnsupported PackageKind = "unsupported"
	PackageUnknown     PackageKind = "unknown"
	// PackageStreamableArchive is a RAR or 7-Zip set. The download client
	// analyzes these archives itself (stored and nested RAR, RAR inside 7-Zip)
	// and reports compressed or corrupt ones as queue failures, which are then
	// excluded as release-attributable. Scene releases, including most
	// NORDiC/DANiSH/DKsubs posts, are packed this way, so they must not be
	// rejected before submission.
	PackageStreamableArchive PackageKind = "streamable_archive"
)

// Release-level failure codes shared with the acquisition and persistence
// layers so a defective package is excluded from the workflow and another
// candidate is tried within the same acquisition round.
const (
	PackageFailureArchiveUnsupported = "archive_unsupported"
	PackageFailureNoPlayableMedia    = "no_playable_media"
	PackageFailureReleaseIncomplete  = "release_incomplete"
)

// PackageClassification is the screening result for one NZB manifest.
type PackageClassification struct {
	Kind   PackageKind
	Code   string
	Reason string
}

var (
	packagePlayableFile = regexp.MustCompile(`(?i)\.(mkv|mp4|m4v|avi|ts)(?:["'\s\)\]]|$)`)
	packageStreamable   = regexp.MustCompile(`(?i)\.(rar|r\d{2,3}|7z)(?:["'\s\)\]]|$)|\.part\d+\.rar(?:["'\s\)\]]|$)`)
	packageArchiveFile  = regexp.MustCompile(`(?i)\.(zip|tar|gz|bz2|xz)(?:["'\s\)\]]|$)`)
	packageNonPlayable  = regexp.MustCompile(`(?i)\.(iso|vob|mpg|mpeg|m2ts|mts|mov|wmv|flv|webm|m2v|divx|rmvb|3gp)(?:["'\s\)\]]|$)`)
)

// ClassifyPackageFiles screens NZB file subjects before submission. Archive
// formats the download client cannot open (zip, tar, gzip, bzip2, xz),
// ISO/DVD structures, and other recognized non-playable containers are
// rejected because they cannot become a Plex-playable file. RAR and 7-Zip sets
// are passed to the download client, which analyzes them and reports the ones
// it cannot stream. Obfuscated manifests stay unknown so a release is never
// dropped only because its names cannot be read.
func ClassifyPackageFiles(subjects []string) PackageClassification {
	playable, streamable, archive, nonPlayable := false, false, false, false
	for _, subject := range subjects {
		if packagePlayableFile.MatchString(subject) {
			if !strings.Contains(strings.ToLower(subject), "sample") {
				playable = true
			}
			continue
		}
		if packageStreamable.MatchString(subject) {
			streamable = true
			continue
		}
		if packageArchiveFile.MatchString(subject) {
			archive = true
			continue
		}
		if packageNonPlayable.MatchString(subject) {
			nonPlayable = true
		}
	}
	switch {
	case playable:
		return PackageClassification{Kind: PackagePlayable, Reason: "release declares a playable media file"}
	case streamable:
		return PackageClassification{Kind: PackageStreamableArchive, Reason: "release is a RAR or 7-Zip set; the download client analyzes it and reports archives it cannot stream"}
	case archive:
		return PackageClassification{Kind: PackageUnsupported, Code: PackageFailureArchiveUnsupported, Reason: "release contains an archive set instead of a playable media file"}
	case nonPlayable:
		return PackageClassification{Kind: PackageUnsupported, Code: PackageFailureNoPlayableMedia, Reason: "release contains only non-playable video containers such as ISO or DVD structures"}
	default:
		return PackageClassification{Kind: PackageUnknown, Reason: "release file names are obfuscated or unrecognized"}
	}
}
