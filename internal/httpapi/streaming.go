package httpapi

import (
	"context"
	"errors"
	"mime"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/media-manager/media-manager/internal/acquisition"
)

func (s *Server) streamingSearch(w http.ResponseWriter, r *http.Request) {
	if s.config.Streaming == nil {
		writeError(w, 503, "streaming_unavailable", "Streaming search is unavailable")
		return
	}
	var in acquisition.BrowseInput
	if decodeJSON(r, &in, 4096) != nil || !in.Valid() {
		writeError(w, 400, "invalid_streaming_search", "Provide a movie or episode, an IMDb or TMDB ID, title and year")
		return
	}
	result, err := s.config.Streaming.Browse(r.Context(), in)
	if err != nil {
		writeError(w, 503, "streaming_search_unavailable", "Search could not complete; try again shortly")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, dataEnvelope{Data: result})
}
func (s *Server) streamingNZB(w http.ResponseWriter, r *http.Request) {
	if s.config.Streaming == nil {
		writeError(w, 503, "streaming_unavailable", "Streaming is unavailable")
		return
	}
	body, filename, in, err := s.config.Streaming.Download(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, acquisition.ErrBrowseExpired) {
			writeError(w, 410, "streaming_choice_expired", "Refresh the stream list and choose the release again")
		} else {
			writeError(w, 503, "streaming_retrieval_unavailable", "The selected release could not be retrieved; retry later")
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/x-nzb")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("X-Media-Kind", string(in.Kind))
	w.Header().Set("X-Media-Season", strconv.Itoa(int(in.Season)))
	w.Header().Set("X-Media-Episode", strconv.Itoa(int(in.Episode)))
	w.WriteHeader(200)
	_, _ = w.Write(body)
}

// streamingPrepare has AltMount prepare the viewer-selected release and answers
// with the prepared file's path on the read-only AltMount mount. No URL, key or
// download key leaves Media Manager.
func (s *Server) streamingPrepare(w http.ResponseWriter, r *http.Request) {
	if !s.config.StreamPreparer.Configured() {
		writeError(w, 503, "streaming_preparation_unavailable", "Stream preparation is not configured")
		return
	}
	// AltMount may take up to its timeout to make the file readable; extend
	// this response's write deadline past the server-wide limit.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(s.config.StreamPreparer.Timeout + 45*time.Second))
	prepared, err := s.config.StreamPreparer.Prepare(r.Context(), r.PathValue("id"))
	if err != nil {
		status, code, message := streamingPrepareError(err)
		writeError(w, status, code, message)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, dataEnvelope{Data: prepared})
}

func streamingPrepareError(err error) (int, string, string) {
	var timeout net.Error
	switch {
	case errors.Is(err, acquisition.ErrBrowseExpired):
		return 410, "streaming_choice_expired", "Refresh the stream list and choose the release again"
	case errors.Is(err, acquisition.ErrStreamPreparationUnavailable):
		return 503, "streaming_preparation_unavailable", "Stream preparation is not configured"
	case errors.Is(err, acquisition.ErrStreamRetrieval):
		return 503, "streaming_retrieval_unavailable", "The selected release could not be retrieved; retry later"
	case errors.Is(err, acquisition.ErrStreamReleaseDead):
		return 422, "streaming_release_dead", "The release's articles are missing from Usenet; choose another release"
	case errors.Is(err, acquisition.ErrStreamAmbiguous):
		return 502, "streaming_release_ambiguous", "AltMount did not identify exactly one playable video"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return 504, "streaming_preparation_timeout", "AltMount preparation did not finish; refresh the stream list before retrying"
	default:
		return 502, "streaming_preparation_failed", "AltMount could not prepare the selected release"
	}
}
