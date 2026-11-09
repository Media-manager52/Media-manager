package postgres

import (
	"os"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
)

func TestOwnerReplacementPinsTheSelectedPublicationAcrossClients(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	first, path := publishedAltMountMovie(t, fixture, "First Publication", "770010", "shared-source")
	other, err := fixture.store.CreateIntegration(fixture.ctx, Integration{Kind: "altmount", Name: "Other AltMount", Enabled: true, Config: fixture.altMount.Config})
	if err != nil {
		t.Fatal(err)
	}
	fixture.altMount = other
	// Reuse the exact synthetic source in this test's private temporary root.
	// The publication helper writes its five-byte fixture with mode 0444.
	source := filepath.Join(fixture.localRoot, "completed", "shared-source", "Movie.mkv")
	if err := os.Chmod(source, 0o644); err != nil {
		t.Fatal(err)
	}
	second, samePath := publishedAltMountMovie(t, fixture, "Second Publication", "770011", "shared-source")
	if err := os.Chmod(source, 0o444); err != nil {
		t.Fatal(err)
	}
	if samePath != path {
		t.Fatalf("fixture must exercise equal paths on different clients: %q != %q", path, samePath)
	}
	files, err := fixture.store.ListManagedFiles(fixture.ctx, 100)
	if err != nil || len(files) != 2 {
		t.Fatalf("files=%+v error=%v", files, err)
	}
	var selected uuid.UUID
	for _, file := range files {
		if file.MediaID == second.ID {
			selected = file.ID
		}
	}
	unknown := uuid.NewV7()
	for _, request := range []struct {
		path string
		id   uuid.UUID
	}{{path, unknown}, {path + "/wrong-file.mkv", selected}} {
		if outcome, err := fixture.store.RequestAltMountReplacementForFile(fixture.ctx, request.path, &request.id, time.Now().UTC()); err != nil || outcome != RepairUnmanaged {
			t.Fatalf("mismatched/stale publication: outcome=%s error=%v", outcome, err)
		}
	}
	if outcome, err := fixture.store.PlanMissingAltMountFile(fixture.ctx, unknown, path, time.Now().UTC()); err != nil || outcome != RepairUnmanaged {
		t.Fatalf("a different client must not repair this path: outcome=%s error=%v", outcome, err)
	}
	if outcome, err := fixture.store.RequestAltMountReplacementForFile(fixture.ctx, path, &selected, time.Now().UTC()); err != nil || outcome != RepairOpened {
		t.Fatalf("selected publication: outcome=%s error=%v", outcome, err)
	}
	var broken, media, client string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT broken_link_version_id::text,media_id::text,altmount_instance_id::text FROM altmount_repairs`).Scan(&broken, &media, &client); err != nil {
		t.Fatal(err)
	}
	if broken != selected.String() || media != second.ID.String() || client != other.ID.String() || media == first.ID.String() {
		t.Fatalf("wrong publication repaired: broken=%s media=%s client=%s", broken, media, client)
	}
	var current int
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT count(*) FROM library_link_versions WHERE state='current'`).Scan(&current); err != nil || current != 2 {
		t.Fatalf("both old publications must be retained: count=%d error=%v", current, err)
	}
	if outcome, err := fixture.store.RequestAltMountReplacementForFile(fixture.ctx, path, &selected, time.Now().UTC()); err != nil || outcome != RepairAlreadyOpen {
		t.Fatalf("repeat must be idempotent: outcome=%s error=%v", outcome, err)
	}
}
