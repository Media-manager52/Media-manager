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
	knownPermalinkExtractors = map[string]*regexp.Regexp{
		"nzbfinder.ws":    nzbFinderPermalink,
		"drunkenslug.com": drunkenSlugPermalink,
	}
)

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
