package languages

import (
	"regexp"
	"strings"
)

var canonicalTag = regexp.MustCompile(`^[a-z]{2,3}(?:-[a-z]{2}|-[0-9]{3})?$`)

// Canonical returns only language values reliable enough for policy evidence.
// Callers retain the raw value separately.
func Canonical(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "da", "dan", "danish", "dansk", "da-dk":
		return "da", true
	}
	if !canonicalTag.MatchString(value) {
		return "", false
	}
	return value, true
}
