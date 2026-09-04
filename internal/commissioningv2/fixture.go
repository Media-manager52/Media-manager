package commissioningv2

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
)

const (
	maximumFixtureDocumentBytes = 64 << 10
	maximumFixtureTitleBytes    = 500
	maximumFixtureIdentities    = 4
	minimumFixtureYear          = 1800
	maximumFixtureYear          = 9999
	maximumFixtureSeason        = 999
)

var (
	fixtureKeyPattern = regexp.MustCompile(`^cfx_[a-f0-9]{32}$`)
	uuidPattern       = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[1-8][a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
	imdbPattern       = regexp.MustCompile(`^tt[0-9]{7,12}$`)
	numericIDPattern  = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
)

type FixtureSource string

const (
	FixtureTrustedLocalMetadata FixtureSource = "trusted_local_metadata"
	FixtureExplicitOwner        FixtureSource = "explicit_owner_assertion"
)

type IdentityNamespace string

const (
	IdentityIMDb   IdentityNamespace = "imdb"
	IdentityTMDb   IdentityNamespace = "tmdb"
	IdentityTVDb   IdentityNamespace = "tvdb"
	IdentityTVMaze IdentityNamespace = "tvmaze"
)

// VerifiedIdentity is a trusted input assertion. It carries no inferred or
// unverified state, so callers selecting catalog identities must pass only
// catalog identities already marked verified.
type VerifiedIdentity struct {
	Namespace IdentityNamespace `json:"namespace"`
	Value     string            `json:"value"`
}

type LocalFixtureRef struct {
	MediaID  string `json:"media_id"`
	Revision uint64 `json:"revision"`
}

type MovieFixture struct {
	Key            string             `json:"key"`
	CanonicalTitle string             `json:"canonical_title"`
	Year           *uint16            `json:"year,omitempty"`
	Identities     []VerifiedIdentity `json:"identities,omitempty"`
	Local          *LocalFixtureRef   `json:"local,omitempty"`
	Source         FixtureSource      `json:"source"`
}

type CompleteSeasonFixture struct {
	Key            string             `json:"key"`
	CanonicalTitle string             `json:"canonical_title"`
	Season         uint16             `json:"season"`
	Year           *uint16            `json:"year,omitempty"`
	Identities     []VerifiedIdentity `json:"identities,omitempty"`
	Local          *LocalFixtureRef   `json:"local,omitempty"`
	Source         FixtureSource      `json:"source"`
}

type FixtureDocument struct {
	SchemaVersion  uint16                 `json:"schema_version"`
	Movie          *MovieFixture          `json:"movie,omitempty"`
	CompleteSeason *CompleteSeasonFixture `json:"complete_season,omitempty"`
}

func NewFixtureKey() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("generate commissioning fixture key")
	}
	return "cfx_" + hex.EncodeToString(random[:]), nil
}

// NormalizeFixtureDocument returns a detached canonical snapshot. It trims
// owner text, normalizes identifiers, and orders identifiers deterministically.
func NormalizeFixtureDocument(input FixtureDocument) (FixtureDocument, error) {
	result := FixtureDocument{SchemaVersion: input.SchemaVersion}
	if input.Movie != nil {
		movie := *input.Movie
		movie.CanonicalTitle = strings.TrimSpace(movie.CanonicalTitle)
		movie.Identities = append([]VerifiedIdentity(nil), movie.Identities...)
		if movie.Local != nil {
			local := *movie.Local
			movie.Local = &local
		}
		if movie.Year != nil {
			year := *movie.Year
			movie.Year = &year
		}
		identities, err := normalizeFixtureIdentities(movie.Identities)
		if err != nil {
			return FixtureDocument{}, err
		}
		movie.Identities = identities
		result.Movie = &movie
	}
	if input.CompleteSeason != nil {
		season := *input.CompleteSeason
		season.CanonicalTitle = strings.TrimSpace(season.CanonicalTitle)
		season.Identities = append([]VerifiedIdentity(nil), season.Identities...)
		if season.Local != nil {
			local := *season.Local
			season.Local = &local
		}
		if season.Year != nil {
			year := *season.Year
			season.Year = &year
		}
		identities, err := normalizeFixtureIdentities(season.Identities)
		if err != nil {
			return FixtureDocument{}, err
		}
		season.Identities = identities
		result.CompleteSeason = &season
	}
	if err := ValidateFixtureDocument(result); err != nil {
		return FixtureDocument{}, err
	}
	return result, nil
}

func ValidateFixtureDocument(document FixtureDocument) error {
	if document.SchemaVersion != DocumentSchemaVersion || document.Movie == nil && document.CompleteSeason == nil {
		return ErrInvalidCommissioningDocument
	}
	keys := make(map[string]struct{}, 2)
	if document.Movie != nil {
		if !validateFixtureBase(document.Movie.Key, document.Movie.CanonicalTitle, document.Movie.Year, document.Movie.Identities, document.Movie.Local, document.Movie.Source) {
			return ErrInvalidCommissioningDocument
		}
		keys[document.Movie.Key] = struct{}{}
	}
	if document.CompleteSeason != nil {
		fixture := document.CompleteSeason
		if fixture.Season > maximumFixtureSeason || !validateFixtureBase(fixture.Key, fixture.CanonicalTitle, fixture.Year, fixture.Identities, fixture.Local, fixture.Source) {
			return ErrInvalidCommissioningDocument
		}
		if _, duplicate := keys[fixture.Key]; duplicate {
			return ErrInvalidCommissioningDocument
		}
	}
	return nil
}

func DecodeFixtureDocument(raw []byte) (FixtureDocument, error) {
	if len(raw) == 0 || len(raw) > maximumFixtureDocumentBytes || !utf8.Valid(raw) {
		return FixtureDocument{}, ErrInvalidCommissioningDocument
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document FixtureDocument
	if err := decoder.Decode(&document); err != nil {
		return FixtureDocument{}, ErrInvalidCommissioningDocument
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return FixtureDocument{}, ErrInvalidCommissioningDocument
	}
	if err := ValidateFixtureDocument(document); err != nil {
		return FixtureDocument{}, err
	}
	return document, nil
}

func CanonicalFixtureDocument(document FixtureDocument) ([]byte, error) {
	normalized, err := NormalizeFixtureDocument(document)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, ErrInvalidCommissioningDocument
	}
	return encoded, nil
}

func FixtureDocumentDigest(document FixtureDocument) (SHA256Digest, error) {
	encoded, err := CanonicalFixtureDocument(document)
	if err != nil {
		return SHA256Digest{}, err
	}
	return SumSHA256(encoded), nil
}

func validateFixtureBase(key, title string, year *uint16, identities []VerifiedIdentity, local *LocalFixtureRef, source FixtureSource) bool {
	if !fixtureKeyPattern.MatchString(key) || strings.TrimSpace(title) != title || !validFixtureText(title, maximumFixtureTitleBytes) || len(identities) > maximumFixtureIdentities {
		return false
	}
	if year != nil && (*year < minimumFixtureYear || *year > maximumFixtureYear) {
		return false
	}
	if source != FixtureTrustedLocalMetadata && source != FixtureExplicitOwner {
		return false
	}
	if source == FixtureTrustedLocalMetadata && local == nil {
		return false
	}
	if local != nil && (!uuidPattern.MatchString(local.MediaID) || local.Revision == 0) {
		return false
	}
	previous := IdentityNamespace("")
	for _, identity := range identities {
		if identity.Namespace <= previous || !validFixtureIdentity(identity) {
			return false
		}
		previous = identity.Namespace
	}
	return true
}

func normalizeFixtureIdentities(input []VerifiedIdentity) ([]VerifiedIdentity, error) {
	if len(input) > maximumFixtureIdentities {
		return nil, ErrInvalidCommissioningDocument
	}
	result := append([]VerifiedIdentity(nil), input...)
	for index := range result {
		result[index].Namespace = IdentityNamespace(strings.ToLower(strings.TrimSpace(string(result[index].Namespace))))
		result[index].Value = strings.TrimSpace(result[index].Value)
		if result[index].Namespace == IdentityIMDb {
			result[index].Value = strings.ToLower(result[index].Value)
		}
		if !validFixtureIdentity(result[index]) {
			return nil, ErrInvalidCommissioningDocument
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Namespace < result[right].Namespace
	})
	for index := 1; index < len(result); index++ {
		if result[index-1].Namespace == result[index].Namespace {
			return nil, ErrInvalidCommissioningDocument
		}
	}
	return result, nil
}

func validFixtureIdentity(identity VerifiedIdentity) bool {
	switch identity.Namespace {
	case IdentityIMDb:
		return imdbPattern.MatchString(identity.Value)
	case IdentityTMDb, IdentityTVDb, IdentityTVMaze:
		return numericIDPattern.MatchString(identity.Value)
	default:
		return false
	}
}

func validFixtureText(value string, maximum int) bool {
	return value != "" && newznab.ValidStructurallySafePersistedText(value, maximum)
}
