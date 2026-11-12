package subtitles

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"uuid"

	"github.com/media-manager/media-manager/internal/mediainfo"
)

type fixtureProbe struct{ evidence map[string]mediainfo.Evidence }

func (probe fixtureProbe) InspectWithin(_ context.Context, _ string, relative string) (mediainfo.Evidence, error) {
	return probe.evidence[relative], nil
}

func (probe fixtureProbe) CountSubtitleCues(context.Context, string, string, int, []float64, float64) ([]int, error) {
	return nil, nil
}

func TestVerifyAcceptsEmbeddedCanonicalDanish(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	video := fixtureAsset(t, root, "Movie.mkv", "video")
	result, err := Verify(context.Background(), root, []Asset{video}, []Asset{video}, Policy{Required: []string{"da"}}, fixtureProbe{evidence: map[string]mediainfo.Evidence{
		video.RelativePath: mediaEvidence(mediainfo.Track{Index: 2, Codec: "subrip", Language: "Danish", CanonicalLanguage: "da"}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "verified" || len(result.Videos) != 1 || result.Videos[0].Languages[0].RawValue != "Danish" || result.Videos[0].Languages[0].Canonical != "da" {
		t.Fatalf("unexpected verification: %+v", result)
	}
}

func TestVerifyAcceptsOnlyStrictManagedDanishSidecar(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	video := fixtureAsset(t, root, "Show.S01E01.mkv", "video")
	matching := fixtureAsset(t, root, "Show.S01E01.da-DK.sdh.srt", "subtitle")
	nonmatching := fixtureAsset(t, root, "Show.S01E010.da.srt", "wrong boundary")
	result, err := Verify(context.Background(), root, []Asset{video}, []Asset{video, matching, nonmatching}, Policy{Required: []string{"da"}}, fixtureProbe{evidence: map[string]mediainfo.Evidence{video.RelativePath: mediaEvidence()}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "verified" || len(result.Companions) != 1 || result.Companions[0].SubtitleAssetID != matching.ID || result.Companions[0].Language != "da" || result.Companions[0].HearingImpaired == nil || !*result.Companions[0].HearingImpaired {
		t.Fatalf("unexpected companions: %+v", result.Companions)
	}
}

func TestVerifyRequiresDanishForEverySelectedPackVideo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first := fixtureAsset(t, root, "Show.S01E01.mkv", "first")
	second := fixtureAsset(t, root, "Show.S01E02.mkv", "second")
	sidecar := fixtureAsset(t, root, "Show.S01E01.dansk.srt", "subtitle")
	result, err := Verify(context.Background(), root, []Asset{first, second}, []Asset{first, second, sidecar}, Policy{Required: []string{"da"}}, fixtureProbe{evidence: map[string]mediainfo.Evidence{first.RelativePath: mediaEvidence(), second.RelativePath: mediaEvidence()}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "unverified" || len(result.FailureReasons) != 1 || len(result.Videos[1].MissingRequired) != 1 {
		t.Fatalf("unexpected pack verification: %+v", result)
	}
}

func TestVerifyRejectsSymlinkedInventoryAsset(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.mkv")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "Movie.mkv")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	asset := Asset{ID: uuid.NewV7(), RelativePath: "Movie.mkv", Size: int64(len("outside"))}
	if _, err := Verify(context.Background(), root, []Asset{asset}, []Asset{asset}, Policy{Required: []string{"da"}}, fixtureProbe{}); err == nil {
		t.Fatal("symlinked output asset was accepted")
	}
}

func fixtureAsset(t *testing.T, root, relative, body string) Asset {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Asset{ID: uuid.NewV7(), RelativePath: relative, Size: int64(len(body))}
}

func mediaEvidence(subtitles ...mediainfo.Track) mediainfo.Evidence {
	return mediainfo.Evidence{SchemaVersion: 1, ContainerNames: []string{"matroska"}, VideoTracks: []mediainfo.Track{{Index: 0, Codec: "hevc", Width: 1920, Height: 1080}}, AudioTracks: []mediainfo.Track{}, SubtitleTracks: subtitles, Facts: map[string]any{}}
}

// A Danish dub needs no Danish subtitles: a Danish audio track satisfies the
// required language. A foreign audio track without Danish subtitles still fails.
func TestVerifyAcceptsDanishAudioForRequiredLanguage(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dub := fixtureAsset(t, root, "Dub.mkv", "video")
	english := fixtureAsset(t, root, "English.mkv", "video-en")
	withAudio := func(language string) mediainfo.Evidence {
		evidence := mediaEvidence()
		evidence.AudioTracks = []mediainfo.Track{{Index: 1, Codec: "ac3", Language: language, CanonicalLanguage: language}}
		return evidence
	}
	probe := fixtureProbe{evidence: map[string]mediainfo.Evidence{dub.RelativePath: withAudio("da"), english.RelativePath: withAudio("en")}}
	result, err := Verify(context.Background(), root, []Asset{dub}, []Asset{dub, english}, Policy{Required: []string{"da"}}, probe)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "verified" || len(result.Videos[0].MissingRequired) != 0 || len(result.Videos[0].AudioLanguages) != 1 || result.Videos[0].AudioLanguages[0] != "da" {
		t.Fatalf("Danish dub should verify: %+v", result)
	}
	result, err = Verify(context.Background(), root, []Asset{english}, []Asset{dub, english}, Policy{Required: []string{"da"}}, probe)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "unverified" || len(result.Videos[0].MissingRequired) != 1 {
		t.Fatalf("English audio without Danish subtitles must stay unverified: %+v", result)
	}
}

// A required audio language is satisfied only by an audio track: Danish
// subtitles do not make a children's title playable for a child.
func TestVerifyRequiredAudioIgnoresSubtitles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	subbed := fixtureAsset(t, root, "Subbed.mkv", "video")
	dubbed := fixtureAsset(t, root, "Dubbed.mkv", "video-da")
	subbedEvidence := mediaEvidence(mediainfo.Track{Index: 2, Codec: "subrip", Language: "Danish", CanonicalLanguage: "da"})
	subbedEvidence.AudioTracks = []mediainfo.Track{{Index: 1, Codec: "ac3", Language: "eng", CanonicalLanguage: "en"}}
	dubbedEvidence := mediaEvidence()
	dubbedEvidence.AudioTracks = []mediainfo.Track{{Index: 1, Codec: "ac3", Language: "dan", CanonicalLanguage: "da"}}
	probe := fixtureProbe{evidence: map[string]mediainfo.Evidence{subbed.RelativePath: subbedEvidence, dubbed.RelativePath: dubbedEvidence}}
	policy := Policy{RequiredAudio: []string{"da"}}
	result, err := Verify(context.Background(), root, []Asset{subbed}, []Asset{subbed, dubbed}, policy, probe)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "unverified" || len(result.Videos[0].MissingRequired) != 1 || result.Videos[0].MissingRequired[0] != "audio:da" {
		t.Fatalf("Danish subtitles must not satisfy required Danish audio: %+v", result)
	}
	if result, err = Verify(context.Background(), root, []Asset{dubbed}, []Asset{subbed, dubbed}, policy, probe); err != nil || result.Status != "verified" {
		t.Fatalf("Danish dub must verify: %+v err=%v", result, err)
	}
	untagged := fixtureAsset(t, root, "Untagged.mkv", "video-und")
	untaggedEvidence := mediaEvidence()
	untaggedEvidence.AudioTracks = []mediainfo.Track{{Index: 1, Codec: "ac3"}}
	probe.evidence[untagged.RelativePath] = untaggedEvidence
	if result, err = Verify(context.Background(), root, []Asset{untagged}, []Asset{untagged}, policy, probe); err != nil || result.Status != "unverified" {
		t.Fatalf("untagged audio of a foreign title must not count as Danish: %+v err=%v", result, err)
	}
	if result, err = Verify(context.Background(), root, []Asset{untagged}, []Asset{untagged}, policy.WithOriginalLanguage("da"), probe); err != nil || result.Status != "verified" {
		t.Fatalf("untagged audio of a Danish title is its Danish audio: %+v err=%v", result, err)
	}
	undetermined := fixtureAsset(t, root, "Undetermined.mkv", "video-undetermined")
	undeterminedEvidence := mediaEvidence()
	undeterminedEvidence.AudioTracks = []mediainfo.Track{{Index: 1, Codec: "ac3", Language: "und", CanonicalLanguage: "und"}}
	probe.evidence[undetermined.RelativePath] = undeterminedEvidence
	if result, err = Verify(context.Background(), root, []Asset{undetermined}, []Asset{undetermined}, policy.WithOriginalLanguage("da"), probe); err != nil || result.Status != "verified" {
		t.Fatalf("und-tagged audio of a Danish title is its Danish audio: %+v err=%v", result, err)
	}
	if result, err = Verify(context.Background(), root, []Asset{undetermined}, []Asset{undetermined}, policy, probe); err != nil || result.Status != "unverified" {
		t.Fatalf("und-tagged audio of a foreign title must not count as Danish: %+v err=%v", result, err)
	}
	parsed, err := ParsePolicy([]byte(`{"rules":[],"required_audio_languages":["Danish"]}`))
	if err != nil || len(parsed.RequiredAudio) != 1 || parsed.RequiredAudio[0] != "da" {
		t.Fatalf("parsed policy=%+v err=%v", parsed, err)
	}
}

type countingInventoryProbe struct {
	fixtureProbe
	calls int
}

func (probe *countingInventoryProbe) InspectWithin(ctx context.Context, root, relative string) (mediainfo.Evidence, error) {
	probe.calls++
	return probe.fixtureProbe.InspectWithin(ctx, root, relative)
}

// A changed frozen inventory must fail before probing or claiming any verified output.
func TestVerifyRejectsChangedInventoryBeforeInspectingAnyVideo(t *testing.T) {
	for _, change := range []string{"removed-sidecar", "resized-sidecar", "selected-path", "selected-size", "unknown-asset"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			video := fixtureAsset(t, root, "Lantern.S01E01.mkv", "video")
			sidecar := fixtureAsset(t, root, "Lantern.S01E01.da.srt", "subtitle")
			selected := video
			switch change {
			case "removed-sidecar":
				if err := os.Remove(filepath.Join(root, sidecar.RelativePath)); err != nil {
					t.Fatal(err)
				}
			case "resized-sidecar":
				if err := os.WriteFile(filepath.Join(root, sidecar.RelativePath), []byte("changed subtitle"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "selected-path":
				selected.RelativePath = "Lantern.S01E02.mkv"
			case "selected-size":
				selected.Size++
			case "unknown-asset":
				selected.ID = uuid.NewV7()
			}
			probe := &countingInventoryProbe{fixtureProbe: fixtureProbe{evidence: map[string]mediainfo.Evidence{video.RelativePath: mediaEvidence(mediainfo.Track{CanonicalLanguage: "da"})}}}
			result, err := Verify(t.Context(), root, []Asset{selected}, []Asset{video, sidecar}, Policy{Required: []string{"da"}}, probe)
			if err == nil || result.Status == "verified" || probe.calls != 0 {
				t.Fatalf("changed inventory = %+v, %v; probe calls=%d", result, err, probe.calls)
			}
		})
	}
}

// Language normalization preserves every hard quality rule and unrelated profile setting.
func TestCanonicalizePolicyPreservesQualityRulesWhileNormalizingLanguages(t *testing.T) {
	original := json.RawMessage(`{"rules":[{"name":"Required resolution","fact":"resolution","kind":"required","allowed_values":["1080p"],"unknown":"hold"}],"upgrades_enabled":false,"custom":{"weight":7},"required_subtitle_languages":["Danish","da-DK","eng"],"preferred_subtitle_languages":["sv","swe"],"required_audio_languages":["dan","da"]}`)
	canonical, policy, err := CanonicalizePolicy(original)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(policy.Required, []string{"da", "en"}) || !reflect.DeepEqual(policy.Preferred, []string{"sv"}) || !reflect.DeepEqual(policy.RequiredAudio, []string{"da"}) {
		t.Fatalf("canonical policy = %+v", policy)
	}
	var before, after map[string]any
	if err := json.Unmarshal(original, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(canonical, &after); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"rules", "upgrades_enabled", "custom"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			t.Fatalf("canonicalization changed %s: %v -> %v", key, before[key], after[key])
		}
	}
	reparsed, err := ParsePolicy(canonical)
	if err != nil || !reflect.DeepEqual(reparsed, policy) {
		t.Fatalf("stored policy did not retain language requirements: %+v, %v", reparsed, err)
	}
	// Older array-only rule documents must also retain their hard rules.
	legacy, _, err := CanonicalizePolicy(json.RawMessage(`[{"fact":"resolution","kind":"required","allowed_values":["1080p"],"unknown":"hold"}]`))
	if err != nil {
		t.Fatal(err)
	}
	var wrapped map[string]json.RawMessage
	if err := json.Unmarshal(legacy, &wrapped); err != nil {
		t.Fatal(err)
	}
	if string(wrapped["rules"]) != `[{"fact":"resolution","kind":"required","allowed_values":["1080p"],"unknown":"hold"}]` {
		t.Fatalf("legacy rules changed: %s", legacy)
	}
}

// An unreliable language in any policy field is refused rather than silently dropping a goal.
func TestCanonicalizePolicyRefusesUnreliableLanguageRequirements(t *testing.T) {
	for _, field := range []string{"required_subtitle_languages", "preferred_subtitle_languages", "required_audio_languages"} {
		t.Run(field, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"rules": []any{}, field: []string{"da", "not-a-language"}})
			if err != nil {
				t.Fatal(err)
			}
			canonical, _, err := CanonicalizePolicy(body)
			if err == nil || canonical != nil {
				t.Fatalf("unreliable language produced a usable policy: %s, %v", canonical, err)
			}
		})
	}
}

// A subtitle named for the video's sample or trailer is not the feature's
// subtitle: it neither satisfies a required language nor is published.
func TestVerifyIgnoresSampleAndTrailerSidecars(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	video := fixtureAsset(t, root, "Movie.mkv", "video")
	sample := fixtureAsset(t, root, "Movie.sample.da.srt", "sample subtitle")
	trailer := fixtureAsset(t, root, "Movie.Trailer.da.srt", "trailer subtitle")
	result, err := Verify(context.Background(), root, []Asset{video}, []Asset{video, sample, trailer}, Policy{Required: []string{"da"}}, fixtureProbe{evidence: map[string]mediainfo.Evidence{video.RelativePath: mediaEvidence()}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "unverified" || len(result.Companions) != 0 {
		t.Fatalf("sample/trailer subtitle was accepted: status=%s companions=%+v", result.Status, result.Companions)
	}
}
