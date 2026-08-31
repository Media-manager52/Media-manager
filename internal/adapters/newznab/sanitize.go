package newznab

import (
	"net/url"
	"strings"

	"github.com/media-manager/media-manager/internal/releases"
)

const (
	maxCredentialSanitizeDepth      = 4
	maxCredentialSanitizeValueBytes = 1 << 20
	maxCredentialQueryParts         = 4096
)

// SanitizeObservation removes credential-bearing URL query parameters before
// provider evidence can reach persistence, policy explanations, events, or API
// read models. It intentionally preserves field order and unrelated metadata.
func SanitizeObservation(observation releases.Observation) releases.Observation {
	return sanitizeObservation(observation, "")
}

// SanitizeObservationWithCredential additionally removes provider-specific
// query parameters whose decoded value contains the configured source
// credential. This covers aliases such as DrunkenSlug's r parameter without
// treating that short name as sensitive for unrelated providers.
func SanitizeObservationWithCredential(observation releases.Observation, credential string) releases.Observation {
	return sanitizeObservation(observation, credential)
}

func sanitizeObservation(observation releases.Observation, credential string) releases.Observation {
	observation.NativeGUID = sanitizeProviderValuePublic(observation.NativeGUID, credential)
	observation.Title = sanitizeProviderValuePublic(observation.Title, credential)
	observation.DownloadURL = sanitizeProviderValuePublic(observation.DownloadURL, credential)
	observation.Fields = sanitizeRawFields(observation.Fields, credential)
	for factIndex := range observation.Facts {
		observation.Facts[factIndex].Values = sanitizeStrings(observation.Facts[factIndex].Values, credential)
	}
	return observation
}

// SanitizeEvaluation prevents provider-derived evidence values from being
// copied into durable policy decisions when a caller supplies an observation
// without going through the normal parser path.
func SanitizeEvaluation(evaluation releases.Evaluation) releases.Evaluation {
	if evaluation.Explanations == nil {
		return evaluation
	}
	evaluation.Explanations = append([]releases.Explanation(nil), evaluation.Explanations...)
	for index := range evaluation.Explanations {
		explanation := &evaluation.Explanations[index]
		explanation.Rule = SanitizeProviderValue(explanation.Rule)
		explanation.Fact = SanitizeProviderValue(explanation.Fact)
		explanation.Values = sanitizeStrings(explanation.Values, "")
		explanation.Reason = SanitizeProviderValue(explanation.Reason)
	}
	return evaluation
}

// SanitizeRawFields returns a detached copy so callers cannot accidentally
// retain an unsanitized alias to provider-owned data.
func SanitizeRawFields(fields []releases.RawField) []releases.RawField {
	return sanitizeRawFields(fields, "")
}

func sanitizeRawFields(fields []releases.RawField, credential string) []releases.RawField {
	if fields == nil {
		return nil
	}
	result := make([]releases.RawField, len(fields))
	copy(result, fields)
	for index := range result {
		result[index].Value = sanitizeProviderValuePublic(result[index].Value, credential)
	}
	return result
}

// SanitizeProviderValue handles direct, nested, and percent-encoded URL values
// to a fixed depth. Malformed credential values are removed based on their key
// without attempting to decode or retain the value.
func SanitizeProviderValue(value string) string {
	return sanitizeProviderValuePublic(value, "")
}

func sanitizeProviderValuePublic(value, credential string) string {
	result, _ := sanitizeProviderValue(value, credential, 0)
	return result
}

func sanitizeProviderValue(value, credential string, depth int) (string, bool) {
	if value == "" || depth >= maxCredentialSanitizeDepth {
		return value, false
	}
	if len(value) > maxCredentialSanitizeValueBytes {
		if queryContainsCredential(value) {
			return "[provider URL metadata removed: safety bound exceeded]", true
		}
		return value, false
	}
	if sanitized, changed, recognized := sanitizeDirectURL(value, credential, depth); recognized {
		return sanitized, changed
	}
	decoded, err := url.QueryUnescape(value)
	if err != nil || decoded == value {
		return value, false
	}
	sanitized, changed := sanitizeProviderValue(decoded, credential, depth+1)
	if !changed {
		return value, false
	}
	return url.QueryEscape(sanitized), true
}

func sanitizeDirectURL(value, credential string, depth int) (string, bool, bool) {
	queryAt := strings.IndexByte(value, '?')
	if queryAt < 0 {
		if !queryContainsCredential(value) {
			return value, false, false
		}
		sanitized, changed := sanitizeRawQuery(value, credential, depth)
		return sanitized, changed, true
	}
	prefix := value[:queryAt]
	if prefix != "" && !looksLikeURLPrefix(prefix) {
		return value, false, false
	}
	queryEnd := len(value)
	if fragmentAt := strings.IndexByte(value[queryAt+1:], '#'); fragmentAt >= 0 {
		queryEnd = queryAt + 1 + fragmentAt
	}
	rawQuery := value[queryAt+1 : queryEnd]
	sanitized, changed := sanitizeRawQuery(rawQuery, credential, depth)
	if !changed {
		return value, false, true
	}
	result := prefix
	if sanitized != "" {
		result += "?" + sanitized
	}
	result += value[queryEnd:]
	return result, true, true
}

func sanitizeRawQuery(rawQuery, credential string, depth int) (string, bool) {
	parts := strings.Split(rawQuery, "&")
	if len(parts) > maxCredentialQueryParts {
		return "", true
	}
	retained := make([]string, 0, len(parts))
	changed := false
	for _, part := range parts {
		rawName, rawValue, hasValue := strings.Cut(part, "=")
		if credentialQueryName(rawName) || credential != "" && containsEncodedCredential(rawName, credential) {
			changed = true
			continue
		}
		if hasValue {
			decoded, err := url.QueryUnescape(rawValue)
			if err == nil {
				if sanitized, nestedChanged := sanitizeProviderValue(decoded, credential, depth+1); nestedChanged {
					part = rawName + "=" + url.QueryEscape(sanitized)
					changed = true
				} else if credential != "" && containsEncodedCredential(rawValue, credential) {
					changed = true
					continue
				}
			} else if credential != "" && containsEncodedCredential(rawValue, credential) {
				changed = true
				continue
			}
		}
		retained = append(retained, part)
	}
	if !changed {
		return rawQuery, false
	}
	return strings.Join(retained, "&"), true
}

func containsEncodedCredential(value, credential string) bool {
	if credential == "" {
		return false
	}
	for attempt := 0; attempt < maxCredentialSanitizeDepth; attempt++ {
		if strings.Contains(value, credential) {
			return true
		}
		decoded, err := url.QueryUnescape(value)
		if err != nil || decoded == value {
			return false
		}
		value = decoded
	}
	return strings.Contains(value, credential)
}

func queryContainsCredential(value string) bool {
	parts := strings.Split(value, "&")
	if len(parts) > maxCredentialQueryParts {
		parts = parts[:maxCredentialQueryParts]
	}
	for _, part := range parts {
		name, _, _ := strings.Cut(part, "=")
		name = strings.TrimPrefix(name, "?")
		if credentialQueryName(name) {
			return true
		}
	}
	return false
}

func credentialQueryName(value string) bool {
	for attempt := 0; attempt < maxCredentialSanitizeDepth; attempt++ {
		decoded, err := url.QueryUnescape(value)
		if err != nil || decoded == value {
			break
		}
		value = decoded
	}
	canonical := strings.Map(func(character rune) rune {
		switch {
		case character >= 'a' && character <= 'z':
			return character
		case character >= 'A' && character <= 'Z':
			return character + ('a' - 'A')
		case character >= '0' && character <= '9':
			return character
		default:
			return -1
		}
	}, strings.TrimSpace(value))
	switch canonical {
	case "apikey", "token", "apitoken", "accesstoken", "authtoken", "authorization", "password", "passwd", "secret", "apisecret", "clientsecret", "credential", "credentials":
		return true
	default:
		return false
	}
}

func looksLikeURLPrefix(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "//") || strings.HasPrefix(lower, "/")
}

func sanitizeStrings(values []string, credential string) []string {
	if values == nil {
		return nil
	}
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = sanitizeProviderValuePublic(value, credential)
	}
	return result
}
