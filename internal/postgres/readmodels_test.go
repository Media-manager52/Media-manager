package postgres

import (
	"encoding/json"
	"testing"
)

func TestIntegrationConfigurationStateDistinguishesDisabledFromUncommissioned(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		kind    string
		enabled bool
		config  json.RawMessage
		want    string
	}{
		{name: "enabled newznab is configured", kind: "newznab", enabled: true, config: json.RawMessage(`{"mode_verified":true}`), want: "configured"},
		{name: "commissioned then disabled is disabled", kind: "newznab", enabled: false, config: json.RawMessage(`{"mode_verified":true}`), want: "disabled"},
		{name: "never commissioned is awaiting commission", kind: "newznab", enabled: false, config: json.RawMessage(`{"mode_verified":false}`), want: "awaiting_commission"},
		{name: "missing config is awaiting commission", kind: "newznab", enabled: false, config: json.RawMessage(`{}`), want: "awaiting_commission"},
		{name: "invalid config is awaiting commission", kind: "newznab", enabled: false, config: json.RawMessage(`not-json`), want: "awaiting_commission"},
		{name: "disabled non-newznab is disabled", kind: "plex", enabled: false, config: json.RawMessage(`{}`), want: "disabled"},
		{name: "enabled non-newznab is configured", kind: "altmount", enabled: true, config: json.RawMessage(`{}`), want: "configured"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := integrationConfigurationState(test.kind, test.enabled, test.config); got != test.want {
				t.Fatalf("integrationConfigurationState(%q,%v,%s)=%q, want %q", test.kind, test.enabled, test.config, got, test.want)
			}
		})
	}
}

func TestManagedPublicationPathStaysInsideLibraryRoot(t *testing.T) {
	tests := []struct {
		name                string
		origin              string
		plexVisibleTarget   string
		targetInsideLibrary bool
		want                string
	}{
		{
			name:                "contained target keeps the Plex-visible file",
			origin:              "altmount",
			plexVisibleTarget:   "/mnt/media-manager/libraries/movies/Movie (2020)/Movie (2020).mkv",
			targetInsideLibrary: true,
			want:                "/mnt/media-manager/libraries/movies/Movie (2020)/Movie (2020).mkv",
		},
		{
			name:                "escaping target reports the managed directory",
			origin:              "altmount",
			plexVisibleTarget:   "/mnt/media-manager/libraries/movies/Movie (2020)/Movie (2020).mkv",
			targetInsideLibrary: false,
			want:                "/mnt/media-manager/libraries/movies/Movie (2020)",
		},
		{
			name:                "adoption path is unchanged",
			origin:              "adoption",
			plexVisibleTarget:   "/mnt/media-manager/libraries/movies/Movie (2020)/Movie (2020).mkv",
			targetInsideLibrary: false,
			want:                "/mnt/media-manager/libraries/movies/Movie (2020)/Movie (2020).mkv",
		},
		{
			name:                "empty target is unchanged",
			origin:              "altmount",
			plexVisibleTarget:   "",
			targetInsideLibrary: false,
			want:                "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := managedPublicationPath(test.origin, test.plexVisibleTarget, test.targetInsideLibrary); got != test.want {
				t.Fatalf("managedPublicationPath()=%q, want %q", got, test.want)
			}
		})
	}
}
