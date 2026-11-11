package importlists

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// ErrInvalidMDBListURL is returned for an address that is not a public
// MDBList list.
var ErrInvalidMDBListURL = errors.New("not an MDBList list address")

var (
	mdbListOwnerPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	mdbListSlugPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,200}$`)
)

// MDBListRef names one public list: mdblist.com/lists/{Owner}/{Slug}.
type MDBListRef struct {
	Owner string `json:"owner"`
	Slug  string `json:"slug"`
}

// Valid reports whether both parts are safe path segments.
func (ref MDBListRef) Valid() bool {
	return mdbListOwnerPattern.MatchString(ref.Owner) && mdbListSlugPattern.MatchString(ref.Slug) && ref.Owner != "." && ref.Owner != ".." && ref.Slug != "." && ref.Slug != ".."
}

// URL is the list's page on mdblist.com.
func (ref MDBListRef) URL() string {
	return "https://mdblist.com/lists/" + ref.Owner + "/" + ref.Slug
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
	ref := MDBListRef{Owner: parts[0], Slug: parts[1]}
	if !ref.Valid() {
		return MDBListRef{}, ErrInvalidMDBListURL
	}
	return ref, nil
}
