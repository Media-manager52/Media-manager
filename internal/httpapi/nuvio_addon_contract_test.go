package httpapi

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/acquisition"
	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

// The Stremio/Nuvio addon (private repo Media-manager52/stremio-media-manager)
// runs beside Media Manager and talks to it only through this API with the
// player token. These lists are the exact routes and JSON fields it uses, so a
// change here that would break playback, catalogs or requests fails CI before
// it is deployed. Removing an entry is a breaking change for the addon: change
// the addon first, then this list.

// nuvioAddonRoutes are the routes the addon calls.
var nuvioAddonRoutes = []string{
	"GET /api/v1/media",
	"POST /api/v1/media",
	"GET /api/v1/media/{id}/detail",
	"POST /api/v1/media/{id}/workflows",
	"GET /api/v1/files/{id}",
	"GET /api/v1/discovery/search",
	"GET /api/v1/discovery/{kind}/{id}",
	"POST /api/v1/streaming/search",
	"POST /api/v1/streaming/releases/{id}/nzb",
	"POST /api/v1/streaming/releases/{id}/prepare",
}

var addonMediaFields = []string{
	"id", "kind", "title", "year", "created_at",
	"identities.namespace", "identities.value", "identities.state", "identities.source",
	"metadata.overview", "metadata.poster_url", "metadata.genres", "metadata.keywords",
	"metadata.countries", "metadata.networks.name", "metadata.companies.name",
	"metadata.original_language", "metadata.release_date", "metadata.runtime_minutes",
	"metadata.certification", "metadata.status",
	"metadata.ratings.source", "metadata.ratings.value", "metadata.ratings.votes", "metadata.ratings.provenance",
}

var addonFileFields = []string{
	"id", "media_id", "media_kind", "title", "destination_relative", "source_relative",
	"state", "variant", "origin", "quality_profile", "source_size_bytes",
	"quality_evidence", "quality_evidence_state",
}

func prefixed(prefix string, fields []string) []string {
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		result = append(result, prefix+"."+field)
	}
	return result
}

// nuvioAddonPayloads maps a route to the Go type of its JSON (the "data" of a
// response, or a request body) and the field paths the addon reads or sends.
var nuvioAddonPayloads = []struct {
	route  string
	what   string
	value  any
	fields []string
}{
	{"GET /api/v1/media", "response item", catalog.MediaItem{}, addonMediaFields},
	{"GET /api/v1/media/{id}/detail", "response", postgres.MediaDetail{}, append(append(prefixed("media", addonMediaFields),
		"workflows.id", "workflows.variant", "workflows.state", "workflows.attention_detail", "workflows.terminal_at",
		"seasons.number", "seasons.state", "seasons.workflow_state", "seasons.waiting_reason"),
		prefixed("files", addonFileFields)...)},
	{"GET /api/v1/files/{id}", "response", postgres.ManagedFile{}, addonFileFields},
	{"GET /api/v1/discovery/search", "response", seerr.SearchPage{}, []string{"results.id", "results.mediaType"}},
	{"GET /api/v1/discovery/{kind}/{id}", "response", discoveryDetail{}, []string{
		"kind", "title", "year", "tmdb_id", "metadata", "identities.namespace", "identities.value", "identities.state", "identities.source",
		"season.number", "season.episodes.number", "season.episodes.provider_id", "season.episodes.title", "season.episodes.air_date",
	}},
	{"POST /api/v1/streaming/search", "response", acquisition.BrowseResult{}, []string{
		"releases.id", "releases.title", "releases.indexer", "releases.facts.name", "releases.facts.values", "releases.facts.state",
		"providers.name", "providers.state",
	}},
	{"POST /api/v1/streaming/releases/{id}/prepare", "response", acquisition.PreparedStream{}, []string{"path"}},
	{"POST /api/v1/media", "request body", createMediaInput{}, []string{
		"kind", "title", "year", "identities", "search_immediately",
		"seasons.number", "seasons.monitored", "seasons.search_immediately", "seasons.prefer_season_pack",
		"seasons.episodes.number", "seasons.episodes.provider_episode_id", "seasons.episodes.title", "seasons.episodes.air_date",
	}},
	{"POST /api/v1/media/{id}/workflows", "request body", createMediaWorkflowInput{}, []string{"season", "search_immediately"}},
}

func TestNuvioAddonRoutesStayOpenToThePlayerToken(t *testing.T) {
	server, err := New(Config{OwnerToken: strings.Repeat("o", 32), PlayerToken: strings.Repeat("p", 32), Version: "test"}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	operations := map[string]apiOperation{}
	for _, operation := range server.apiOperations() {
		operations[operation.Method+" "+operation.Path] = operation
	}
	for _, route := range nuvioAddonRoutes {
		operation, exists := operations[route]
		if !exists {
			t.Errorf("%s is gone; the Stremio/Nuvio addon calls it", route)
			continue
		}
		if operation.Auth != authOwnerOrPlayer {
			t.Errorf("%s no longer accepts the player token (auth %q); the Stremio/Nuvio addon calls it with that token", route, operation.Auth)
		}
	}
	for _, payload := range nuvioAddonPayloads {
		if _, exists := operations[payload.route]; !exists {
			t.Errorf("payload contract names unknown route %s", payload.route)
		}
	}
	// The request bodies above are the ones the catalog decodes, so a renamed
	// input type is caught as well.
	for _, payload := range nuvioAddonPayloads {
		if payload.what != "request body" {
			continue
		}
		if body := operations[payload.route].Body; reflect.TypeOf(body) != reflect.TypeOf(payload.value) {
			t.Errorf("%s decodes %T, the contract checks %T", payload.route, body, payload.value)
		}
	}
}

func TestNuvioAddonFieldsStayInTheirPayloads(t *testing.T) {
	for _, payload := range nuvioAddonPayloads {
		for _, field := range payload.fields {
			if problem := jsonFieldProblem(reflect.TypeOf(payload.value), field); problem != "" {
				t.Errorf("%s %s: field %q %s; the Stremio/Nuvio addon uses it", payload.route, payload.what, field, problem)
			}
		}
	}
}

// The addon reads quality_evidence in both shapes Media Manager stores: the
// output inspection (facts + subtitle_tracks) and release facts (a list).
func TestNuvioAddonQualityEvidenceShapes(t *testing.T) {
	if problem := jsonFieldProblem(reflect.TypeOf(releases.Fact{}), "name"); problem != "" {
		t.Errorf("release fact name %s", problem)
	}
	for _, field := range []string{"values", "state"} {
		if problem := jsonFieldProblem(reflect.TypeOf(releases.Fact{}), field); problem != "" {
			t.Errorf("release fact %s %s", field, problem)
		}
	}
	var file postgres.ManagedFile
	if reflect.TypeOf(file.QualityEvidence) != reflect.TypeOf(json.RawMessage(nil)) {
		t.Errorf("quality_evidence is %T; check the addon's fileQualityEvidence decoder", file.QualityEvidence)
	}
}

// jsonFieldProblem walks a dotted JSON path through Go types by their json
// tags (slices and pointers are stepped through). It returns "" when the path
// exists, or why it does not.
func jsonFieldProblem(current reflect.Type, path string) string {
	for _, name := range strings.Split(path, ".") {
		for current.Kind() == reflect.Pointer || current.Kind() == reflect.Slice || current.Kind() == reflect.Array {
			if current == reflect.TypeOf(json.RawMessage(nil)) {
				return "is inside raw JSON that cannot be checked"
			}
			current = current.Elem()
		}
		if current.Kind() != reflect.Struct {
			return "is inside " + current.String() + ", not an object with named fields"
		}
		field, found := jsonField(current, name)
		if !found {
			return "does not exist in " + current.String()
		}
		current = field
	}
	return ""
}

func jsonField(structType reflect.Type, name string) (reflect.Type, bool) {
	for index := 0; index < structType.NumField(); index++ {
		field := structType.Field(index)
		tag := strings.Split(field.Tag.Get("json"), ",")[0]
		if tag == "-" {
			continue
		}
		if field.Anonymous && tag == "" {
			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				if found, ok := jsonField(embedded, name); ok {
					return found, true
				}
			}
			continue
		}
		if tag == "" {
			tag = field.Name
		}
		if tag == name && field.IsExported() {
			return field.Type, true
		}
	}
	return nil, false
}
