package newznab

import (
	"context"
	"encoding/xml"
	"regexp"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
)

// These are provider media fields, not guesses from a scene release name.
var mediaAliases = map[string]string{
	"video": "video_codec", "videocodec": "video_codec", "video_codec": "video_codec",
	"audio": "audio_codec", "audiocodec": "audio_codec", "audio_codec": "audio_codec",
	"audiochannels": "audio_channels", "audio_channels": "audio_channels", "channels": "audio_channels",
	"audiolanguage": "audio_languages", "audiolanguages": "audio_languages", "audio_languages": "audio_languages",
	"framerate": "frame_rate", "fps": "frame_rate", "duration": "duration", "runtime": "runtime",
	"container": "container", "format": "container", "hdr": "dynamic_range", "dynamicrange": "dynamic_range",
}

func canonicalMediaValue(name, value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	if name == "video_codec" {
		switch v {
		case "h.264", "h264", "x264", "avc":
			return "avc"
		case "h.265", "h265", "x265", "hevc":
			return "hevc"
		case "av1":
			return "av1"
		}
	}
	if name == "audio_codec" {
		switch v {
		case "ac-3", "ac3":
			return "ac3"
		case "e-ac-3", "eac3", "dd+":
			return "eac3"
		case "dts-hd ma", "dts-hd master audio":
			return "dts_hd_ma"
		}
	}
	return v
}

// MediaInfoFields accepts only explicit key/value fields from a provider NFO.
// Language lines are interpreted within MediaInfo Audio/Text sections, never
// across sections. General language tags and narrative prose are ignored.
func MediaInfoFields(body []byte) []releases.RawField {
	if len(body) > 1<<20 {
		return nil
	}
	text := string(body)
	if strings.HasPrefix(strings.TrimSpace(text), "<") {
		var rss struct {
			Channel struct {
				Items []struct {
					Description string `xml:"description"`
				} `xml:"item"`
			} `xml:"channel"`
		}
		if xml.Unmarshal(body, &rss) != nil || len(rss.Channel.Items) != 1 {
			return nil
		}
		text = rss.Channel.Items[0].Description
	}
	section := ""
	fields := []releases.RawField{}
	add := func(name, value string) {
		if len(fields) < 256 && len(value) <= 500 {
			fields = append(fields, releases.RawField{Name: name, Namespace: "provider:nfo", Value: SanitizeProviderValue(value), Ordinal: len(fields)})
		}
	}
	heading := regexp.MustCompile(`(?i)^(general|video|audio|text|menu)(?:\s*#\s*\d+)?$`)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if match := heading.FindStringSubmatch(line); len(match) > 0 {
			section = strings.ToLower(match[1])
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "subtitles", "subtitle languages", "subs":
			add("subtitles", value)
		case "language":
			if section == "text" {
				add("subtitles", value)
			} else if section == "audio" {
				add("audiolanguage", value)
			}
		case "format":
			if section == "video" {
				add("videocodec", value)
			} else if section == "audio" {
				add("audiocodec", value)
			} else if section == "general" {
				add("container", value)
			}
		case "channel(s)":
			if section == "audio" {
				add("audiochannels", value)
			}
		case "frame rate":
			if section == "video" {
				add("framerate", value)
			}
		case "duration":
			if section == "general" {
				add("duration", value)
			}
		case "hdr format":
			if section == "video" {
				add("hdr", value)
			}
		}
	}
	return fields
}

// InspectMedia retrieves optional provider details followed by an advertised NFO
// when details still omit subtitles. It uses the same durable query budget.
func (c *Client) InspectMedia(ctx context.Context, o releases.Observation) (releases.Observation, error) {
	native, err := RetrievalID(o.NativeGUID, c.config.BaseURL)
	if err != nil {
		return o, err
	}
	var detail []releases.Observation
	_, err = c.request(ctx, indexers.WorkloadInteractive, []Param{{Name: "t", Value: "details"}, {Name: "id", Value: native}}, c.bodyLimit, func(body []byte) (indexers.Outcome, error) {
		var e error
		detail, e = ParseSearch(body, c.config.IndexerID, "details", time.Now().UTC())
		if e != nil {
			return validationFailureOutcome(e), e
		}
		return indexers.OutcomeSuccess, nil
	})
	if err != nil {
		return o, err
	}
	for _, d := range detail {
		dn, e := RetrievalID(d.NativeGUID, c.config.BaseURL)
		if e != nil || dn != native {
			continue
		}
		// Preserve the original exact search locator and append additional evidence.
		for _, f := range d.Fields {
			f.Ordinal = len(o.Fields)
			o.Fields = append(o.Fields, f)
		}
	}
	o.Facts = normalizeFacts(o.Fields, time.Now().UTC())
	return SanitizeObservationWithCredential(o, c.config.APIKey), nil
}

func (c *Client) InspectNFO(ctx context.Context, o releases.Observation) (releases.Observation, error) {
	native, err := RetrievalID(o.NativeGUID, c.config.BaseURL)
	if err != nil {
		return o, err
	}
	body, err := c.request(ctx, indexers.WorkloadInteractive, []Param{{Name: "t", Value: "getnfo"}, {Name: "id", Value: native}, {Name: "raw", Value: "1"}}, 1<<20, func(body []byte) (indexers.Outcome, error) {
		if provider, ok := providerErrorDocument(body); ok {
			return validationFailureOutcome(provider), provider
		}
		return indexers.OutcomeSuccess, nil
	})
	if err != nil {
		return o, err
	}
	for _, f := range MediaInfoFields(body) {
		f.Ordinal = len(o.Fields)
		o.Fields = append(o.Fields, f)
	}
	o.Facts = normalizeFacts(o.Fields, time.Now().UTC())
	return SanitizeObservationWithCredential(o, c.config.APIKey), nil
}

// Provider APIs often use display names or ISO-639-2 instead of two-letter tags.
func canonicalProviderLanguage(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	aliases := map[string]string{"english": "en", "eng": "en", "swedish": "sv", "swe": "sv", "norwegian": "no", "nor": "no", "nob": "nb", "nno": "nn", "finnish": "fi", "fin": "fi", "german": "de", "deu": "de", "ger": "de", "french": "fr", "fra": "fr", "fre": "fr", "spanish": "es", "spa": "es", "italian": "it", "ita": "it", "portuguese": "pt", "por": "pt", "dutch": "nl", "nld": "nl", "dut": "nl", "polish": "pl", "pol": "pl", "japanese": "ja", "jpn": "ja", "korean": "ko", "kor": "ko", "chinese": "zh", "zho": "zh", "chi": "zh", "arabic": "ar", "ara": "ar", "russian": "ru", "rus": "ru", "turkish": "tr", "tur": "tr", "czech": "cs", "ces": "cs", "cze": "cs", "greek": "el", "ell": "el", "gre": "el", "hungarian": "hu", "hun": "hu", "romanian": "ro", "ron": "ro", "rum": "ro", "ukrainian": "uk", "ukr": "uk", "hindi": "hi", "hin": "hi"}
	if mapped, ok := aliases[value]; ok {
		return mapped, true
	}
	return subtitles.CanonicalLanguage(value)
}
