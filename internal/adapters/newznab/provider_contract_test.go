package newznab

import (
	"strings"
	"testing"
)

func TestProviderContractRegistryRequiresExactCanonicalKeys(t *testing.T) {
	t.Parallel()
	canonicalKeys := []string{
		ProviderContractNZBFinderV1,
		ProviderContractPath32GetNZBV1,
		ProviderContractDrunkenSlugV1,
	}
	for _, key := range canonicalKeys {
		if !ValidProviderContractKey(key) {
			t.Errorf("canonical provider-contract key %q was rejected", key)
		}
	}

	invalidKeys := []struct {
		name string
		key  string
	}{
		{name: "empty", key: ""},
		{name: "leading ASCII space", key: " " + ProviderContractNZBFinderV1},
		{name: "trailing ASCII space", key: ProviderContractNZBFinderV1 + " "},
		{name: "leading tab", key: "\t" + ProviderContractNZBFinderV1},
		{name: "trailing tab", key: ProviderContractNZBFinderV1 + "\t"},
		{name: "leading newline", key: "\n" + ProviderContractNZBFinderV1},
		{name: "trailing newline", key: ProviderContractNZBFinderV1 + "\n"},
		{name: "carriage return", key: ProviderContractNZBFinderV1 + "\r"},
		{name: "CRLF", key: ProviderContractNZBFinderV1 + "\r\n"},
		{name: "non-breaking space", key: "\u00a0" + ProviderContractNZBFinderV1},
		{name: "Unicode em space", key: ProviderContractNZBFinderV1 + "\u2003"},
		{name: "excessive leading padding", key: strings.Repeat(" ", 4096) + ProviderContractNZBFinderV1},
		{name: "excessive trailing padding", key: ProviderContractNZBFinderV1 + strings.Repeat(" ", 4096)},
		{name: "whitespace only", key: " \t\r\n"},
		{name: "embedded newline", key: "nzb\nfinder"},
		{name: "embedded NUL", key: "nzb\x00finder"},
		{name: "invalid UTF-8", key: string([]byte("nzbfinder\xff"))},
		{name: "wrong case", key: "NZBFINDER"},
		{name: "unknown", key: "unknown.contract"},
	}
	for _, test := range invalidKeys {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if ValidProviderContractKey(test.key) {
				t.Fatal("noncanonical provider-contract key was accepted")
			}
		})
	}
}

func TestProviderContractEndpointRegistryRequiresExactReviewedOrigin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		reference ProviderContractRef
		endpoint  string
		want      bool
	}{
		{name: "NZBFinder canonical HTTPS", reference: ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 1}, endpoint: "https://NZBFinder.ws:443/api", want: true},
		{name: "path32 known origin", reference: ProviderContractRef{Key: ProviderContractPath32GetNZBV1, Version: 1}, endpoint: "https://api.nzb.life/api", want: true},
		{name: "DrunkenSlug canonical HTTPS", reference: ProviderContractRef{Key: ProviderContractDrunkenSlugV1, Version: 1}, endpoint: "https://drunkenslug.com/api", want: true},
		{name: "wrong origin", reference: ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 1}, endpoint: "https://provider.invalid/api"},
		{name: "wrong contract", reference: ProviderContractRef{Key: ProviderContractDrunkenSlugV1, Version: 1}, endpoint: "https://nzbfinder.ws/api"},
		{name: "nonstandard port", reference: ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 1}, endpoint: "https://nzbfinder.ws:444/api"},
		{name: "insecure scheme", reference: ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 1}, endpoint: "http://nzbfinder.ws/api"},
		{name: "userinfo", reference: ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 1}, endpoint: "https://owner@nzbfinder.ws/api"},
		{name: "unknown version", reference: ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 2}, endpoint: "https://nzbfinder.ws/api"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ProviderContractAcceptsEndpoint(test.reference, test.endpoint); got != test.want {
				t.Fatalf("accepted=%v, want %v", got, test.want)
			}
		})
	}
}
