package library

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var invalidComponent = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
var safeProviderID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$`)
var safeExtension = regexp.MustCompile(`^[a-z0-9]{1,10}$`)
var mediaExtensions = map[string]struct{}{"mkv": {}, "mp4": {}, "m4v": {}, "avi": {}, "ts": {}}

type MovieName struct {
	Title      string
	Year       uint16
	Provider   string
	ProviderID string
	Edition    string
	Technical  []string
	Extension  string
}

func RenderMovie(name MovieName) (folder, file string, err error) {
	if strings.TrimSpace(name.Title) == "" || name.Year == 0 || strings.TrimSpace(name.ProviderID) == "" {
		return "", "", ErrUnsafePath
	}
	provider := strings.ToLower(strings.TrimSpace(name.Provider))
	if provider != "tmdb" && provider != "imdb" || !safeProviderID.MatchString(name.ProviderID) {
		return "", "", ErrUnsafePath
	}
	identity := fmt.Sprintf(" (%d) {%s-%s}", name.Year, provider, name.ProviderID)
	edition := ""
	if name.Edition != "" {
		cleaned := cleanComponent(name.Edition)
		if cleaned == "Unknown" {
			return "", "", ErrUnsafePath
		}
		edition = " {edition-" + cleaned + "}"
	}
	title := truncateComponent(cleanComponent(name.Title), 180-len(identity)-len(edition))
	if title == "" {
		return "", "", ErrUnsafePath
	}
	base := title + identity + edition
	folder = base
	technical := cleanTechnical(name.Technical)
	fileBase := base
	if len(technical) > 0 {
		fileBase += " - [" + strings.Join(technical, " ") + "]"
	}
	extension, err := validateExtension(name.Extension)
	if err != nil {
		return "", "", err
	}
	file = truncateComponent(fileBase, 220-len(extension)-1) + "." + extension
	return folder, file, nil
}

func RenderEpisode(show string, year uint16, provider, providerID string, season uint16, firstEpisode, lastEpisode uint16, title string, technical []string, extension string) (folder, seasonFolder, file string, err error) {
	if firstEpisode == 0 || lastEpisode < firstEpisode || lastEpisode-firstEpisode > 99 {
		return "", "", "", ErrUnsafePath
	}
	episodes := make([]uint16, 0, lastEpisode-firstEpisode+1)
	for episode := firstEpisode; episode <= lastEpisode; episode++ {
		episodes = append(episodes, episode)
	}
	return RenderEpisodeSet(show, year, provider, providerID, season, episodes, title, technical, extension)
}

func RenderEpisodeSet(show string, year uint16, provider, providerID string, season uint16, episodes []uint16, title string, technical []string, extension string) (folder, seasonFolder, file string, err error) {
	if len(episodes) == 0 {
		return "", "", "", ErrUnsafePath
	}
	episodes = append([]uint16(nil), episodes...)
	sort.Slice(episodes, func(i, j int) bool { return episodes[i] < episodes[j] })
	for index, episode := range episodes {
		if episode == 0 || index > 0 && episode != episodes[index-1]+1 {
			return "", "", "", ErrUnsafePath
		}
	}
	firstEpisode, lastEpisode := episodes[0], episodes[len(episodes)-1]
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != "tvdb" && provider != "tmdb" || !safeProviderID.MatchString(providerID) || strings.TrimSpace(show) == "" || year == 0 {
		return "", "", "", ErrUnsafePath
	}
	identity := fmt.Sprintf(" (%d) {%s-%s}", year, provider, providerID)
	showTitle := cleanComponent(show)
	folder = truncateComponent(showTitle, 180-len(identity)) + identity
	seasonFolder = fmt.Sprintf("Season %02d", season)
	episodeToken := fmt.Sprintf("S%02dE%02d", season, firstEpisode)
	if lastEpisode > firstEpisode {
		episodeToken += fmt.Sprintf("-E%02d", lastEpisode)
	}
	prefix := fmt.Sprintf(" (%d) - %s", year, episodeToken)
	base := truncateComponent(showTitle, 170-len(prefix)) + prefix
	if title != "" && firstEpisode == lastEpisode {
		base += " - " + cleanComponent(title)
	}
	if facts := cleanTechnical(technical); len(facts) > 0 {
		base += " [" + strings.Join(facts, " ") + "]"
	}
	ext, err := validateExtension(extension)
	if err != nil {
		return "", "", "", err
	}
	file = truncateComponent(base, 220-len(ext)-1) + "." + ext
	return folder, seasonFolder, file, nil
}

func cleanTechnical(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.Join(strings.Fields(cleanComponent(value)), "")
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func cleanComponent(value string) string {
	value = invalidComponent.ReplaceAllString(strings.TrimSpace(value), "-")
	value = strings.Join(strings.Fields(value), " ")
	value = strings.Trim(value, " .")
	if value == "" {
		return "Unknown"
	}
	return value
}

func truncateComponent(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	for len(value) > maxBytes {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return strings.TrimRight(value, " .-")
}

func validateExtension(value string) (string, error) {
	extension := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
	if !safeExtension.MatchString(extension) {
		return "", ErrUnsafePath
	}
	if _, supported := mediaExtensions[extension]; !supported {
		return "", ErrUnsafePath
	}
	return extension, nil
}

func DestinationRelative(parts ...string) (string, error) {
	for _, part := range parts {
		if _, err := ValidateRelative(part); err != nil {
			return "", err
		}
	}
	return ValidateRelative(filepath.Join(parts...))
}
