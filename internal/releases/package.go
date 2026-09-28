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
	packageArchiveFile  = regexp.MustCompile(`(?i)\.(rar|r\d{2,3}|7z|zip|tar|gz|bz2|xz)(?:["'\s\)\]]|$)|\.part\d+\.rar(?:["'\s\)\]]|$)`)
	packageNonPlayable  = regexp.MustCompile(`(?i)\.(iso|vob|mpg|mpeg|m2ts|mts|mov|wmv|flv|webm|m2v|divx|rmvb|3gp)(?:["'\s\)\]]|$)`)
)

// ClassifyPackageFiles screens NZB file subjects before submission. Archive
// sets, ISO/DVD structures, and other recognized non-playable containers are
// rejected because the download client cannot turn them into a Plex-playable
// file. Obfuscated manifests stay unknown so a release is never dropped only
// because its names cannot be read.
func ClassifyPackageFiles(subjects []string) PackageClassification {
	playable, archive, nonPlayable := false, false, false
	for _, subject := range subjects {
		if packagePlayableFile.MatchString(subject) {
			if !strings.Contains(strings.ToLower(subject), "sample") {
				playable = true
			}
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
	case archive:
		return PackageClassification{Kind: PackageUnsupported, Code: PackageFailureArchiveUnsupported, Reason: "release contains an archive set instead of a playable media file"}
	case nonPlayable:
		return PackageClassification{Kind: PackageUnsupported, Code: PackageFailureNoPlayableMedia, Reason: "release contains only non-playable video containers such as ISO or DVD structures"}
	default:
		return PackageClassification{Kind: PackageUnknown, Reason: "release file names are obfuscated or unrecognized"}
	}
}
