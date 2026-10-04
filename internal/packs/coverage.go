package packs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"uuid"

	"github.com/media-manager/media-manager/internal/library"
)

type EpisodeKey struct {
	SeriesID string `json:"series_id"`
	Season   uint16 `json:"season"`
	Episode  uint16 `json:"episode"`
}

func (key EpisodeKey) String() string {
	return fmt.Sprintf("%s:S%02dE%02d", key.SeriesID, key.Season, key.Episode)
}

type ExpectedEpisode struct {
	Key               EpisodeKey `json:"key"`
	ProviderEpisodeID string     `json:"provider_episode_id"`
	Title             string     `json:"title"`
	AirDate           string     `json:"air_date,omitempty"`
}

type ObservedAsset struct {
	ID           uuid.UUID `json:"id"`
	RelativePath string    `json:"relative_path"`
	Size         int64     `json:"size"`
}

type ContentGroup struct {
	ID       string       `json:"id"`
	AssetIDs []uuid.UUID  `json:"asset_ids"`
	Episodes []EpisodeKey `json:"episodes"`
}

type Issue struct {
	Code     string       `json:"code"`
	Path     string       `json:"path,omitempty"`
	Episodes []EpisodeKey `json:"episodes,omitempty"`
	Detail   string       `json:"detail"`
}

type Verdict string

const (
	VerdictVerified    Verdict = "verified"
	VerdictIncomplete  Verdict = "incomplete"
	VerdictNeedsReview Verdict = "needs_review"
	VerdictInvalid     Verdict = "invalid"
)

type Report struct {
	Expected        []EpisodeKey   `json:"expected"`
	Covered         []EpisodeKey   `json:"covered"`
	Missing         []EpisodeKey   `json:"missing"`
	Groups          []ContentGroup `json:"groups"`
	Issues          []Issue        `json:"issues"`
	InventoryDigest string         `json:"inventory_digest"`
	Verdict         Verdict        `json:"verdict"`
}

func (report Report) Publishable() bool {
	return report.Verdict == VerdictVerified && len(report.Missing) == 0 && len(report.Covered) == len(report.Expected)
}

var episodePattern = regexp.MustCompile(`(?i)S(\d{1,3})E(\d{1,3})(?:-E(\d{1,3})|((?:E\d{1,3})*))?`)
var chainedEpisodePattern = regexp.MustCompile(`(?i)E(\d{1,3})`)
var playableExtensions = map[string]struct{}{".mkv": {}, ".mp4": {}, ".m4v": {}, ".avi": {}, ".ts": {}}

func Evaluate(seriesID string, season uint16, expected []ExpectedEpisode, assets []ObservedAsset) Report {
	return EvaluateNumbered(seriesID, season, expected, assets, EpisodeNumbering{})
}

// EvaluateNumbered keeps inventory paths unchanged while resolving explicitly
// numbered annual episodes against a verified frozen metadata mapping.
func EvaluateNumbered(seriesID string, season uint16, expected []ExpectedEpisode, assets []ObservedAsset, numbering EpisodeNumbering) Report {
	report := Report{}
	expectedSet := make(map[EpisodeKey]struct{}, len(expected))
	if strings.TrimSpace(seriesID) == "" || len(expected) == 0 {
		report.Issues = append(report.Issues, Issue{Code: "invalid_expectation", Detail: "a pack requires a non-empty series identity and expectation snapshot"})
	}
	for _, episode := range expected {
		if episode.Key.SeriesID != seriesID || episode.Key.Season != season || episode.Key.Episode == 0 {
			report.Issues = append(report.Issues, Issue{Code: "invalid_expectation", Episodes: []EpisodeKey{episode.Key}, Detail: "expectation is outside the pack scope"})
			continue
		}
		if _, duplicate := expectedSet[episode.Key]; duplicate {
			report.Issues = append(report.Issues, Issue{Code: "invalid_expectation", Episodes: []EpisodeKey{episode.Key}, Detail: "episode appears more than once in the immutable expectation"})
			continue
		}
		expectedSet[episode.Key] = struct{}{}
	}
	for key := range expectedSet {
		report.Expected = append(report.Expected, key)
	}
	sortKeys(report.Expected)

	sortedAssets := append([]ObservedAsset(nil), assets...)
	sort.Slice(sortedAssets, func(i, j int) bool { return sortedAssets[i].RelativePath < sortedAssets[j].RelativePath })
	hash := sha256.New()
	owners := make(map[EpisodeKey][]string)
	paths := make(map[string]uuid.UUID)
	assetIDs := make(map[uuid.UUID]string)

	for _, asset := range sortedAssets {
		clean, err := library.ValidateRelative(asset.RelativePath)
		collisionKey := strings.ToLower(filepath.ToSlash(clean))
		if err != nil || clean != asset.RelativePath {
			report.Issues = append(report.Issues, Issue{Code: "unsafe_path", Path: asset.RelativePath, Detail: "asset escapes or is not canonical"})
			continue
		}
		if asset.ID == (uuid.UUID{}) || asset.Size <= 0 {
			report.Issues = append(report.Issues, Issue{Code: "invalid_asset", Path: clean, Detail: "asset needs a stable identity and positive size"})
			continue
		}
		if previous, exists := paths[collisionKey]; exists && previous != asset.ID {
			report.Issues = append(report.Issues, Issue{Code: "path_collision", Path: clean, Detail: "two assets share a case-insensitive canonical path"})
			continue
		}
		if previous, exists := assetIDs[asset.ID]; exists && previous != clean {
			report.Issues = append(report.Issues, Issue{Code: "invalid_asset", Path: clean, Detail: "one asset identity was reused for different paths"})
			continue
		}
		paths[collisionKey] = asset.ID
		assetIDs[asset.ID] = clean
		fmt.Fprintf(hash, "coverage-v2\x00%s\x00%s\x00%d\x00", asset.ID.String(), clean, asset.Size)
		extension := strings.ToLower(filepath.Ext(clean))
		if _, playable := playableExtensions[extension]; !playable {
			continue
		}
		if isSample(clean) {
			continue
		}

		keys, err := parseEpisodeKeys(seriesID, season, filepath.Base(clean))
		if episode, broadcast := numbering.BroadcastEpisode(filepath.Base(clean)); broadcast {
			if episode > 0 && numbering.Season == season {
				keys, err = []EpisodeKey{{SeriesID: seriesID, Season: season, Episode: episode}}, nil
			} else {
				err = errors.New("broadcast numbering does not match the immutable season expectation")
			}
		}
		if err != nil {
			report.Issues = append(report.Issues, Issue{Code: "ambiguous", Path: clean, Detail: err.Error()})
			continue
		}
		group := ContentGroup{ID: groupID(asset.ID, keys), AssetIDs: []uuid.UUID{asset.ID}, Episodes: keys}
		report.Groups = append(report.Groups, group)
		for _, key := range keys {
			if _, exists := expectedSet[key]; !exists {
				report.Issues = append(report.Issues, Issue{Code: "unexpected", Path: clean, Episodes: []EpisodeKey{key}, Detail: "episode is not part of the immutable expectation"})
				continue
			}
			owners[key] = append(owners[key], group.ID)
		}
	}

	for _, key := range report.Expected {
		switch len(owners[key]) {
		case 0:
			report.Missing = append(report.Missing, key)
		case 1:
			report.Covered = append(report.Covered, key)
		default:
			report.Issues = append(report.Issues, Issue{Code: "duplicate", Episodes: []EpisodeKey{key}, Detail: "more than one content group owns the episode"})
		}
	}

	report.InventoryDigest = hex.EncodeToString(hash.Sum(nil))
	report.Verdict = chooseVerdict(report)
	sort.Slice(report.Groups, func(i, j int) bool { return report.Groups[i].ID < report.Groups[j].ID })
	sort.Slice(report.Issues, func(i, j int) bool {
		if report.Issues[i].Code == report.Issues[j].Code {
			return report.Issues[i].Path < report.Issues[j].Path
		}
		return report.Issues[i].Code < report.Issues[j].Code
	})
	return report
}

func parseEpisodeKeys(seriesID string, expectedSeason uint16, name string) ([]EpisodeKey, error) {
	indices := episodePattern.FindAllStringSubmatchIndex(name, -1)
	matches := episodePattern.FindAllStringSubmatch(name, -1)
	if len(matches) != 1 {
		return nil, errors.New("filename must contain exactly one unambiguous Plex-style episode token")
	}
	start, end := indices[0][0], indices[0][1]
	if (start > 0 && isASCIIAlphaNumeric(name[start-1])) || (end < len(name) && isASCIIAlphaNumeric(name[end])) {
		return nil, errors.New("episode token must have clear filename boundaries")
	}
	season64, _ := strconv.ParseUint(matches[0][1], 10, 16)
	first64, _ := strconv.ParseUint(matches[0][2], 10, 16)
	if uint16(season64) != expectedSeason || first64 == 0 {
		return nil, errors.New("episode token is outside the expected season")
	}
	numbers := []uint64{first64}
	if matches[0][3] != "" {
		last64, _ := strconv.ParseUint(matches[0][3], 10, 16)
		if last64 < first64 || last64-first64 > 99 {
			return nil, errors.New("episode range is invalid")
		}
		for episode := first64 + 1; episode <= last64; episode++ {
			numbers = append(numbers, episode)
		}
	} else if matches[0][4] != "" {
		for _, chained := range chainedEpisodePattern.FindAllStringSubmatch(matches[0][4], -1) {
			number, _ := strconv.ParseUint(chained[1], 10, 16)
			if number == 0 || number != numbers[len(numbers)-1]+1 {
				return nil, errors.New("episode chain must be contiguous for Plex-safe naming")
			}
			numbers = append(numbers, number)
		}
	}
	seen := make(map[uint64]struct{}, len(numbers))
	keys := make([]EpisodeKey, 0, len(numbers))
	for _, episode := range numbers {
		if _, duplicate := seen[episode]; duplicate {
			return nil, errors.New("episode token repeats an episode")
		}
		seen[episode] = struct{}{}
		keys = append(keys, EpisodeKey{SeriesID: seriesID, Season: expectedSeason, Episode: uint16(episode)})
	}
	sortKeys(keys)
	return keys, nil
}

func chooseVerdict(report Report) Verdict {
	for _, issue := range report.Issues {
		if issue.Code == "unsafe_path" || issue.Code == "invalid_expectation" || issue.Code == "invalid_asset" || issue.Code == "path_collision" {
			return VerdictInvalid
		}
	}
	for _, issue := range report.Issues {
		if issue.Code == "duplicate" || issue.Code == "ambiguous" || issue.Code == "unexpected" {
			return VerdictNeedsReview
		}
	}
	if len(report.Missing) > 0 {
		return VerdictIncomplete
	}
	return VerdictVerified
}

func sortKeys(keys []EpisodeKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Season == keys[j].Season {
			return keys[i].Episode < keys[j].Episode
		}
		return keys[i].Season < keys[j].Season
	})
}

func isSample(path string) bool {
	for _, component := range strings.Split(strings.ToLower(filepath.ToSlash(path)), "/") {
		stem := strings.TrimSuffix(component, filepath.Ext(component))
		for _, token := range regexp.MustCompile(`[^a-z0-9]+`).Split(stem, -1) {
			if token == "sample" || token == "samples" {
				return true
			}
		}
	}
	return false
}

func groupID(assetID uuid.UUID, keys []EpisodeKey) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "group-v1\x00%s\x00", assetID.String())
	for _, key := range keys {
		fmt.Fprintf(hash, "%s\x00", key.String())
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func isASCIIAlphaNumeric(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}
