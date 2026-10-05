package packs

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/media-manager/media-manager/internal/library"
	"uuid"
)

type SeasonRange struct {
	First uint16
	Last  uint16
}

var seasonRangeToken = regexp.MustCompile(`(?i)S([0-9]{1,3})-S([0-9]{1,3})`)
var seasonRangeMarker = regexp.MustCompile(`(?i)(?:^|[ ._])S[0-9]{1,3}-S`)

func HasSeasonRange(title string) bool { return seasonRangeMarker.MatchString(title) }

func DeclaredSeasonRange(title string) (SeasonRange, bool) {
	matches := seasonRangeToken.FindAllStringSubmatch(title, -1)
	if len(matches) != 1 || episodePattern.MatchString(title) {
		return SeasonRange{}, false
	}
	indices := seasonRangeToken.FindStringIndex(title)
	if (indices[0] > 0 && isASCIIAlphaNumeric(title[indices[0]-1])) || (indices[1] < len(title) && isASCIIAlphaNumeric(title[indices[1]])) {
		return SeasonRange{}, false
	}
	first, _ := strconv.Atoi(matches[0][1])
	last, _ := strconv.Atoi(matches[0][2])
	if first < 1 || last <= first || last > 100 || last-first > 50 {
		return SeasonRange{}, false
	}
	return SeasonRange{First: uint16(first), Last: uint16(last)}, true
}

// EvaluateSeasonRange preserves the whole inventory but verifies only the
// frozen requested scope. Extra named episodes from the declared collection
// are kept on disk and cannot be published by this workflow. Unsafe, ambiguous
// and out-of-range playable files remain verification failures.
func EvaluateSeasonRange(seriesID string, season uint16, expected []ExpectedEpisode, assets []ObservedAsset, numbering EpisodeNumbering, declared SeasonRange) Report {
	if season < declared.First || season > declared.Last || declared.First == 0 || declared.Last < declared.First {
		report := EvaluateNumbered(seriesID, season, expected, assets, numbering)
		report.Issues = append(report.Issues, Issue{Code: "invalid_expectation", Detail: "requested season is outside the declared collection"})
		report.Verdict = chooseVerdict(report)
		return report
	}
	wanted := map[EpisodeKey]bool{}
	for _, e := range expected {
		wanted[e.Key] = true
	}
	selected := []ObservedAsset{}
	for _, asset := range assets {
		clean, err := library.ValidateRelative(asset.RelativePath)
		if err != nil || clean != asset.RelativePath || asset.ID == (uuid.UUID{}) || asset.Size <= 0 {
			selected = append(selected, asset)
			continue
		}
		if _, playable := playableExtensions[strings.ToLower(filepath.Ext(clean))]; !playable || isSample(clean) {
			selected = append(selected, asset)
			continue
		}
		name := filepath.Base(clean)
		matches := episodePattern.FindAllStringSubmatch(name, -1)
		if len(matches) != 1 {
			selected = append(selected, asset) // Existing evaluator rejects ambiguity.
			continue
		}
		observed, _ := strconv.Atoi(matches[0][1])
		keys, parseErr := parseEpisodeKeys(seriesID, uint16(observed), name)
		if parseErr != nil || observed < int(declared.First) || observed > int(declared.Last) {
			selected = append(selected, asset)
			continue
		}
		for _, key := range keys {
			if wanted[key] {
				selected = append(selected, asset)
				break
			}
		}
	}
	report := EvaluateNumbered(seriesID, season, expected, selected, numbering)
	whole := EvaluateNumbered(seriesID, season, expected, assets, numbering)
	report.InventoryDigest = whole.InventoryDigest
	for _, issue := range whole.Issues {
		if issue.Code == "unsafe_path" || issue.Code == "path_collision" || issue.Code == "invalid_asset" {
			report.Issues = append(report.Issues, issue)
		}
	}
	report.Verdict = chooseVerdict(report)
	return report
}
