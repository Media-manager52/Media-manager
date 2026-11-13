// Package altmountswitch moves an existing AltMount to Media Manager's build
// of AltMount through Cosmos, when the owner asks and pastes a Cosmos API
// token (never stored). Only the image changes: AltMount's settings,
// database, files and drive stay where they are, and Media Manager's build
// copies the database and settings before its first start. When the new
// AltMount does not come up, the old image is put back.
package altmountswitch

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/cosmos"
)

const (
	// Image is Media Manager's build of AltMount.
	Image = "ghcr.io/media-manager52/altmount:mm-stable"
	// BaseVersion is the AltMount release Image is built on. Its database
	// is the same, so an AltMount of this version or older can switch.
	BaseVersion = "0.4.0"
	// imageRepository names Media Manager's build in any tag.
	imageRepository = "media-manager52/altmount"
)

// Build kinds of a running AltMount.
const (
	KindMediaManager = "media_manager" // Media Manager's build
	KindStandard     = "standard"      // the normal AltMount release
	KindUnknown      = "unknown"       // a build without a release version
)

// Kind tells which build an AltMount version string belongs to.
func Kind(version string) string {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if strings.HasPrefix(version, "mm-") || strings.Contains(version, "-mm") {
		return KindMediaManager
	}
	if _, ok := release(version); ok {
		return KindStandard
	}
	return KindUnknown
}

// CanSwitch reports whether an AltMount with this version can move to
// Media Manager's build: a normal release no newer than BaseVersion.
func CanSwitch(version string) bool {
	if Kind(version) != KindStandard {
		return false
	}
	current, _ := release(strings.TrimPrefix(strings.TrimSpace(version), "v"))
	base, _ := release(BaseVersion)
	return compare(current, base) <= 0
}

// release reads "0.4.0" (also "0.4.0-amd64" or "0.4") as numbers.
func release(version string) ([3]int, bool) {
	var parts [3]int
	core, _, _ := strings.Cut(version, "-")
	core, _, _ = strings.Cut(core, "+")
	fields := strings.Split(core, ".")
	if len(fields) < 2 || len(fields) > 3 {
		return parts, false
	}
	for index, field := range fields {
		number, err := strconv.Atoi(field)
		if err != nil || number < 0 {
			return parts, false
		}
		parts[index] = number
	}
	return parts, true
}

func compare(a, b [3]int) int {
	for index := range a {
		if a[index] != b[index] {
			if a[index] < b[index] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Cosmos is what the switch needs from Cosmos.
type Cosmos interface {
	Containers(ctx context.Context) ([]cosmos.ContainerSummary, error)
	Container(ctx context.Context, name string) (cosmos.Container, error)
	UpdateImage(ctx context.Context, name, image string, mounts []cosmos.Mount, memory, nanoCPUs int64) error
}

// AltMount is what the switch needs from AltMount, through Media Manager's
// own AltMount connection.
type AltMount interface {
	Version(ctx context.Context) (string, error)
}

// Request says which AltMount to switch.
type Request struct {
	// Self is Media Manager's own container (its hostname).
	Self string
	// AltMountHost is the host of the AltMount connection's address.
	AltMountHost string
	// Container is the owner's choice when AltMount's container is not
	// found by its address.
	Container string
	Apply     bool
}

// Plan is what the switch does or did.
type Plan struct {
	Container  string   `json:"container,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	// CurrentImage and CurrentVersion describe AltMount before the switch.
	CurrentImage   string `json:"current_image,omitempty"`
	CurrentVersion string `json:"current_version,omitempty"`
	NewImage       string `json:"new_image"`
	BaseVersion    string `json:"base_version"`
	// AlreadyMediaManager means AltMount already is Media Manager's build.
	AlreadyMediaManager bool `json:"already_media_manager"`
	Applied             bool `json:"applied"`
	// NewVersion is what AltMount reports after the switch.
	NewVersion string `json:"new_version,omitempty"`
}

var (
	// ErrChooseAltMount means several containers look like AltMount; the
	// owner chooses one of Plan.Candidates.
	ErrChooseAltMount = errors.New("several containers look like AltMount; choose one")
	// ErrAltMountNotFound means no container in Cosmos is AltMount.
	ErrAltMountNotFound = errors.New("AltMount's container was not found in Cosmos")
	// ErrUnsupportedFolder means one of AltMount's folders cannot be kept by
	// Cosmos' update (an SELinux z/Z option); nothing is changed.
	ErrUnsupportedFolder = errors.New("one of AltMount's folders cannot be kept when Cosmos recreates it")
)

// VersionError means this AltMount cannot switch safely: it is newer than
// Media Manager's build (its database may be ahead) or reports no release.
type VersionError struct {
	Version string
	Newer   bool
}

func (err VersionError) Error() string {
	if err.Newer {
		return fmt.Sprintf("AltMount %s is newer than Media Manager's build (AltMount %s)", err.Version, BaseVersion)
	}
	return fmt.Sprintf("AltMount reports no release version (%q)", err.Version)
}

// RolledBackError means the new AltMount did not come up and the old image
// was put back (or could not be: RollbackErr).
type RolledBackError struct {
	Cause       error
	RollbackErr error
}

func (err RolledBackError) Error() string {
	if err.RollbackErr != nil {
		return fmt.Sprintf("Media Manager's AltMount did not start (%v), and putting the old image back failed: %v", err.Cause, err.RollbackErr)
	}
	return fmt.Sprintf("Media Manager's AltMount did not start (%v); the old AltMount was put back", err.Cause)
}

func (err RolledBackError) Unwrap() error { return err.Cause }

// Switcher runs one switch.
type Switcher struct {
	Cosmos Cosmos
	// AltMount logs in to AltMount anew; sessions end when it restarts.
	AltMount func(ctx context.Context) (AltMount, error)
	Sleep    func(ctx context.Context, wait time.Duration) error
	// StartTimeout is how long the new AltMount gets to answer.
	StartTimeout time.Duration
}

// Run finds AltMount's container, checks its version and, with Apply,
// switches its image and waits until Media Manager's build answers.
func (switcher Switcher) Run(ctx context.Context, request Request) (Plan, error) {
	plan := Plan{NewImage: Image, BaseVersion: BaseVersion}
	altMount, err := switcher.AltMount(ctx)
	if err != nil {
		return plan, err
	}
	version, err := altMount.Version(ctx)
	if err != nil {
		return plan, err
	}
	plan.CurrentVersion = version

	containers, err := switcher.Cosmos.Containers(ctx)
	if err != nil {
		return plan, err
	}
	name, candidates := cosmos.FindContainer(containers, request.Self, request.AltMountHost, request.Container, func(image string) bool {
		return strings.Contains(image, "altmount")
	})
	if name == "" {
		if len(candidates) > 1 {
			plan.Candidates = candidates
			return plan, ErrChooseAltMount
		}
		return plan, ErrAltMountNotFound
	}
	plan.Container = name
	container, err := switcher.Cosmos.Container(ctx, name)
	if err != nil {
		return plan, err
	}
	plan.CurrentImage = container.Config.Image
	if strings.Contains(strings.ToLower(container.Config.Image), imageRepository) || Kind(version) == KindMediaManager {
		plan.AlreadyMediaManager = true
		return plan, nil
	}
	if !CanSwitch(version) {
		return plan, VersionError{Version: version, Newer: Kind(version) == KindStandard}
	}
	// Every folder and volume is sent along: a volume the image made
	// itself (AltMount's /metadata when no folder was chosen for it) would
	// otherwise be replaced by an empty one when Cosmos recreates AltMount.
	mounts, err := container.MountList()
	if err != nil {
		return plan, fmt.Errorf("%w: %v", ErrUnsupportedFolder, err)
	}
	if !request.Apply {
		return plan, nil
	}

	memory, cpus := container.HostConfig.Memory, container.HostConfig.NanoCpus
	if err := switcher.Cosmos.UpdateImage(ctx, name, Image, mounts, memory, cpus); err != nil {
		return plan, err
	}
	newVersion, startErr := switcher.waitForAltMount(ctx, name)
	if startErr != nil {
		rollbackErr := switcher.Cosmos.UpdateImage(context.WithoutCancel(ctx), name, container.Config.Image, mounts, memory, cpus)
		return plan, RolledBackError{Cause: startErr, RollbackErr: rollbackErr}
	}
	plan.NewVersion = newVersion
	plan.Applied = true
	return plan, nil
}

// waitForAltMount waits until the recreated container runs and AltMount
// answers as Media Manager's build.
func (switcher Switcher) waitForAltMount(ctx context.Context, name string) (string, error) {
	sleep := switcher.Sleep
	if sleep == nil {
		sleep = func(ctx context.Context, wait time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
				return nil
			}
		}
	}
	timeout := switcher.StartTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if container, err := switcher.Cosmos.Container(ctx, name); err != nil {
			lastErr = err
		} else if !container.State.Running {
			lastErr = fmt.Errorf("the container is %s", container.State.Status)
		} else if client, err := switcher.AltMount(ctx); err != nil {
			lastErr = err
		} else if version, err := client.Version(ctx); err != nil {
			lastErr = err
		} else if Kind(version) != KindMediaManager {
			lastErr = fmt.Errorf("AltMount still reports %q", version)
		} else {
			return version, nil
		}
		if !time.Now().Before(deadline) {
			return "", lastErr
		}
		if err := sleep(ctx, 3*time.Second); err != nil {
			return "", err
		}
	}
}
