package postgres

import (
	"fmt"
	"testing"
	"time"
	"uuid"
)

// TestDanishGroupRecordsLearnFromMeasuredOutputs proves what the group record
// counts: only measured Danish outcomes under a Danish requirement, per
// release group, without releases whose name already says Danish and without
// titles originally in Danish, and separately for Danish-audio profiles.
func TestDanishGroupRecordsLearnFromMeasuredOutputs(t *testing.T) {
	f := newFailureRecoveryFixture(t)
	now := time.Now().UTC()
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE quality_profiles SET policy=policy||'{"required_subtitle_languages":["da"]}'::jsonb WHERE id=$1`, f.profile.ID.String()); err != nil {
		t.Fatal(err)
	}
	var roundSlot int
	// One workflow per download: a round has one job per slot.
	job := func(t *testing.T, workflowID uuid.UUID, title, outcome string) {
		t.Helper()
		roundSlot++
		if workflowID == (uuid.UUID{}) {
			workflowID = f.newMovieWorkflow(t, fmt.Sprintf("Group Record %d", roundSlot), fmt.Sprintf("9051%02d", roundSlot)).ID
		}
		var roundID string
		if err := f.store.Pool.QueryRow(f.ctx, `SELECT id::text FROM acquisition_rounds WHERE workflow_id=$1 ORDER BY round_ordinal LIMIT 1`, workflowID.String()).Scan(&roundID); err != nil {
			t.Fatal(err)
		}
		candidate := seedEligibleCandidate(t, f.ctx, f.store, workflowID, f.indexer.ID, fmt.Sprintf("group-%d", roundSlot), 100, now.Add(-time.Hour))
		if _, err := f.store.Pool.Exec(f.ctx, `UPDATE release_candidates SET title=$2 WHERE id=$1`, candidate.String(), title); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,submission_started_at,revision,round_id,round_slot,acquisition_outcome) VALUES($1,$2,$3,$4,$5,$6,'needs_attention',now(),1,$7,1,$8)`,
			uuid.NewV7().String(), workflowID.String(), candidate.String(), f.altMount.ID.String(), roundSlot, fmt.Sprintf("job-%d.nzb", roundSlot), roundID, outcome); err != nil {
			t.Fatal(err)
		}
	}
	w := uuid.UUID{}
	job(t, w, "Group.Record.2020.1080p.WEB-DL-GOOD", "output_verified")
	job(t, w, "Group.Record.2020.720p.WEB-DL-GOOD", "output_verified")
	job(t, w, "Group.Record.2020.1080p.WEB-DL-BAD", "subtitle_requirements_unverified")
	job(t, w, "Group.Record.2020.1080p.WEB-DL-BAD.mkv", "subtitle_requirements_unverified")
	job(t, w, "Group.Record.2020.NORDiC.1080p.WEB-DL-BAD", "output_verified")
	job(t, w, "Group.Record.2020.1080p.WEB-DL-GOOD", "release_incomplete")
	job(t, w, "Group Record 2020 1080p", "subtitle_requirements_unverified")

	danish := f.newMovieWorkflow(t, "Dansk Original", "905002")
	if _, err := f.store.Pool.Exec(f.ctx, `INSERT INTO media_metadata(media_id,data,created_at,updated_at) VALUES($1,'{"original_language":"da"}',now(),now()) ON CONFLICT(media_id) DO UPDATE SET data=EXCLUDED.data`, danish.MediaID.String()); err != nil {
		t.Fatal(err)
	}
	job(t, danish.ID, "Dansk.Original.2020.1080p.WEB-DL-BAD", "output_verified")

	records, err := loadDanishGroupRecords(f.ctx, f.store.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if got := records.Subtitles["good"]; got != (DanishGroupRecord{Verified: 2}) {
		t.Fatalf("good=%+v", got)
	}
	if got := records.Subtitles["bad"]; got != (DanishGroupRecord{Missing: 2}) {
		t.Fatalf("bad=%+v (Danish-named and Danish-original releases must not count)", got)
	}
	if len(records.Subtitles) != 2 || len(records.Audio) != 0 {
		t.Fatalf("records=%+v", records)
	}

	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE quality_profiles SET policy=policy-'required_subtitle_languages'||'{"required_audio_languages":["da"]}'::jsonb WHERE id=$1`, f.profile.ID.String()); err != nil {
		t.Fatal(err)
	}
	records, err = loadDanishGroupRecords(f.ctx, f.store.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(records.Subtitles) != 0 || records.Audio["bad"] != (DanishGroupRecord{Missing: 2}) {
		t.Fatalf("audio records=%+v", records)
	}
	if _, err := f.store.Pool.Exec(f.ctx, `UPDATE quality_profiles SET policy=policy-'required_audio_languages' WHERE id=$1`, f.profile.ID.String()); err != nil {
		t.Fatal(err)
	}
	if records, err = loadDanishGroupRecords(f.ctx, f.store.Pool); err != nil || len(records.Subtitles)+len(records.Audio) != 0 {
		t.Fatalf("a profile without a Danish requirement teaches nothing: %+v err=%v", records, err)
	}
}
