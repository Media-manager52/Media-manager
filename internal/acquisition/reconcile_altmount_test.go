package acquisition

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/subtitles"
)

type failingSubtitleProbe struct{}

func (failingSubtitleProbe) InspectWithin(context.Context, string, string) (mediainfo.Evidence, error) {
	return mediainfo.Evidence{}, errors.New("bounded probe failed")
}

func TestInspectSubtitleRequirementsKeepsFailureDurable(t *testing.T) {
	root := t.TempDir()
	path := "Movie.mkv"
	if err := os.WriteFile(filepath.Join(root, path), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	asset := subtitles.Asset{ID: uuid.NewV7(), RelativePath: path, Size: int64(len("fixture"))}
	policy := subtitles.Policy{Required: []string{"da"}}

	verification := inspectSubtitleRequirements(t.Context(), root, []subtitles.Asset{asset}, []subtitles.Asset{asset}, policy, failingSubtitleProbe{}, nil)

	if verification.Status != "unverified" || len(verification.RequiredLanguages) != 1 || verification.RequiredLanguages[0] != "da" || len(verification.FailureReasons) != 1 || verification.FailureReasons[0] != "bounded media-info inspection failed for Movie.mkv: bounded probe failed" {
		t.Fatalf("verification=%+v", verification)
	}
}
