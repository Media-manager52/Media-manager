package commissioningv2

import (
	"encoding/base64"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestExactSecretGuardDetectsConfiguredRepresentations(t *testing.T) {
	t.Parallel()
	credential := "S3cr et+/="
	query := url.QueryEscape(credential)
	path := url.PathEscape(credential)
	fullyEncoded := percentEncodeEveryByte([]byte(credential))
	nested := strings.ReplaceAll(strings.ReplaceAll(query, "%", "%25"), "%", "%25")
	basic := base64.StdEncoding.EncodeToString([]byte("owner:" + credential))
	use := SecretWireUse{QueryParameter: true, PathParameter: true, Bearer: true, BasicUsername: "owner"}
	for _, test := range []struct {
		name     string
		material ProviderMaterial
	}{
		{"raw", ProviderMaterial{Context: MaterialRaw, Value: []byte("before " + credential + " after")}},
		{"query encoding", ProviderMaterial{Context: MaterialRaw, Value: []byte(query)}},
		{"path encoding", ProviderMaterial{Context: MaterialRaw, Value: []byte(path)}},
		{"fully encoded", ProviderMaterial{Context: MaterialRaw, Value: []byte(fullyEncoded)}},
		{"nested percent", ProviderMaterial{Context: MaterialRaw, Value: []byte(nested)}},
		{"query parameter", ProviderMaterial{Context: MaterialQuery, Value: []byte("apikey=" + query)}},
		{"authorization bearer", ProviderMaterial{Context: MaterialHeader, Value: []byte("Authorization: Bearer " + credential)}},
		{"authorization basic", ProviderMaterial{Context: MaterialHeader, Value: []byte("Authorization: Basic " + basic)}},
		{"decoded XML entity value", ProviderMaterial{Context: MaterialXMLDecoded, Value: []byte("title=" + credential)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := InspectExactSecret(credential, use, test.material)
			if !errors.Is(err, ErrExactSecretDetected) {
				t.Fatalf("got %v", err)
			}
			if strings.Contains(err.Error(), credential) || strings.Contains(err.Error(), string(test.material.Value)) {
				t.Fatal("safe error disclosed secret or material")
			}
		})
	}
}

func TestExactSecretGuardUsesQueryPlusSemanticsOnlyForQueryMaterial(t *testing.T) {
	t.Parallel()
	credential := "alpha beta"
	encoded := []byte("value=alpha+beta")
	if err := InspectExactSecret(credential, SecretWireUse{}, ProviderMaterial{Context: MaterialRaw, Value: encoded}); err != nil {
		t.Fatalf("raw material unexpectedly used query-form decoding: %v", err)
	}
	if err := InspectExactSecret(credential, SecretWireUse{QueryParameter: true}, ProviderMaterial{Context: MaterialQuery, Value: encoded}); !errors.Is(err, ErrExactSecretDetected) {
		t.Fatalf("query material failed exact query decoding: %v", err)
	}
	malformedPrefix := []byte("junk%&value=alpha+beta")
	if err := InspectExactSecret(credential, SecretWireUse{QueryParameter: true}, ProviderMaterial{Context: MaterialQuery, Value: malformedPrefix}); !errors.Is(err, ErrExactSecretDetected) {
		t.Fatalf("literal malformed percent suppressed exact query inspection: %v", err)
	}
	if err := InspectExactSecret(credential, SecretWireUse{}, ProviderMaterial{Context: MaterialQuery, Value: []byte("value=alpha%2Bbeta")}); err != nil {
		t.Fatalf("unconfigured query encoding was inferred: %v", err)
	}
	if err := InspectExactSecret(credential, SecretWireUse{QueryParameter: true}, ProviderMaterial{Context: MaterialQuery, Value: []byte("value=alpha%2Bbeta")}); !errors.Is(err, ErrExactSecretDetected) {
		t.Fatalf("nested query-escaped secret was missed: %v", err)
	}
	if err := InspectExactSecret(credential, SecretWireUse{QueryParameter: true}, ProviderMaterial{Context: MaterialQuery, Value: []byte("value=alpha%2bbeta")}); !errors.Is(err, ErrExactSecretDetected) {
		t.Fatalf("lowercase nested query escape was missed: %v", err)
	}
	if err := InspectExactSecret(credential, SecretWireUse{QueryParameter: true}, ProviderMaterial{Context: MaterialQuery, Value: []byte("value=alpha%252Bbeta")}); !errors.Is(err, ErrExactSecretDetected) {
		t.Fatalf("repeated query escape was missed: %v", err)
	}
	if err := InspectExactSecret(credential, SecretWireUse{QueryParameter: true}, ProviderMaterial{Context: MaterialQuery, Value: []byte("value=%61lpha+beta")}); !errors.Is(err, ErrExactSecretDetected) {
		t.Fatalf("mixed query encoding was missed: %v", err)
	}
}

func TestExactSecretGuardDoesNotGuessUnknownSecrets(t *testing.T) {
	t.Parallel()
	credential := "known-value"
	for _, safe := range []string{
		"another-high-entropy-value-1234567890",
		"Authorization: Bearer unrelated",
		"apikey=unrelated",
		"Movies 100% complete",
		"normal Unicode ÆØÅ",
	} {
		if err := InspectExactSecret(credential, SecretWireUse{QueryParameter: true}, ProviderMaterial{Context: MaterialRaw, Value: []byte(safe)}); err != nil {
			t.Fatalf("heuristically rejected %q: %v", safe, err)
		}
	}
}

func TestSecretRepresentationConstructionIsBoundedAndDeterministic(t *testing.T) {
	t.Parallel()
	use := SecretWireUse{QueryParameter: true, PathParameter: true, Bearer: true, BasicUsername: "owner"}
	first := exactSecretRepresentations("s e+c/r=e?t", use)
	second := exactSecretRepresentations("s e+c/r=e?t", use)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("representation construction is nondeterministic")
	}
	if len(first) == 0 || len(first) > maximumSecretRepresentations {
		t.Fatalf("representation count=%d", len(first))
	}
	seen := make(map[string]struct{}, len(first))
	for _, value := range first {
		if _, duplicate := seen[string(value)]; duplicate {
			t.Fatal("duplicate representation")
		}
		seen[string(value)] = struct{}{}
	}
}

func TestExactSecretGuardRejectsInvalidOrUnboundedInput(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		credential string
		use        SecretWireUse
		materials  []ProviderMaterial
	}{
		{"", SecretWireUse{}, nil},
		{"bad\nsecret", SecretWireUse{}, nil},
		{"secret", SecretWireUse{BasicUsername: "bad\nuser"}, nil},
		{"secret", SecretWireUse{}, []ProviderMaterial{{Context: "invented", Value: []byte("safe")}}},
	} {
		if err := InspectExactSecret(test.credential, test.use, test.materials...); !errors.Is(err, ErrInvalidSecretGuardInput) {
			t.Fatalf("got %v", err)
		}
	}
	tooMany := make([]ProviderMaterial, maximumProviderMaterials+1)
	for index := range tooMany {
		tooMany[index] = ProviderMaterial{Context: MaterialRaw, Value: []byte("safe")}
	}
	if err := InspectExactSecret("secret", SecretWireUse{}, tooMany...); !errors.Is(err, ErrInvalidSecretGuardInput) {
		t.Fatalf("unbounded material count got %v", err)
	}
}

func TestNoConfiguredBasicRepresentationIsInvented(t *testing.T) {
	t.Parallel()
	credential := "basic-secret"
	basic := base64.StdEncoding.EncodeToString([]byte("owner:" + credential))
	material := ProviderMaterial{Context: MaterialHeader, Value: []byte("Basic " + basic)}
	if err := InspectExactSecret(credential, SecretWireUse{}, material); err != nil {
		t.Fatalf("unconfigured Basic representation was inferred: %v", err)
	}
	if err := InspectExactSecret(credential, SecretWireUse{BasicUsername: "owner"}, material); !errors.Is(err, ErrExactSecretDetected) {
		t.Fatalf("configured Basic representation was missed: %v", err)
	}
}
