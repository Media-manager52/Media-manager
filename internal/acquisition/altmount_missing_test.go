package acquisition

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"time"
	"uuid"
)

// fakeMissingSweep is a missing-file sweep over fake publications, a fake
// mount and a recording planner.
type fakeMissingSweep struct {
	service  *AltMountRepairService
	present  map[string]bool
	mountErr error
	planned  []string
}

func newFakeMissingSweep(publications ...string) *fakeMissingSweep {
	fake := &fakeMissingSweep{present: map[string]bool{}}
	for _, path := range publications {
		fake.present[path] = true
	}
	fake.service = &AltMountRepairService{
		CheckMount: func(string) error { return fake.mountErr },
		StatFile: func(path string) error {
			if fake.present[path[len("/mnt/altmount/"):]] {
				return nil
			}
			return fs.ErrNotExist
		},
		ListPublications: func(context.Context, uuid.UUID) ([]string, error) {
			return append([]string(nil), publications...), nil
		},
		PlanMissing: func(_ context.Context, _ uuid.UUID, path string, _ time.Time) (string, error) {
			fake.planned = append(fake.planned, path)
			return "opened", nil
		},
	}
	return fake
}

func publicationPaths(count int) []string {
	paths := make([]string, count)
	for index := range paths {
		paths[index] = "media-manager/release-" + string(rune('a'+index%26)) + "-" + time.Duration(index).String() + "/Episode.mkv"
	}
	return paths
}

// TestMissingFileSweepRepairsAfterTwoSweeps proves a publication whose
// AltMount file is gone is repaired only when it is missing on two sweeps in
// a row: a file that comes back in between is left alone.
func TestMissingFileSweepRepairsAfterTwoSweeps(t *testing.T) {
	paths := publicationPaths(200)
	fake := newFakeMissingSweep(paths...)
	ctx, altMount := context.Background(), uuid.NewV7()
	gone, flapping := paths[3], paths[7]
	fake.present[gone], fake.present[flapping] = false, false

	counts, err := fake.service.missingFileSweep(ctx, altMount, "/mnt/altmount")
	if err != nil || counts["missing_once"] != 2 || len(fake.planned) != 0 {
		t.Fatalf("first sweep counts=%v planned=%v err=%v, want two seen once and nothing planned", counts, fake.planned, err)
	}
	fake.present[flapping] = true
	counts, err = fake.service.missingFileSweep(ctx, altMount, "/mnt/altmount")
	if err != nil || counts["opened"] != 1 || len(fake.planned) != 1 || fake.planned[0] != gone {
		t.Fatalf("second sweep counts=%v planned=%v err=%v, want only %q repaired", counts, fake.planned, err, gone)
	}
	// Another AltMount client keeps its own history.
	if counts, _ := fake.service.missingFileSweep(ctx, uuid.NewV7(), "/mnt/altmount"); counts["missing_once"] != 1 || len(fake.planned) != 1 {
		t.Fatalf("other client counts=%v planned=%v, want the file seen once there", counts, fake.planned)
	}
}

// TestMissingFileSweepRepairsNothingDuringAnOutage proves a mount that is
// down, or one where many files vanish at once, repairs nothing and starts
// the two-sweep confirmation over.
func TestMissingFileSweepRepairsNothingDuringAnOutage(t *testing.T) {
	paths := publicationPaths(200)
	fake := newFakeMissingSweep(paths...)
	ctx, altMount := context.Background(), uuid.NewV7()
	fake.present[paths[0]] = false
	if _, err := fake.service.missingFileSweep(ctx, altMount, "/mnt/altmount"); err != nil {
		t.Fatal(err)
	}

	fake.mountErr = errors.New("transport endpoint is not connected")
	counts, err := fake.service.missingFileSweep(ctx, altMount, "/mnt/altmount")
	if err != nil || counts["mount_unavailable"] != 1 || len(fake.planned) != 0 {
		t.Fatalf("mount down counts=%v planned=%v err=%v", counts, fake.planned, err)
	}
	fake.mountErr = nil
	if counts, _ := fake.service.missingFileSweep(ctx, altMount, "/mnt/altmount"); counts["missing_once"] != 1 || len(fake.planned) != 0 {
		t.Fatalf("after the outage counts=%v planned=%v, want confirmation to start over", counts, fake.planned)
	}

	for _, path := range paths[:30] {
		fake.present[path] = false
	}
	counts, err = fake.service.missingFileSweep(ctx, altMount, "/mnt/altmount")
	if err != nil || counts["too_many_missing"] != 30 || len(fake.planned) != 0 {
		t.Fatalf("mass vanish counts=%v planned=%v err=%v, want nothing repaired", counts, fake.planned, err)
	}
	if counts, _ := fake.service.missingFileSweep(ctx, altMount, "/mnt/altmount"); len(fake.planned) != 0 || counts["too_many_missing"] != 30 {
		t.Fatalf("still mass missing counts=%v planned=%v", counts, fake.planned)
	}
}

// TestMissingFileSweepRepairsDozensInALargeLibrary proves the outage guard
// needs both limits: 33 missing files out of 5,162 publications (what
// production had after AltMount moved a batch of corrupted files aside) are
// repaired, not mistaken for an outage.
func TestMissingFileSweepRepairsDozensInALargeLibrary(t *testing.T) {
	paths := publicationPaths(5162)
	fake := newFakeMissingSweep(paths...)
	ctx, altMount := context.Background(), uuid.NewV7()
	for _, path := range paths[:33] {
		fake.present[path] = false
	}
	if counts, err := fake.service.missingFileSweep(ctx, altMount, "/mnt/altmount"); err != nil || counts["missing_once"] != 33 {
		t.Fatalf("first sweep counts=%v err=%v, want 33 seen once", counts, err)
	}
	if counts, err := fake.service.missingFileSweep(ctx, altMount, "/mnt/altmount"); err != nil || counts["opened"] != 33 || len(fake.planned) != 33 {
		t.Fatalf("second sweep counts=%v planned=%d err=%v, want 33 repairs", counts, len(fake.planned), err)
	}
}

// TestMissingFileSweepStopsOnAnUnresponsiveFile proves a file that does not
// answer stops the sweep instead of counting as missing.
func TestMissingFileSweepStopsOnAnUnresponsiveFile(t *testing.T) {
	paths := publicationPaths(10)
	fake := newFakeMissingSweep(paths...)
	fake.service.StatFile = func(string) error { return errStatTimeout }
	counts, err := fake.service.missingFileSweep(context.Background(), uuid.NewV7(), "/mnt/altmount")
	if err != nil || counts["unresponsive"] != 1 || len(fake.planned) != 0 {
		t.Fatalf("counts=%v planned=%v err=%v", counts, fake.planned, err)
	}
	if counts, _ := fake.service.missingFileSweep(context.Background(), uuid.NewV7(), ""); len(counts) != 0 {
		t.Fatalf("no read-only root configured: counts=%v, want the sweep skipped", counts)
	}
}
