package mediainfo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/languages"
	"github.com/media-manager/media-manager/internal/library"
)

const (
	SchemaVersion    = 1
	defaultTimeout   = 30 * time.Second
	defaultMaxOutput = 1 << 20
	maxTracks        = 512
)

var ErrOutputLimit = errors.New("media-info output exceeded its limit")

// ErrNoVideoTrack identifies a successfully parsed media document with no
// video. It is distinct from an unavailable or inconclusive inspection.
var ErrNoVideoTrack = errors.New("media-info has no video track")

// ErrEncryptedVideo identifies explicit protected-video evidence with no
// usable codec, rather than an inconclusive probe or an arbitrary missing codec.
var ErrEncryptedVideo = errors.New("media-info video has an unsupported encrypted codec tag")

type Evidence struct {
	SchemaVersion   int            `json:"schema_version"`
	ContainerNames  []string       `json:"container_names"`
	DurationSeconds *float64       `json:"duration_seconds,omitempty"`
	VideoTracks     []Track        `json:"video_tracks"`
	AudioTracks     []Track        `json:"audio_tracks"`
	SubtitleTracks  []Track        `json:"subtitle_tracks"`
	Facts           map[string]any `json:"facts"`
}

type Track struct {
	Index             int    `json:"index"`
	Codec             string `json:"codec"`
	Width             int    `json:"width,omitempty"`
	Height            int    `json:"height,omitempty"`
	Channels          int    `json:"channels,omitempty"`
	ChannelLayout     string `json:"channel_layout,omitempty"`
	Language          string `json:"language,omitempty"`
	CanonicalLanguage string `json:"canonical_language,omitempty"`
	Title             string `json:"title,omitempty"`
	ColorTransfer     string `json:"color_transfer,omitempty"`
	ColorPrimaries    string `json:"color_primaries,omitempty"`
	ColorSpace        string `json:"color_space,omitempty"`
	Default           bool   `json:"default,omitempty"`
	Forced            *bool  `json:"forced,omitempty"`
	HearingImpaired   *bool  `json:"hearing_impaired,omitempty"`
}

type Probe struct {
	Executable string
	Timeout    time.Duration
	MaxOutput  int
}

func NewProbe() *Probe {
	return &Probe{Executable: "ffprobe", Timeout: defaultTimeout, MaxOutput: defaultMaxOutput}
}

func (probe *Probe) Inspect(ctx context.Context, path string) (Evidence, error) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return Evidence{}, errors.New("media-info path must be absolute")
	}
	info, err := os.Lstat(clean)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Evidence{}, errors.New("media-info source is not a regular file")
	}
	return probe.inspect(ctx, clean, nil)
}

// InspectWithin opens the media through os.Root and passes only that already
// contained file descriptor to ffprobe. It cannot follow a relative path or a
// symlink outside the authorized completed-job root.
func (probe *Probe) InspectWithin(ctx context.Context, rootPath, relative string) (Evidence, error) {
	if !filepath.IsAbs(rootPath) || filepath.Clean(rootPath) != rootPath {
		return Evidence{}, errors.New("media-info root must be an absolute clean path")
	}
	clean, err := library.ValidateRelative(filepath.Clean(relative))
	if err != nil || clean != relative {
		return Evidence{}, errors.New("media-info relative path is unsafe")
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return Evidence{}, errors.New("media-info root is unavailable")
	}
	defer root.Close()
	info, err := root.Lstat(clean)
	if err != nil || !info.Mode().IsRegular() {
		return Evidence{}, errors.New("media-info source is not a regular file")
	}
	file, err := root.Open(clean)
	if err != nil {
		return Evidence{}, errors.New("media-info source is unavailable")
	}
	defer file.Close()
	return probe.inspect(ctx, "/proc/self/fd/3", []*os.File{file})
}

func (probe *Probe) inspect(ctx context.Context, path string, extraFiles []*os.File) (Evidence, error) {
	executable := strings.TrimSpace(probe.Executable)
	if executable == "" {
		executable = "ffprobe"
	}
	timeout := probe.Timeout
	if timeout <= 0 || timeout > 2*time.Minute {
		timeout = defaultTimeout
	}
	maxOutput := probe.MaxOutput
	if maxOutput < 4096 || maxOutput > 4<<20 {
		maxOutput = defaultMaxOutput
	}
	probeContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(probeContext, executable,
		"-v", "error",
		"-show_entries", "format=format_name,duration:stream=index,codec_type,codec_name,codec_tag_string,width,height,channels,channel_layout,color_transfer,color_primaries,color_space:stream_tags=language,title:stream_disposition=default,forced,hearing_impaired",
		"-of", "json", path,
	)
	command.ExtraFiles = extraFiles
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C", "LC_ALL=C"}
	stdout := &boundedBuffer{limit: maxOutput}
	stderr := &boundedBuffer{limit: 16 << 10}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if errors.Is(stdout.err, ErrOutputLimit) || errors.Is(stderr.err, ErrOutputLimit) {
			return Evidence{}, ErrOutputLimit
		}
		if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
			return Evidence{}, errors.New("media-info probe timed out")
		}
		return Evidence{}, errors.New("media-info probe failed")
	}
	return Parse(stdout.Bytes())
}

type rawDocument struct {
	Format struct {
		Name     string `json:"format_name"`
		Duration string `json:"duration"`
	} `json:"format"`
	Streams      []rawStream     `json:"streams"`
	Programs     json.RawMessage `json:"programs"`
	StreamGroups json.RawMessage `json:"stream_groups"`
}

type rawStream struct {
	Index          int    `json:"index"`
	Type           string `json:"codec_type"`
	Codec          string `json:"codec_name"`
	CodecTag       string `json:"codec_tag_string"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	Channels       int    `json:"channels"`
	ChannelLayout  string `json:"channel_layout"`
	ColorTransfer  string `json:"color_transfer"`
	ColorPrimaries string `json:"color_primaries"`
	ColorSpace     string `json:"color_space"`
	Tags           struct {
		Language string `json:"language"`
		Title    string `json:"title"`
	} `json:"tags"`
	Disposition struct {
		Default         int `json:"default"`
		Forced          int `json:"forced"`
		HearingImpaired int `json:"hearing_impaired"`
	} `json:"disposition"`
	SideDataList json.RawMessage `json:"side_data_list"`
}

func Parse(body []byte) (Evidence, error) {
	if len(body) == 0 || len(body) > defaultMaxOutput {
		return Evidence{}, errors.New("invalid media-info document size")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var raw rawDocument
	if err := decoder.Decode(&raw); err != nil {
		return Evidence{}, errors.New("invalid media-info document")
	}
	if len(raw.Streams) == 0 || len(raw.Streams) > maxTracks {
		return Evidence{}, errors.New("invalid media-info track count")
	}
	result := Evidence{SchemaVersion: SchemaVersion, ContainerNames: normalizedList(strings.Split(raw.Format.Name, ","), 16, 64), VideoTracks: []Track{}, AudioTracks: []Track{}, SubtitleTracks: []Track{}, Facts: map[string]any{}}
	if len(result.ContainerNames) == 0 {
		return Evidence{}, errors.New("media-info container is unavailable")
	}
	if duration, err := strconv.ParseFloat(strings.TrimSpace(raw.Format.Duration), 64); err == nil && !math.IsNaN(duration) && !math.IsInf(duration, 0) && duration >= 0 && duration <= 7*24*60*60 {
		result.DurationSeconds = &duration
	}
	for _, stream := range raw.Streams {
		if stream.Type == "video" && strings.TrimSpace(stream.Codec) == "" && strings.EqualFold(strings.TrimSpace(stream.CodecTag), "encv") {
			return Evidence{}, ErrEncryptedVideo
		}
		track, err := normalizeTrack(stream)
		if err != nil {
			return Evidence{}, err
		}
		switch stream.Type {
		case "video":
			if track.Width < 1 || track.Width > 32768 || track.Height < 1 || track.Height > 32768 {
				return Evidence{}, errors.New("invalid video dimensions")
			}
			result.VideoTracks = append(result.VideoTracks, track)
		case "audio":
			if track.Channels < 1 || track.Channels > 128 {
				return Evidence{}, errors.New("invalid audio channel count")
			}
			result.AudioTracks = append(result.AudioTracks, track)
		case "subtitle":
			result.SubtitleTracks = append(result.SubtitleTracks, track)
		default:
			continue
		}
	}
	if len(result.VideoTracks) == 0 {
		return Evidence{}, ErrNoVideoTrack
	}
	result.Facts = deriveFacts(result)
	return result, nil
}

func normalizeTrack(stream rawStream) (Track, error) {
	if stream.Index < 0 || stream.Index > 100000 {
		return Track{}, errors.New("invalid media-info track index")
	}
	codec := normalizedToken(stream.Codec, 64)
	if codec == "" {
		return Track{}, errors.New("media-info track codec is unavailable")
	}
	rawLanguage := normalizedText(stream.Tags.Language, 32)
	canonicalLanguage, _ := languages.Canonical(rawLanguage)
	return Track{
		Index: stream.Index, Codec: codec, Width: stream.Width, Height: stream.Height,
		Channels: stream.Channels, ChannelLayout: normalizedToken(stream.ChannelLayout, 64),
		Language: rawLanguage, CanonicalLanguage: canonicalLanguage, Title: normalizedText(stream.Tags.Title, 200),
		ColorTransfer: normalizedToken(stream.ColorTransfer, 64), ColorPrimaries: normalizedToken(stream.ColorPrimaries, 64),
		ColorSpace: normalizedToken(stream.ColorSpace, 64), Default: stream.Disposition.Default == 1,
		Forced: explicitDisposition(stream.Disposition.Forced), HearingImpaired: explicitDisposition(stream.Disposition.HearingImpaired),
	}, nil
}

func deriveFacts(evidence Evidence) map[string]any {
	facts := map[string]any{"container": evidence.ContainerNames[0]}
	primary := evidence.VideoTracks[0]
	for _, track := range evidence.VideoTracks[1:] {
		if track.Width*track.Height > primary.Width*primary.Height {
			primary = track
		}
	}
	facts["resolution"] = resolutionLabel(primary.Height)
	facts["video_codec"] = primary.Codec
	if dynamicRange := dynamicRangeLabel(primary.ColorTransfer); dynamicRange != "" {
		facts["dynamic_range"] = dynamicRange
	}
	facts["audio_codecs"] = trackValues(evidence.AudioTracks, func(track Track) string { return track.Codec })
	facts["audio_languages"] = trackValues(evidence.AudioTracks, func(track Track) string { return track.CanonicalLanguage })
	facts["subtitle_languages"] = trackValues(evidence.SubtitleTracks, func(track Track) string { return track.CanonicalLanguage })
	return facts
}

func resolutionLabel(height int) string {
	switch {
	case height >= 2160:
		return "2160p"
	case height >= 1440:
		return "1440p"
	case height >= 1080:
		return "1080p"
	case height >= 720:
		return "720p"
	case height >= 576:
		return "576p"
	case height >= 480:
		return "480p"
	default:
		return strconv.Itoa(height) + "p"
	}
}

func dynamicRangeLabel(transfer string) string {
	switch strings.ToLower(transfer) {
	case "smpte2084":
		return "pq"
	case "arib-std-b67":
		return "hlg"
	default:
		return ""
	}
}

func trackValues(tracks []Track, value func(Track) string) []string {
	items := make([]string, 0, len(tracks))
	for _, track := range tracks {
		items = append(items, value(track))
	}
	return normalizedList(items, 64, 64)
}

func normalizedList(values []string, maxItems, maxLength int) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = normalizedToken(value, maxLength)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
		if len(result) == maxItems {
			break
		}
	}
	sort.Strings(result)
	return result
}

func normalizedToken(value string, max int) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > max {
		return ""
	}
	for _, character := range value {
		letter := character >= 'a' && character <= 'z'
		digit := character >= '0' && character <= '9'
		if !letter && !digit && !strings.ContainsRune("._-+", character) {
			return ""
		}
	}
	return value
}

func normalizedText(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		value = value[:max]
	}
	return strings.Map(func(character rune) rune {
		if character < 0x20 && character != '\t' {
			return -1
		}
		return character
	}, value)
}

func explicitDisposition(value int) *bool {
	if value != 1 {
		return nil
	}
	result := true
	return &result
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
	err    error
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.err = ErrOutputLimit
		return 0, buffer.err
	}
	if len(value) > remaining {
		written, _ := buffer.buffer.Write(value[:remaining])
		buffer.err = ErrOutputLimit
		return written, buffer.err
	}
	return buffer.buffer.Write(value)
}

func (buffer *boundedBuffer) Bytes() []byte { return buffer.buffer.Bytes() }
