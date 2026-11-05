package mediainfo

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The Stremio/Nuvio addon (private repo Media-manager52/stremio-media-manager)
// reads a managed file's quality_evidence, which is this Evidence as JSON, to
// label its stream cards. It reads these facts and detects an inspected file by
// the subtitle_tracks key. Renaming one breaks the addon's labels; change the
// addon first, then this list.
func TestNuvioAddonReadsTheseEvidenceFacts(t *testing.T) {
	t.Parallel()
	evidence, err := Parse([]byte(`{"format":{"format_name":"matroska,webm"},"streams":[
	  {"index":0,"codec_type":"video","codec_name":"hevc","width":1920,"height":1080,"tags":{},"disposition":{}},
	  {"index":1,"codec_type":"audio","codec_name":"eac3","channels":6,"tags":{"language":"dan"},"disposition":{}},
	  {"index":2,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"dan"},"disposition":{}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Facts          map[string]json.RawMessage `json:"facts"`
		SubtitleTracks json.RawMessage            `json:"subtitle_tracks"`
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.SubtitleTracks) == 0 {
		t.Error("subtitle_tracks is missing; the addon uses it to tell an inspected file from release facts")
	}
	want := map[string]any{
		"container":          "matroska",
		"resolution":         "1080p",
		"video_codec":        "hevc",
		"audio_codecs":       []any{"eac3"},
		"audio_languages":    []any{"da"},
		"subtitle_languages": []any{"da"},
	}
	for name, value := range want {
		raw, found := document.Facts[name]
		if !found {
			t.Errorf("fact %q is missing; the addon shows it on stream cards", name)
			continue
		}
		var got any
		if err := json.Unmarshal(raw, &got); err != nil || !reflect.DeepEqual(got, value) {
			t.Errorf("fact %q = %s, want %v in the shape the addon decodes", name, raw, value)
		}
	}
}
