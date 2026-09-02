package newznab

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	retrievalIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,199}$`)
	nzbFinderPermalink       = regexp.MustCompile(`(?i)^/details/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)
	path32Permalink          = regexp.MustCompile(`(?i)^/details/([0-9a-f]{32})$`)
	path32Download           = regexp.MustCompile(`(?i)^/getnzb/([0-9a-f]{32})\.nzb&i=([0-9]{1,20})$`)
	drunkenSlugPermalink     = regexp.MustCompile(`(?i)^/details/([0-9a-f]{40})$`)
	drunkenSlugDownloadPath  = regexp.MustCompile(`(?i)^/getnzb/([0-9a-f]{40})\.nzb&i=([0-9]{1,20})$`)
	knownPermalinkExtractors = map[string]*regexp.Regexp{
		"abnzb.com":          path32Permalink,
		"api.althub.co.za":   path32Permalink,
		"api.nzb.life":       path32Permalink,
		"drunkenslug.com":    drunkenSlugPermalink,
		"msgnews.live":       path32Permalink,
		"ninjacentral.co.za": path32Permalink,
		"nzbfinder.ws":       nzbFinderPermalink,
	}
	sameOriginPathDownloadContracts = map[string]pathDownloadContract{
		"abnzb.com":          {permalink: path32Permalink, download: path32Download},
		"api.althub.co.za":   {permalink: path32Permalink, download: path32Download},
		"api.nzb.life":       {permalink: path32Permalink, download: path32Download},
		"drunkenslug.com":    {permalink: drunkenSlugPermalink, download: drunkenSlugDownloadPath},
		"msgnews.live":       {permalink: path32Permalink, download: path32Download},
		"ninjacentral.co.za": {permalink: path32Permalink, download: path32Download},
	}
)

type pathDownloadContract struct {
	permalink *regexp.Regexp
	download  *regexp.Regexp
}

const advertisedLocatorPrefix = "advertised:"

// DownloadLocatorReference keeps only verified credential-free provider
// locator shapes. The locator is not exposed by API read models and is
// independently revalidated before use.
func DownloadLocatorReference(downloadURL, nativeKey string) string {
	fallback := "native:" + nativeKey
	locator := strings.TrimSpace(SanitizeProviderValue(downloadURL))
	if locator == "" || len(locator) > 8192 {
		return fallback
	}
	parsedLocator, err := url.ParseRequestURI(locator)
	if err != nil || !parsedLocator.IsAbs() || !strings.EqualFold(parsedLocator.Scheme, "https") || parsedLocator.User != nil || parsedLocator.Fragment != "" || parsedLocator.RawPath != "" || parsedLocator.Port() != "" && parsedLocator.Port() != "443" {
		return fallback
	}
	if strings.EqualFold(parsedLocator.Hostname(), "nzbfinder.ws") {
		retrievalID, ok := locatorNativeID(nativeKey, parsedLocator, nzbFinderPermalink)
		locatorID, locatorOK := nzbFinderLocatorID(parsedLocator)
		if !ok || !locatorOK || !strings.EqualFold(retrievalID, locatorID) {
			return fallback
		}
		return advertisedLocatorPrefix + locator
	}
	contract, known := sameOriginPathDownloadContracts[strings.ToLower(parsedLocator.Hostname())]
	if !known || parsedLocator.RawQuery != "" {
		return fallback
	}
	retrievalID, ok := locatorNativeID(nativeKey, parsedLocator, contract.permalink)
	locatorMatch := contract.download.FindStringSubmatch(parsedLocator.Path)
	if !ok || len(locatorMatch) != 3 || !strings.EqualFold(locatorMatch[1], retrievalID) {
		return fallback
	}
	return advertisedLocatorPrefix + locator
}

func locatorNativeID(nativeKey string, locator *url.URL, permalink *regexp.Regexp) (string, bool) {
	value := strings.TrimSpace(nativeKey)
	if retrievalIDPattern.MatchString(value) {
		return value, permalink.MatchString("/details/" + value)
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || !parsed.IsAbs() || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" || parsed.RawPath != "" {
		return "", false
	}
	locatorOrigin, locatorErr := exactOrigin(locator)
	nativeOrigin, nativeErr := exactOrigin(parsed)
	match := permalink.FindStringSubmatch(parsed.Path)
	if locatorErr != nil || nativeErr != nil || locatorOrigin != nativeOrigin || len(match) != 2 {
		return "", false
	}
	return match[1], true
}

func nzbFinderLocatorID(locator *url.URL) (string, bool) {
	if locator.Path != "/api/v1/getnzb" || locator.RawQuery == "" {
		return "", false
	}
	query, err := url.ParseQuery(locator.RawQuery)
	if err != nil || len(query) != 1 || len(query["id"]) != 1 {
		return "", false
	}
	value := strings.TrimSpace(query.Get("id"))
	if len(value) <= len(".nzb") || !strings.EqualFold(value[len(value)-len(".nzb"):], ".nzb") {
		return "", false
	}
	value = value[:len(value)-len(".nzb")]
	return value, nzbFinderPermalink.MatchString("/details/" + value)
}

// RetrievalID converts a durable Newznab native key into the identifier used
// by t=get. Native keys remain unchanged in persistence so workflow-scoped
// release identity is exact and auditable.
func RetrievalID(nativeKey, configuredBaseURL string) (string, error) {
	value := strings.TrimSpace(nativeKey)
	if value == "" || len(value) > 2048 || strings.HasPrefix(value, "weak:") {
		return "", errors.New("Newznab native key is not retrievable")
	}
	if retrievalIDPattern.MatchString(value) {
		return value, nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || !parsed.IsAbs() || parsed.User != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" {
		return "", errors.New("Newznab native key is not a safe details permalink")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", errors.New("Newznab details permalink uses an unsupported scheme")
	}
	configured, err := url.ParseRequestURI(strings.TrimSpace(configuredBaseURL))
	if err != nil || !configured.IsAbs() || configured.Host == "" || !strings.EqualFold(parsed.Host, configured.Host) || !strings.EqualFold(parsed.Scheme, configured.Scheme) {
		return "", errors.New("Newznab details permalink does not belong to the configured provider")
	}
	pattern := knownPermalinkExtractors[strings.ToLower(parsed.Hostname())]
	if pattern == nil {
		return "", errors.New("Newznab provider does not have a verified permalink contract")
	}
	match := pattern.FindStringSubmatch(parsed.Path)
	if len(match) != 2 || !retrievalIDPattern.MatchString(match[1]) {
		return "", errors.New("Newznab details permalink has an unsafe retrieval identifier")
	}
	return match[1], nil
}

// advertisedDownloadTarget implements verified provider-specific direct-
// download contracts. Other providers continue to use the standard Newznab
// t=get path. The API key exists only in the in-memory request URL.
func advertisedDownloadTarget(locatorReference, nativeKey, configuredBaseURL, apiKey string) (*url.URL, bool, error) {
	if !strings.HasPrefix(locatorReference, advertisedLocatorPrefix) {
		return nil, false, nil
	}
	configured, err := url.ParseRequestURI(strings.TrimSpace(configuredBaseURL))
	if err != nil || !configured.IsAbs() || configured.User != nil || configured.Fragment != "" {
		return nil, false, errors.New("configured Newznab endpoint is invalid")
	}
	locator, err := url.ParseRequestURI(strings.TrimPrefix(locatorReference, advertisedLocatorPrefix))
	if err != nil || !locator.IsAbs() || locator.User != nil || locator.Fragment != "" || locator.RawPath != "" {
		return nil, true, errors.New("advertised Newznab download locator is invalid")
	}
	configuredOrigin, err := exactOrigin(configured)
	if err != nil {
		return nil, true, errors.New("configured Newznab endpoint is invalid")
	}
	locatorOrigin, err := exactOrigin(locator)
	if err != nil || configuredOrigin != locatorOrigin {
		return nil, true, errors.New("advertised Newznab download locator has the wrong origin")
	}
	retrievalID, err := RetrievalID(nativeKey, configuredBaseURL)
	if err != nil {
		return nil, true, err
	}
	if strings.TrimSpace(apiKey) == "" || len(apiKey) > 512 {
		return nil, true, errors.New("advertised Newznab download locator does not match the release")
	}
	configuredHost := strings.ToLower(configured.Hostname())
	if contract, known := sameOriginPathDownloadContracts[configuredHost]; known {
		match := contract.download.FindStringSubmatch(locator.Path)
		if locator.RawQuery != "" || len(match) != 3 || !strings.EqualFold(match[1], retrievalID) {
			return nil, true, errors.New("advertised Newznab download locator does not match the release")
		}
		target := *locator
		target.Path += "&r=" + apiKey
		return &target, true, nil
	}
	switch configuredHost {
	case "nzbfinder.ws":
		locatorID, ok := nzbFinderLocatorID(locator)
		if !ok || !strings.EqualFold(locatorID, retrievalID) {
			return nil, true, errors.New("advertised Newznab download locator does not match the release")
		}
		target := *locator
		query := target.Query()
		query.Set("apikey", apiKey)
		target.RawQuery = query.Encode()
		return &target, true, nil
	default:
		return nil, true, errors.New("advertised Newznab download locator provider is not supported")
	}
}
