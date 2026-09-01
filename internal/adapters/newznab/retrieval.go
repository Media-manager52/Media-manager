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
	drunkenSlugPermalink     = regexp.MustCompile(`(?i)^/details/([0-9a-f]{40})$`)
	drunkenSlugDownloadPath  = regexp.MustCompile(`(?i)^/getnzb/([0-9a-f]{40})\.nzb&i=([0-9]{1,20})$`)
	knownPermalinkExtractors = map[string]*regexp.Regexp{
		"nzbfinder.ws":    nzbFinderPermalink,
		"drunkenslug.com": drunkenSlugPermalink,
	}
)

const advertisedLocatorPrefix = "advertised:"

// DownloadLocatorReference keeps only the verified credential-free
// DrunkenSlug locator shape. The locator is not exposed by API read models and
// is independently revalidated before use.
func DownloadLocatorReference(downloadURL, nativeKey string) string {
	fallback := "native:" + nativeKey
	locator := strings.TrimSpace(SanitizeProviderValue(downloadURL))
	if locator == "" || len(locator) > 8192 {
		return fallback
	}
	parsedLocator, err := url.ParseRequestURI(locator)
	if err != nil || !parsedLocator.IsAbs() || !strings.EqualFold(parsedLocator.Scheme, "https") || !strings.EqualFold(parsedLocator.Hostname(), "drunkenslug.com") || parsedLocator.User != nil || parsedLocator.Fragment != "" || parsedLocator.RawQuery != "" || parsedLocator.RawPath != "" {
		return fallback
	}
	parsedNative, err := url.ParseRequestURI(strings.TrimSpace(nativeKey))
	if err != nil || !parsedNative.IsAbs() || parsedNative.User != nil || parsedNative.Fragment != "" || parsedNative.RawQuery != "" || parsedNative.RawPath != "" {
		return fallback
	}
	locatorOrigin, locatorErr := exactOrigin(parsedLocator)
	nativeOrigin, nativeErr := exactOrigin(parsedNative)
	locatorMatch := drunkenSlugDownloadPath.FindStringSubmatch(parsedLocator.Path)
	nativeMatch := drunkenSlugPermalink.FindStringSubmatch(parsedNative.Path)
	if locatorErr != nil || nativeErr != nil || locatorOrigin != nativeOrigin || len(locatorMatch) != 3 || len(nativeMatch) != 2 || !strings.EqualFold(locatorMatch[1], nativeMatch[1]) {
		return fallback
	}
	return advertisedLocatorPrefix + locator
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

// advertisedDownloadTarget implements DrunkenSlug's verified direct-download
// contract. Other providers continue to use the standard Newznab t=get path.
// The API key exists only in the in-memory request URL.
func advertisedDownloadTarget(locatorReference, nativeKey, configuredBaseURL, apiKey string) (*url.URL, bool, error) {
	if !strings.HasPrefix(locatorReference, advertisedLocatorPrefix) {
		return nil, false, nil
	}
	configured, err := url.ParseRequestURI(strings.TrimSpace(configuredBaseURL))
	if err != nil || !configured.IsAbs() || configured.User != nil || configured.Fragment != "" {
		return nil, false, errors.New("configured Newznab endpoint is invalid")
	}
	if !strings.EqualFold(configured.Hostname(), "drunkenslug.com") {
		return nil, false, nil
	}
	locator, err := url.ParseRequestURI(strings.TrimPrefix(locatorReference, advertisedLocatorPrefix))
	if err != nil || !locator.IsAbs() || locator.User != nil || locator.Fragment != "" || locator.RawQuery != "" || locator.RawPath != "" {
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
	match := drunkenSlugDownloadPath.FindStringSubmatch(locator.Path)
	if len(match) != 3 || !strings.EqualFold(match[1], retrievalID) || strings.TrimSpace(apiKey) == "" || len(apiKey) > 512 {
		return nil, true, errors.New("advertised Newznab download locator does not match the release")
	}
	target := *locator
	target.Path += "&r=" + apiKey
	return &target, true, nil
}
