package acquisition

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
	"uuid"
)

// seasonShareFixture is a series whose season 1 has four released, missing
// episodes (one episode workflow each) and one future episode, searched on
// one indexer whose series dialect sends tvdbid, season and ep.
func seasonShareFixture(t *testing.T, respond func(url.Values) string) (*postgres.Store, *SearchService, []workflows.Workflow, func() []url.Values) {
	t.Helper()
	store := openMovieSearchTestStore(t)
	ctx := context.Background()
	server, captured := newMovieSearchServer(t, respond)
	t.Cleanup(server.Close)
	config, err := json.Marshal(map[string]any{"base_url": server.URL, "allow_http": true, "movie_category_ids": []int{2000}, "series_category_ids": []int{5000}, "category_selection_review_required": false})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "newznab", Name: "Season share indexer", Enabled: true, Config: config, SecretRef: "SEASON_SHARE_TEST_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	dialects := newznab.SearchDialects{
		Movie:  newznab.SearchDialect{Mode: "movie", Params: []string{"q", "year"}},
		Series: newznab.SearchDialect{Mode: "tvsearch", Params: []string{"q", "tvdbid", "season", "ep"}},
	}
	if _, err := store.CompleteNewznabCommission(ctx, indexer.ID, newznab.CapsSnapshot{ServerVersion: "test", BodyHash: "season-share-caps", DefaultLimit: 50, MaximumLimit: 100}, dialects, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	plex, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "plex", Name: "Season Share Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex.invalid:32400","allow_http":true}`), SecretRef: "SEASON_SHARE_PLEX"})
	if err != nil {
		t.Fatal(err)
	}
	altMount, err := store.CreateIntegration(ctx, postgres.Integration{Kind: "altmount", Name: "Season Share AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount.invalid:8080","allow_http":true}`), SecretRef: "SEASON_SHARE_ALTMOUNT"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateLibraryRoot(ctx, postgres.LibraryRoot{MediaKind: catalog.KindSeries, Name: "TV", DestinationRoot: "/libraries/tv", PlexVisibleRoot: "/libraries/tv", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "2", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// 720p only: every release in the answers is held, so no episode selects
	// a download and each search ends waiting with its candidates stored.
	policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"720p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateQualityProfile(ctx, postgres.QualityProfile{MediaKind: catalog.KindSeries, Name: "TV quality", PolicyVersion: 1, Policy: policy, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	year := uint16(2020)
	series, err := catalog.New(catalog.KindSeries, "Example Series", &year, []catalog.ExternalIdentity{
		{Namespace: "tvdb", Value: "378992", State: catalog.IdentityVerified, Source: "test"},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	episodes := []postgres.ManualEpisode{}
	for number := 1; number <= 5; number++ {
		episodes = append(episodes, postgres.ManualEpisode{Number: uint16(number), ProviderID: fmt.Sprintf("tvdb:20%02d", number), Title: fmt.Sprintf("Episode %d", number)})
	}
	created, err := store.CreateManualMedia(ctx, series, root.ID, profile.ID, altMount.ID, []postgres.ManualSeason{{Number: 1, Monitored: true, Episodes: episodes}}, false, "test")
	if err != nil || len(created) != 1 {
		t.Fatalf("season workflows=%d err=%v", len(created), err)
	}
	past, future := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02"), time.Now().UTC().AddDate(0, 0, 30).Format("2006-01-02")
	if _, err := store.Pool.Exec(ctx, `UPDATE pack_expected_episodes SET air_date=CASE WHEN episode<5 THEN $2::date ELSE $3::date END WHERE expectation_id IN (SELECT id FROM pack_expectation_sets WHERE workflow_id=$1)`, created[0].ID.String(), past, future); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DispatchAiringSeason(ctx, created[0].ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Pool.Query(ctx, `SELECT id::text FROM workflows WHERE media_id=$1 AND variant LIKE 'season:01:episode:%' ORDER BY variant`, created[0].MediaID.String())
	if err != nil {
		t.Fatal(err)
	}
	var children []workflows.Workflow
	for rows.Next() {
		var idText string
		if err := rows.Scan(&idText); err != nil {
			t.Fatal(err)
		}
		child, err := store.GetWorkflow(ctx, uuid.MustParse(idText))
		if err != nil {
			t.Fatal(err)
		}
		children = append(children, child)
	}
	rows.Close()
	if len(children) != 4 {
		t.Fatalf("episode workflows=%d, want 4", len(children))
	}
	// The episode workflows exist before the season answer is fetched.
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET created_at=created_at-interval '1 hour' WHERE media_id=$1`, created[0].MediaID.String()); err != nil {
		t.Fatal(err)
	}
	return store, movieSearchService(store, server), children, captured
}

func seasonAnswerXML(episodes ...int) string {
	var items strings.Builder
	for _, episode := range episodes {
		fmt.Fprintf(&items, `<item><title>Example.Series.S01E%02d.1080p.WEB-DL-GRP</title><guid>season-share-%d</guid><link>https://indexer.invalid/getnzb/season-share-%d</link><newznab:attr name="resolution" value="1920x1080"/></item>`, episode, episode, episode)
	}
	return `<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>` + items.String() + `</channel></rss>`
}

func episodeCandidates(t *testing.T, store *postgres.Store, workflowID uuid.UUID) []string {
	t.Helper()
	rows, err := store.Pool.Query(context.Background(), `SELECT title FROM release_candidates WHERE workflow_id=$1 ORDER BY title`, workflowID.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	titles := []string{}
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatal(err)
		}
		titles = append(titles, title)
	}
	return titles
}

// TestEpisodesOfOneSeasonShareOneSeasonSearch proves the saving and its
// limits: four missing episodes of one season cost one indexer request, and
// every episode keeps exactly its own release, evaluated under its own rules,
// as if the indexer had answered an episode search.
func TestEpisodesOfOneSeasonShareOneSeasonSearch(t *testing.T) {
	store, service, episodes, captured := seasonShareFixture(t, func(query url.Values) string {
		if query.Get("t") == "tvsearch" && query.Get("season") == "1" && query.Get("ep") == "" {
			return seasonAnswerXML(1, 2, 3, 4, 5)
		}
		return emptySearchXML()
	})
	ctx := context.Background()
	for _, episode := range episodes {
		if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: episode.ID, AggregateRevision: episode.Revision}); err != nil {
			t.Fatalf("search %s: %v", episode.ID, err)
		}
	}
	requests := captured()
	if len(requests) != 1 || requests[0].Get("ep") != "" || requests[0].Get("season") != "1" || requests[0].Get("tvdbid") != "378992" {
		t.Fatalf("indexer requests=%v, want one season search", requests)
	}
	for index, episode := range episodes {
		want := fmt.Sprintf("Example.Series.S01E%02d.1080p.WEB-DL-GRP", index+1)
		if got := episodeCandidates(t, store, episode.ID); len(got) != 1 || got[0] != want {
			t.Fatalf("%s candidates=%v, want only %s", episode.ID, got, want)
		}
		current, err := store.GetWorkflow(ctx, episode.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State != workflows.StateHeld && current.State != workflows.StateWaiting {
			t.Fatalf("%s state=%s, want held or waiting under its own rules", episode.ID, current.State)
		}
	}
	var details []string
	rows, err := store.Pool.Query(ctx, `SELECT COALESCE(indexer.detail,'') FROM search_run_indexers indexer JOIN search_runs run ON run.id=indexer.search_run_id JOIN workflows workflow ON workflow.id=run.workflow_id WHERE workflow.variant LIKE 'season:01:episode:%' AND indexer.state='succeeded' ORDER BY run.started_at`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	rows.Close()
	if strings.Join(details, ",") != "season search,reused season search,reused season search,reused season search" {
		t.Fatalf("indexer outcomes=%v", details)
	}
	var locator string
	if err := store.Pool.QueryRow(ctx, `SELECT download_locator_ref FROM release_candidates WHERE workflow_id=$1`, episodes[3].ID.String()).Scan(&locator); err != nil {
		t.Fatal(err)
	}
	if locator == "" {
		t.Fatal("a reused release lost its download locator")
	}
	// Every episode's candidate carries the facts its own evaluation derived
	// from the title, as a fresh episode search would store them.
	for _, episode := range episodes {
		var derived bool
		if err := store.Pool.QueryRow(ctx, `SELECT observation.normalized_facts @> '[{"name":"source","values":["webdl"]}]'::jsonb FROM release_candidates candidate JOIN release_observations observation ON observation.id=candidate.observation_id WHERE candidate.workflow_id=$1`, episode.ID.String()).Scan(&derived); err != nil {
			t.Fatal(err)
		}
		if !derived {
			t.Fatalf("%s candidate lost the facts its evaluation derived", episode.ID)
		}
	}
}

// TestSeasonSearchIsNotSharedWhenTooFewEpisodesAreMissing keeps the ordinary
// episode search for a season with only a couple of open episodes.
func TestSeasonSearchIsNotSharedWhenTooFewEpisodesAreMissing(t *testing.T) {
	store, service, episodes, captured := seasonShareFixture(t, func(query url.Values) string {
		if query.Get("ep") != "" {
			return seasonAnswerXML(mustAtoi(t, query.Get("ep")))
		}
		return emptySearchXML()
	})
	ctx := context.Background()
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='cancelled',terminal_at=now() WHERE id=ANY($1::uuid[])`, []string{episodes[0].ID.String(), episodes[1].ID.String()}); err != nil {
		t.Fatal(err)
	}
	if err := service.Handle(ctx, postgres.OutboxEvent{AggregateID: episodes[2].ID, AggregateRevision: episodes[2].Revision}); err != nil {
		t.Fatal(err)
	}
	requests := captured()
	if len(requests) != 1 || requests[0].Get("ep") != "3" {
		t.Fatalf("requests=%v, want one episode search", requests)
	}
	if got := episodeCandidates(t, store, episodes[2].ID); len(got) != 1 {
		t.Fatalf("candidates=%v", got)
	}
}

func mustAtoi(t *testing.T, value string) int {
	t.Helper()
	var number int
	if _, err := fmt.Sscanf(value, "%d", &number); err != nil {
		t.Fatal(err)
	}
	return number
}
