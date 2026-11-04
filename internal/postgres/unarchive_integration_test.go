package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
)

// TestSeerrRequestAfterArchiveRevivesMedia proves that archived media comes
// back only for a Seerr request made after the archive: the older request
// keeps refusing, the newer one clears the archive mark, records who did it and
// starts a fresh workflow, while the workflow the archive cancelled keeps its
// history.
func TestSeerrRequestAfterArchiveRevivesMedia(t *testing.T) {
	ctx, store, seerrID, rootID := unarchiveFixture(t)
	year := uint16(2026)
	now := time.Now().UTC().Truncate(time.Microsecond)
	identities := []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "860508", State: catalog.IdentityVerified, Source: "seerr"}}
	intake := func(requestID string, requestedAt time.Time, payload byte) SeerrIntake {
		t.Helper()
		deliveryID := uuid.NewV7()
		if _, err := store.Pool.Exec(ctx, `INSERT INTO seerr_deliveries(id,seerr_instance_id,source,event_id,request_id,payload_sha256,payload,received_at) VALUES($1,$2,'webhook',$3,$4,$5,'{}',$6)`, deliveryID.String(), seerrID, "event-"+requestID+string(rune('a'+payload)), requestID, []byte{payload}, now); err != nil {
			t.Fatal(err)
		}
		value := SeerrIntake{InstanceID: seerrID, RequestID: requestID, UpdatedAt: now, RequestedAt: requestedAt, RequestState: "2", Kind: catalog.KindMovie, Title: "Lucky Strike", Year: &year, Identities: identities, Metadata: &catalog.MediaMetadata{}, PayloadHash: []byte{payload}, DeliveryID: deliveryID}
		if _, err := store.UpsertSeerrSnapshot(ctx, value); err != nil {
			t.Fatal(err)
		}
		return value
	}

	first := intake("100", now.Add(-48*time.Hour), 1)
	created, err := store.EnsureMediaFromSeerr(ctx, first, now.Add(-48*time.Hour))
	if err != nil || len(created.WorkflowIDs) != 1 {
		t.Fatalf("first intake=%+v err=%v", created, err)
	}
	archivedAt := now.Add(-24 * time.Hour)
	if _, err := store.ArchiveMedia(ctx, created.MediaID, archivedAt); err != nil {
		t.Fatal(err)
	}

	// The request the owner made before archiving does not undo the archive.
	if _, err := store.EnsureMediaFromSeerr(ctx, first, now); !errors.Is(err, ErrMediaRecycled) {
		t.Fatalf("pre-archive request err=%v, want ErrMediaRecycled", err)
	}
	assertArchived(t, ctx, store, created.MediaID, true)

	second := intake("200", now.Add(-time.Hour), 2)
	revived, err := store.EnsureMediaFromSeerr(ctx, second, now)
	if err != nil {
		t.Fatalf("post-archive request err=%v", err)
	}
	if revived.MediaID != created.MediaID || revived.Created || len(revived.WorkflowIDs) != 1 || revived.WorkflowIDs[0] == created.WorkflowIDs[0] {
		t.Fatalf("revived=%+v, want the same media with a new workflow", revived)
	}
	assertArchived(t, ctx, store, created.MediaID, false)
	var actor string
	var detail []byte
	if err := store.Pool.QueryRow(ctx, `SELECT actor,detail FROM audit_events WHERE action='media.unarchived' AND aggregate_id=$1`, created.MediaID.String()).Scan(&actor, &detail); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(detail, &fields); err != nil || actor != "seerr" || fields["request_id"] != "200" {
		t.Fatalf("unarchive audit actor=%q detail=%s err=%v", actor, detail, err)
	}
	var oldState string
	if err := store.Pool.QueryRow(ctx, `SELECT state FROM workflows WHERE id=$1`, created.WorkflowIDs[0].String()).Scan(&oldState); err != nil || oldState != "cancelled" {
		t.Fatalf("archived workflow state=%q err=%v, want it to stay cancelled", oldState, err)
	}
	var root string
	if err := store.Pool.QueryRow(ctx, `SELECT root_id::text FROM workflows WHERE id=$1`, revived.WorkflowIDs[0].String()).Scan(&root); err != nil || root != rootID {
		t.Fatalf("new workflow root=%q err=%v", root, err)
	}
}

// TestUnarchiveMediaOwnerAction proves the owner action: it clears the archive
// mark once, is idempotent, leaves cancelled workflows alone, and refuses media
// that is in the recycle bin.
func TestUnarchiveMediaOwnerAction(t *testing.T) {
	fixture := newFailureRecoveryFixture(t)
	workflow := fixture.newMovieWorkflow(t, "Unarchive Candidate", "900901")
	if _, err := fixture.store.ArchiveMedia(fixture.ctx, workflow.MediaID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	result, err := fixture.store.UnarchiveMedia(fixture.ctx, workflow.MediaID, time.Now().UTC())
	if err != nil || result.AlreadyActive || result.Title != "Unarchive Candidate" {
		t.Fatalf("unarchive=%+v err=%v", result, err)
	}
	assertArchived(t, fixture.ctx, fixture.store, workflow.MediaID, false)
	var state string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM workflows WHERE id=$1`, workflow.ID.String()).Scan(&state); err != nil || state != "cancelled" {
		t.Fatalf("workflow state=%q err=%v, want cancelled", state, err)
	}
	var audits int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM audit_events WHERE action='media.unarchived' AND actor='owner' AND aggregate_id=$1`, workflow.MediaID.String()).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("owner unarchive audits=%d err=%v", audits, err)
	}
	again, err := fixture.store.UnarchiveMedia(fixture.ctx, workflow.MediaID, time.Now().UTC())
	if err != nil || !again.AlreadyActive {
		t.Fatalf("second unarchive=%+v err=%v, want already active", again, err)
	}
	if _, err := fixture.store.UnarchiveMedia(fixture.ctx, uuid.NewV7(), time.Now().UTC()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown media err=%v, want ErrNotFound", err)
	}

	// Media in the recycle bin is restored from there, never by unarchive.
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE media_items SET deleted_at=now() WHERE id=$1`, workflow.MediaID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO recycle_entries(id,cycle_id,media_id,root_id,entry_kind,original_path,purge_after,state,confirmation_required,created_at) VALUES($1,$1,$2,$3,'link','/libraries/movies/x.mkv',now()+interval '30 days','retained',false,now())`, uuid.NewV7().String(), workflow.MediaID.String(), workflow.RootID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.UnarchiveMedia(fixture.ctx, workflow.MediaID, time.Now().UTC()); !errors.Is(err, ErrMediaInRecycleBin) {
		t.Fatalf("recycled media err=%v, want ErrMediaInRecycleBin", err)
	}
	assertArchived(t, fixture.ctx, fixture.store, workflow.MediaID, true)
}

func assertArchived(t *testing.T, ctx context.Context, store *Store, mediaID uuid.UUID, want bool) {
	t.Helper()
	var deleted *time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT deleted_at FROM media_items WHERE id=$1`, mediaID.String()).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if (deleted != nil) != want {
		t.Fatalf("media archived=%v, want %v", deleted != nil, want)
	}
}

func unarchiveFixture(t *testing.T) (context.Context, *Store, string, string) {
	t.Helper()
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	t.Cleanup(store.Close)
	plex, err := store.CreateIntegration(ctx, Integration{Kind: "plex", Name: "Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIntegration(ctx, Integration{Kind: "altmount", Name: "AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount:8080","allow_http":true}`)}); err != nil {
		t.Fatal(err)
	}
	seerr, err := store.CreateIntegration(ctx, Integration{Kind: "seerr", Name: "Seerr", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://seerr:5055","allow_http":true}`), SecretRef: "SEERR_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "INDEXER_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	dialects := newznab.SearchDialects{Movie: newznab.SearchDialect{Mode: "movie", Params: []string{"q", "imdbid"}}, Series: newznab.SearchDialect{Mode: "tvsearch", Params: []string{"q", "tvdbid", "season"}}}
	if _, err := store.CompleteNewznabCommission(ctx, indexer.ID, newznab.CapsSnapshot{ServerVersion: "test", BodyHash: "test", DefaultLimit: 100, MaximumLimit: 100}, dialects, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Movies", DestinationRoot: "/libraries/movies", PlexVisibleRoot: "/libraries/movies", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "1", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	rules := []releases.Rule{{Name: "Required resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}
	policy, _ := json.Marshal(map[string]any{"rules": rules})
	if _, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Movie", Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	return ctx, store, seerr.ID.String(), root.ID.String()
}
