package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
)

func TestSeerrStatusContextAndBootstrapAreExactAndIdempotent(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := subtitleIntegrationStore(t, ctx, databaseURL)
	defer store.Close()

	seerr, err := store.CreateIntegration(ctx, Integration{Kind: "seerr", Name: "Seerr", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://seerr:5055","allow_http":true}`), SecretRef: "SEERR_API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	altmount, err := store.CreateIntegration(ctx, Integration{Kind: "altmount", Name: "AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount:8080","allow_http":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	plex, err := store.CreateIntegration(ctx, Integration{Kind: "plex", Name: "Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex:32400","allow_http":true}`), SecretRef: "PLEX_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindSeries, Name: "TV", DestinationRoot: "/libraries/tv", PlexVisibleRoot: "/libraries/tv", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "2", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{{Name: "Required resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindSeries, Name: "TV quality", Policy: policy, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	mediaID := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO media_items(id,kind,title,year,revision,created_at,updated_at) VALUES($1,'series','Status Series',2026,1,$2,$2)`, mediaID.String(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO external_identities(id,media_id,media_kind,namespace,value,state,source,observed_at) VALUES($1,$2,'series','tmdb','123456','verified','test',$3)`, uuid.NewV7().String(), mediaID.String(), now); err != nil {
		t.Fatal(err)
	}

	requestID := "42"
	if _, err := store.Pool.Exec(ctx, `INSERT INTO seerr_request_snapshots(seerr_instance_id,request_id,external_updated_at,state,identities,payload_sha256,revision,reconciled_at) VALUES($1,$2,$3,'2','[]',$4,1,$3)`, seerr.ID.String(), requestID, now, []byte{1}); err != nil {
		t.Fatal(err)
	}
	for season := int16(1); season <= 3; season++ {
		if _, err := store.Pool.Exec(ctx, `INSERT INTO seerr_request_seasons(seerr_instance_id,request_id,season_number,availability_status,requested) VALUES($1,$2,$3,3,true)`, seerr.ID.String(), requestID, season); err != nil {
			t.Fatal(err)
		}
	}

	workflowIDs := make([]uuid.UUID, 3)
	variants := []string{"season:01", "season:02", "season:03"}
	for index := range workflowIDs {
		workflowIDs[index] = uuid.NewV7()
		state := "plex_confirmed"
		var terminal any = now
		if index == 2 {
			state, terminal = "needs_attention", nil
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO workflows(id,media_id,state,root_id,quality_profile_id,download_client_id,variant,revision,terminal_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,9,$8,$9,$9)`, workflowIDs[index].String(), mediaID.String(), state, root.ID.String(), profile.ID.String(), altmount.ID.String(), variants[index], terminal, now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO seerr_request_workflows(seerr_instance_id,request_id,workflow_id,attached_snapshot_revision,owns_workflow) VALUES($1,$2,$3,1,true)`, seerr.ID.String(), requestID, workflowIDs[index].String()); err != nil {
			t.Fatal(err)
		}
	}

	status, err := store.LoadSeerrStatusSyncContext(ctx, seerr.ID.String(), workflowIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if status.Kind != catalog.KindSeries || len(status.RequestIDs) != 1 || status.RequestIDs[0] != requestID || status.AllAvailable || len(status.AvailableSeasons) != 2 || status.AvailableSeasons[0] != 1 || status.AvailableSeasons[1] != 2 {
		t.Fatalf("unexpected partial Seerr status context: %+v", status)
	}
	if len(status.Identities) != 1 || status.Identities[0].Namespace != "tmdb" || status.Identities[0].Value != "123456" {
		t.Fatalf("status context omitted exact identity: %+v", status.Identities)
	}

	queued, err := store.QueueConfirmedSeerrStatusSyncs(ctx, seerr.ID.String(), now, 25)
	if err != nil || queued != 2 {
		t.Fatalf("first status bootstrap queued=%d err=%v", queued, err)
	}
	queued, err = store.QueueConfirmedSeerrStatusSyncs(ctx, seerr.ID.String(), now.Add(time.Second), 25)
	if err != nil || queued != 0 {
		t.Fatalf("idempotent status bootstrap queued=%d err=%v", queued, err)
	}
	var count int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type='seerr.status_sync_requested'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("status outbox count=%d err=%v", count, err)
	}
}
