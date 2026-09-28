package postgres

import (
	"reflect"
	"testing"

	"github.com/media-manager/media-manager/internal/subtitles"
)

func TestMergeRequiredSubtitlesFailsClosed(t *testing.T) {
	t.Parallel()
	snapshot := subtitles.Policy{Required: []string{"da"}, Preferred: []string{"da"}}
	live := subtitles.Policy{Required: []string{"en", "da"}, Preferred: []string{"en"}}
	merged := mergeRequiredSubtitles(snapshot, live)
	if !reflect.DeepEqual(merged.Required, []string{"da", "en"}) {
		t.Fatalf("required=%v, want the union sorted", merged.Required)
	}
	if !reflect.DeepEqual(merged.Preferred, []string{"da"}) {
		t.Fatalf("preferred=%v, want the attempt preferences", merged.Preferred)
	}
	if relaxed := mergeRequiredSubtitles(subtitles.Policy{}, live); !reflect.DeepEqual(relaxed.Required, []string{"da", "en"}) {
		t.Fatalf("relaxed snapshot required=%v, want the live requirements", relaxed.Required)
	}
	if loosened := mergeRequiredSubtitles(subtitles.Policy{Required: []string{"da"}}, subtitles.Policy{}); !reflect.DeepEqual(loosened.Required, []string{"da"}) {
		t.Fatalf("loosened profile required=%v, want the attempt requirement retained", loosened.Required)
	}
}
