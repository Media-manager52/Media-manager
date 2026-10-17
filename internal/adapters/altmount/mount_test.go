package altmount

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckMountAcceptsListableRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "media-manager"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckMount(root); err != nil {
		t.Fatalf("CheckMount = %v, want nil", err)
	}
}

// After a FUSE remount, a bind that follows propagation briefly shows the bare
// mount-point directory; an empty root must not look like a healthy mount.
func TestCheckMountRejectsEmptyRoot(t *testing.T) {
	if err := CheckMount(t.TempDir()); !errors.Is(err, ErrMountUnavailable) {
		t.Fatalf("CheckMount = %v, want ErrMountUnavailable", err)
	}
}

func TestCheckMountRejectsMissingOrRelativeRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	for _, root := range []string{missing, "relative/altmount"} {
		if err := CheckMount(root); !errors.Is(err, ErrMountUnavailable) {
			t.Fatalf("CheckMount(%q) = %v, want ErrMountUnavailable", root, err)
		}
	}
}
