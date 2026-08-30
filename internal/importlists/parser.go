package importlists

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const MaxItems = 5000

type Item struct {
	Ordinal    int     `json:"ordinal"`
	MediaKind  string  `json:"media_kind"`
	Provider   string  `json:"provider"`
	ProviderID string  `json:"provider_id"`
	Title      string  `json:"title"`
	Year       *uint16 `json:"year,omitempty"`
	State      string  `json:"state"`
	Attention  string  `json:"attention,omitempty"`
}

func ParseIMDbExport(reader io.Reader) ([]Item, error) {
	csvReader := csv.NewReader(reader)
	csvReader.FieldsPerRecord = -1
	header, err := csvReader.Read()
	if err != nil {
		return nil, errors.New("IMDb export is empty")
	}
	columns := map[string]int{}
	for index, value := range header {
		columns[strings.ToLower(strings.TrimSpace(value))] = index
	}
	for _, required := range []string{"const", "title", "title type", "year"} {
		if _, ok := columns[required]; !ok {
			return nil, fmt.Errorf("IMDb export is missing %q column", required)
		}
	}
	items := []Item{}
	seen := map[string]bool{}
	for rowNumber := 2; ; rowNumber++ {
		row, readErr := csvReader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read IMDb export row %d: %w", rowNumber, readErr)
		}
		providerID := strings.ToLower(field(row, columns["const"]))
		title := strings.TrimSpace(field(row, columns["title"]))
		if !validIMDbID(providerID) || title == "" {
			return nil, fmt.Errorf("IMDb export row %d has an invalid identity or title", rowNumber)
		}
		if seen[providerID] {
			return nil, fmt.Errorf("IMDb export contains duplicate identity %s", providerID)
		}
		seen[providerID] = true
		if len(items) >= MaxItems {
			return nil, fmt.Errorf("IMDb export exceeds %d items", MaxItems)
		}
		kind, state, attention := classifyIMDbType(field(row, columns["title type"]))
		item := Item{Ordinal: len(items) + 1, MediaKind: kind, Provider: "imdb", ProviderID: providerID, Title: title, State: state, Attention: attention}
		yearText := strings.TrimSpace(field(row, columns["year"]))
		if yearText != "" {
			year, parseErr := strconv.ParseUint(yearText, 10, 16)
			if parseErr != nil || year < 1870 || year > 2100 {
				return nil, fmt.Errorf("IMDb export row %d has an invalid year", rowNumber)
			}
			converted := uint16(year)
			item.Year = &converted
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, errors.New("IMDb export contains no items")
	}
	return items, nil
}

func field(row []string, index int) string {
	if index < 0 || index >= len(row) {
		return ""
	}
	return row[index]
}

func validIMDbID(value string) bool {
	if len(value) < 9 || len(value) > 14 || !strings.HasPrefix(value, "tt") {
		return false
	}
	for _, character := range value[2:] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func classifyIMDbType(value string) (string, string, string) {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(value)))
	switch normalized {
	case "movie", "tvmovie":
		return "movie", "pending_review", ""
	case "tvseries", "tvminiseries":
		return "series", "unsupported", "Exact TV season selection is required before monitoring."
	default:
		return "unknown", "unsupported", "This IMDb title type is not supported for monitoring."
	}
}
