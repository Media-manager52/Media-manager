package newznab

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/releases"
)

func TestSanitizeProviderValueRemovesCredentialQueriesAndPreservesMetadata(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain", input: "https://provider.test/api?t=details&apikey=plain-secret&id=42", want: "https://provider.test/api?t=details&id=42"},
		{name: "mixed case repeated", input: "https://provider.test/api?ApiKey=one&ticket=keep&API_KEY=two&keep=yes", want: "https://provider.test/api?ticket=keep&keep=yes"},
		{name: "encoded key malformed value", input: "https://provider.test/api?%61piKey=%zz-secret&ticket=keep", want: "https://provider.test/api?ticket=keep"},
		{name: "percent encoded URL", input: "https%3A%2F%2Fprovider.test%2Fapi%3FAPIKEY%3Dencoded-secret%26ticket%3Done", want: "https%3A%2F%2Fprovider.test%2Fapi%3Fticket%3Done"},
		{name: "double encoded URL", input: "https%253A%252F%252Fprovider.test%252Fapi%253Fapikey%253Ddouble-secret%2526ticket%253Dtwo", want: "https%253A%252F%252Fprovider.test%252Fapi%253Fticket%253Dtwo"},
		{name: "nested URL", input: "https://outer.test/get?redirect=https%3A%2F%2Finner.test%2Ffile%3Faccess_token%3Dnested-secret%26ticket%3Ddownload&ticket=outer", want: "https://outer.test/get?redirect=https%3A%2F%2Finner.test%2Ffile%3Fticket%3Ddownload&ticket=outer"},
		{name: "plain query", input: "apikey=plain-secret&ticket=keep&offset=20", want: "ticket=keep&offset=20"},
		{name: "fragment preserved", input: "https://provider.test/api?password=secret&ticket=keep#section", want: "https://provider.test/api?ticket=keep#section"},
		{name: "benign unchanged", input: "https://provider.test/api?ticket=keep&offset=20&redirect_id=abc", want: "https://provider.test/api?ticket=keep&offset=20&redirect_id=abc"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := SanitizeProviderValue(test.input)
			if got != test.want {
				t.Fatalf("sanitized value=%q want=%q", got, test.want)
			}
			if repeated := SanitizeProviderValue(got); repeated != got {
				t.Fatalf("sanitization is not idempotent: first=%q second=%q", got, repeated)
			}
		})
	}
}

func TestSanitizeRawFieldsPreservesOrderAndDetachesInput(t *testing.T) {
	t.Parallel()
	secret := "multi-field-secret"
	fields := []releases.RawField{
		{Name: "info", Namespace: "newznab", Value: "https://one.test/api?apikey=" + secret + "&ticket=one", Ordinal: 0},
		{Name: "comments", Value: "https://two.test/view?TOKEN=" + secret + "&page=2", Ordinal: 1},
		{Name: "group", Value: "release-group", Ordinal: 2},
	}
	sanitized := SanitizeRawFields(fields)
	if len(sanitized) != len(fields) || sanitized[0].Name != "info" || sanitized[1].Ordinal != 1 || sanitized[2].Value != "release-group" {
		t.Fatalf("provider metadata changed: %#v", sanitized)
	}
	if strings.Contains(sanitized[0].Value, secret) || strings.Contains(sanitized[1].Value, secret) || sanitized[0].Value != "https://one.test/api?ticket=one" || sanitized[1].Value != "https://two.test/view?page=2" {
		t.Fatalf("credential-bearing fields were not sanitized: %#v", sanitized)
	}
	if fields[0].Value == sanitized[0].Value {
		t.Fatal("sanitizer mutated or aliased caller evidence")
	}
}

func TestSanitizeObservationWithCredentialRemovesProviderSpecificAliases(t *testing.T) {
	t.Parallel()
	credential := "provider-specific-secret"
	observation := releases.Observation{Fields: []releases.RawField{
		{Name: "info", Value: "https://drunkenslug.example/details?id=42&r=" + credential + "&ticket=kept", Ordinal: 0},
		{Name: "nested", Value: "https://outer.test/get?redirect=https%3A%2F%2Fdrunkenslug.example%2Fdetails%3Fr%3D" + credential + "%26ticket%3Dinner&ticket=outer", Ordinal: 1},
		{Name: "benign-r", Value: "https://other.test/details?r=ordinary-metadata&ticket=other", Ordinal: 2},
	}}
	sanitized := SanitizeObservationWithCredential(observation, credential)
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), credential) {
		t.Fatal("provider-specific credential alias remained")
	}
	if sanitized.Fields[0].Value != "https://drunkenslug.example/details?id=42&ticket=kept" || sanitized.Fields[1].Value != "https://outer.test/get?redirect=https%3A%2F%2Fdrunkenslug.example%2Fdetails%3Fticket%3Dinner&ticket=outer" || sanitized.Fields[2].Value != observation.Fields[2].Value {
		t.Fatalf("provider-specific sanitization changed benign metadata: %#v", sanitized.Fields)
	}
}

func TestParseSearchNeverReturnsCredentialMaterial(t *testing.T) {
	t.Parallel()
	secret := "parser-secret-must-not-escape"
	body := []byte(`<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><item><title>Safe.Release.2026</title><guid>stable-native-id</guid><link>https://provider.test/get?apikey=` + secret + `&amp;ticket=download</link><newznab:attr name="info" value="https://provider.test/details?ApiKey=` + secret + `&amp;ticket=evidence"/><newznab:attr name="comments" value="https%3A%2F%2Fprovider.test%2Fcomments%3Ftoken%3D` + secret + `%26page%3D2"/></item></channel></rss>`)
	observations, err := ParseSearch(body, "", "search", time.Now())
	if err != nil || len(observations) != 1 {
		t.Fatalf("observations=%d err=%v", len(observations), err)
	}
	encoded, err := json.Marshal(observations[0])
	if err != nil {
		t.Fatal(err)
	}
	combined := string(encoded) + observations[0].DownloadURL
	if strings.Contains(combined, secret) || strings.Contains(strings.ToLower(combined), "apikey=") || strings.Contains(strings.ToLower(combined), "token%3d") {
		t.Fatal("parsed observation retained credential material")
	}
	if observations[0].DownloadURL != "https://provider.test/get?ticket=download" {
		t.Fatalf("download URL metadata=%q", observations[0].DownloadURL)
	}
}

func TestProviderErrorsDoNotExposeProviderDescriptionOrCode(t *testing.T) {
	t.Parallel()
	secret := "provider-error-secret"
	_, err := ParseSearch([]byte(`<error code="apikey=`+secret+`" description="failed https://provider.test/api?apikey=`+secret+`"/>`), "", "search", time.Now())
	if err == nil {
		t.Fatal("expected provider error")
	}
	provider, ok := err.(ProviderError)
	if !ok {
		t.Fatalf("error type=%T", err)
	}
	encoded, marshalErr := json.Marshal(provider)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, exposed := range []string{err.Error(), string(encoded), provider.Description} {
		if strings.Contains(exposed, secret) || strings.Contains(exposed, "provider.test") {
			t.Fatal("provider error exposed untrusted credential-bearing detail")
		}
	}
}

func TestSanitizeObservationAndEvaluationRemoveProviderURLsFromDurableShapes(t *testing.T) {
	t.Parallel()
	secret := "durable-shape-secret"
	observation := releases.Observation{
		NativeGUID:  "https://provider.test/details/id?apikey=" + secret + "&ticket=native",
		Title:       "Safe.Release.2026",
		DownloadURL: "https://provider.test/get?token=" + secret + "&ticket=download",
		Fields:      []releases.RawField{{Name: "info", Value: "https://provider.test/info?password=" + secret + "&ticket=evidence"}},
		Facts:       []releases.Fact{{Name: "group", Values: []string{"https://provider.test/group?client_secret=" + secret + "&name=safe"}}},
	}
	evaluation := releases.Evaluation{Explanations: []releases.Explanation{{Rule: "provider evidence", Fact: "info", Values: []string{"https://provider.test/value?apikey=" + secret + "&ticket=decision"}, Reason: "safe reason"}}}
	sanitizedObservation := SanitizeObservation(observation)
	sanitizedEvaluation := SanitizeEvaluation(evaluation)
	encoded, err := json.Marshal(struct {
		Observation releases.Observation `json:"observation"`
		Evaluation  releases.Evaluation  `json:"evaluation"`
	}{sanitizedObservation, sanitizedEvaluation})
	if err != nil {
		t.Fatal(err)
	}
	combined := string(encoded) + sanitizedObservation.DownloadURL
	if strings.Contains(combined, secret) || strings.Contains(strings.ToLower(combined), "apikey=") || strings.Contains(strings.ToLower(combined), "client_secret=") {
		t.Fatal("durable observation or evaluation shape retained credential material")
	}
	for _, benign := range []string{"ticket=native", "ticket=download", "ticket=evidence", "name=safe", "ticket=decision"} {
		if !strings.Contains(combined, benign) {
			t.Fatalf("benign provider metadata %q was not preserved", benign)
		}
	}
}
