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
