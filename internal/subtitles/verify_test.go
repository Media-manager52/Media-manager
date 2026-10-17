package subtitles

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/media-manager/media-manager/internal/mediainfo"
)

type fixtureProbe struct{ evidence map[string]mediainfo.Evidence }

func (probe fixtureProbe) InspectWithin(_ context.Context, _ string, relative string) (mediainfo.Evidence, error) {
	return probe.evidence[relative], nil
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
	matching := fixtureAsset(t, root, "Show.S01E01.da-DK.forced.srt", "subtitle")
	nonmatching := fixtureAsset(t, root, "Show.S01E010.da.srt", "wrong boundary")
	result, err := Verify(context.Background(), root, []Asset{video}, []Asset{video, matching, nonmatching}, Policy{Required: []string{"da"}}, fixtureProbe{evidence: map[string]mediainfo.Evidence{video.RelativePath: mediaEvidence()}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "verified" || len(result.Companions) != 1 || result.Companions[0].SubtitleAssetID != matching.ID || result.Companions[0].Language != "da" || result.Companions[0].Forced == nil || !*result.Companions[0].Forced {
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
	parsed, err := ParsePolicy([]byte(`{"rules":[],"required_audio_languages":["Danish"]}`))
	if err != nil || len(parsed.RequiredAudio) != 1 || parsed.RequiredAudio[0] != "da" {
		t.Fatalf("parsed policy=%+v err=%v", parsed, err)
	}
}
