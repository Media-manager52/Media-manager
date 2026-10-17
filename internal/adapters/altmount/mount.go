package altmount

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrMountUnavailable means the read-only AltMount root itself cannot be read:
// the FUSE connection is gone ("socket not connected"), the read hangs, or the
// mount is missing and only the bare mount-point directory is visible. It is an
// infrastructure outage, never evidence about one release, so callers must
// wait for the mount instead of failing or excluding a workflow's release.
var ErrMountUnavailable = errors.New("AltMount read-only mount is unavailable")

const (
	mountProbeTimeout = 5 * time.Second
	mountProbeTTL     = 5 * time.Second
)

type mountProbe struct {
	checkedAt time.Time
	err       error
	inflight  chan struct{}
}

var (
	mountProbesMu sync.Mutex
	mountProbes   = map[string]*mountProbe{}
)

// CheckMount reports whether the AltMount read-only root can be listed. A
// healthy root always has entries once AltMount has completed any job, so an
// empty root means the FUSE mount is not attached at that path. Results are
// cached briefly and at most one probe per root runs at a time, because a
// read of a hung FUSE mount can block in the kernel indefinitely.
func CheckMount(root string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("%w: %q is not an absolute path", ErrMountUnavailable, root)
	}
	root = filepath.Clean(root)
	mountProbesMu.Lock()
	probe := mountProbes[root]
	if probe == nil {
		probe = &mountProbe{}
		mountProbes[root] = probe
	}
	if probe.inflight == nil && !probe.checkedAt.IsZero() && time.Since(probe.checkedAt) < mountProbeTTL {
		err := probe.err
		mountProbesMu.Unlock()
		return err
	}
	if probe.inflight == nil {
		done := make(chan struct{})
		probe.inflight = done
		go func() {
			err := listMountRoot(root)
			mountProbesMu.Lock()
			probe.err, probe.checkedAt, probe.inflight = err, time.Now(), nil
			mountProbesMu.Unlock()
			close(done)
		}()
	}
	inflight := probe.inflight
	mountProbesMu.Unlock()

	select {
	case <-inflight:
		mountProbesMu.Lock()
		defer mountProbesMu.Unlock()
		return probe.err
	case <-time.After(mountProbeTimeout):
		return fmt.Errorf("%w: listing %s did not finish within %s", ErrMountUnavailable, root, mountProbeTimeout)
	}
}

func listMountRoot(root string) error {
	directory, err := os.Open(root)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMountUnavailable, err)
	}
	defer directory.Close()
	entries, err := directory.ReadDir(1)
	if err != nil && len(entries) == 0 {
		return fmt.Errorf("%w: %s is empty or unreadable (%v); the mount is not attached", ErrMountUnavailable, root, err)
	}
	return nil
}
