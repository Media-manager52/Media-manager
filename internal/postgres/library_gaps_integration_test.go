package postgres

import (
	"slices"
	"testing"
	"time"
	"uuid"
)

// TestLibraryGapsListsUnfollowedSeasonsAndFilmsWithoutFiles covers the
// library's "Find manglende": a series lists the provider's seasons that have
// neither open work nor a file, split into seasons after the highest known
// one and seasons before it, with stopped work noted; a film is listed only
// when it has no file and no open work. Nothing is written.
func TestLibraryGapsListsUnfollowedSeasonsAndFilmsWithoutFiles(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	ctx, store := fixture.ctx, fixture.store
	exec := func(query string, arguments ...any) {
		t.Helper()
		if _, err := store.Pool.Exec(ctx, query, arguments...); err != nil {
			t.Fatal(err)
		}
	}
	series := uuid.NewV7()
	exec(`INSERT INTO media_items(id,kind,title,year,revision,created_at,updated_at) VALUES($1,'series','Gap Series',2020,1,now(),now())`, series.String())
	exec(`INSERT INTO media_metadata(media_id,data,created_at,updated_at) VALUES($1,'{"season_count":5,"status":"Returning Series","poster_url":"https://image.tmdb.org/t/p/w342/gap.jpg"}',now(),now())`, series.String())
	exec(`INSERT INTO external_identities(id,media_id,media_kind,namespace,value,state,source,observed_at) VALUES($1,$2,'series','tmdb','990001','verified','test',now())`, uuid.NewV7().String(), series.String())
	workflow := func(media uuid.UUID, variant string, ended bool) {
		t.Helper()
		state, terminal := "waiting", any(nil)
		if ended {
			state, terminal = "cancelled", time.Now().UTC()
		}
		exec(`INSERT INTO workflows(id,media_id,state,root_id,quality_profile_id,download_client_id,variant,revision,terminal_at,created_at,updated_at,edition)
			VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,now(),now(),'standard')`, uuid.NewV7().String(), media.String(), state, fixture.root.ID.String(), fixture.profile.ID.String(), fixture.altMount.ID.String(), variant, terminal)
	}
	workflow(series, "season:02", false)
	workflow(series, "season:03:episode:04", false)
	workflow(series, "season:01", true)

	waitingFilm := fixture.movieItem(t, "Gap Film Waiting", "990011")
	cancelledFilm := fixture.movieItem(t, "Gap Film Cancelled", "990012")
	presentFilm := fixture.movieItem(t, "Gap Film Present", "990013")
	if _, err := store.CreateManualMedia(ctx, waitingFilm, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateManualMedia(ctx, cancelledFilm, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelOpenWorkOfMedia(ctx, cancelledFilm.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateManualMedia(ctx, presentFilm, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, nil, false, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelOpenWorkOfMedia(ctx, presentFilm.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	seedCurrentSubtitleVersion(t, ctx, store, presentFilm.ID, fixture.root.ID, fixture.profile.ID, fixture.altMount.ID, fixture.indexer.ID)

	gaps, err := store.LibraryGaps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found *SeriesGap
	for index := range gaps.Series {
		if gaps.Series[index].MediaID == series {
			found = &gaps.Series[index]
		}
	}
	if found == nil {
		t.Fatalf("series missing from gaps: %+v", gaps.Series)
	}
	if !slices.Equal(found.KnownSeasons, []int{2, 3}) || !slices.Equal(found.NewSeasons, []int{4, 5}) || !slices.Equal(found.OlderSeasons, []int{1}) || !slices.Equal(found.StoppedSeasons, []int{1}) {
		t.Fatalf("series gap = %+v", *found)
	}
	if found.SeasonCount != 5 || found.TMDBID != "990001" || found.Status != "Returning Series" || found.PosterURL == "" || found.RootID == nil || *found.RootID != fixture.root.ID || found.QualityProfileID == nil || *found.QualityProfileID != fixture.profile.ID {
		t.Fatalf("series details = %+v", *found)
	}
	films := map[uuid.UUID]MovieGap{}
	for _, film := range gaps.Movies {
		films[film.MediaID] = film
	}
	if _, listed := films[waitingFilm.ID]; listed {
		t.Fatal("a film with open work is listed as missing")
	}
	if _, listed := films[presentFilm.ID]; listed {
		t.Fatal("a film with a file is listed as missing")
	}
	cancelled, listed := films[cancelledFilm.ID]
	if !listed || cancelled.LastState != "cancelled" || cancelled.LastAt == nil || cancelled.RootID == nil {
		t.Fatalf("cancelled film gap = %+v listed=%v", cancelled, listed)
	}

	// A series whose every season is known is not listed.
	workflow(series, "season:01", false)
	workflow(series, "season:04", false)
	workflow(series, "season:05", false)
	gaps, err = store.LibraryGaps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, gap := range gaps.Series {
		if gap.MediaID == series {
			t.Fatalf("complete series still listed: %+v", gap)
		}
	}
}
