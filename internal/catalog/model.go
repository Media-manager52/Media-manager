package catalog

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"uuid"
)

type MediaKind string

const (
	KindMovie  MediaKind = "movie"
	KindSeries MediaKind = "series"
)

type IdentityState string

const (
	IdentityVerified       IdentityState = "verified"
	IdentityUnverified     IdentityState = "unverified"
	IdentityNeedsAttention IdentityState = "needs_attention"
)

type ExternalIdentity struct {
	Namespace string        `json:"namespace"`
	Value     string        `json:"value"`
	State     IdentityState `json:"state"`
	Source    string        `json:"source"`
}

type MediaItem struct {
	ID         uuid.UUID          `json:"id"`
	Kind       MediaKind          `json:"kind"`
	Title      string             `json:"title"`
	Year       *uint16            `json:"year,omitempty"`
	Identities []ExternalIdentity `json:"identities"`
	Revision   uint64             `json:"revision"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

var (
	ErrInvalidMediaKind = errors.New("invalid media kind")
	ErrTitleRequired    = errors.New("title is required")
	ErrInvalidYear      = errors.New("year is outside the supported range")
	ErrIdentityConflict = errors.New("conflicting external identities")
	ErrInvalidIdentity  = errors.New("external identity has an invalid value")
	numericProviderID   = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
	imdbProviderID      = regexp.MustCompile(`^tt[0-9]{7,12}$`)
)

func New(kind MediaKind, title string, year *uint16, identities []ExternalIdentity, now time.Time) (MediaItem, error) {
	if kind != KindMovie && kind != KindSeries {
		return MediaItem{}, ErrInvalidMediaKind
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return MediaItem{}, ErrTitleRequired
	}
	if year != nil && (*year < 1800 || *year > 9999) {
		return MediaItem{}, ErrInvalidYear
	}
	identities, err := canonicalIdentities(kind, identities)
	if err != nil {
		return MediaItem{}, err
	}
	return MediaItem{
		ID:         uuid.NewV7(),
		Kind:       kind,
		Title:      title,
		Year:       year,
		Identities: append([]ExternalIdentity(nil), identities...),
		Revision:   1,
		CreatedAt:  now.UTC(),
		UpdatedAt:  now.UTC(),
	}, nil
}

func ValidateIdentities(kind MediaKind, identities []ExternalIdentity) error {
	_, err := canonicalIdentities(kind, identities)
	return err
}

func canonicalIdentities(kind MediaKind, identities []ExternalIdentity) ([]ExternalIdentity, error) {
	seen := make(map[string]string, len(identities))
	unique := make(map[string]struct{}, len(identities))
	result := make([]ExternalIdentity, 0, len(identities))
	for _, identity := range identities {
		namespace := strings.ToLower(strings.TrimSpace(identity.Namespace))
		value := strings.TrimSpace(identity.Value)
		if namespace == "" || value == "" || identity.State != IdentityVerified && identity.State != IdentityUnverified && identity.State != IdentityNeedsAttention {
			return nil, ErrIdentityConflict
		}
		if identity.State == IdentityVerified && ((namespace == "tmdb" || namespace == "tvdb") && !numericProviderID.MatchString(value) || namespace == "imdb" && !imdbProviderID.MatchString(strings.ToLower(value))) {
			return nil, ErrInvalidIdentity
		}
		if namespace == "imdb" {
			value = strings.ToLower(value)
		}
		key := string(kind) + ":" + namespace
		if previous, exists := seen[key]; exists && previous != value {
			return nil, ErrIdentityConflict
		}
		seen[key] = value
		identity.Namespace = namespace
		identity.Value = value
		identity.Source = strings.TrimSpace(identity.Source)
		exact := key + "\x00" + value
		if _, duplicate := unique[exact]; duplicate {
			continue
		}
		unique[exact] = struct{}{}
		result = append(result, identity)
	}
	return result, nil
}
