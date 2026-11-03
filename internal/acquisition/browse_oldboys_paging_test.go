package acquisition

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
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

// TestOldBoysBrowseShowsPageOneAndFetchesTheRestInTheBackground pins that a
// stream list waits only for OldBoys' first page. After a full first page the
// remaining pages arrive in the background, stopping at the first short page
// or MaxPage, and the next open of the title gets every page without new
// requests. A short first page is complete at once.
func TestOldBoysBrowseShowsPageOneAndFetchesTheRestInTheBackground(t *testing.T) {
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
			var requests atomic.Int32
			transport := oldBoysPageTransport(func(r *http.Request) (*http.Response, error) {
				requests.Add(1)
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
			in := BrowseInput{Kind: catalog.KindMovie, IMDb: "tt0000123", Title: "Example", Year: 2020}
			service := &BrowseService{}
			// A stream list built from page one must not outlive the
			// complete result.
			service.cache = map[BrowseInput]browseCache{in: {until: time.Now().Add(time.Minute)}}
			first, partial, err := service.oldBoysBrowseObservations(context.Background(), client, id.String(), in)
			if err != nil {
				t.Fatal(err)
			}
			wantFirst := test.pages[0]
			if len(first) != wantFirst || partial != (wantFirst == oldboys.PageSize) {
				t.Fatalf("first open rows=%d partial=%v", len(first), partial)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				all, partial, err := service.oldBoysBrowseObservations(context.Background(), client, id.String(), in)
				if err != nil {
					t.Fatal(err)
				}
				if !partial {
					if len(all) != test.rows || int(requests.Load()) != test.requests {
						t.Fatalf("complete rows=%d requests=%d, want rows=%d requests=%d", len(all), requests.Load(), test.rows, test.requests)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the remaining pages never arrived")
				}
				time.Sleep(10 * time.Millisecond)
			}
			service.mu.Lock()
			_, stale := service.cache[in]
			service.mu.Unlock()
			if stale && partial {
				t.Fatal("the stream list built from page one was kept")
			}
		})
	}
}
