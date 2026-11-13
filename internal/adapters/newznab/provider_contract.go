package newznab

import (
	"net/url"
	"regexp"
	"strings"
)

// maxCAPSParameterNames bounds the advertised parameter names kept per mode.
const maxCAPSParameterNames = 256

var (
	profileKeyPattern              = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,99}$`)
	advertisedParameterNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,63}$`)
)

type SearchMode string

const (
	SearchModeGeneral SearchMode = "search"
	SearchModeMovie   SearchMode = "movie"
	SearchModeTV      SearchMode = "tvsearch"
)

type WireParameter string

const (
	WireQuery    WireParameter = "q"
	WireIMDbID   WireParameter = "imdbid"
	WireTMDbID   WireParameter = "tmdbid"
	WireTVDbID   WireParameter = "tvdbid"
	WireTVMazeID WireParameter = "tvmazeid"
	WireYear     WireParameter = "year"
	WireSeason   WireParameter = "season"
	WireEpisode  WireParameter = "ep"
	WireExtended WireParameter = "extended"
)

type CAPSElementState string

const (
	CAPSElementAbsent                 CAPSElementState = "absent"
	CAPSElementPresentAvailable       CAPSElementState = "present_available"
	CAPSElementPresentUnavailable     CAPSElementState = "present_unavailable"
	CAPSElementPresentUninterpretable CAPSElementState = "present_uninterpretable"
)

// CAPSModeObservation preserves the provider's bounded advertised names and
// the closed subset of parameters Media Manager recognizes.
type CAPSModeObservation struct {
	Mode                     SearchMode       `json:"mode"`
	ElementState             CAPSElementState `json:"element_state"`
	Repeated                 bool             `json:"repeated,omitempty"`
	AdvertisedParameterNames []string         `json:"advertised_parameter_names,omitempty"`
	RecognizedParameters     []WireParameter  `json:"recognized_parameters,omitempty"`
}

type ProviderContractRef struct {
	Key     string `json:"key"`
	Version uint16 `json:"version"`
}

const (
	ProviderContractNZBFinderV1    = "nzbfinder"
	ProviderContractPath32GetNZBV1 = "path32.getnzb"
	ProviderContractDrunkenSlugV1  = "drunkenslug"
)

// validContractRef accepts only audited provider contracts; new ones are added
// here alongside their Go implementations.
func validContractRef(reference ProviderContractRef) bool {
	return reference.Version > 0 && validProfileKey(reference.Key) && knownProviderContract(reference)
}

func knownProviderContract(reference ProviderContractRef) bool {
	if reference.Version != 1 {
		return false
	}
	switch reference.Key {
	case ProviderContractNZBFinderV1, ProviderContractPath32GetNZBV1, ProviderContractDrunkenSlugV1:
		return true
	default:
		return false
	}
}

// ValidProviderContractKey lets owner configuration share this exact closed
// registry without duplicating provider knowledge.
func ValidProviderContractKey(key string) bool {
	return validContractRef(ProviderContractRef{Key: key, Version: 1})
}

// ProviderContractAcceptsEndpoint verifies the owner-selected contract against
// the small audited origin registry. It performs no request and does not infer
// a contract from provider-controlled data.
func ProviderContractAcceptsEndpoint(reference ProviderContractRef, endpoint string) bool {
	if !knownProviderContract(reference) {
		return false
	}
	parsed, err := url.ParseRequestURI(strings.TrimSpace(endpoint))
	if err != nil || !parsed.IsAbs() || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	origin, err := exactOrigin(parsed)
	if err != nil || origin.scheme != "https" || origin.port != "443" {
		return false
	}
	switch reference.Key {
	case ProviderContractNZBFinderV1:
		return origin.hostname == "nzbfinder.ws"
	case ProviderContractDrunkenSlugV1:
		return origin.hostname == "drunkenslug.com"
	case ProviderContractPath32GetNZBV1:
		switch origin.hostname {
		case "abnzb.com", "api.althub.co.za", "api.nzb.life", "msgnews.live", "ninjacentral.co.za":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func validProfileKey(value string) bool { return profileKeyPattern.MatchString(value) }
