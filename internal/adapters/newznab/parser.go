package newznab

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/releases"
)

type CapabilityState string

const (
	CapabilityAdvertised CapabilityState = "advertised"
	CapabilityUnknown    CapabilityState = "unknown"
)

type SearchCapability struct {
	Mode      string          `json:"mode"`
	Available bool            `json:"available"`
	Params    []string        `json:"params"`
	State     CapabilityState `json:"state"`
}

type Category struct {
	ID       int        `json:"id"`
	Name     string     `json:"name"`
	Children []Category `json:"children,omitempty"`
}

type CapsSnapshot struct {
	ServerTitle           string                `json:"server_title"`
	ServerVersion         string                `json:"server_version"`
	DefaultLimit          int                   `json:"default_limit"`
	MaximumLimit          int                   `json:"maximum_limit"`
	RegistrationAvailable bool                  `json:"registration_available"`
	RegistrationOpen      bool                  `json:"registration_open"`
	Searches              []SearchCapability    `json:"searches"`
	ModeObservations      []CAPSModeObservation `json:"mode_observations"`
	Categories            []Category            `json:"categories"`
	ObservedAt            time.Time             `json:"observed_at"`
	BodyHash              string                `json:"body_hash"`
}

type capsSearchXML struct {
	Available       string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
	availableSet    bool
	paramsSet       bool
}

const maxCAPSSearchModeOccurrences = 16

type capsModeElements struct {
	occurrences int
	elements    []capsSearchXML
	legacy      capsSearchXML
}

func (elements *capsModeElements) add(value capsSearchXML) {
	if elements.occurrences <= maxCAPSSearchModeOccurrences {
		elements.occurrences++
	}
	if len(elements.elements) < maxCAPSSearchModeOccurrences {
		elements.elements = append(elements.elements, value)
	}
	if value.availableSet {
		elements.legacy.Available = value.Available
		elements.legacy.availableSet = true
	}
	if value.paramsSet {
		elements.legacy.SupportedParams = value.SupportedParams
		elements.legacy.paramsSet = true
	}
}

type capsSearchingXML struct {
	Search capsModeElements
	TV     capsModeElements
	Movie  capsModeElements
	Audio  capsModeElements
	Book   capsModeElements
}

func (value *capsSearchXML) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	*value = capsSearchXML{}
	for _, attribute := range start.Attr {
		switch attribute.Name.Local {
		case "available":
			value.Available = attribute.Value
			value.availableSet = true
		case "supportedParams":
			value.SupportedParams = attribute.Value
			value.paramsSet = true
		}
	}
	return decoder.Skip()
}

func (searching *capsSearchingXML) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			var destination *capsModeElements
			switch typed.Name.Local {
			case "search":
				destination = &searching.Search
			case "tv-search":
				destination = &searching.TV
			case "movie-search":
				destination = &searching.Movie
			case "audio-search":
				destination = &searching.Audio
			case "book-search":
				destination = &searching.Book
			}
			if destination == nil {
				if err := decoder.Skip(); err != nil {
					return err
				}
				continue
			}
			var element capsSearchXML
			if err := decoder.DecodeElement(&element, &typed); err != nil {
				return err
			}
			destination.add(element)
		case xml.EndElement:
			if typed.Name == start.Name {
				return nil
			}
		}
	}
}

type capCategoryXML struct {
	ID       string           `xml:"id,attr"`
	Name     string           `xml:"name,attr"`
	Children []capCategoryXML `xml:"subcat"`
}

type ProviderError struct {
	Code        string
	Description string
	Unsupported bool
}

func (err ProviderError) Error() string {
	return "Newznab provider error (" + err.SafeCode() + ")"
}

func (err ProviderError) SafeCode() string {
	code := strings.TrimSpace(err.Code)
	if !validProviderCode(code) {
		return "newznab_provider_error"
	}
	return "newznab_provider_" + code
}

func validProviderCode(code string) bool {
	if len(code) < 1 || len(code) > 4 {
		return false
	}
	for _, character := range code {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func finalizeProviderError(provider ProviderError) ProviderError {
	code := strings.TrimSpace(provider.Code)
	description := provider.Description
	provider.Unsupported = code == "203" || strings.Contains(strings.ToLower(description), "no such function") || strings.Contains(strings.ToLower(description), "not supported")
	if validProviderCode(code) {
		provider.Code = code
	} else {
		provider.Code = ""
	}
	provider.Description = ""
	return provider
}

var ErrMalformedXML = errors.New("malformed Newznab XML")

func ParseSearch(body []byte, indexerID, operation string, observedAt time.Time) ([]releases.Observation, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true
	var observations []releases.Observation
	var current *itemBuilder
	tokens := 0
	hash := sha256.Sum256(body)
	bodyHash := hex.EncodeToString(hash[:])
	sawEnvelope := false
	rootObserved := false
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("%w: %v", ErrMalformedXML, err)
		}
		tokens++
		if tokens > 250000 {
			return nil, ErrResponseTooLarge
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if !rootObserved {
				rootObserved = true
				root := strings.ToLower(typed.Name.Local)
				if root != "rss" && root != "error" {
					return nil, ErrMalformedXML
				}
			}
			if strings.EqualFold(typed.Name.Local, "rss") {
				sawEnvelope = true
			}
			switch strings.ToLower(typed.Name.Local) {
			case "error":
				provider := ProviderError{}
				for _, attribute := range typed.Attr {
					if attribute.Name.Local == "code" {
						provider.Code = attribute.Value
					}
					if attribute.Name.Local == "description" {
						provider.Description = attribute.Value
					}
				}
				return nil, finalizeProviderError(provider)
			case "item":
				current = &itemBuilder{}
			case "attr":
				if current != nil {
					field := releases.RawField{Namespace: typed.Name.Space, Ordinal: len(current.fields)}
					for _, attribute := range typed.Attr {
						if attribute.Name.Local == "name" {
							field.Name = attribute.Value
						}
						if attribute.Name.Local == "value" {
							field.Value = attribute.Value
						}
					}
					current.fields = append(current.fields, field)
				}
			default:
				if current != nil {
					current.element = strings.ToLower(typed.Name.Local)
				}
			}
		case xml.CharData:
			if !rootObserved && strings.TrimSpace(string(typed)) != "" {
				return nil, ErrMalformedXML
			}
			if current != nil && current.element != "" {
				current.text(current.element, string(typed))
			}
		case xml.EndElement:
			if current != nil && strings.EqualFold(typed.Name.Local, "item") {
				observations = append(observations, current.finish(indexerID, operation, observedAt, bodyHash))
				current = nil
			} else if current != nil {
				current.element = ""
			}
		}
	}
	if !sawEnvelope {
		return nil, ErrMalformedXML
	}
	return observations, nil
}

type itemBuilder struct {
	element   string
	title     string
	guid      string
	link      string
	category  []string
	published string
	fields    []releases.RawField
}

func (builder *itemBuilder) text(element, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	switch element {
	case "title":
		builder.title += value
	case "guid":
		builder.guid += value
	case "link":
		builder.link += value
	case "category":
		builder.category = append(builder.category, value)
	case "pubdate":
		builder.published += value
	}
}

func (builder *itemBuilder) finish(indexerID, operation string, observedAt time.Time, bodyHash string) releases.Observation {
	sanitizedFields := SanitizeRawFields(builder.fields)
	nativeGUID, conflictingAttributeGUID := nativeAttributeGUID(sanitizedFields)
	if nativeGUID == "" {
		nativeGUID = SanitizeProviderValue(strings.TrimSpace(builder.guid))
	}
	observation := releases.Observation{ID: uuid.NewV7(), Operation: operation, NativeGUID: nativeGUID, Title: SanitizeProviderValue(strings.TrimSpace(builder.title)), DownloadURL: SanitizeProviderValue(strings.TrimSpace(builder.link)), Fields: sanitizedFields, BodyHash: bodyHash, ObservedAt: observedAt.UTC(), ParseVersion: "newznab-v1"}
	if parsed, err := uuid.Parse(indexerID); err == nil {
		observation.IndexerID = parsed
	}
	if conflictingAttributeGUID {
		observation.Problems = append(observation.Problems, "conflicting Newznab guid attributes; RSS guid retained")
	}
	for _, category := range builder.category {
		observation.Fields = append(observation.Fields, releases.RawField{Name: "category", Value: category, Ordinal: len(observation.Fields)})
	}
	if observation.NativeGUID == "" {
		weak := sha256.Sum256([]byte(observation.Title + "\x00" + observation.DownloadURL + "\x00" + builder.published))
		observation.NativeGUID = "weak:" + hex.EncodeToString(weak[:])
		observation.Problems = append(observation.Problems, "native GUID missing; weak same-indexer fingerprint used")
	}
	if observation.Title == "" {
		observation.Problems = append(observation.Problems, "title missing")
	}
	observation.Facts = normalizeFacts(observation.Fields, observedAt)
	return observation
}

func nativeAttributeGUID(fields []releases.RawField) (string, bool) {
	value := ""
	for _, field := range fields {
		if !strings.EqualFold(strings.TrimSpace(field.Name), "guid") {
			continue
		}
		candidate := strings.TrimSpace(field.Value)
		if candidate == "" || !retrievalIDPattern.MatchString(candidate) {
			continue
		}
		if value != "" && value != candidate {
			return "", true
		}
		value = candidate
	}
	return value, false
}

func normalizeFacts(fields []releases.RawField, observedAt time.Time) []releases.Fact {
	aliases := map[string]string{"tvdbid": "tvdb_id", "tmdbid": "tmdb_id", "imdb": "imdb_id", "imdbid": "imdb_id", "tvmazeid": "tvmaze_id", "rageid": "rage_id", "season": "season_number", "episode": "episode", "language": "language", "resolution": "resolution", "size": "size", "group": "group", "subtitle": "subtitle_languages", "subtitles": "subtitle_languages", "subs": "subtitle_languages", "subtitlelanguage": "subtitle_languages", "subtitlelanguages": "subtitle_languages", "subtitle_language": "subtitle_languages"}
	for alias, name := range mediaAliases {
		aliases[alias] = name
	}
	values := make(map[string][]string)
	indices := make(map[string][]int)
	for index, field := range fields {
		name, exists := aliases[strings.ToLower(strings.TrimSpace(field.Name))]
		if !exists || strings.TrimSpace(field.Value) == "" {
			continue
		}
		if name == "subtitle_languages" || name == "audio_languages" {
			for _, token := range strings.FieldsFunc(field.Value, func(value rune) bool {
				return value == ',' || value == ';' || value == '/' || value == '|' || value == ' ' || value == '\t'
			}) {
				if canonical, reliable := canonicalProviderLanguage(token); reliable {
					values[name] = append(values[name], canonical)
					indices[name] = append(indices[name], index)
				}
			}
			continue
		}
		value := field.Value
		if _, media := mediaAliases[strings.ToLower(strings.TrimSpace(field.Name))]; media {
			value = canonicalMediaValue(name, value)
		}
		if name == "season_number" {
			if canonical, reliable := canonicalSeasonNumber(value); reliable {
				value = canonical
			}
		} else if name == "resolution" {
			if canonical, reliable := releases.CanonicalResolution(value); reliable {
				value = canonical
			}
		}
		values[name] = append(values[name], value)
		indices[name] = append(indices[name], index)
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sortStrings(names)
	facts := make([]releases.Fact, 0, len(names))
	for _, name := range names {
		state := releases.FactObserved
		if distinct(values[name]) > 1 && strings.HasSuffix(name, "_id") {
			state = releases.FactConflicting
		}
		facts = append(facts, releases.Fact{Name: name, Values: values[name], State: state, EvidenceAt: indices[name], ObservedAt: observedAt.UTC()})
	}
	return facts
}

func canonicalSeasonNumber(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) > 1 && (value[0] == 's' || value[0] == 'S') {
		value = value[1:]
	}
	if value == "" || len(value) > 3 {
		return "", false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return "", false
		}
	}
	season, err := strconv.ParseUint(value, 10, 16)
	if err != nil || season > 999 {
		return "", false
	}
	return strconv.FormatUint(season, 10), true
}

func ParseCaps(body []byte, observedAt time.Time) (CapsSnapshot, error) {
	var document struct {
		XMLName xml.Name `xml:"caps"`
		Server  struct {
			Title   string `xml:"title,attr"`
			Version string `xml:"version,attr"`
		} `xml:"server"`
		Limits struct {
			Default int `xml:"default,attr"`
			Max     int `xml:"max,attr"`
		} `xml:"limits"`
		Registration struct {
			Available string `xml:"available,attr"`
			Open      string `xml:"open,attr"`
		} `xml:"registration"`
		Searching  capsSearchingXML `xml:"searching"`
		Categories []capCategoryXML `xml:"categories>category"`
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true
	if err := decoder.Decode(&document); err != nil {
		return CapsSnapshot{}, fmt.Errorf("%w: %v", ErrMalformedXML, err)
	}
	hash := sha256.Sum256(body)
	result := CapsSnapshot{ServerTitle: document.Server.Title, ServerVersion: document.Server.Version, DefaultLimit: document.Limits.Default, MaximumLimit: document.Limits.Max, RegistrationAvailable: yes(document.Registration.Available), RegistrationOpen: yes(document.Registration.Open), ObservedAt: observedAt.UTC(), BodyHash: hex.EncodeToString(hash[:])}
	for _, entry := range []struct {
		name  string
		value capsModeElements
	}{{"search", document.Searching.Search}, {"tv-search", document.Searching.TV}, {"movie-search", document.Searching.Movie}, {"audio-search", document.Searching.Audio}, {"book-search", document.Searching.Book}} {
		legacy := entry.value.legacy
		params := splitParams(legacy.SupportedParams)
		result.Searches = append(result.Searches, SearchCapability{Mode: entry.name, Available: yes(legacy.Available), Params: params, State: CapabilityAdvertised})
	}
	result.ModeObservations = []CAPSModeObservation{
		capsModeObservation(SearchModeGeneral, document.Searching.Search),
		capsModeObservation(SearchModeMovie, document.Searching.Movie),
		capsModeObservation(SearchModeTV, document.Searching.TV),
	}
	for _, category := range document.Categories {
		result.Categories = append(result.Categories, convertCategory(category))
	}
	return result, nil
}

func capsModeObservation(mode SearchMode, elements capsModeElements) CAPSModeObservation {
	observation := CAPSModeObservation{Mode: mode, ElementState: CAPSElementAbsent}
	if elements.occurrences == 0 {
		return observation
	}
	if elements.occurrences == 1 {
		observation.ElementState = capsElementState(elements.elements[0].Available)
	} else {
		observation.ElementState = CAPSElementPresentUninterpretable
		observation.Repeated = true
	}
	parameterValues := make([]string, 0, len(elements.elements))
	for _, element := range elements.elements {
		if element.paramsSet {
			parameterValues = append(parameterValues, element.SupportedParams)
		}
	}
	advertised := boundedCAPSParameterNames(strings.Join(parameterValues, ","))
	observation.AdvertisedParameterNames = advertised
	observation.RecognizedParameters = recognizedCAPSParameters(advertised)
	return observation
}

func capsElementState(value string) CAPSElementState {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "true", "1":
		return CAPSElementPresentAvailable
	case "no", "false", "0":
		return CAPSElementPresentUnavailable
	default:
		return CAPSElementPresentUninterpretable
	}
}

func boundedCAPSParameterNames(value string) []string {
	parts := strings.SplitN(value, ",", maxCAPSParameterNames+1)
	result := make([]string, 0, min(len(parts), maxCAPSParameterNames))
	for index, part := range parts {
		if index == maxCAPSParameterNames {
			break
		}
		name := strings.TrimSpace(SanitizeProviderValue(part))
		if name == "" {
			continue
		}
		if !advertisedParameterNamePattern.MatchString(name) {
			continue
		}
		result = append(result, name)
	}
	return result
}

func recognizedCAPSParameters(advertised []string) []WireParameter {
	known := map[string]WireParameter{
		"q":        WireQuery,
		"imdbid":   WireIMDbID,
		"tmdbid":   WireTMDbID,
		"tvdbid":   WireTVDbID,
		"tvmazeid": WireTVMazeID,
		"year":     WireYear,
		"season":   WireSeason,
		"ep":       WireEpisode,
		"extended": WireExtended,
	}
	seen := make(map[WireParameter]struct{}, len(known))
	result := make([]WireParameter, 0, len(known))
	for _, name := range advertised {
		parameter, recognized := known[strings.ToLower(name)]
		if !recognized {
			continue
		}
		if _, duplicate := seen[parameter]; duplicate {
			continue
		}
		seen[parameter] = struct{}{}
		result = append(result, parameter)
	}
	return result
}

func convertCategory(input capCategoryXML) Category {
	id, _ := strconv.Atoi(input.ID)
	result := Category{ID: id, Name: input.Name}
	for _, child := range input.Children {
		result.Children = append(result.Children, convertCategory(child))
	}
	return result
}

func splitParams(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func yes(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "yes" || value == "true" || value == "1"
}

func distinct(values []string) int {
	set := map[string]struct{}{}
	for _, value := range values {
		set[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	return len(set)
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
