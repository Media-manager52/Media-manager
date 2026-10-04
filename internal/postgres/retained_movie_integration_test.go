package postgres

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/subtitles"
)

func seedAmbiguousRetainedMovie(t *testing.T, f *failureRecoveryFixture) (uuid.UUID, AltMountJobContext, uuid.UUID, []uuid.UUID) {
	t.Helper()
	if err := f.store.ConfigureAcquisitionLimits(AcquisitionLimits{AttemptsPerRound: 1, RetainedFailedOutputs: 4}); err != nil {
		t.Fatal(err)
	}
	requireDanishForRecovery(t, f)
	w := f.newMovieWorkflow(t, "Movie", "915002")
	r := seedRetrievalCandidate(t, f.ctx, f.store, w.ID, f.indexer.ID, "ambiguous")
	intent, err := f.store.BeginAltMountSubmission(f.ctx, r, testNZBPackageIdentity(111), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	job, inventory, assets := seedSubmittedSubtitleOutput(t, f.ctx, f.store, w.ID, r.CandidateID, intent.JobID, "ambiguous")
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO altmount_output_assets(id,inventory_id,relative_path,size_bytes,media_kind) VALUES($1,$2,'extras/Other.Film.1990.mkv',900,'movie')`, uuid.NewV7().String(), inventory.String()); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CompleteReleaseFailure(f.ctx, job, "movie_primary_ambiguous", "two apparent features", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return w.ID, job, inventory, assets
}

func TestRetainedMovieInspectionPreservesSlotInventoryAndVerifiedSelection(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	wid, job, inventory, assets := seedAmbiguousRetainedMovie(t, f)
	if err := f.store.RequestRetainedMovieInspection(f.ctx, wid, job.JobID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RequestRetainedMovieInspection(f.ctx, wid, job.JobID, time.Now().UTC()); !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("duplicate owner request=%v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, wid.String()); got != 1 {
		t.Fatalf("new NZB job created: %d", got)
	}
	if got := f.count(t, `SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1`, wid.String()); got != 1 {
		t.Fatalf("round opened: %d", got)
	}
	if got := f.count(t, `SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1`, wid.String()); got != 1 {
		t.Fatalf("exclusion removed: %d", got)
	}
	retained, err := f.store.LoadRetainedMovieOutput(f.ctx, wid, job.JobID)
	if err != nil || retained.InventoryID != inventory || len(retained.Assets) != 3 {
		t.Fatalf("retained=%+v err=%v", retained, err)
	}
	job, err = f.store.LoadAltMountJob(f.ctx, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	verification := subtitles.Verification{SchemaVersion: 1, Status: "verified", RequiredLanguages: []string{"da"}, ObservedLanguages: []string{"da"}, Videos: []subtitles.VideoEvidence{{AssetID: assets[0], RelativePath: "Movie.mkv", MediaInfo: mediainfo.Evidence{SchemaVersion: 1, VideoTracks: []mediainfo.Track{{Index: 0, Codec: "h264"}}, SubtitleTracks: []mediainfo.Track{{Index: 2, Codec: "subrip", Language: "dan", CanonicalLanguage: "da"}}}}}}
	if _, err := f.store.RecordSubtitleVerification(f.ctx, job, inventory, verification, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CompleteImport(f.ctx, job, true, "verified retained main movie", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	publication, err := f.store.LoadPublicationContext(f.ctx, wid, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(publication.Assets) != 1 || publication.Assets[0].ID != assets[0] {
		t.Fatalf("uninspected bonus selected for publication: %+v", publication.Assets)
	}
	if got := f.count(t, `SELECT count(*) FROM workflow_release_exclusions WHERE workflow_id=$1`, wid.String()); got != 1 {
		t.Fatal("historical exclusion deleted")
	}
	if got := f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='release.retrieve_requested'`, wid.String()); got != 0 {
		t.Fatalf("inspection queued external retrieval: %d", got)
	}
	// A malformed/stale verification cannot redirect publication to another asset.
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE subtitle_verifications SET evidence=jsonb_set(evidence,'{videos,0,relative_path}','"extras/Other.Film.1990.mkv"') WHERE altmount_job_id=$1`, job.JobID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.LoadPublicationContext(f.ctx, wid, job.JobID); err == nil {
		t.Fatal("changed verified path selected an unverified file")
	}
}

func TestRetainedMovieInspectionRefusesUnsafeRecovery(t *testing.T) {
	for _, name := range []string{"unknown job", "foreign job", "open round", "changed policy", "prior publication", "wrong failure", "newer round", "empty inventory"} {
		t.Run(name, func(t *testing.T) {
			f := newFailureRecoveryFixture(t)
			wid, job, inventory, _ := seedAmbiguousRetainedMovie(t, f)
			switch name {
			case "unknown job":
				job.JobID = uuid.NewV7()
			case "foreign job":
				wid = uuid.NewV7()
			case "open round":
				_, err := f.store.Pool.Exec(f.ctx, `UPDATE acquisition_rounds SET state='open' WHERE workflow_id=$1`, wid.String())
				if err != nil {
					t.Fatal(err)
				}
			case "changed policy":
				_, err := f.store.Pool.Exec(f.ctx, `UPDATE quality_profiles SET policy_version=policy_version+1 WHERE id=$1`, f.profile.ID.String())
				if err != nil {
					t.Fatal(err)
				}
			case "prior publication":
				w, err := f.store.GetWorkflow(f.ctx, wid)
				if err != nil {
					t.Fatal(err)
				}
				seedCurrentSubtitleVersion(t, f.ctx, f.store, w.MediaID, f.root.ID, f.profile.ID, f.altMount.ID, f.indexer.ID)
			case "wrong failure":
				_, err := f.store.Pool.Exec(f.ctx, `UPDATE altmount_jobs SET error_code='release_incomplete' WHERE id=$1`, job.JobID.String())
				if err != nil {
					t.Fatal(err)
				}
			case "newer round":
				_, err := f.store.Pool.Exec(f.ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,policy_version,policy_snapshot,opened_at) SELECT $1,workflow_id,2,'exhausted',attempt_limit_snapshot,policy_version,policy_snapshot,now() FROM acquisition_rounds WHERE workflow_id=$2`, uuid.NewV7().String(), wid.String())
				if err != nil {
					t.Fatal(err)
				}
			case "empty inventory":
				_, err := f.store.Pool.Exec(f.ctx, `DELETE FROM altmount_output_assets WHERE inventory_id=$1`, inventory.String())
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := f.store.RequestRetainedMovieInspection(f.ctx, wid, job.JobID, time.Now().UTC()); err == nil {
				t.Fatal("unsafe retained inspection admitted")
			}
			if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action='output.movie_reinspection_requested'`); got != 0 {
				t.Fatalf("unsafe request recorded authorization: %d", got)
			}
		})
	}
}

func TestRetainedMovieUnverifiedInspectionCannotAcquireAgain(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	wid, job, inventory, assets := seedAmbiguousRetainedMovie(t, f)
	if err := f.store.RequestRetainedMovieInspection(f.ctx, wid, job.JobID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	job, err := f.store.LoadAltMountJob(f.ctx, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	verification, err := f.store.RecordSubtitleVerification(f.ctx, job, inventory, unverifiedSubtitleResult(assets[0], "no verified Danish subtitle"), time.Now().UTC())
	if err != nil || verification.Status != "unverified" {
		t.Fatalf("verification=%+v err=%v", verification, err)
	}
	w, err := f.store.GetWorkflow(f.ctx, wid)
	if err != nil || w.State != "waiting" {
		t.Fatalf("workflow=%+v err=%v", w, err)
	}
	if got := f.count(t, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type IN ('release.retrieve_requested','workflow.search_requested')`, wid.String()); got != 0 {
		t.Fatalf("failed retained inspection queued a new acquisition: %d", got)
	}
	if got := f.count(t, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1`, wid.String()); got != 1 {
		t.Fatalf("retained inspection created job: %d", got)
	}
	if err := f.store.RequestRetainedMovieInspection(f.ctx, wid, job.JobID, time.Now().UTC()); err == nil {
		t.Fatal("unverified retained inspection could be repeated")
	}
	search, err := f.store.LoadSearchContext(f.ctx, wid)
	if err != nil || len(search.Exclusions) != 1 || !search.Exclusions[0].MatchesTitle(search.Exclusions[0].ReleaseTitle) {
		t.Fatalf("new negative subtitle evidence missing from historical ambiguity exclusion: %+v err=%v", search.Exclusions, err)
	}
	var knownFailure bool
	if err := f.store.Pool.QueryRow(f.ctx, `SELECT `+knownSubtitleFailureSQL("c")+` FROM release_candidates c WHERE c.id=$1`, job.CandidateID.String()).Scan(&knownFailure); err != nil || !knownFailure {
		t.Fatalf("persisted title guard missed negative retained inspection: %v err=%v", knownFailure, err)
	}
}
