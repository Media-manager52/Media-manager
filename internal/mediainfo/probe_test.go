package mediainfo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseDerivesBoundedQualityFacts(t *testing.T) {
	t.Parallel()
	body := []byte(`{
  "format":{"format_name":"matroska,webm","duration":"3661.250000"},
  "streams":[
    {"index":0,"codec_type":"video","codec_name":"hevc","width":3840,"height":2160,"color_transfer":"smpte2084","color_primaries":"bt2020","color_space":"bt2020nc","tags":{},"disposition":{"default":1,"forced":0},"side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":8}]},
    {"index":1,"codec_type":"audio","codec_name":"eac3","channels":6,"channel_layout":"5.1(side)","tags":{"language":"eng","title":"Main"},"disposition":{"default":1,"forced":0}},
    {"index":2,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"dan"},"disposition":{"default":0,"forced":1}}
  ]
}`)
	evidence, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.SchemaVersion != 1 || len(evidence.VideoTracks) != 1 || len(evidence.AudioTracks) != 1 || len(evidence.SubtitleTracks) != 1 {
		t.Fatalf("unexpected evidence: %+v", evidence)
	}
	if evidence.Facts["resolution"] != "2160p" || evidence.Facts["video_codec"] != "hevc" || evidence.Facts["dynamic_range"] != "pq" {
		t.Fatalf("unexpected facts: %+v", evidence.Facts)
	}
	if evidence.SubtitleTracks[0].Language != "dan" || evidence.SubtitleTracks[0].CanonicalLanguage != "da" || evidence.SubtitleTracks[0].Forced == nil || !*evidence.SubtitleTracks[0].Forced {
		t.Fatalf("unexpected subtitle evidence: %+v", evidence.SubtitleTracks[0])
	}
}

func TestInspectWithinRejectsPathEscapeAndSymlink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.mkv")
	if err := os.WriteFile(outside, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.mkv")); err != nil {
		t.Fatal(err)
	}
	probe := NewProbe()
	if _, err := probe.InspectWithin(t.Context(), root, "../outside.mkv"); err == nil {
		t.Fatal("path escape was accepted")
	}
	if _, err := probe.InspectWithin(t.Context(), root, "linked.mkv"); err == nil {
		t.Fatal("symlinked source was accepted")
	}
}

func TestParseRejectsUnknownOrMissingVideoEvidence(t *testing.T) {
	t.Parallel()
	if _, err := Parse([]byte(`{"format":{"format_name":"matroska"},"streams":[],"invented":true}`)); err == nil {
		t.Fatal("unknown document field was accepted")
	}
	if _, err := Parse([]byte(`{"format":{"format_name":"matroska"},"streams":[{"index":0,"codec_type":"audio","codec_name":"aac","channels":2,"tags":{},"disposition":{}}]}`)); !errors.Is(err, ErrNoVideoTrack) {
		t.Fatalf("audio-only document error=%v, want ErrNoVideoTrack", err)
	}
	if _, err := Parse([]byte(`{"format":{"format_name":"matroska"},"streams":[{"index":0,"codec_type":"video","codec_name":"hevc","width":3840,"height":2160,"tags":{},"disposition":{},"invented":true}]}`)); err == nil {
		t.Fatal("unknown stream field was accepted")
	}
}

func TestBoundedBufferStopsAtLimit(t *testing.T) {
	t.Parallel()
	buffer := &boundedBuffer{limit: 4}
	written, err := buffer.Write([]byte("abcdef"))
	if written != 4 || !errors.Is(err, ErrOutputLimit) || string(buffer.Bytes()) != "abcd" {
		t.Fatalf("bounded write=%d err=%v body=%q", written, err, buffer.Bytes())
	}
	if _, err := buffer.Write([]byte(strings.Repeat("x", 2))); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("second bounded write err=%v", err)
	}
}

func TestInspectWithinDefersAnFFprobeTimeout(t *testing.T) {
	root := t.TempDir()
	mediaPath := filepath.Join(root, "Movie.mkv")
	signature := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, make([]byte, 4096)...)
	if err := os.WriteFile(mediaPath, signature, 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "slow-ffprobe")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	probe := &Probe{Executable: executable, Timeout: 50 * time.Millisecond}
	if _, err := probe.InspectWithin(t.Context(), root, "Movie.mkv"); !errors.Is(err, ErrSourceUnresponsive) {
		t.Fatalf("timeout error=%v, want ErrSourceUnresponsive", err)
	}
}

func TestInspectWithinUsesFFprobeCompatibleArguments(t *testing.T) {
	root := t.TempDir()
	mediaPath := filepath.Join(root, "Movie.mkv")
	if err := os.WriteFile(mediaPath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(t.TempDir(), "ffprobe-fixture")
	script := `#!/bin/sh
for argument in "$@"; do
  if [ "$argument" = "-nostdin" ]; then
    exit 9
  fi
done
printf '%s' '{"format":{"format_name":"matroska","duration":"60"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"tags":{},"disposition":{"default":1,"forced":0,"hearing_impaired":0}}]}'
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	probe := NewProbe()
	probe.Executable = executable
	evidence, err := probe.InspectWithin(t.Context(), root, "Movie.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.VideoTracks) != 1 || evidence.Facts["resolution"] != "1080p" {
		t.Fatalf("evidence=%+v", evidence)
	}
}

func TestParseDistinguishesUnsupportedEncryptedVideoFromUnknownCodec(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, stream string
		encrypted    bool
	}{
		{"encrypted video with Danish subtitle", `{"index":0,"codec_type":"video","codec_tag_string":"encv","width":1920,"height":1080}`, true},
		{"missing codec without encryption", `{"index":0,"codec_type":"video","width":1920,"height":1080}`, false},
		{"unknown codec tag", `{"index":0,"codec_type":"video","codec_tag_string":"unknown","width":1920,"height":1080}`, false},
		{"audio is not encrypted video", `{"index":0,"codec_type":"audio","codec_tag_string":"encv","channels":2}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"format":{"format_name":"matroska"},"streams":[` + tc.stream + `,{"index":1,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"dan"}}]}`)
			evidence, err := Parse(body)
			if err == nil || errors.Is(err, ErrEncryptedVideo) != tc.encrypted || evidence.SchemaVersion != 0 {
				t.Fatalf("error=%v evidence=%+v encrypted=%v", err, evidence, tc.encrypted)
			}
		})
	}
	body := []byte(`{"format":{"format_name":"matroska"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","codec_tag_string":"avc1","width":1920,"height":1080}]}`)
	if evidence, err := Parse(body); err != nil || evidence.VideoTracks[0].Codec != "h264" {
		t.Fatalf("clear video evidence=%+v error=%v", evidence, err)
	}
}

// TestParseSkipsUnidentifiableSubtitleTracks proves one subtitle track without
// a codec (seen on a real screener) no longer fails inspection of an
// otherwise playable file, never counts as a subtitle, and that video or audio
// without a codec still fails.
func TestParseSkipsUnidentifiableSubtitleTracks(t *testing.T) {
	t.Parallel()
	body := []byte(`{"format":{"format_name":"matroska"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080},{"index":1,"codec_type":"audio","codec_name":"aac","channels":2},{"index":2,"codec_type":"subtitle","tags":{"language":"dan"}},{"index":3,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng"}},{"index":4,"codec_type":"attachment"}]}`)
	evidence, err := Parse(body)
	if err != nil || len(evidence.SubtitleTracks) != 1 || evidence.SubtitleTracks[0].Codec != "subrip" {
		t.Fatalf("evidence=%+v error=%v", evidence, err)
	}
	for _, stream := range []string{`{"index":1,"codec_type":"audio","channels":2}`, `{"index":1,"codec_type":"video","width":1920,"height":1080}`} {
		body := []byte(`{"format":{"format_name":"matroska"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080},` + stream + `]}`)
		if _, err := Parse(body); err == nil {
			t.Fatalf("stream without codec accepted: %s", stream)
		}
	}
}

// TestInspectWithinReportsZeroFilledFiles proves a file opening with zeros
// (AltMount's fill for articles no provider has) is reported as such before
// ffprobe runs, while a real container signature is passed on to ffprobe.
func TestInspectWithinReportsZeroFilledFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "zero.mkv"), make([]byte, 200000), 0o644); err != nil {
		t.Fatal(err)
	}
	signature := append([]byte{0x1a, 0x45, 0xdf, 0xa3}, make([]byte, 200000)...)
	if err := os.WriteFile(filepath.Join(root, "real.mkv"), signature, 0o644); err != nil {
		t.Fatal(err)
	}
	probe := &Probe{Executable: "/nonexistent/ffprobe"}
	if _, err := probe.InspectWithin(context.Background(), root, "zero.mkv"); !errors.Is(err, ErrZeroFilledOutput) {
		t.Fatalf("zero-filled file error=%v", err)
	}
	if _, err := probe.InspectWithin(context.Background(), root, "real.mkv"); err == nil || errors.Is(err, ErrZeroFilledOutput) {
		t.Fatalf("file with a signature must reach ffprobe, error=%v", err)
	}
}
