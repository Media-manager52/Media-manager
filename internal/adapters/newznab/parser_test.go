package newznab

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/releases"
)

func TestParsersRejectUnrelatedXML(t *testing.T) {
	if _, err := ParseSearch([]byte(`<html><body>ok</body></html>`), "", "search", time.Now()); !errors.Is(err, ErrMalformedXML) {
		t.Fatalf("unrelated search XML accepted: %v", err)
	}
	if _, err := ParseCaps([]byte(`<html><body>ok</body></html>`), time.Now()); !errors.Is(err, ErrMalformedXML) {
		t.Fatalf("unrelated CAPS XML accepted: %v", err)
	}
	if _, err := ParseSearch([]byte(`<html><rss><channel/></rss></html>`), "", "search", time.Now()); !errors.Is(err, ErrMalformedXML) {
		t.Fatalf("nested RSS envelope accepted: %v", err)
	}
}

func TestParseSearchPreservesRepeatedAttributes(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>Show.S01E01.2160p</title><guid>native-1</guid><newznab:attr name="category" value="5000"/><newznab:attr name="category" value="5040"/><newznab:attr name="tvdbid" value="42"/></item></channel></rss>`)
	items, err := ParseSearch(body, "", "tvsearch", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Fields) != 3 {
		t.Fatalf("unexpected items: %#v", items)
	}
	if items[0].Fields[0].Value != "5000" || items[0].Fields[1].Value != "5040" {
		t.Fatal("duplicate order was not retained")
	}
}

func TestParseSearchPrefersCleanNewznabGUIDAttribute(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>Klaus.2019.1080p.WEB-DL</title><guid>https://treasure-maps.com/details/0123456789abcdef0123456789abcdef</guid><link>https://treasure-maps.com/details/0123456789abcdef0123456789abcdef</link><newznab:attr name="guid" value="0123456789abcdef0123456789abcdef"/></item></channel></rss>`)
	items, err := ParseSearch(body, "", "movie", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].NativeGUID != "0123456789abcdef0123456789abcdef" || len(items[0].Fields) != 1 || items[0].Fields[0].Value != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("clean Newznab guid attribute was not preserved as native identity: %#v", items)
	}
}

func TestParseSearchDoesNotGuessBetweenConflictingGUIDAttributes(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>Movie.2026</title><guid>rss-native</guid><newznab:attr name="guid" value="attribute-one"/><newznab:attr name="guid" value="attribute-two"/></item></channel></rss>`)
	items, err := ParseSearch(body, "", "movie", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].NativeGUID != "rss-native" || len(items[0].Problems) != 1 {
		t.Fatalf("conflicting Newznab guid attributes were not held safely: %#v", items)
	}
}

func TestParseSearchCanonicalizesOnlyExplicitSubtitleEvidence(t *testing.T) {
	t.Parallel()
	body := []byte(`<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>Movie.2026</title><guid>native-subtitles</guid><newznab:attr name="language" value="Danish"/><newznab:attr name="subtitles" value="da-DK, en"/></item></channel></rss>`)
	items, err := ParseSearch(body, "", "movie", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("unexpected items: %#v", items)
	}
	var subtitleValues []string
	for _, fact := range items[0].Facts {
		if fact.Name == "subtitle_languages" {
			subtitleValues = fact.Values
		}
	}
	if len(subtitleValues) != 2 || subtitleValues[0] != "da" || subtitleValues[1] != "en" {
		t.Fatalf("unexpected explicit subtitle evidence: %#v", subtitleValues)
	}
}

func TestParseSearchCanonicalizesRepeatedProviderSubsWithoutChangingRawFields(t *testing.T) {
	t.Parallel()
	body := []byte(`<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>Show.S01.1080p.WEB-DL</title><guid>native-subs</guid><newznab:attr name="subs" value="English (US), Danish (DK)"/><newznab:attr name="SuBs" value="da-DK"/></item></channel></rss>`)
	items, err := ParseSearch(body, "", "tvsearch", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Fields) != 2 || items[0].Fields[0].Name != "subs" || items[0].Fields[0].Value != "English (US), Danish (DK)" || items[0].Fields[1].Name != "SuBs" || items[0].Fields[1].Value != "da-DK" {
		t.Fatalf("raw repeated subs evidence changed: %#v", items)
	}
	for _, fact := range items[0].Facts {
		if fact.Name == "subtitle_languages" {
			if !reflect.DeepEqual(fact.Values, []string{"da", "da"}) || !reflect.DeepEqual(fact.EvidenceAt, []int{0, 1}) || fact.State != releases.FactObserved {
				t.Fatalf("unexpected canonical subs fact: %#v", fact)
			}
			return
		}
	}
	t.Fatal("provider subs attribute did not produce subtitle_languages")
}

func TestParseSearchCanonicalizesReliableSeasonMetadataWithoutLosingRawEvidence(t *testing.T) {
	t.Parallel()
	body := []byte(`<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>Show.S01.2026.1080p.WEB-DL</title><guid>native-season</guid><newznab:attr name="season" value="S01"/></item></channel></rss>`)
	items, err := ParseSearch(body, "", "tvsearch", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Fields) != 1 || items[0].Fields[0].Value != "S01" {
		t.Fatalf("raw season evidence was not preserved: %#v", items)
	}
	items[0] = releases.InferTitleFacts(items[0], time.Unix(2, 0))
	for _, fact := range items[0].Facts {
		if fact.Name == "season_number" {
			if fact.State != releases.FactObserved || len(fact.Values) != 1 || fact.Values[0] != "1" {
				t.Fatalf("season fact was not canonical and consistent: %#v", fact)
			}
			return
		}
	}
	t.Fatal("canonical season fact was not emitted")
}

func TestParseSearchCanonicalizesReliableResolutionMetadataWithoutLosingRawEvidence(t *testing.T) {
	t.Parallel()
	body := []byte(`<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>Show.S01.1080p.WEB-DL</title><guid>native-resolution</guid><newznab:attr name="resolution" value="1920x1080 1.78:1"/></item></channel></rss>`)
	items, err := ParseSearch(body, "", "tvsearch", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Fields) != 1 || items[0].Fields[0].Value != "1920x1080 1.78:1" {
		t.Fatalf("raw resolution evidence was not preserved: %#v", items)
	}
	items[0] = releases.InferTitleFacts(items[0], time.Unix(2, 0))
	for _, fact := range items[0].Facts {
		if fact.Name == "resolution" {
			if fact.State != releases.FactObserved || len(fact.Values) != 1 || fact.Values[0] != "1080p" {
				t.Fatalf("resolution fact was not canonical and consistent: %#v", fact)
			}
			return
		}
	}
	t.Fatal("canonical resolution fact was not emitted")
}

func TestCanonicalResolutionRejectsAmbiguousValues(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"4K": "2160p", "UHD": "2160p", "3840x2160 1.78:1": "2160p",
		"1920 x 1080": "1080p", "1280×720": "720p", "720x576 PAL": "576p", "720x480": "480p",
	} {
		got, reliable := releases.CanonicalResolution(input)
		if !reliable || got != want {
			t.Fatalf("CanonicalResolution(%q)=(%q,%v), want (%q,true)", input, got, reliable, want)
		}
	}
	for _, input := range []string{"", "1080i", "2K", "1920x", "640x1080", "unknown"} {
		if got, reliable := releases.CanonicalResolution(input); reliable {
			t.Fatalf("CanonicalResolution(%q)=(%q,true), want unreliable", input, got)
		}
	}
}

func TestCanonicalSeasonNumberRejectsAmbiguousValues(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{"1": "1", "01": "1", "S001": "1", "s12": "12", "000": "0"} {
		got, reliable := canonicalSeasonNumber(input)
		if !reliable || got != want {
			t.Fatalf("canonicalSeasonNumber(%q)=(%q,%v), want (%q,true)", input, got, reliable, want)
		}
	}
	for _, input := range []string{"", "S", "Season 1", "1,2", "S0001", "-1"} {
		if got, reliable := canonicalSeasonNumber(input); reliable {
			t.Fatalf("canonicalSeasonNumber(%q)=(%q,true), want unreliable", input, got)
		}
	}
}

func TestProviderUnsupportedIsExplicit(t *testing.T) {
	_, err := ParseSearch([]byte(`<error code="203" description="No such function"/>`), "", "anime", time.Now())
	provider, ok := err.(ProviderError)
	if !ok || !provider.Unsupported {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseCapsPreservesDuplicateAdvertisedParams(t *testing.T) {
	body := []byte(`<caps><server title="Example" version="1"/><limits default="100" max="100"/><registration available="yes" open="no"/><searching><tv-search available="yes" supportedParams="q,tvdbid,tvdbid,season,ep"/></searching></caps>`)
	caps, err := ParseCaps(body, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(caps.Searches[1].Params) != 5 || caps.Searches[1].Params[1] != "tvdbid" || caps.Searches[1].Params[2] != "tvdbid" {
		t.Fatalf("duplicate params lost: %#v", caps.Searches[1].Params)
	}
}

func TestParseCapsDistinguishesSearchElementPresenceAndAvailability(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		search          string
		wantState       CAPSElementState
		legacyAvailable bool
	}{
		{name: "absent", wantState: CAPSElementAbsent},
		{name: "available yes", search: `<search available=" YeS "/>`, wantState: CAPSElementPresentAvailable, legacyAvailable: true},
		{name: "available no", search: `<search available=" NO "/>`, wantState: CAPSElementPresentUnavailable},
		{name: "availability missing", search: `<search/>`, wantState: CAPSElementPresentUninterpretable},
		{name: "availability empty", search: `<search available=" "/>`, wantState: CAPSElementPresentUninterpretable},
		{name: "availability invalid", search: `<search available="sometimes"/>`, wantState: CAPSElementPresentUninterpretable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot, err := ParseCaps([]byte(`<caps><searching>`+test.search+`</searching></caps>`), time.Unix(1, 0))
			if err != nil {
				t.Fatal(err)
			}
			wantLegacy := []SearchCapability{
				{Mode: "search", Available: test.legacyAvailable, Params: []string{}, State: CapabilityAdvertised},
				{Mode: "tv-search", Params: []string{}, State: CapabilityAdvertised},
				{Mode: "movie-search", Params: []string{}, State: CapabilityAdvertised},
				{Mode: "audio-search", Params: []string{}, State: CapabilityAdvertised},
				{Mode: "book-search", Params: []string{}, State: CapabilityAdvertised},
			}
			if !reflect.DeepEqual(snapshot.Searches, wantLegacy) {
				t.Fatalf("legacy searches changed: %#v, want %#v", snapshot.Searches, wantLegacy)
			}
			if len(snapshot.ModeObservations) != 3 || snapshot.ModeObservations[0].Mode != SearchModeGeneral || snapshot.ModeObservations[0].ElementState != test.wantState {
				t.Fatalf("mode observations=%#v, want general state %q", snapshot.ModeObservations, test.wantState)
			}
		})
	}
}

func TestParseCapsPreservesBoundedAdvertisedNamesAndRecognizesTypedSubset(t *testing.T) {
	t.Parallel()
	body := []byte(`<caps><searching><search available="TrUe" supportedParams="Q, imdbid, future.flag, Q, tvmazeid, EP"/></searching></caps>`)
	snapshot, err := ParseCaps(body, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	wantAdvertised := []string{"Q", "imdbid", "future.flag", "Q", "tvmazeid", "EP"}
	wantRecognized := []WireParameter{WireQuery, WireIMDbID, WireTVMazeID, WireEpisode}
	observation := snapshot.ModeObservations[0]
	if observation.ElementState != CAPSElementPresentAvailable || !reflect.DeepEqual(observation.AdvertisedParameterNames, wantAdvertised) || !reflect.DeepEqual(observation.RecognizedParameters, wantRecognized) {
		t.Fatalf("observation=%#v", observation)
	}
	if !reflect.DeepEqual(snapshot.Searches[0].Params, wantAdvertised) {
		t.Fatalf("legacy parameters changed: %#v", snapshot.Searches[0].Params)
	}
}

func TestParseCapsBoundsModeParameterEvidence(t *testing.T) {
	t.Parallel()
	maximum := make([]string, maxCAPSParameterNames)
	for index := range maximum {
		maximum[index] = "future" + strconv.Itoa(index)
	}
	tests := []struct {
		name           string
		parameters     string
		wantAdvertised int
		wantRecognized int
	}{
		{name: "maximum count", parameters: strings.Join(maximum, ","), wantAdvertised: maxCAPSParameterNames},
		{name: "over count", parameters: strings.Join(append(maximum, "overflow"), ","), wantAdvertised: maxCAPSParameterNames},
		{name: "maximum name length", parameters: "a" + strings.Repeat("b", 63), wantAdvertised: 1},
		{name: "over name length", parameters: "a" + strings.Repeat("b", 64)},
		{name: "invalid name preserves valid evidence", parameters: "q,bad parameter", wantAdvertised: 1, wantRecognized: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot, err := ParseCaps([]byte(`<caps><searching><search available="yes" supportedParams="`+test.parameters+`"/></searching></caps>`), time.Unix(1, 0))
			if err != nil {
				t.Fatal(err)
			}
			observation := snapshot.ModeObservations[0]
			if observation.ElementState != CAPSElementPresentAvailable || len(observation.AdvertisedParameterNames) != test.wantAdvertised || len(observation.RecognizedParameters) != test.wantRecognized {
				t.Fatalf("observation state=%q advertised=%d recognized=%d", observation.ElementState, len(observation.AdvertisedParameterNames), len(observation.RecognizedParameters))
			}
		})
	}
}

func TestValidateNZBRequiresParsedDocumentWithFilesAndSegments(t *testing.T) {
	valid := []byte(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file poster="owner"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="12" number="1">message@example</segment></segments></file></nzb>`)
	if err := validateNZB(valid); err != nil {
		t.Fatalf("valid NZB rejected: %v", err)
	}
	invalid := [][]byte{
		[]byte(`<?xml version="1.0"?><html><!-- <nzb><file><segment>fake</segment></file></nzb> --></html>`),
		[]byte(`<nzb><file></nzb>`),
		[]byte(`<nzb></nzb>`),
	}
	for _, body := range invalid {
		if err := validateNZB(body); err == nil {
			t.Fatalf("invalid NZB accepted: %s", body)
		}
	}
}

func TestPackageIdentityUsesOnlyTheOrderedUsenetSegmentManifest(t *testing.T) {
	t.Parallel()
	first := []byte(`<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><head><meta type="title">Provider title</meta></head><file poster="poster-a" subject="first subject"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="12" number="1"> message-one@example </segment><segment bytes="34" number="2">message-two@example</segment></segments></file></nzb>`)
	second := []byte(`<nzb>
	<head><meta type="title">Different provider title</meta></head>
	<file poster="poster-b" subject="different subject"><groups><group>another.group</group></groups><segments>
		<segment number="1" bytes="12">message-one@example</segment>
		<segment number="2" bytes="34">message-two@example</segment>
	</segments></file></nzb>`)
	firstIdentity, err := PackageIdentity(first)
	if err != nil {
		t.Fatal(err)
	}
	secondIdentity, err := PackageIdentity(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstIdentity != secondIdentity {
		t.Fatal("provider metadata or XML formatting changed exact package identity")
	}
}

func TestPackageIdentityChangesWithManifestContentOrBoundaries(t *testing.T) {
	t.Parallel()
	base := []byte(`<nzb><file><segments><segment bytes="12" number="1">one@example</segment><segment bytes="34" number="2">two@example</segment></segments></file></nzb>`)
	changed := [][]byte{
		[]byte(`<nzb><file><segments><segment bytes="13" number="1">one@example</segment><segment bytes="34" number="2">two@example</segment></segments></file></nzb>`),
		[]byte(`<nzb><file><segments><segment bytes="12" number="2">one@example</segment><segment bytes="34" number="1">two@example</segment></segments></file></nzb>`),
		[]byte(`<nzb><file><segments><segment bytes="12" number="1">different@example</segment><segment bytes="34" number="2">two@example</segment></segments></file></nzb>`),
		[]byte(`<nzb><file><segments><segment bytes="34" number="2">two@example</segment><segment bytes="12" number="1">one@example</segment></segments></file></nzb>`),
		[]byte(`<nzb><file><segments><segment bytes="12" number="1">one@example</segment></segments></file><file><segments><segment bytes="34" number="2">two@example</segment></segments></file></nzb>`),
	}
	want, err := PackageIdentity(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range changed {
		got, parseErr := PackageIdentity(body)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if got == want {
			t.Fatalf("different manifest produced the same identity: %s", body)
		}
	}
}

func TestPackageIdentityRejectsIncompleteSegmentEvidence(t *testing.T) {
	t.Parallel()
	invalid := [][]byte{
		[]byte(`<nzb><file><segments><segment number="1">one@example</segment></segments></file></nzb>`),
		[]byte(`<nzb><file><segments><segment bytes="12">one@example</segment></segments></file></nzb>`),
		[]byte(`<nzb><file><segments><segment bytes="0" number="1">one@example</segment></segments></file></nzb>`),
		[]byte(`<nzb><file><segments><segment bytes="12" number="1"> </segment></segments></file></nzb>`),
		[]byte(`<nzb><segment bytes="12" number="1">one@example</segment></nzb>`),
	}
	for _, body := range invalid {
		if _, err := PackageIdentity(body); err == nil {
			t.Fatalf("incomplete manifest evidence accepted: %s", body)
		}
	}
}
