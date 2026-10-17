package subtitles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/library"
	"github.com/media-manager/media-manager/internal/mediainfo"
)

type Asset struct {
	ID           uuid.UUID `json:"id"`
	RelativePath string    `json:"relative_path"`
	Size         int64     `json:"size_bytes"`
}

type Probe interface {
	InspectWithin(context.Context, string, string) (mediainfo.Evidence, error)
}

type LanguageEvidence struct {
	Kind            string `json:"kind"`
	RawValue        string `json:"raw_value,omitempty"`
	Canonical       string `json:"canonical,omitempty"`
	Source          string `json:"source"`
	TrackIndex      *int   `json:"track_index,omitempty"`
	RelativePath    string `json:"relative_path,omitempty"`
	Forced          *bool  `json:"forced,omitempty"`
	HearingImpaired *bool  `json:"hearing_impaired,omitempty"`
}

type Companion struct {
	VideoAssetID    uuid.UUID `json:"video_asset_id"`
	SubtitleAssetID uuid.UUID `json:"subtitle_asset_id"`
	RelativePath    string    `json:"relative_path"`
	Language        string    `json:"language"`
	RawLanguage     string    `json:"raw_language"`
	Forced          *bool     `json:"forced,omitempty"`
	HearingImpaired *bool     `json:"hearing_impaired,omitempty"`
}

type VideoEvidence struct {
	AssetID           uuid.UUID          `json:"asset_id"`
	RelativePath      string             `json:"relative_path"`
	MediaInfo         mediainfo.Evidence `json:"media_info"`
	Languages         []LanguageEvidence `json:"languages"`
	ObservedLanguages []string           `json:"observed_languages"`
	// AudioLanguages lists the canonical audio-track languages. A required
	// language is satisfied by a subtitle or an audio track in that language.
	AudioLanguages  []string `json:"audio_languages"`
	MissingRequired []string `json:"missing_required"`
}

type Verification struct {
	SchemaVersion      int             `json:"schema_version"`
	Status             string          `json:"status"`
	RequiredLanguages  []string        `json:"required_languages"`
	RequiredAudio      []string        `json:"required_audio_languages"`
	PreferredLanguages []string        `json:"preferred_languages"`
	ObservedLanguages  []string        `json:"observed_languages"`
	Videos             []VideoEvidence `json:"videos"`
	Companions         []Companion     `json:"managed_companions"`
	FailureReasons     []string        `json:"failure_reasons"`
}

var subtitleExtensions = map[string]bool{
	".ass": true, ".idx": true, ".srt": true, ".ssa": true, ".sub": true, ".vtt": true,
}

const (
	maxInventoryAssets = 20000
	maxSelectedVideos  = 256
	verificationLimit  = 5 * time.Minute
)

func Verify(ctx context.Context, authorizedRoot string, selectedVideos, inventory []Asset, policy Policy, probe Probe) (Verification, error) {
	result := Verification{SchemaVersion: 1, Status: "verified", RequiredLanguages: append([]string(nil), policy.Required...), RequiredAudio: append([]string{}, policy.RequiredAudio...), PreferredLanguages: append([]string(nil), policy.Preferred...), Videos: []VideoEvidence{}, Companions: []Companion{}, FailureReasons: []string{}}
	if probe == nil || !filepath.IsAbs(authorizedRoot) || filepath.Clean(authorizedRoot) != authorizedRoot || len(selectedVideos) == 0 || len(selectedVideos) > maxSelectedVideos || len(inventory) == 0 || len(inventory) > maxInventoryAssets {
		return Verification{}, errors.New("subtitle verification is not safely configured")
	}
	verificationContext, cancel := context.WithTimeout(ctx, verificationLimit)
	defer cancel()
	all := make(map[uuid.UUID]Asset, len(inventory))
	for _, asset := range inventory {
		if asset.ID == (uuid.UUID{}) || asset.Size <= 0 {
			return Verification{}, errors.New("subtitle verification inventory is invalid")
		}
		clean, err := library.ValidateRelative(filepath.Clean(asset.RelativePath))
		if err != nil || clean != asset.RelativePath {
			return Verification{}, errors.New("subtitle verification inventory path is unsafe")
		}
		all[asset.ID] = asset
	}
	root, err := os.OpenRoot(authorizedRoot)
	if err != nil {
		return Verification{}, errors.New("authorized AltMount output is unavailable")
	}
	defer root.Close()
	for _, asset := range inventory {
		info, statErr := root.Lstat(asset.RelativePath)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() != asset.Size {
			return Verification{}, errors.New("authorized AltMount inventory changed before inspection")
		}
	}

	selected := make(map[uuid.UUID]Asset, len(selectedVideos))
	for _, video := range selectedVideos {
		persisted, exists := all[video.ID]
		if !exists || persisted.RelativePath != video.RelativePath || persisted.Size != video.Size {
			return Verification{}, errors.New("selected video is outside the frozen AltMount inventory")
		}
		selected[video.ID] = persisted
	}
	pairs := pairSidecars(selectedVideos, inventory)
	observedAll := map[string]bool{}
	for _, video := range selectedVideos {
		evidence, probeErr := probe.InspectWithin(verificationContext, authorizedRoot, video.RelativePath)
		if probeErr != nil {
			return Verification{}, fmt.Errorf("bounded media-info inspection failed for %s: %w", video.RelativePath, probeErr)
		}
		entry := VideoEvidence{AssetID: video.ID, RelativePath: video.RelativePath, MediaInfo: evidence, Languages: []LanguageEvidence{}, ObservedLanguages: []string{}, AudioLanguages: []string{}, MissingRequired: []string{}}
		audio := map[string]bool{}
		untaggedAudio := false
		for _, track := range evidence.AudioTracks {
			if track.CanonicalLanguage != "" {
				audio[track.CanonicalLanguage] = true
			} else {
				untaggedAudio = true
			}
		}
		entry.AudioLanguages = sortedKeys(audio)
		observed := map[string]bool{}
		for _, track := range evidence.SubtitleTracks {
			if track.CanonicalLanguage == "" {
				continue
			}
			index := track.Index
			forced := explicitTrue(track.Forced, hasExactTag(track.Title, "forced"))
			hearing := explicitTrue(track.HearingImpaired, hasExactTag(track.Title, "sdh") || hasHearingImpairedTag(track.Title))
			entry.Languages = append(entry.Languages, LanguageEvidence{Kind: "embedded", RawValue: track.Language, Canonical: track.CanonicalLanguage, Source: "stream_language", TrackIndex: &index, Forced: forced, HearingImpaired: hearing})
			observed[track.CanonicalLanguage], observedAll[track.CanonicalLanguage] = true, true
		}
		for _, sidecar := range pairs[video.ID] {
			entry.Languages = append(entry.Languages, LanguageEvidence{Kind: "external", RawValue: sidecar.RawLanguage, Canonical: sidecar.Language, Source: "filename_tag", RelativePath: sidecar.RelativePath, Forced: sidecar.Forced, HearingImpaired: sidecar.HearingImpaired})
			if sidecar.Language != "" {
				observed[sidecar.Language], observedAll[sidecar.Language] = true, true
				result.Companions = append(result.Companions, sidecar)
			}
		}
		entry.ObservedLanguages = sortedKeys(observed)
		for _, required := range policy.Required {
			if !observed[required] && !audio[required] {
				entry.MissingRequired = append(entry.MissingRequired, required)
				result.FailureReasons = append(result.FailureReasons, fmt.Sprintf("%s has no verified %s subtitle or audio track", video.RelativePath, required))
			}
		}
		for _, required := range policy.RequiredAudio {
			// An untagged audio track is the title's original audio.
			if !audio[required] && !(untaggedAudio && required == policy.OriginalLanguage) {
				entry.MissingRequired = append(entry.MissingRequired, "audio:"+required)
				result.FailureReasons = append(result.FailureReasons, fmt.Sprintf("%s has no verified %s audio track", video.RelativePath, required))
			}
		}
		result.Videos = append(result.Videos, entry)
	}
	result.ObservedLanguages = sortedKeys(observedAll)
	if len(result.FailureReasons) > 0 {
		result.Status = "unverified"
	}
	sort.Slice(result.Companions, func(i, j int) bool {
		if result.Companions[i].VideoAssetID == result.Companions[j].VideoAssetID {
			return result.Companions[i].RelativePath < result.Companions[j].RelativePath
		}
		return result.Companions[i].VideoAssetID.String() < result.Companions[j].VideoAssetID.String()
	})
	return result, nil
}

func pairSidecars(videos, inventory []Asset) map[uuid.UUID][]Companion {
	result := map[uuid.UUID][]Companion{}
	for _, asset := range inventory {
		if !subtitleExtensions[strings.ToLower(filepath.Ext(asset.RelativePath))] {
			continue
		}
		matches := make([]Asset, 0, 1)
		for _, video := range videos {
			if strictSidecarMatch(video.RelativePath, asset.RelativePath) {
				matches = append(matches, video)
			}
		}
		if len(matches) != 1 {
			continue
		}
		raw, language, forced, hearing := filenameMetadata(matches[0].RelativePath, asset.RelativePath)
		result[matches[0].ID] = append(result[matches[0].ID], Companion{VideoAssetID: matches[0].ID, SubtitleAssetID: asset.ID, RelativePath: asset.RelativePath, Language: language, RawLanguage: raw, Forced: forced, HearingImpaired: hearing})
	}
	return result
}

func strictSidecarMatch(videoPath, sidecarPath string) bool {
	if filepath.Dir(videoPath) != filepath.Dir(sidecarPath) {
		return false
	}
	videoStem := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	sidecarStem := strings.TrimSuffix(filepath.Base(sidecarPath), filepath.Ext(sidecarPath))
	return sidecarStem == videoStem || strings.HasPrefix(sidecarStem, videoStem+".")
}

func filenameMetadata(videoPath, sidecarPath string) (string, string, *bool, *bool) {
	videoStem := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	sidecarStem := strings.TrimSuffix(filepath.Base(sidecarPath), filepath.Ext(sidecarPath))
	suffix := strings.TrimPrefix(sidecarStem, videoStem)
	suffix = strings.TrimPrefix(suffix, ".")
	var raw, canonical string
	var forced, hearing *bool
	conflictingLanguage := false
	for _, token := range strings.Split(suffix, ".") {
		if value, ok := CanonicalLanguage(token); ok {
			if canonical != "" && canonical != value {
				raw, canonical = "", ""
				conflictingLanguage = true
				continue
			}
			if !conflictingLanguage {
				raw, canonical = token, value
			}
			continue
		}
		switch strings.ToLower(strings.TrimSpace(token)) {
		case "forced":
			forced = boolPointer(true)
		case "sdh", "hi", "hearing-impaired", "hearingimpaired":
			hearing = boolPointer(true)
		}
	}
	return raw, canonical, forced, hearing
}

func explicitTrue(value *bool, tagged bool) *bool {
	if value != nil && *value || tagged {
		return boolPointer(true)
	}
	return nil
}

func hasExactTag(value, wanted string) bool {
	for _, token := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return r < 'a' || r > 'z' }) {
		if token == wanted {
			return true
		}
	}
	return false
}

func hasHearingImpairedTag(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "hearing impaired") || strings.Contains(value, "hearing-impaired")
}

func boolPointer(value bool) *bool { return &value }

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
