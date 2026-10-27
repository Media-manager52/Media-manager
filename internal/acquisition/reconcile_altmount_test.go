package acquisition

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/altmount"
	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/packs"
	"github.com/media-manager/media-manager/internal/subtitles"
)

func TestMoviePrimaryExcludesExplicitBonusDirectories(t *testing.T) {
	main := packs.ObservedAsset{ID: uuid.NewV7(), RelativePath: "Film.2026/Film.2026.mkv", Size: 1000}
	for _, path := range []string{"Film.2026/extra/Other.Film.1990.mkv", "Film.2026/EXTRAS/Interview.mkv", "Film.2026/featurettes/Interview.mkv"} {
		got := selectedMovieAssets([]packs.ObservedAsset{main, {ID: uuid.NewV7(), RelativePath: path, Size: 900}})
		if len(got) != 1 || got[0].ID != main.ID {
			t.Fatalf("bonus %s changed selected main: %+v", path, got)
		}
	}
	for _, path := range []string{"Film.2026/Other.Film.2026.mkv", "Film.2026/extraordinary/Feature.mkv", "Film.2026/Bonus.2026.mkv"} {
		if got := selectedMovieAssets([]packs.ObservedAsset{main, {ID: uuid.NewV7(), RelativePath: path, Size: 900}}); len(got) != 0 {
			t.Fatalf("ambiguous feature %s selected: %+v", path, got)
		}
	}
	if got := selectedMovieAssets([]packs.ObservedAsset{{ID: uuid.NewV7(), RelativePath: "extras/Film.mkv", Size: 900}}); len(got) != 0 {
		t.Fatalf("extras-only inventory selected: %+v", got)
	}
}

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
	for _, probeErr := range []error{mediainfo.ErrNoVideoTrack, mediainfo.ErrEncryptedVideo, mediainfo.ErrZeroFilledOutput, errors.New("media-info has no video track"), errors.New("media-info video has an unsupported encrypted codec tag"), errors.New("media file starts with zero-filled bytes; the release is missing its opening articles"), context.DeadlineExceeded} {
		verification, err := inspectSubtitleRequirements(t.Context(), root, []subtitles.Asset{asset}, []subtitles.Asset{asset}, subtitles.Policy{Required: []string{"da"}}, failingSubtitleProbe{err: probeErr}, nil)
		if err == nil || verification.Status != "" || errors.Is(err, mediainfo.ErrNoVideoTrack) != errors.Is(probeErr, mediainfo.ErrNoVideoTrack) || errors.Is(err, mediainfo.ErrEncryptedVideo) != errors.Is(probeErr, mediainfo.ErrEncryptedVideo) || errors.Is(err, mediainfo.ErrZeroFilledOutput) != errors.Is(probeErr, mediainfo.ErrZeroFilledOutput) {
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
		{"compressed media files are not supported: Krummerne.Saa.er.det.Jul.igen.2006.Danish.1080p.Web-dl.DD5.1.x264-NDF.mkv (uses rar5.0 compression)", "archive_unsupported"},
		{"compressed media files are not supported: permission denied", ""},
		{`failed to iterate RAR archive "Movie.part01.rar": rardecode: bad header crc`, "archive_unsupported"},
		{"no files with allowed extensions found (allowed: [.mp4 .mkv ...])", "no_playable_media"},
		{"The file is incomplete or missing parts. Some segments of this file could not be found on any provider", "release_incomplete"},
		{"NZB file contains only PAR2 files. This indicates that there are missing segments in your providers.", "release_incomplete"},
		{"no valid first RAR part found in archive", "release_incomplete"},
		{"NZB validation failed: invalid NZB: total size is zero", "release_incomplete"},
		{"fast-fail segment check failed: no regular files were successfully processed (all files failed validation)", "release_incomplete"},
		{"no regular files were successfully processed (all files failed validation)", ""},
		{"no files were successfully processed (all files failed validation)", "release_incomplete"},
		{`RAR archive "Bonderoeven.S13.DANiSH.PDTV.x264-SCENE.part01.rar" is truncated: followed 99999882 of 4447278073 volume bytes across 1 of 45 volumes`, "release_incomplete"},
		{`RAR file "Bonderoeven.S02E01.DVDrip.SLaursen.mkv" is incomplete: segments cover only 199999888 of 980818266 bytes`, "release_incomplete"},
		{`RAR archive "x.part01.rar" is truncated: followed 1 of 9 volume bytes: context deadline exceeded`, ""},
		{"file 'luksusfaelden.s08e03.danish.pdtv.xvid-preben.sfv' does not match allowed extensions (allowed: [.mp4 .mkv])", "no_playable_media"},
		{"file 'frank.og.kastaniegaarden.s06e07.danish.1080p.web.h264-dontdodrugs.nfo' does not match allowed extensions (allowed: [.mp4 .mkv])", "no_playable_media"},
		{"failed to ensure persistent NZB: failed to move NZB to persistent storage: failed to stat source file: stat /tmp/altmount-uploads/x.nzb: no such file or directory", ""},
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

// A dead shared mount makes every completed job's output look missing. The
// reconcile must defer the event (retryable, with a stable safe code) instead
// of recording altmount_output_unavailable or an inspection failure.
func TestMountOutageDefersInsteadOfFailing(t *testing.T) {
	dead := &AltMountReconcileService{CheckMount: func(string) error {
		return fmt.Errorf("%w: socket not connected", altmount.ErrMountUnavailable)
	}}
	err := dead.mountOutage("/mnt/altmount")
	if !errors.Is(err, altmount.ErrMountUnavailable) {
		t.Fatalf("mountOutage = %v, want ErrMountUnavailable", err)
	}
	var deferred interface {
		SafeCode() string
		RetryAt() time.Time
	}
	if !errors.As(err, &deferred) || deferred.SafeCode() != "altmount_mount_unavailable" || !deferred.RetryAt().After(time.Now()) {
		t.Fatalf("mountOutage = %#v, want a future-dated altmount_mount_unavailable deferral", err)
	}

	healthy := &AltMountReconcileService{CheckMount: func(string) error { return nil }}
	if err := healthy.mountOutage("/mnt/altmount"); err != nil {
		t.Fatalf("healthy mount: mountOutage = %v, want nil", err)
	}

	// A file that does not answer reads (a blocked FUSE read) defers too, with
	// its own code; any other inspection failure is not deferred.
	unresponsive := healthy.inspectionDeferral("/mnt/altmount", fmt.Errorf("probe: %w", mediainfo.ErrSourceUnresponsive))
	var unresponsiveDeferralValue interface {
		SafeCode() string
		RetryAt() time.Time
		PreserveDeferralAttempts() bool
		UnresponsiveTimeout() bool
	}
	if !errors.As(unresponsive, &unresponsiveDeferralValue) || unresponsiveDeferralValue.SafeCode() != "altmount_output_unresponsive" || unresponsiveDeferralValue.RetryAt().Before(time.Now().Add(5*time.Minute)) || !unresponsiveDeferralValue.PreserveDeferralAttempts() || !unresponsiveDeferralValue.UnresponsiveTimeout() {
		t.Fatalf("inspectionDeferral = %#v, want a preserved altmount_output_unresponsive deferral", unresponsive)
	}
	if err := healthy.inspectionDeferral("/mnt/altmount", mediainfo.ErrNoVideoTrack); err != nil {
		t.Fatalf("a real inspection failure must not be deferred: %v", err)
	}

	// The default check treats a bare, empty mount point as unavailable.
	if err := (&AltMountReconcileService{}).mountOutage(t.TempDir()); !errors.Is(err, altmount.ErrMountUnavailable) {
		t.Fatalf("empty root: mountOutage = %v, want ErrMountUnavailable", err)
	}
}
