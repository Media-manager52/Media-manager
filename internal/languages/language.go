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
	// "dk" is the country code, but uploads do tag Danish tracks with it and no
	// language uses it.
	case "da", "dan", "danish", "dansk", "da-dk", "dk":
		return "da", true
	}
	if language, known := commonLanguages[value]; known {
		return language, true
	}
	if !canonicalTag.MatchString(value) {
		return "", false
	}
	return value, true
}

// commonLanguages maps the three-letter ISO 639-2 codes (both the B and T
// forms) and English names that media files use for their tracks to the
// two-letter code a profile stores, so a profile requiring "en" matches an
// "eng" track. Norwegian Bokmål and Nynorsk count as Norwegian ("no").
var commonLanguages = map[string]string{
	"eng": "en", "english": "en",
	"swe": "sv", "swedish": "sv", "svenska": "sv",
	"nor": "no", "nob": "no", "nno": "no", "nb": "no", "nn": "no", "norwegian": "no", "norsk": "no",
	"fin": "fi", "finnish": "fi",
	"isl": "is", "ice": "is", "icelandic": "is",
	"deu": "de", "ger": "de", "german": "de",
	"fra": "fr", "fre": "fr", "french": "fr",
	"spa": "es", "spanish": "es",
	"ita": "it", "italian": "it",
	"nld": "nl", "dut": "nl", "dutch": "nl",
	"por": "pt", "portuguese": "pt",
	"pol": "pl", "polish": "pl",
	"rus": "ru", "russian": "ru",
	"jpn": "ja", "japanese": "ja",
	"kor": "ko", "korean": "ko",
	"zho": "zh", "chi": "zh", "chinese": "zh",
	"ara": "ar", "arabic": "ar",
	"tur": "tr", "turkish": "tr",
	"ell": "el", "gre": "el", "greek": "el",
	"hin": "hi", "hindi": "hi",
}
