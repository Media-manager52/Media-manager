package newznab

import (
	"github.com/media-manager/media-manager/internal/releases"
	"testing"
	"time"
)

func TestProviderMediaFieldsAndNFO(t *testing.T) {
	body := []byte("General\nFormat : Matroska\nDuration : 1 h 42 min\nVideo\nFormat : HEVC\nAudio #1\nFormat : E-AC-3\nLanguage : English\nChannel(s) : 6 channels\nText #1\nFormat : UTF-8\nLanguage : Danish\nText #2\nLanguage : English\nMenu\nLanguage : German\n")
	fields := MediaInfoFields(body)
	facts := normalizeFacts(fields, time.Now())
	values := map[string][]string{}
	for _, f := range facts {
		if f.State != releases.FactObserved {
			t.Fatal(f)
		}
		values[f.Name] = f.Values
	}
	if len(values["subtitle_languages"]) != 2 || values["subtitle_languages"][0] != "da" || values["audio_languages"][0] != "en" || values["video_codec"][0] != "hevc" {
		t.Fatalf("wrong media fields: %v", values)
	}
	if got := MediaInfoFields([]byte("Movie.DANISH.NORDIC\nLanguage : Danish\n")); len(got) != 0 {
		t.Fatal("unscoped language became subtitle evidence", got)
	}
}
func TestProviderMediaAttributesRepeatedAndSeparate(t *testing.T) {
	o, err := ParseSearch([]byte(`<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>Movie.2025</title><guid>native-1</guid><newznab:attr name="video" value="HEVC"/><newznab:attr name="audio" value="E-AC-3"/><newznab:attr name="audiolanguage" value="Danish"/><newznab:attr name="subs" value="English"/><newznab:attr name="subs" value="Swedish"/></item></channel></rss>`), "", "movie", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range o[0].Facts {
		if f.Name == "subtitle_languages" {
			for _, v := range f.Values {
				if v == "da" {
					t.Fatal("Danish audio treated as Danish subtitles")
				}
			}
		}
	}
	if len(o[0].Fields) != 5 {
		t.Fatal("lost repeated attributes")
	}
}
