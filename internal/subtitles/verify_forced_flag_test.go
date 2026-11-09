package subtitles

import (
	"context"
	"errors"
	"testing"

	"github.com/media-manager/media-manager/internal/mediainfo"
)

type cueProbe struct {
	fixtureProbe
	cues  map[string][]int
	err   error
	calls *[]string
}

func (probe cueProbe) CountSubtitleCues(_ context.Context, _ string, relative string, _ int, _ []float64, _ float64) ([]int, error) {
	*probe.calls = append(*probe.calls, relative)
	return probe.cues[relative], probe.err
}

func flaggedEvidence(track mediainfo.Track, duration float64) mediainfo.Evidence {
	evidence := mediaEvidence(track)
	evidence.DurationSeconds = &duration
	return evidence
}

func TestVerifyChecksTheContentOfATrackOnlyFlaggedForced(t *testing.T) {
	t.Parallel()
	flag := true
	flagged := mediainfo.Track{Index: 2, Codec: "subrip", CanonicalLanguage: "da", Forced: &flag}
	bitmap := mediainfo.Track{Index: 2, Codec: "hdmv_pgs_subtitle", CanonicalLanguage: "da", Forced: &flag}
	named := mediainfo.Track{Index: 2, Codec: "subrip", CanonicalLanguage: "da", Forced: &flag, Title: "Forced"}
	for _, test := range []struct {
		name     string
		track    mediainfo.Track
		duration float64
		cues     []int
		want     string
		calls    int
	}{
		{name: "complete track with a wrong flag", track: flagged, duration: 2587, cues: []int{24, 10, 8}, want: "verified", calls: 1},
		{name: "complete track with one quiet scene", track: flagged, duration: 3347, cues: []int{1, 9, 13}, want: "verified", calls: 1},
		{name: "real forced track", track: flagged, duration: 1461, cues: []int{0, 0, 0}, want: "unverified", calls: 1},
		{name: "one busy foreign scene is not dialogue throughout", track: flagged, duration: 5693, cues: []int{0, 20, 0}, want: "unverified", calls: 1},
		{name: "bitmap packets count twice", track: bitmap, duration: 5693, cues: []int{4, 5, 4}, want: "unverified", calls: 1},
		{name: "complete bitmap track", track: bitmap, duration: 5693, cues: []int{12, 14, 9}, want: "verified", calls: 1},
		{name: "track named forced is never sampled", track: named, duration: 5693, cues: []int{30, 30, 30}, want: "unverified", calls: 0},
		{name: "too short to judge", track: flagged, duration: 120, cues: []int{30, 30, 30}, want: "unverified", calls: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			video := fixtureAsset(t, root, "Movie.mkv", "video")
			calls := []string{}
			probe := cueProbe{fixtureProbe: fixtureProbe{evidence: map[string]mediainfo.Evidence{video.RelativePath: flaggedEvidence(test.track, test.duration)}}, cues: map[string][]int{video.RelativePath: test.cues}, calls: &calls}
			result, err := Verify(context.Background(), root, []Asset{video}, []Asset{video}, Policy{Required: []string{"da"}}, probe)
			if err != nil || result.Status != test.want {
				t.Fatalf("status=%s, want=%s, error=%v, reasons=%v", result.Status, test.want, err, result.FailureReasons)
			}
			if len(calls) != test.calls {
				t.Fatalf("sampled %d times, want %d", len(calls), test.calls)
			}
			language := result.Videos[0].Languages[0]
			if language.Forced == nil || !*language.Forced {
				t.Fatalf("the forced flag must stay in the evidence: %+v", language)
			}
			if test.calls == 1 && (language.FullDialogue == nil || (*language.FullDialogue != (test.want == "verified")) || len(language.DialogueCues) != len(test.cues)) {
				t.Fatalf("the sample must be recorded: %+v", language)
			}
		})
	}
}

func TestVerifyDoesNotSampleWhenOtherEvidenceAlreadyProvesDanish(t *testing.T) {
	t.Parallel()
	flag := true
	root := t.TempDir()
	video := fixtureAsset(t, root, "Movie.mkv", "video")
	duration := 5400.0
	evidence := mediaEvidence(mediainfo.Track{Index: 2, CanonicalLanguage: "da", Forced: &flag}, mediainfo.Track{Index: 3, CanonicalLanguage: "da"})
	evidence.DurationSeconds = &duration
	calls := []string{}
	probe := cueProbe{fixtureProbe: fixtureProbe{evidence: map[string]mediainfo.Evidence{video.RelativePath: evidence}}, calls: &calls}
	result, err := Verify(context.Background(), root, []Asset{video}, []Asset{video}, Policy{Required: []string{"da"}}, probe)
	if err != nil || result.Status != "verified" || len(calls) != 0 {
		t.Fatalf("status=%s error=%v calls=%v", result.Status, err, calls)
	}
}

func TestVerifySamplesTheFirstFilesOfAPackAndAppliesTheResult(t *testing.T) {
	t.Parallel()
	flag := true
	for _, test := range []struct {
		name string
		cues [][]int
		want string
	}{
		{name: "complete", cues: [][]int{{20, 20, 20}, {15, 15, 15}}, want: "verified"},
		{name: "second file is really forced", cues: [][]int{{20, 20, 20}, {0, 0, 0}}, want: "unverified"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			videos := []Asset{}
			evidence := map[string]mediainfo.Evidence{}
			for _, name := range []string{"Show.S01E01.mkv", "Show.S01E02.mkv", "Show.S01E03.mkv", "Show.S01E04.mkv"} {
				video := fixtureAsset(t, root, name, "video")
				videos = append(videos, video)
				evidence[name] = flaggedEvidence(mediainfo.Track{Index: 2, Codec: "subrip", CanonicalLanguage: "da", Forced: &flag}, 2500)
			}
			calls := []string{}
			probe := cueProbe{fixtureProbe: fixtureProbe{evidence: evidence}, cues: map[string][]int{videos[0].RelativePath: test.cues[0], videos[1].RelativePath: test.cues[1]}, calls: &calls}
			result, err := Verify(context.Background(), root, videos, videos, Policy{Required: []string{"da"}}, probe)
			if err != nil || result.Status != test.want {
				t.Fatalf("status=%s, want=%s, error=%v, reasons=%v", result.Status, test.want, err, result.FailureReasons)
			}
			if len(calls) != maxDialogueSamples {
				t.Fatalf("a pack must stay inside the verification limit, sampled %v", calls)
			}
			last := result.Videos[3].Languages[0]
			if last.FullDialogue == nil || (*last.FullDialogue != (test.want == "verified")) || last.DialogueCues != nil {
				t.Fatalf("a later file must record the inherited result: %+v", last)
			}
		})
	}
}

func TestVerifyDefersWhenASampleCannotBeRead(t *testing.T) {
	t.Parallel()
	flag := true
	root := t.TempDir()
	video := fixtureAsset(t, root, "Movie.mkv", "video")
	calls := []string{}
	probe := cueProbe{fixtureProbe: fixtureProbe{evidence: map[string]mediainfo.Evidence{video.RelativePath: flaggedEvidence(mediainfo.Track{Index: 2, CanonicalLanguage: "da", Forced: &flag}, 5400)}}, err: mediainfo.ErrSourceUnresponsive, calls: &calls}
	if _, err := Verify(context.Background(), root, []Asset{video}, []Asset{video}, Policy{Required: []string{"da"}}, probe); !errors.Is(err, mediainfo.ErrSourceUnresponsive) {
		t.Fatalf("a slow read is no evidence about the release, error=%v", err)
	}
}
