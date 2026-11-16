package newznab

import "net/url"

// FeedStream is one request an indexer's newest uploads are read with.
type FeedStream struct {
	// Name is "movie" or "series"; the single ordinary request has no name.
	Name string
	// Mode is the Newznab function (t=) of the request.
	Mode string
	// RootCategory is the standard Newznab root the stream covers.
	RootCategory int
}

// Feed stream names. They are stored with each stream's reader state.
const (
	FeedStreamMovie  = "movie"
	FeedStreamSeries = "series"
)

// FeedStreams returns the requests an indexer's newest uploads are read with
// when one ordinary search request is not enough, or nil for the ordinary
// single request.
//
// abNZB answers t=search without the subtitle list and NFO flag that the same
// uploads carry in its t=tvsearch and t=movie answers (measured 2026-11-15:
// 100 of 100 identical upload IDs, 29 of them with a subtitle list only in
// the movie and series answers). Its newest uploads are therefore read as one
// movie and one series request. The decision is bound to the measured
// endpoint, as the other provider contracts in this package are.
func FeedStreams(baseURL string) []FeedStream {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}
	origin, err := exactOrigin(parsed)
	if err != nil || origin.scheme != "https" || origin.port != "443" {
		return nil
	}
	switch origin.hostname {
	case "abnzb.com":
		return []FeedStream{
			{Name: FeedStreamMovie, Mode: string(SearchModeMovie), RootCategory: 2000},
			{Name: FeedStreamSeries, Mode: string(SearchModeTV), RootCategory: 5000},
		}
	default:
		return nil
	}
}
