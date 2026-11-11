package importlists

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidMDBListURL is returned for an address that is not a public
// MDBList list.
var ErrInvalidMDBListURL = errors.New("not an MDBList list address")

// segmentValid accepts one path segment of a list address: any letters
// (also æ, ø and å), digits and punctuation, but no slash, backslash, query,
// fragment, percent sign, space or control character, and not "." or "..".
// The segment is escaped again when the list is fetched.
func segmentValid(value string, maxRunes int) bool {
	if value == "" || value == "." || value == ".." || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) || strings.ContainsRune(`/\?#%`, character) {
			return false
		}
	}
	return true
}

// MDBListRef names one public list: mdblist.com/lists/{Owner}/{Slug}.
type MDBListRef struct {
	Owner string `json:"owner"`
	Slug  string `json:"slug"`
}

// Valid reports whether both parts are safe path segments.
func (ref MDBListRef) Valid() bool {
	return segmentValid(ref.Owner, 100) && segmentValid(ref.Slug, 200)
}

// URL is the list's page on mdblist.com.
func (ref MDBListRef) URL() string {
	return "https://mdblist.com/lists/" + url.PathEscape(ref.Owner) + "/" + url.PathEscape(ref.Slug)
}

// ParseMDBListURL accepts the address of a list page as copied from the
// browser, with or without "www.", a trailing slash, "/json" or a query, and
// also the short "owner/slug" form. Any other host is refused.
func ParseMDBListURL(raw string) (MDBListRef, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 500 {
		return MDBListRef{}, ErrInvalidMDBListURL
	}
	path := value
	lower := strings.ToLower(value)
	if strings.Contains(value, "://") || strings.HasPrefix(lower, "mdblist.com") || strings.HasPrefix(lower, "www.mdblist.com") {
		if !strings.Contains(value, "://") {
			value = "https://" + value
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.User != nil {
			return MDBListRef{}, ErrInvalidMDBListURL
		}
		host := strings.ToLower(parsed.Hostname())
		if host != "mdblist.com" && host != "www.mdblist.com" || parsed.Port() != "" || !strings.HasPrefix(parsed.Path, "/lists/") {
			return MDBListRef{}, ErrInvalidMDBListURL
		}
		path = strings.TrimPrefix(parsed.Path, "/lists/")
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), "/json")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return MDBListRef{}, ErrInvalidMDBListURL
	}
	// A pasted address may keep æ, ø and å as they are or carry them
	// percent-encoded (%C3%A6); both mean the same list.
	for index, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return MDBListRef{}, ErrInvalidMDBListURL
		}
		parts[index] = decoded
	}
	ref := MDBListRef{Owner: parts[0], Slug: parts[1]}
	if !ref.Valid() {
		return MDBListRef{}, ErrInvalidMDBListURL
	}
	return ref, nil
}
