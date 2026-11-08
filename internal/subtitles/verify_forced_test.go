package subtitles

import (
	"context"
	"testing"

	"github.com/media-manager/media-manager/internal/mediainfo"
)

func TestVerifyForcedSubtitlesDoNotSatisfyRequiredLanguage(t *testing.T) {
	t.Parallel()
	forced := true
	for _, test := range []struct {
		name    string
		tracks  []mediainfo.Track
		sidecar string
		audio   string
		want    string
	}{
		{name: "embedded flag", tracks: []mediainfo.Track{{CanonicalLanguage: "da", Forced: &forced}}, want: "unverified"},
		{name: "embedded title", tracks: []mediainfo.Track{{CanonicalLanguage: "da", Title: "Danish FORCED"}}, want: "unverified"},
		{name: "bundled forced", sidecar: "Movie.da.forced.srt", want: "unverified"},
		{name: "other full language is insufficient", tracks: []mediainfo.Track{{CanonicalLanguage: "da", Forced: &forced}, {CanonicalLanguage: "en"}}, want: "unverified"},
		{name: "full and forced embedded", tracks: []mediainfo.Track{{CanonicalLanguage: "da", Forced: &forced}, {CanonicalLanguage: "da"}}, want: "verified"},
		{name: "full bundled with forced embedded", tracks: []mediainfo.Track{{CanonicalLanguage: "da", Forced: &forced}}, sidecar: "Movie.da.srt", want: "verified"},
		{name: "full embedded with forced bundled", tracks: []mediainfo.Track{{CanonicalLanguage: "da"}}, sidecar: "Movie.da.forced.srt", want: "verified"},
		{name: "Danish audio with forced subtitles", tracks: []mediainfo.Track{{CanonicalLanguage: "da", Forced: &forced}}, audio: "da", want: "verified"},
		{name: "SDH is a full subtitle", tracks: []mediainfo.Track{{CanonicalLanguage: "da", Title: "Danish SDH"}}, want: "verified"},
		{name: "bundled SDH is a full subtitle", sidecar: "Movie.da.sdh.srt", want: "verified"},
		{name: "bundled HI after language is a full subtitle", sidecar: "Movie.da.hi.srt", want: "verified"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			video := fixtureAsset(t, root, "Movie.mkv", "video")
			inventory := []Asset{video}
			if test.sidecar != "" {
				inventory = append(inventory, fixtureAsset(t, root, test.sidecar, "subtitle"))
			}
			evidence := mediaEvidence(test.tracks...)
			if test.audio != "" {
				evidence.AudioTracks = []mediainfo.Track{{CanonicalLanguage: test.audio}}
			}
			result, err := Verify(context.Background(), root, []Asset{video}, inventory, Policy{Required: []string{"da"}}, fixtureProbe{evidence: map[string]mediainfo.Evidence{video.RelativePath: evidence}})
			if err != nil || result.Status != test.want {
				t.Fatalf("status=%s, want=%s, error=%v, evidence=%+v", result.Status, test.want, err, result)
			}
			if len(result.Videos[0].Languages) != len(test.tracks)+len(inventory)-1 {
				t.Fatalf("forced tracks must remain visible in evidence: %+v", result)
			}
			if test.want == "unverified" && len(result.Videos[0].MissingRequired) != 1 {
				t.Fatalf("missing required language was not recorded: %+v", result)
			}
		})
	}
}

func TestFilenameHindiLanguageIsNotAnUnqualifiedHearingFlag(t *testing.T) {
	_, language, _, hearing := filenameMetadata("Movie.mkv", "Movie.hi.srt")
	if language != "hi" || hearing != nil {
		t.Fatalf("Hindi language changed: language=%s hearing=%v", language, hearing)
	}
	_, language, _, _ = filenameMetadata("Movie.mkv", "Movie.da.en.srt")
	if language != "" {
		t.Fatalf("conflicting languages must remain unknown: %s", language)
	}
}
