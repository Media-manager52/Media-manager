package newznab

import (
	"context"
	"encoding/xml"
	"errors"
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/releases"
)

// NFOLanguages is the subtitle and audio language evidence in a release NFO.
//
// A list is "known" only when the NFO states it completely and every entry is
// a recognized language: a MediaInfo report whose every Text (or Audio)
// section names a recognized language, or a release-group line such as
// "SUBTITLES....: DANISH, FINNISH" or "Subtitles: None". Anything partial,
// unrecognized or narrative leaves the list unknown, so a caller can tell
// "the NFO says there is no Danish" from "the NFO does not say".
type NFOLanguages struct {
	Subtitles      []string
	SubtitlesKnown bool
	Audio          []string
	AudioKnown     bool
	// AudioMentions are languages a release-group "Audio" line names. They are
	// only positive evidence: such lines often mix codecs and languages.
	AudioMentions []string
}

// nfoLanguageNames is deliberately strict. The general language parser accepts
// any two- or three-letter tag, which would read "Subtitles: No" as Norwegian;
// an NFO decision may only rely on names and codes listed here.
var nfoLanguageNames = map[string]string{
	"da": "da", "dan": "da", "danish": "da", "dansk": "da", "danske": "da", "dk": "da",
	"en": "en", "eng": "en", "english": "en", "engelsk": "en",
	"sv": "sv", "swe": "sv", "swedish": "sv", "svensk": "sv", "se": "sv",
	"nb": "no", "nob": "no", "nn": "no", "nno": "no", "nor": "no", "norwegian": "no", "norsk": "no", "bokmal": "no", "bokmål": "no", "nynorsk": "no",
	"fi": "fi", "fin": "fi", "finnish": "fi", "finsk": "fi", "suomi": "fi",
	"is": "is", "isl": "is", "ice": "is", "icelandic": "is",
	"de": "de", "deu": "de", "ger": "de", "german": "de", "deutsch": "de", "tysk": "de",
	"fr": "fr", "fra": "fr", "fre": "fr", "french": "fr", "fransk": "fr",
	"es": "es", "spa": "es", "spanish": "es", "castilian": "es", "spansk": "es",
	"it": "it", "ita": "it", "italian": "it",
	"pt": "pt", "por": "pt", "portuguese": "pt",
	"nl": "nl", "nld": "nl", "dut": "nl", "dutch": "nl", "flemish": "nl",
	"pl": "pl", "pol": "pl", "polish": "pl",
	"cs": "cs", "ces": "cs", "cze": "cs", "czech": "cs",
	"sk": "sk", "slk": "sk", "slo": "sk", "slovak": "sk",
	"sl": "sl", "slv": "sl", "slovenian": "sl", "slovene": "sl",
	"hr": "hr", "hrv": "hr", "croatian": "hr",
	"sr": "sr", "srp": "sr", "serbian": "sr",
	"bg": "bg", "bul": "bg", "bulgarian": "bg",
	"ro": "ro", "ron": "ro", "rum": "ro", "romanian": "ro",
	"hu": "hu", "hun": "hu", "hungarian": "hu",
	"el": "el", "ell": "el", "gre": "el", "greek": "el",
	"tr": "tr", "tur": "tr", "turkish": "tr",
	"ru": "ru", "rus": "ru", "russian": "ru",
	"uk": "uk", "ukr": "uk", "ukrainian": "uk",
	"et": "et", "est": "et", "estonian": "et",
	"lv": "lv", "lav": "lv", "latvian": "lv",
	"lt": "lt", "lit": "lt", "lithuanian": "lt",
	"ar": "ar", "ara": "ar", "arabic": "ar",
	"he": "he", "heb": "he", "hebrew": "he",
	"fa": "fa", "fas": "fa", "per": "fa", "persian": "fa",
	"hi": "hi", "hin": "hi", "hindi": "hi",
	"th": "th", "tha": "th", "thai": "th",
	"vi": "vi", "vie": "vi", "vietnamese": "vi",
	"id": "id", "ind": "id", "indonesian": "id",
	"ms": "ms", "msa": "ms", "may": "ms", "malay": "ms",
	"fil": "fil", "filipino": "fil", "tagalog": "fil",
	"ja": "ja", "jpn": "ja", "japanese": "ja",
	"ko": "ko", "kor": "ko", "korean": "ko",
	"zh": "zh", "zho": "zh", "chi": "zh", "chinese": "zh", "mandarin": "zh", "cantonese": "zh",
	"ca": "ca", "cat": "ca", "catalan": "ca",
	"eu": "eu", "eus": "eu", "baq": "eu", "basque": "eu",
	"gl": "gl", "glg": "gl", "galician": "gl",
	// Names only: their short codes collide with ordinary words.
	"azerbaijani": "az", "georgian": "ka", "armenian": "hy", "albanian": "sq", "macedonian": "mk",
	"bosnian": "bs", "belarusian": "be", "kazakh": "kk", "uzbek": "uz", "mongolian": "mn",
	"bengali": "bn", "bangla": "bn", "tamil": "ta", "telugu": "te", "urdu": "ur", "punjabi": "pa",
	"marathi": "mr", "kannada": "kn", "malayalam": "ml", "gujarati": "gu", "swahili": "sw",
	"afrikaans": "af", "welsh": "cy", "irish": "ga", "khmer": "km", "lao": "lo", "burmese": "my",
	"nepali": "ne", "sinhala": "si", "tajik": "tg", "kurdish": "ku", "pashto": "ps", "amharic": "am",
}

// nfoQualifiers describe a subtitle entry without naming a language.
var nfoQualifiers = map[string]bool{
	"forced": true, "sdh": true, "cc": true, "full": true, "retail": true, "srt": true, "pgs": true,
	"vobsub": true, "ass": true, "ssa": true, "sup": true, "utf": true, "embedded": true, "external": true,
	"internal": true, "subs": true, "sub": true, "subtitles": true, "subtitle": true, "and": true, "og": true,
	"simplified": true, "traditional": true, "hearing": true, "impaired": true, "commentary": true,
	"brazilian": true, "brazil": true, "latin": true, "american": true, "european": true, "canadian": true,
	"text": true, "image": true, "bitmap": true, "idx": true,
}

// nfoNone is an explicit statement that a release has no subtitles. A bare
// "No" is not accepted: it is also Norwegian's language code.
var nfoNone = map[string]bool{"none": true, "n/a": true, "na": true, "-": true, "ingen": true, "nope": true}

var (
	nfoBreak         = regexp.MustCompile(`(?i)<br\s*/?>`)
	nfoTag           = regexp.MustCompile(`<[^>]{0,200}>`)
	nfoBBCode        = regexp.MustCompile(`(?i)\[/?[a-z*]{1,10}(?:=[^\]]{0,100})?\]`)
	nfoHeading       = regexp.MustCompile(`(?i)^(general|video|audio|text|menu|chapters?)(?:\s*#\s*\d+)?$`)
	nfoSceneSubtitle = regexp.MustCompile(`(?i)(?:^|[^a-z])(subtitles?|subtitle\(s\)|subs|subtitle languages|undertekster|tekster)[\s._]*[:=]\s*(.*)$`)
	nfoSceneAudio    = regexp.MustCompile(`(?i)(?:^|[^a-z])(audio|languages?|sprog|lyd)[\s._]*[:=]\s*(.*)$`)
	nfoColumnBreak   = regexp.MustCompile(`\s{3,}`)
	nfoLanguageWord  = regexp.MustCompile(`\p{L}+`)
	nfoEnumerated    = regexp.MustCompile(`^\s*\d{1,2}\s*[:.)]\s*`)
	nfoParenthetical = regexp.MustCompile(`\([^)]{0,40}\)`)
)

// nfoText turns a provider NFO response into plain lines. Providers return a
// raw text NFO, an RSS item whose description holds it, or (DrunkenSlug's
// t=info) HTML with <br /> line breaks and entities.
func nfoText(body []byte) (string, bool) {
	if len(body) > 1<<20 {
		return "", false
	}
	text := string(body)
	trimmed := strings.TrimSpace(text)
	if lower := strings.ToLower(trimmed); strings.HasPrefix(lower, "<?xml") || strings.HasPrefix(lower, "<rss") || strings.HasPrefix(lower, "<error") {
		if _, provider := providerErrorDocument(body); provider {
			return "", false
		}
		var rss struct {
			Channel struct {
				Items []struct {
					Description string `xml:"description"`
				} `xml:"item"`
			} `xml:"channel"`
		}
		if xml.Unmarshal(body, &rss) != nil || len(rss.Channel.Items) != 1 {
			return "", false
		}
		text = rss.Channel.Items[0].Description
	}
	if nfoBreak.MatchString(text) || strings.Contains(text, "&nbsp;") || strings.Contains(text, "&amp;") {
		text = nfoBreak.ReplaceAllString(text, "\n")
		text = nfoTag.ReplaceAllString(text, "")
		text = html.UnescapeString(text)
	}
	text = nfoBBCode.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text, true
}

// nfoLine strips release-group framing (box drawing, block characters and
// other symbols) from both ends of a line.
func nfoLine(line string) string {
	return strings.TrimFunc(line, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '(' && r != ')'
	})
}

// nfoLanguageList reads one stated list such as "DANISH, FINNISH (forced)" or
// "English (US), da (sdh)". complete is false when anything that is neither a
// recognized language nor a known qualifier remains.
func nfoLanguageList(value string) (languages []string, none, complete bool) {
	value = strings.TrimSpace(value)
	if loc := nfoColumnBreak.FindStringIndex(value); loc != nil {
		// "SUBTITLES: English      RUNTIME: 1h" — the list ends at the column gap.
		value = value[:loc[0]]
	}
	value = nfoParenthetical.ReplaceAllString(value, " ")
	value = strings.Trim(strings.TrimSpace(value), ".:;")
	if nfoNone[strings.ToLower(value)] {
		return nil, true, true
	}
	complete = true
	seen := map[string]bool{}
	for _, word := range nfoLanguageWord.FindAllString(value, -1) {
		word = strings.ToLower(word)
		if word == "" || nfoQualifiers[word] {
			continue
		}
		language, ok := nfoLanguageNames[word]
		if !ok {
			complete = false
			continue
		}
		if !seen[language] {
			seen[language] = true
			languages = append(languages, language)
		}
	}
	for _, r := range value {
		if r >= '0' && r <= '9' {
			// "2 subs", "UTF-8": not a plain language list.
			complete = false
			break
		}
	}
	return languages, false, complete && len(languages) > 0
}

// ParseNFOLanguages reads subtitle and audio language evidence from an NFO.
func ParseNFOLanguages(body []byte) NFOLanguages {
	text, ok := nfoText(body)
	if !ok {
		return NFOLanguages{}
	}
	result := NFOLanguages{}
	subtitles, audio, mentions := map[string]bool{}, map[string]bool{}, map[string]bool{}
	add := func(set map[string]bool, list *[]string, language string) {
		if !set[language] {
			set[language] = true
			*list = append(*list, language)
		}
	}
	type section struct {
		kind     string
		language string
		known    bool
	}
	var sections []section
	current := -1
	sceneKnown, sceneUnknown := false, false
	lines := strings.Split(text, "\n")
	for index := 0; index < len(lines); index++ {
		line := nfoLine(strings.TrimSpace(lines[index]))
		if line == "" {
			continue
		}
		if match := nfoHeading.FindStringSubmatch(line); match != nil {
			kind := strings.ToLower(match[1])
			sections = append(sections, section{kind: kind})
			current = len(sections) - 1
			continue
		}
		if key, value, hasValue := strings.Cut(line, ":"); current >= 0 && hasValue && strings.EqualFold(strings.TrimSpace(key), "language") {
			// MediaInfo: the section's own language.
			s := &sections[current]
			languages, _, complete := nfoLanguageList(value)
			if complete && len(languages) == 1 {
				s.language, s.known = languages[0], true
			}
			continue
		}
		if match := nfoSceneSubtitle.FindStringSubmatch(line); match != nil {
			value := match[2]
			if nfoEnumerated.MatchString(value) {
				// "Subs : 1: English" continued by "       2: Danish" lines:
				// every numbered entry belongs to the same list.
				entries := []string{nfoEnumerated.ReplaceAllString(value, "")}
				for index+1 < len(lines) {
					next := nfoLine(strings.TrimSpace(lines[index+1]))
					if !nfoEnumerated.MatchString(next) {
						break
					}
					entries = append(entries, nfoEnumerated.ReplaceAllString(next, ""))
					index++
				}
				value = strings.Join(entries, ", ")
			}
			languages, none, complete := nfoLanguageList(value)
			switch {
			case none:
				sceneKnown = true
			case complete:
				sceneKnown = true
				for _, language := range languages {
					add(subtitles, &result.Subtitles, language)
				}
			default:
				sceneUnknown = true
				for _, language := range languages {
					// A partly recognized line still proves the languages it names.
					add(subtitles, &result.Subtitles, language)
				}
			}
			continue
		}
		if match := nfoSceneAudio.FindStringSubmatch(line); match != nil && current < 0 {
			languages, _, _ := nfoLanguageList(match[2])
			for _, language := range languages {
				add(mentions, &result.AudioMentions, language)
			}
		}
	}
	textSections, textKnown := 0, true
	audioSections, audioKnown := 0, true
	for _, s := range sections {
		switch s.kind {
		case "text":
			textSections++
			if s.known {
				add(subtitles, &result.Subtitles, s.language)
			} else {
				textKnown = false
			}
		case "audio":
			audioSections++
			if s.known {
				add(audio, &result.Audio, s.language)
			} else {
				audioKnown = false
			}
		}
	}
	// MediaInfo without any Text section describes only the video file:
	// external subtitle files may still exist, so it proves nothing.
	mediaInfoKnown := textSections > 0 && textKnown
	mediaInfoUnknown := textSections > 0 && !textKnown
	result.SubtitlesKnown = (mediaInfoKnown || sceneKnown) && !mediaInfoUnknown && !sceneUnknown
	result.AudioKnown = audioSections > 0 && audioKnown
	return result
}

// AdvertisedNFOFunction returns the NFO function an observation's provider
// advertised in its "info" attribute ("getnfo", or DrunkenSlug's "info") with
// the release identifier, when the link belongs to the configured endpoint
// and names this exact release. Only the function name is taken from the
// link: the request itself is built against the configured endpoint, so the
// API key never follows a provider-supplied URL.
func AdvertisedNFOFunction(o releases.Observation, configuredBaseURL string) (function string, raw bool, ok bool) {
	native, err := RetrievalID(o.NativeGUID, configuredBaseURL)
	if err != nil {
		return "", false, false
	}
	base, err := url.Parse(strings.TrimSpace(configuredBaseURL))
	if err != nil || base.Host == "" {
		return "", false, false
	}
	for _, field := range o.Fields {
		if !strings.EqualFold(strings.TrimSpace(field.Name), "info") {
			continue
		}
		link, err := url.Parse(strings.TrimSpace(field.Value))
		if err != nil || !strings.EqualFold(link.Scheme, base.Scheme) || !strings.EqualFold(link.Host, base.Host) || strings.TrimSuffix(link.Path, "/") != strings.TrimSuffix(base.Path, "/") {
			continue
		}
		query := link.Query()
		function := strings.ToLower(query.Get("t"))
		if (function != "getnfo" && function != "info") || query.Get("id") != native {
			continue
		}
		return function, function == "getnfo", true
	}
	return "", false, false
}

// ErrNFOUnavailable means the provider has no NFO for the release.
var ErrNFOUnavailable = errors.New("provider has no NFO for this release")

// InspectAdvertisedNFO retrieves the NFO the provider advertised for an
// observation: the function its "info" link names, else getnfo for nfo=1. It
// is optional evidence: "no such item" and "no such function" answers are
// recorded as no results, never as a provider failure that could back the
// indexer off.
func (c *Client) InspectAdvertisedNFO(ctx context.Context, o releases.Observation) ([]byte, error) {
	native, err := RetrievalID(o.NativeGUID, c.config.BaseURL)
	if err != nil {
		return nil, ErrNFOUnavailable
	}
	function, raw, ok := AdvertisedNFOFunction(o, c.config.BaseURL)
	if !ok {
		if !advertisesNFOFlag(o) {
			return nil, ErrNFOUnavailable
		}
		// Newznab's nfo=1 attribute without a link: the standard function.
		function, raw = "getnfo", true
	}
	params := []Param{{Name: "t", Value: function}, {Name: "id", Value: native}}
	if raw {
		params = append(params, Param{Name: "raw", Value: "1"})
	}
	unavailable := false
	body, err := c.request(ctx, indexers.WorkloadInteractive, params, 1<<20, nfoResponseValidator(&unavailable))
	if err != nil {
		return nil, err
	}
	if unavailable {
		return nil, ErrNFOUnavailable
	}
	return body, nil
}

// AdvertisesNFO reports whether the provider offered an NFO for this release.
func (c *Client) AdvertisesNFO(o releases.Observation) bool {
	_, _, ok := AdvertisedNFOFunction(o, c.config.BaseURL)
	return ok || advertisesNFOFlag(o)
}

func advertisesNFOFlag(o releases.Observation) bool {
	for _, f := range o.Fields {
		if strings.EqualFold(strings.TrimSpace(f.Name), "nfo") && (strings.TrimSpace(f.Value) == "1" || strings.EqualFold(strings.TrimSpace(f.Value), "yes")) {
			return true
		}
	}
	return false
}

func nfoResponseValidator(unavailable *bool) responseValidator {
	return func(body []byte) (indexers.Outcome, error) {
		if provider, ok := providerErrorDocument(body); ok {
			if provider.Code == "100" || provider.Code == "101" || provider.Code == "102" {
				return indexers.OutcomeAuthFailed, provider
			}
			*unavailable = true
			return indexers.OutcomeNoResults, nil
		}
		if _, ok := nfoText(body); !ok {
			// For example NZBgeek answers an unsupported getnfo with its feed.
			*unavailable = true
			return indexers.OutcomeNoResults, nil
		}
		return indexers.OutcomeSuccess, nil
	}
}

// nfoTitleHints are release-name words that suggest a release may carry the
// language even though the NFO's audio list is unknown.
var nfoTitleHints = map[string][]string{
	"nordic": {"da", "sv", "no", "fi"}, "nordisk": {"da", "sv", "no", "fi"}, "scandinavian": {"da", "sv", "no"},
	"skandinavisk": {"da", "sv", "no"}, "dksubs": {"da"}, "dksub": {"da"}, "dkaudio": {"da"}, "dktale": {"da"},
	"multi": nil, "multisub": nil, "multisubs": nil,
}

// MissingRequired reports whether the NFO states that a release has neither a
// subtitle nor an audio track in language. It is true only when the subtitle
// list is completely known and lacks the language, and either the audio list is
// completely known and lacks it too, or the release name gives no hint of it.
// Any doubt leaves the release to the normal download-and-inspect path.
func (e NFOLanguages) MissingRequired(language, title string) bool {
	for _, list := range [][]string{e.Subtitles, e.Audio, e.AudioMentions} {
		for _, value := range list {
			if value == language {
				return false
			}
		}
	}
	if !e.SubtitlesKnown {
		return false
	}
	if e.AudioKnown {
		return true
	}
	for _, word := range nfoLanguageWord.FindAllString(title, -1) {
		word = strings.ToLower(word)
		if nfoLanguageNames[word] == language {
			return false
		}
		if hinted, ok := nfoTitleHints[word]; ok {
			if hinted == nil {
				return false
			}
			for _, value := range hinted {
				if value == language {
					return false
				}
			}
		}
	}
	return true
}
