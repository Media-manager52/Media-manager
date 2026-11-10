package postgres

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
)

// TestDeleteQualityProfileOnlyWhenUnused proves that deleting a profile can
// never change what an existing title requires: the default profile and a
// profile any workflow uses are refused, and an unused one is removed.
func TestDeleteQualityProfileOnlyWhenUnused(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	now := time.Now().UTC()
	deleteProfile := func(id uuid.UUID) error {
		_, err := store.DeleteQualityProfile(ctx, id, nil, "owner", now)
		return err
	}

	spare, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Spare movie profile", PolicyVersion: 1, Policy: fixture.profile.Policy, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	fixture.movieWorkflow(t, "Profile In Use", "590001")

	if err := deleteProfile(fixture.profile.ID); !errors.Is(err, ErrQualityProfileIsDefault) && !errors.Is(err, ErrQualityProfileInUse) {
		t.Fatalf("deleting the fixture profile: err=%v, want default or in-use refusal", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE quality_profiles SET is_default=false WHERE id=$1`, fixture.profile.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := deleteProfile(fixture.profile.ID); !errors.Is(err, ErrQualityProfileInUse) {
		t.Fatalf("deleting a profile a workflow uses: err=%v, want ErrQualityProfileInUse", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE quality_profiles SET is_default=true WHERE id=$1`, spare.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := deleteProfile(spare.ID); !errors.Is(err, ErrQualityProfileIsDefault) {
		t.Fatalf("deleting the default profile: err=%v, want ErrQualityProfileIsDefault", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE quality_profiles SET is_default=false WHERE id=$1`, spare.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := deleteProfile(spare.ID); err != nil {
		t.Fatalf("deleting an unused profile: %v", err)
	}
	var remaining, audited int
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM quality_profiles WHERE id IN ($1,$2)),(SELECT count(*) FROM audit_events WHERE action='quality_profile.deleted' AND aggregate_id=$2)`, fixture.profile.ID.String(), spare.ID.String()).Scan(&remaining, &audited); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 || audited != 1 {
		t.Fatalf("remaining=%d audited=%d, want the used profile kept and one audit event", remaining, audited)
	}
	if err := deleteProfile(spare.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting twice: err=%v, want ErrNotFound", err)
	}
	if err := deleteProfile(uuid.NewV7()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting an unknown profile: err=%v, want ErrNotFound", err)
	}
}

// TestDeleteQualityProfileMovesReferencesToReplacement proves the owner can
// delete a profile in use by naming the profile that takes over: every
// reference moves, the replacement must be an enabled profile of the same
// media kind, and a moved title whose round was frozen under different rules
// is stale under the replacement (it waits for apply-policy) even when the two
// profiles had the same version number.
func TestDeleteQualityProfileMovesReferencesToReplacement(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	now := time.Now().UTC()

	_, workflowID := fixture.movieWorkflow(t, "Profile Moved", "590002")
	stricter := json.RawMessage(`{"rules":[{"name":"Resolution","fact":"resolution","allowed_values":["2160p"],"kind":"required"}],"required_audio_languages":["da"]}`)
	replacement, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Replacement profile", PolicyVersion: 1, Policy: stricter, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	series, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindSeries, Name: "Series profile", PolicyVersion: 1, Policy: fixture.profile.Policy, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// The replacement becomes the default so the fixture profile is deletable.
	if _, err := store.Pool.Exec(ctx, `UPDATE quality_profiles SET is_default=(id=$1) WHERE media_kind='movie'`, replacement.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,upgrade_baseline_link_versions,opened_at)
		SELECT $1,$2,1,'open',3,0,profile.policy_version,profile.policy,'',$3 FROM quality_profiles profile WHERE profile.id=$4
		AND NOT EXISTS(SELECT 1 FROM acquisition_rounds WHERE workflow_id=$2)`, uuid.NewV7().String(), workflowID.String(), now, fixture.profile.ID.String()); err != nil {
		t.Fatal(err)
	}

	for name, bad := range map[string]uuid.UUID{"itself": fixture.profile.ID, "another media kind": series.ID, "unknown": uuid.NewV7()} {
		if _, err := store.DeleteQualityProfile(ctx, fixture.profile.ID, &bad, "owner", now); !errors.Is(err, ErrQualityProfileReplacementInvalid) {
			t.Fatalf("replacement %s: err=%v, want ErrQualityProfileReplacementInvalid", name, err)
		}
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE quality_profiles SET enabled=false,is_default=false WHERE id=$1`, replacement.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteQualityProfile(ctx, fixture.profile.ID, &replacement.ID, "owner", now); !errors.Is(err, ErrQualityProfileReplacementInvalid) {
		t.Fatalf("disabled replacement: err=%v, want ErrQualityProfileReplacementInvalid", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE quality_profiles SET enabled=true WHERE id=$1`, replacement.ID.String()); err != nil {
		t.Fatal(err)
	}
	var stillThere int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM workflows WHERE quality_profile_id=$1`, fixture.profile.ID.String()).Scan(&stillThere); err != nil || stillThere != 1 {
		t.Fatalf("refused deletes moved the workflow: count=%d err=%v", stillThere, err)
	}

	result, err := store.DeleteQualityProfile(ctx, fixture.profile.ID, &replacement.ID, "owner", now)
	if err != nil {
		t.Fatalf("delete with replacement: %v", err)
	}
	if result.MovedTitles != 1 || result.ReplacedBy != "Replacement profile" {
		t.Fatalf("result=%+v, want one moved title and the replacement's name", result)
	}
	var onReplacement, deleted int
	var replacementVersion int64
	var roundCurrent bool
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workflows WHERE id=$1 AND quality_profile_id=$2),(SELECT count(*) FROM quality_profiles WHERE id=$3),profile.policy_version,
		(SELECT `+roundPolicyCurrentSQL("round", "profile")+` FROM acquisition_rounds round WHERE round.workflow_id=$1 AND round.state='open')
		FROM quality_profiles profile WHERE profile.id=$2`, workflowID.String(), replacement.ID.String(), fixture.profile.ID.String()).Scan(&onReplacement, &deleted, &replacementVersion, &roundCurrent); err != nil {
		t.Fatal(err)
	}
	if onReplacement != 1 || deleted != 0 {
		t.Fatalf("onReplacement=%d remaining=%d, want the workflow moved and the profile gone", onReplacement, deleted)
	}
	if replacementVersion <= int64(fixture.profile.PolicyVersion) || replacementVersion <= 1 {
		t.Fatalf("replacement policy_version=%d, want it above both profiles' versions", replacementVersion)
	}
	if roundCurrent {
		t.Fatal("a round frozen under the deleted profile's rules counts as current under the stricter replacement")
	}
}
