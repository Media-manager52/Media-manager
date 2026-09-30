package acquisition

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/subtitles"
)

type failingSubtitleProbe struct{ err error }

func (probe failingSubtitleProbe) InspectWithin(context.Context, string, string) (mediainfo.Evidence, error) {
	if probe.err != nil {
		return mediainfo.Evidence{}, probe.err
	}
	return mediainfo.Evidence{}, errors.New("bounded probe failed")
}

func TestInspectSubtitleRequirementsPreservesDefinitiveNoVideoEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Movie.mkv"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	asset := subtitles.Asset{ID: uuid.NewV7(), RelativePath: "Movie.mkv", Size: 7}
	for _, probeErr := range []error{mediainfo.ErrNoVideoTrack, errors.New("media-info has no video track"), context.DeadlineExceeded} {
		verification, err := inspectSubtitleRequirements(t.Context(), root, []subtitles.Asset{asset}, []subtitles.Asset{asset}, subtitles.Policy{Required: []string{"da"}}, failingSubtitleProbe{err: probeErr}, nil)
		if err == nil || verification.Status != "" || errors.Is(err, mediainfo.ErrNoVideoTrack) != errors.Is(probeErr, mediainfo.ErrNoVideoTrack) {
			t.Fatalf("probe error=%v verification=%+v returned error=%v", probeErr, verification, err)
		}
	}
}

func TestInspectSubtitleRequirementsClassifiesProbeFailureAsInfrastructure(t *testing.T) {
	root := t.TempDir()
	path := "Movie.mkv"
	if err := os.WriteFile(filepath.Join(root, path), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	asset := subtitles.Asset{ID: uuid.NewV7(), RelativePath: path, Size: int64(len("fixture"))}
	policy := subtitles.Policy{Required: []string{"da"}}

	verification, err := inspectSubtitleRequirements(t.Context(), root, []subtitles.Asset{asset}, []subtitles.Asset{asset}, policy, failingSubtitleProbe{}, nil)

	if err == nil || verification.Status != "" || !strings.Contains(err.Error(), "bounded probe failed") {
		t.Fatalf("verification=%+v error=%v", verification, err)
	}
}

func TestReleaseAttributableQueueFailure(t *testing.T) {
	tests := []struct {
		detail string
		code   string
	}{
		{"compressed files are not supported: Under.The.Skin.iso (uses rar2.9 compression)", "archive_unsupported"},
		{`failed to iterate RAR archive "Movie.part01.rar": rardecode: bad header crc`, "archive_unsupported"},
		{"no files with allowed extensions found (allowed: [.mp4 .mkv ...])", "no_playable_media"},
		{"The file is incomplete or missing parts. Some segments of this file could not be found on any provider", "release_incomplete"},
		{"NZB file contains only PAR2 files. This indicates that there are missing segments in your providers.", "release_incomplete"},
		{"no valid first RAR part found in archive", "release_incomplete"},
		{"NZB validation failed: invalid NZB: total size is zero", "release_incomplete"},
		{"fast-fail segment check failed: no regular files were successfully processed (all files failed validation)", "release_incomplete"},
		{"no regular files were successfully processed (all files failed validation)", ""},
		{"fast-fail STAT probe inconclusive: context deadline exceeded", ""},
		{"failed to iterate RAR archive: context deadline exceeded", ""},
		{"failed to iterate RAR archive: permission denied", ""},
		{"incomplete or missing parts: connection reset by peer", ""},
		{"NZB file contains only PAR2 files: permission denied", ""},
		{"XML syntax error on line 1: illegal character code U+001F", ""},
		{"AltMount job needs attention", ""},
		{"connection refused by provider gateway", ""},
	}
	for _, test := range tests {
		code, matched := releaseAttributableQueueFailure(test.detail)
		if code != test.code || matched != (test.code != "") {
			t.Fatalf("detail=%q code=%q matched=%v, want code=%q", test.detail, code, matched, test.code)
		}
	}
}
