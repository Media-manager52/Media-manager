package mediainfo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDerivesBoundedQualityFacts(t *testing.T) {
	t.Parallel()
	body := []byte(`{
  "format":{"format_name":"matroska,webm","duration":"3661.250000"},
  "streams":[
    {"index":0,"codec_type":"video","codec_name":"hevc","width":3840,"height":2160,"color_transfer":"smpte2084","color_primaries":"bt2020","color_space":"bt2020nc","tags":{},"disposition":{"default":1,"forced":0}},
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
	if _, err := Parse([]byte(`{"format":{"format_name":"matroska"},"streams":[{"index":0,"codec_type":"audio","codec_name":"aac","channels":2,"tags":{},"disposition":{}}]}`)); err == nil {
		t.Fatal("document without video was accepted")
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
