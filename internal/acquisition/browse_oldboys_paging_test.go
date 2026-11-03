package acquisition

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/oldboys"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/oldboyscredentials"
)

type oldBoysPageTransport func(*http.Request) (*http.Response, error)

func (f oldBoysPageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestOldBoysBrowseStopsAtTheShortPage pins that a stream list asks OldBoys
// for the next page only after a full page: each extra request waits for
// OldBoys' pacing interval, and the page after a short one is always empty.
func TestOldBoysBrowseStopsAtTheShortPage(t *testing.T) {
	for _, test := range []struct {
		name     string
		pages    []int
		requests int
		rows     int
	}{
		{"one short page", []int{3}, 1, 3},
		{"full then short", []int{oldboys.PageSize, 7}, 2, oldboys.PageSize + 7},
		{"every page full", []int{oldboys.PageSize, oldboys.PageSize, oldboys.PageSize, oldboys.PageSize, oldboys.PageSize}, oldboys.MaxPage, oldboys.MaxPage * oldboys.PageSize},
		{"empty", []int{0}, 1, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			transport := oldBoysPageTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				count := 0
				if page >= 1 && page <= len(test.pages) {
					count = test.pages[page-1]
				}
				rows := make([]string, 0, count)
				for index := 0; index < count; index++ {
					rows = append(rows, fmt.Sprintf(`{"id":"%d","attributes":{"name":"Example.2020.1080p.WEB-DL.DANiSH-P%dR%d","category_id":1}}`, page*100+index, page, index))
				}
				body := `{"data":[` + strings.Join(rows, ",") + `]}`
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			scheduler := indexers.NewScheduler(1, nil)
			id := uuid.NewV7()
			if err := scheduler.Configure(id.String(), indexers.Budget{MinimumInterval: time.Millisecond, FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, GrabLimit: 10, LimitWindow: time.Hour}); err != nil {
				t.Fatal(err)
			}
			client, err := oldboys.New(id, oldboyscredentials.Credentials{APIToken: "token", PassKey: "pid", RSSKey: "rid"}, scheduler, transport, nil)
			if err != nil {
				t.Fatal(err)
			}
			observations, err := oldBoysBrowseObservations(context.Background(), client, BrowseInput{Kind: catalog.KindMovie, IMDb: "tt0000123", Title: "Example", Year: 2020})
			if err != nil {
				t.Fatal(err)
			}
			if requests != test.requests || len(observations) != test.rows {
				t.Fatalf("requests=%d rows=%d, want requests=%d rows=%d", requests, len(observations), test.requests, test.rows)
			}
		})
	}
}
