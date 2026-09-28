// Package servarr defines the small Radarr/Sonarr-compatible API surface that
// Seerr needs so Media Manager can replace the *arr applications entirely.
//
// Seerr configures a "Radarr" and a "Sonarr" server, validates it with
// system/status, qualityProfile, rootfolder, and tag, and then adds requested
// movies and series through POST endpoints. This package provides the JSON
// shapes and stable synthetic IDs for that conversation; the HTTP handlers and
// SQL live in internal/httpapi and internal/postgres. It has no I/O.
package servarr

import (
	"hash/crc32"
	"strings"
	"time"
)

// Fake versions reported to Seerr. Radarr 5.x and Sonarr 4.x are both recent
// enough that Seerr skips the removed language-profile flow for Sonarr.
const (
	RadarrVersion = "5.14.0.9383"
	SonarrVersion = "4.0.10.2544"
)

// SyntheticID derives a stable positive int32 from a Media Manager UUID or
// provider value. Seerr expects integer IDs for profiles, root folders, and
// library items; Media Manager keeps UUIDs, so the same input always maps to
// the same integer without an extra table.
func SyntheticID(namespace, value string) int {
	sum := crc32.ChecksumIEEE([]byte(namespace + ":" + value))
	id := int(sum & 0x7fffffff)
	if id == 0 {
		id = 1
	}
	return id
}

// Slug renders the *arr titleSlug form Seerr stores as externalServiceSlug.
func Slug(title string) string {
	lower := strings.ToLower(strings.TrimSpace(title))
	var builder strings.Builder
	lastDash := false
	for _, character := range lower {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			builder.WriteRune(character)
			lastDash = false
		case character == '-':
			if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		default:
			if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(builder.String(), "-")
}

type SystemStatus struct {
	AppName           string `json:"appName"`
	InstanceName      string `json:"instanceName"`
	Version           string `json:"version"`
	BuildTime         string `json:"buildTime"`
	IsDebug           bool   `json:"isDebug"`
	IsProduction      bool   `json:"isProduction"`
	IsAdmin           bool   `json:"isAdmin"`
	IsUserInteractive bool   `json:"isUserInteractive"`
	StartupPath       string `json:"startupPath"`
	AppData           string `json:"appData"`
	OsName            string `json:"osName"`
	OsVersion         string `json:"osVersion"`
	IsNetCore         bool   `json:"isNetCore"`
	IsLinux           bool   `json:"isLinux"`
	IsOsx             bool   `json:"isOsx"`
	IsWindows         bool   `json:"isWindows"`
	IsDocker          bool   `json:"isDocker"`
	Mode              string `json:"mode"`
	Branch            string `json:"branch"`
	Authentication    string `json:"authentication"`
	StartTime         string `json:"startTime"`
	PackageVersion    string `json:"packageVersion"`
}

func NewSystemStatus(appName, version string) SystemStatus {
	return SystemStatus{
		AppName:        appName,
		InstanceName:   "Media Manager",
		Version:        version,
		BuildTime:      time.Now().UTC().Format(time.RFC3339),
		IsProduction:   true,
		IsAdmin:        true,
		StartupPath:    "/",
		AppData:        "/",
		OsName:         "linux",
		IsNetCore:      true,
		IsLinux:        true,
		IsDocker:       true,
		Mode:           "console",
		Branch:         "main",
		Authentication: "none",
		StartTime:      time.Now().UTC().Format(time.RFC3339),
		PackageVersion: version,
	}
}

type QualityProfile struct {
	ID             int        `json:"id"`
	Name           string     `json:"name"`
	UpgradeAllowed bool       `json:"upgradeAllowed"`
	Cutoff         int        `json:"cutoff"`
	Items          []struct{} `json:"items"`
}

type RootFolder struct {
	ID              int        `json:"id"`
	Path            string     `json:"path"`
	Accessible      bool       `json:"accessible"`
	FreeSpace       int64      `json:"freeSpace"`
	UnmappedFolders []struct{} `json:"unmappedFolders"`
}

type Tag struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

type QueuePage struct {
	Page          int        `json:"page"`
	PageSize      int        `json:"pageSize"`
	SortKey       string     `json:"sortKey"`
	SortDirection string     `json:"sortDirection"`
	TotalRecords  int        `json:"totalRecords"`
	Records       []struct{} `json:"records"`
}

func EmptyQueue() QueuePage {
	return QueuePage{Page: 1, PageSize: 20, SortKey: "timeleft", SortDirection: "ascending", TotalRecords: 0, Records: []struct{}{}}
}

type Movie struct {
	ID                  int    `json:"id"`
	Title               string `json:"title"`
	OriginalTitle       string `json:"originalTitle,omitempty"`
	TMDBID              int64  `json:"tmdbId"`
	Year                int    `json:"year"`
	TitleSlug           string `json:"titleSlug"`
	Monitored           bool   `json:"monitored"`
	MinimumAvailability string `json:"minimumAvailability,omitempty"`
	HasFile             bool   `json:"hasFile"`
	QualityProfileID    int    `json:"qualityProfileId,omitempty"`
	RootFolderPath      string `json:"rootFolderPath,omitempty"`
	Added               string `json:"added,omitempty"`
	Tags                []Tag  `json:"tags"`
}

type SeasonStatistics struct {
	EpisodeFileCount  int `json:"episodeFileCount"`
	EpisodeCount      int `json:"episodeCount"`
	TotalEpisodeCount int `json:"totalEpisodeCount"`
}

type SeriesSeason struct {
	SeasonNumber int              `json:"seasonNumber"`
	Monitored    bool             `json:"monitored"`
	Statistics   SeasonStatistics `json:"statistics"`
}

type Series struct {
	ID                int            `json:"id"`
	Title             string         `json:"title"`
	TVDBID            int64          `json:"tvdbId"`
	Year              int            `json:"year"`
	TitleSlug         string         `json:"titleSlug"`
	Monitored         bool           `json:"monitored"`
	SeasonFolder      bool           `json:"seasonFolder"`
	QualityProfileID  int            `json:"qualityProfileId,omitempty"`
	LanguageProfileID int            `json:"languageProfileId,omitempty"`
	RootFolderPath    string         `json:"rootFolderPath,omitempty"`
	Added             string         `json:"added,omitempty"`
	Seasons           []SeriesSeason `json:"seasons"`
	Tags              []Tag          `json:"tags"`
}

type LanguageProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Episode struct {
	ID            int    `json:"id"`
	SeriesID      int    `json:"seriesId"`
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"episodeNumber"`
	Title         string `json:"title"`
	Monitored     bool   `json:"monitored"`
	HasFile       bool   `json:"hasFile"`
}

type CommandResponse struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}
