package newznab

import (
	"reflect"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/releases"
)

func TestParseNFOLanguages(t *testing.T) {
	t.Parallel()
	mediaInfo := func(sections ...string) string {
		return "General\nComplete name : x.mkv\n\nVideo\nFormat : AVC\n\n" + strings.Join(sections, "\n\n")
	}
	cases := []struct {
		name         string
		body         string
		subtitles    []string
		subsKnown    bool
		audio        []string
		audioKnown   bool
		missingDA    bool
		releaseTitle string
	}{
		{
			name:      "MediaInfo with English text tracks only",
			body:      mediaInfo("Audio\nFormat : E-AC-3\nLanguage : English", "Text #1\nFormat : UTF-8\nLanguage : English", "Text #2\nFormat : UTF-8\nTitle : SDH\nLanguage : English"),
			subtitles: []string{"en"}, subsKnown: true, audio: []string{"en"}, audioKnown: true, missingDA: true,
		},
		{
			name:      "MediaInfo with a Danish text track",
			body:      mediaInfo("Audio\nLanguage : English", "Text #1\nLanguage : English", "Text #2\nTitle : Dansk\nLanguage : Danish"),
			subtitles: []string{"en", "da"}, subsKnown: true, audio: []string{"en"}, audioKnown: true,
		},
		{
			name:      "MediaInfo with Danish audio and no Danish subtitles",
			body:      mediaInfo("Audio #1\nLanguage : English", "Audio #2\nTitle : Dansk DD 5.1\nLanguage : Danish", "Text\nLanguage : English"),
			subtitles: []string{"en"}, subsKnown: true, audio: []string{"en", "da"}, audioKnown: true,
		},
		{
			name: "MediaInfo text track without a language is unknown",
			body: mediaInfo("Audio\nLanguage : English", "Text #1\nLanguage : English", "Text #2\nFormat : PGS"),
			// The untagged track might be Danish.
			subtitles: []string{"en"}, audio: []string{"en"}, audioKnown: true,
		},
		{
			name: "MediaInfo without any text section says nothing about external subtitles",
			body: mediaInfo("Audio\nLanguage : English"),
			// No Text section: no subtitle evidence at all.
			audio: []string{"en"}, audioKnown: true,
		},
		{
			name:      "MediaInfo audio without a language leaves audio unknown",
			body:      mediaInfo("Audio\nFormat : AAC LC", "Text\nLanguage : French"),
			subtitles: []string{"fr"}, subsKnown: true, missingDA: true,
		},
		{
			name:      "unknown audio with a Danish hint in the release name",
			body:      mediaInfo("Audio\nFormat : AAC LC", "Text\nLanguage : French"),
			subtitles: []string{"fr"}, subsKnown: true,
			releaseTitle: "Film.2020.NORDiC.1080p.WEB-DL",
		},
		{
			name:      "DrunkenSlug t=info HTML with a release-group subtitle line",
			body:      "<br />\n  ▓▓  FORMAT.......: .mkv  ▓▓<br />\n       │█░█   AUDIO........: GER/ENG DTS 1510kbs         █░█│<br />\n       ███▀  ▄ ▄  SUBTiTLES....: DE                     ▄ ▄  ▀███<br />",
			subtitles: []string{"de"}, subsKnown: true, missingDA: true,
		},
		{
			name:      "release-group list naming Danish",
			body:      "RELEASE INFO\n SUBTITLES.: DANISH, FINNISH, NORWEGIAN, SWEDISH\n",
			subtitles: []string{"da", "fi", "no", "sv"}, subsKnown: true,
		},
		{
			name:      "language codes with qualifiers",
			body:      "subtitles. : da (forced), da, da (sdh), en\n",
			subtitles: []string{"da", "en"}, subsKnown: true,
		},
		{
			name:      "Subtitle(s) with regions",
			body:      "Subtitle(s) : English (US), English (US)\n",
			subtitles: []string{"en"}, subsKnown: true, missingDA: true,
		},
		{
			name:      "explicit none in BBCode",
			body:      "[b]SUBTiTLES[/b] : None\n",
			subsKnown: true, missingDA: true,
		},
		{
			name: "a bare No is not read as Norwegian or as none",
			body: "[b]SUBTiTLES[/b] : No\n",
		},
		{
			name: "unrecognized words leave the list unknown",
			body: "Subtitles: NORDiC\n",
		},
		{
			name:      "a column gap ends the list",
			body:      "Subtitles: English      Runtime: 1h 32m\n",
			subtitles: []string{"en"}, subsKnown: true, missingDA: true,
		},
		{
			name:      "numbered list with one entry",
			body:      "Audio        : English E-AC-3 224 kb/s @ 2 channels\nSubs         : 1: English\n\nSource       : AMAZON\n",
			subtitles: []string{"en"}, subsKnown: true, missingDA: true,
		},
		{
			name:      "numbered list continued on following lines",
			body:      "Subs         : 1: English\n               2: Danish\n             : 3: Swedish\nSource       : AMAZON\n",
			subtitles: []string{"en", "da", "sv"}, subsKnown: true,
		},
		{
			name:      "numbered list with an unrecognized entry is unknown",
			body:      "Subs : 1: English\n       2: Klingon\n",
			subtitles: []string{"en"},
		},
		{
			name:      "less common MediaInfo languages are recognized",
			body:      mediaInfo("Audio\nLanguage : English", "Text #1\nLanguage : Azerbaijani", "Text #2\nLanguage : Spanish (Latin America)"),
			subtitles: []string{"az", "es"}, subsKnown: true, audio: []string{"en"}, audioKnown: true, missingDA: true,
		},
		{
			name: "a provider error is no NFO",
			body: `<?xml version="1.0" encoding="UTF-8"?><error code="300" description="No such item"/>`,
		},
		{
			name: "a feed is no NFO",
			body: `<?xml version="1.0"?><rss><channel><item><description>a</description></item><item><description>b</description></item></channel></rss>`,
		},
		{
			name:      "an RSS-wrapped NFO",
			body:      `<?xml version="1.0"?><rss><channel><item><description>Subtitles: English, Dutch</description></item></channel></rss>`,
			subtitles: []string{"en", "nl"}, subsKnown: true, missingDA: true,
		},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := ParseNFOLanguages([]byte(test.body))
			if !reflect.DeepEqual(nonNil(got.Subtitles), nonNil(test.subtitles)) || got.SubtitlesKnown != test.subsKnown {
				t.Fatalf("subtitles=%v known=%v, want %v known=%v", got.Subtitles, got.SubtitlesKnown, test.subtitles, test.subsKnown)
			}
			if !reflect.DeepEqual(nonNil(got.Audio), nonNil(test.audio)) || got.AudioKnown != test.audioKnown {
				t.Fatalf("audio=%v known=%v, want %v known=%v", got.Audio, got.AudioKnown, test.audio, test.audioKnown)
			}
			title := test.releaseTitle
			if title == "" {
				title = "Some.Film.2020.1080p.WEB-DL.DDP5.1.H.264-GRP"
			}
			if missing := got.MissingRequired("da", title); missing != test.missingDA {
				t.Fatalf("MissingRequired(da)=%v, want %v (%+v)", missing, test.missingDA, got)
			}
		})
	}
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func TestAudioMentionIsPositiveEvidence(t *testing.T) {
	t.Parallel()
	got := ParseNFOLanguages([]byte("AUDIO......: Danish DD5.1\nSUBTITLES..: English\n"))
	if got.MissingRequired("da", "Film.2020.1080p") {
		t.Fatalf("a Danish audio line must satisfy Danish: %+v", got)
	}
}

func TestMediaInfoFieldsReadHTMLAndReleaseGroupLines(t *testing.T) {
	t.Parallel()
	fields := MediaInfoFields([]byte("<br />Text #1<br />Language : Danish<br />\n SUBTiTLES....: DE, EN   ▄▄\n"))
	values := map[string][]string{}
	for _, field := range fields {
		values[field.Name] = append(values[field.Name], field.Value)
	}
	if !reflect.DeepEqual(values["subtitles"], []string{"Danish", "de,en"}) {
		t.Fatalf("subtitles=%v", values["subtitles"])
	}
}

func TestAdvertisedNFOFunction(t *testing.T) {
	t.Parallel()
	observation := func(native, info string) releases.Observation {
		return releases.Observation{NativeGUID: native, Fields: []releases.RawField{{Name: "info", Value: info}}}
	}
	cases := []struct {
		name, base, native, info, function string
		raw, ok                            bool
	}{
		{"DrunkenSlug info", "https://drunkenslug.com/api", "https://drunkenslug.com/details/017b403e1c38034923765833c923e3e0490a3faf", "https://drunkenslug.com/api?t=info&id=017b403e1c38034923765833c923e3e0490a3faf", "info", false, true},
		{"getnfo", "https://abnzb.com/api", "0123456789abcdef0123456789abcdef", "https://abnzb.com/api?t=getnfo&id=0123456789abcdef0123456789abcdef&raw=1", "getnfo", true, true},
		{"other host", "https://abnzb.com/api", "0123456789abcdef0123456789abcdef", "https://evil.example/api?t=getnfo&id=0123456789abcdef0123456789abcdef", "", false, false},
		{"other release", "https://abnzb.com/api", "0123456789abcdef0123456789abcdef", "https://abnzb.com/api?t=getnfo&id=ffffffffffffffffffffffffffffffff", "", false, false},
		{"other function", "https://abnzb.com/api", "0123456789abcdef0123456789abcdef", "https://abnzb.com/api?t=get&id=0123456789abcdef0123456789abcdef", "", false, false},
		{"downgraded scheme", "https://abnzb.com/api", "0123456789abcdef0123456789abcdef", "http://abnzb.com/api?t=getnfo&id=0123456789abcdef0123456789abcdef", "", false, false},
	}
	for _, test := range cases {
		function, raw, ok := AdvertisedNFOFunction(observation(test.native, test.info), test.base)
		if function != test.function || raw != test.raw || ok != test.ok {
			t.Fatalf("%s: got %q raw=%v ok=%v, want %q raw=%v ok=%v", test.name, function, raw, ok, test.function, test.raw, test.ok)
		}
	}
}
