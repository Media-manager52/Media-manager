package httpapi

import (
	"errors"
	"github.com/media-manager/media-manager/internal/acquisition"
	"mime"
	"net/http"
	"strconv"
)

func (s *Server) streamingSearch(w http.ResponseWriter, r *http.Request) {
	if s.config.Streaming == nil {
		writeError(w, 503, "streaming_unavailable", "Streaming search is unavailable")
		return
	}
	var in acquisition.BrowseInput
	if decodeJSON(r, &in, 4096) != nil || !in.Valid() {
		writeError(w, 400, "invalid_streaming_search", "Provide a movie or episode, IMDb ID, title and year")
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
