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

type failingSubtitleProbe struct{}

func (failingSubtitleProbe) InspectWithin(context.Context, string, string) (mediainfo.Evidence, error) {
	return mediainfo.Evidence{}, errors.New("bounded probe failed")
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
