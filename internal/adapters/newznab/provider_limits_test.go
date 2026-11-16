package newznab

import (
	"os"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// The fixtures are anonymised answers of the two measured indexers: one
// states query counters and a download maximum, the other all of them.
func TestParseProviderLimitsReadsTheStatedCounters(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	partial, ok := ParseProviderLimits(fixture(t, "abnzb_tvsearch.xml"), now)
	if !ok || partial.QueryUsed != 120 || partial.QueryMax != 1000 || partial.GrabUsed != -1 || partial.GrabMax != 30 || partial.QueryOldest.IsZero() || !partial.GrabOldest.IsZero() || !partial.ObservedAt.Equal(now) {
		t.Fatalf("partial reading=%+v ok=%v", partial, ok)
	}
	full, ok := ParseProviderLimits(fixture(t, "msgnews_search.xml"), now)
	if !ok || full.QueryUsed != 120 || full.QueryMax != 1000 || full.GrabUsed != 29 || full.GrabMax != 30 || full.QueryOldest.IsZero() || full.GrabOldest.IsZero() {
		t.Fatalf("full reading=%+v ok=%v", full, ok)
	}
	for name, body := range map[string]string{
		"no element":       `<rss><channel><item><title>apilimits</title></item></channel></rss>`,
		"no numbers":       `<rss><channel><newznab:apilimits /></channel></rss>`,
		"negative numbers": `<rss><channel><newznab:apilimits apicurrent="-1" apimax="-5"/></channel></rss>`,
		"not numbers":      `<rss><channel><newznab:apilimits apicurrent="many" apimax="lots"/></channel></rss>`,
		"zero maximum":     `<rss><channel><newznab:apilimits apicurrent="0" apimax="0" grabmax="0"/></channel></rss>`,
		"an NZB":           `<nzb><file subject="apilimits"><segments><segment bytes="1" number="1">a@b</segment></segments></file></nzb>`,
	} {
		if reading, ok := ParseProviderLimits([]byte(body), now); ok {
			t.Fatalf("%s: read %+v", name, reading)
		}
	}
	// A count without a usable time still counts; the time is simply unknown.
	if reading, ok := ParseProviderLimits([]byte(`<newznab:apilimits apicurrent="7" apimax="10" apioldesttime="yesterday"/>`), now); !ok || reading.QueryUsed != 7 || !reading.QueryOldest.IsZero() {
		t.Fatalf("reading=%+v ok=%v", reading, ok)
	}
}

// Why abNZB is read as two requests: the same uploads carry a subtitle list
// and an NFO flag in its series answer and neither in its general answer.
// The other measured indexer states both in its general answer.
func TestSplitFeedFixturesShowWhatTheGeneralAnswerLacks(t *testing.T) {
	t.Parallel()
	now := time.Now()
	attributes := func(file, mode string) map[string]map[string]bool {
		observations, _, err := ParseSearchPage(fixture(t, file), "indexer", mode, now)
		if err != nil {
			t.Fatal(err)
		}
		result := map[string]map[string]bool{}
		for _, observation := range observations {
			names := map[string]bool{}
			for _, field := range observation.Fields {
				if field.Value != "" {
					names[field.Name] = true
				}
			}
			result[observation.NativeGUID] = names
		}
		return result
	}
	general, series := attributes("abnzb_search.xml", "search"), attributes("abnzb_tvsearch.xml", "tvsearch")
	if len(general) != 4 || len(series) != 4 {
		t.Fatalf("general=%d series=%d uploads", len(general), len(series))
	}
	withSubtitles := 0
	for id, names := range series {
		if general[id] == nil {
			t.Fatalf("upload %s is missing from the general answer", id)
		}
		if general[id]["subs"] || general[id]["nfo"] {
			t.Fatalf("the general answer carries subs or nfo for %s", id)
		}
		if !names["nfo"] {
			t.Fatalf("the series answer has no nfo flag for %s", id)
		}
		if names["subs"] {
			withSubtitles++
		}
	}
	if withSubtitles != 3 {
		t.Fatalf("%d series uploads carry a subtitle list, want 3", withSubtitles)
	}
	if movies := attributes("abnzb_movie.xml", "movie"); len(movies) != 4 {
		t.Fatalf("movies=%d", len(movies))
	}
	other := attributes("msgnews_search.xml", "search")
	withSubtitles, withAudioLanguage := 0, 0
	for id, names := range other {
		if !names["audio"] || !names["nfo"] {
			t.Fatalf("the other indexer's general answer lacks audio or nfo for %s", id)
		}
		if names["subs"] {
			withSubtitles++
		}
		if names["language"] {
			withAudioLanguage++
		}
	}
	if len(other) != 4 || withSubtitles != 3 || withAudioLanguage != 3 {
		t.Fatalf("the other indexer's general answer: %d uploads, %d with subtitles, %d with an audio language", len(other), withSubtitles, withAudioLanguage)
	}
}

func TestFeedStreamsIsBoundToTheMeasuredEndpoint(t *testing.T) {
	t.Parallel()
	streams := FeedStreams("https://abnzb.com/api")
	if len(streams) != 2 || streams[0] != (FeedStream{Name: "movie", Mode: "movie", RootCategory: 2000}) || streams[1] != (FeedStream{Name: "series", Mode: "tvsearch", RootCategory: 5000}) {
		t.Fatalf("streams=%+v", streams)
	}
	for _, other := range []string{"https://msgnews.live/api", "http://abnzb.com/api", "https://abnzb.com:8443/api", "https://api.abnzb.com/api", "https://abnzb.com.example/api", "https://example.org/abnzb.com", "", "::"} {
		if got := FeedStreams(other); got != nil {
			t.Fatalf("%q is read as %+v", other, got)
		}
	}
}
