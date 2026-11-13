// Package plexaccess gives Plex the folders Media Manager uses, through the
// Cosmos server both run on: AltMount's drive (with mount propagation, so
// Plex keeps it when AltMount restarts) and Media Manager's library folders,
// which are then added to Plex's libraries. It runs only when the owner asks
// and pastes a Cosmos API token, which is never stored.
package plexaccess

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/cosmos"
	"github.com/media-manager/media-manager/internal/adapters/plex"
)

// Cosmos is what the fix needs from Cosmos.
type Cosmos interface {
	Containers(ctx context.Context) ([]cosmos.ContainerSummary, error)
	Container(ctx context.Context, name string) (cosmos.Container, error)
	UpdateMounts(ctx context.Context, name string, mounts []cosmos.Mount, memory, nanoCPUs int64) error
}

// Plex is what the fix needs from Plex.
type Plex interface {
	Sections(ctx context.Context) ([]plex.Section, error)
	CreateSection(ctx context.Context, kind, name, language, folder string) error
	AddLocation(ctx context.Context, section plex.Section, folder string) error
}

// Purposes of a folder Plex must see.
const (
	PurposeAltMount = "altmount"
	PurposeMovies   = "movies"
	PurposeTV       = "tv"
)

// Need is one folder Plex must see, named by where Media Manager sees it.
type Need struct {
	Purpose string
	Path    string
}

// Request says what to fix.
type Request struct {
	// Self is Media Manager's own container (its hostname).
	Self string
	// PlexHost is the host of the Plex connection's address.
	PlexHost string
	// PlexContainer is the owner's choice when Plex's container is not
	// found by its address.
	PlexContainer string
	Needs         []Need
	Apply         bool
}

// Change kinds.
const (
	ChangeAddFolder        = "add_folder"       // Plex gets a folder it did not have
	ChangeSetPropagation   = "set_propagation"  // Plex's folder follows AltMount's restarts
	ChangeAddLibraryPath   = "add_library_path" // a Plex library gets Media Manager's folder
	ChangeCreateLibrary    = "create_library"   // a Plex library is created for the folder
	defaultLibraryLanguage = "da-DK"
)

// Change is one thing the fix does.
type Change struct {
	Kind    string `json:"kind"`
	Purpose string `json:"purpose"`
	// Path is where Plex sees the folder.
	Path    string `json:"path"`
	Library string `json:"library,omitempty"`
}

// Plan is what the fix does or did.
type Plan struct {
	PlexContainer string `json:"plex_container,omitempty"`
	// Candidates are Plex containers to choose from when the Plex
	// connection's address names none of them.
	Candidates []string `json:"candidates,omitempty"`
	Changes    []Change `json:"changes"`
	// PlexPaths is where Plex sees each folder after the fix.
	PlexPaths map[string]string `json:"plex_paths"`
	// Restart is true when Plex is recreated to get its folders.
	Restart bool `json:"restart"`
	Applied bool `json:"applied"`
}

// Fixer runs the fix.
type Fixer struct {
	Cosmos Cosmos
	// Plex opens the Plex connection; it is called only when libraries are
	// changed.
	Plex func(ctx context.Context) (Plex, error)
	// Sleep waits between checks; tests replace it.
	Sleep func(ctx context.Context, wait time.Duration) error
	// Timeouts for Plex to come back after it is recreated.
	HealthyTimeout time.Duration
	APITimeout     time.Duration
}

var (
	// ErrPlexNotFound means no container looks like Plex.
	ErrPlexNotFound = errors.New("no Plex container was found in Cosmos")
	// ErrChoosePlex means several containers look like Plex; Plan.Candidates
	// lists them.
	ErrChoosePlex = errors.New("several containers look like Plex; choose one")
)

// FolderError means a folder cannot be given to Plex automatically.
type FolderError struct{ Reason string }

func (err FolderError) Error() string { return err.Reason }

// Run plans the fix and, with Apply, carries it out.
func (fixer Fixer) Run(ctx context.Context, request Request) (Plan, error) {
	plan := Plan{Changes: []Change{}, PlexPaths: map[string]string{}}
	self, err := fixer.Cosmos.Container(ctx, request.Self)
	if err != nil {
		return plan, fmt.Errorf("Media Manager's own container: %w", err)
	}
	containers, err := fixer.Cosmos.Containers(ctx)
	if err != nil {
		return plan, err
	}
	name, candidates := findPlex(containers, self.ID, request.PlexHost, request.PlexContainer)
	if name == "" {
		plan.Candidates = candidates
		if len(candidates) > 0 {
			return plan, ErrChoosePlex
		}
		return plan, ErrPlexNotFound
	}
	plan.PlexContainer = name
	target, err := fixer.Cosmos.Container(ctx, name)
	if err != nil {
		return plan, err
	}
	mounts, err := target.MountList()
	if err != nil {
		return plan, err
	}
	mountsChanged := false
	for _, need := range request.Needs {
		plexPath, change, err := giveFolder(self, target, &mounts, need)
		if err != nil {
			return plan, err
		}
		plan.PlexPaths[need.Purpose] = plexPath
		if change != "" {
			plan.Changes = append(plan.Changes, Change{Kind: change, Purpose: need.Purpose, Path: plexPath})
			mountsChanged = true
		}
	}
	var plexClient Plex
	var sections []plex.Section
	libraryChanges := []Change{}
	for _, need := range request.Needs {
		kind := map[string]string{PurposeMovies: "movie", PurposeTV: "show"}[need.Purpose]
		if kind == "" {
			continue
		}
		if plexClient == nil {
			if plexClient, err = fixer.Plex(ctx); err != nil {
				return plan, err
			}
			if sections, err = plexClient.Sections(ctx); err != nil {
				return plan, err
			}
		}
		libraryChanges = append(libraryChanges, libraryChange(sections, kind, need.Purpose, plan.PlexPaths[need.Purpose]))
	}
	for _, change := range libraryChanges {
		if change.Kind != "" {
			plan.Changes = append(plan.Changes, change)
		}
	}
	plan.Restart = mountsChanged
	if !request.Apply || len(plan.Changes) == 0 {
		return plan, nil
	}
	if mountsChanged {
		if err := fixer.Cosmos.UpdateMounts(ctx, name, mounts, target.HostConfig.Memory, target.HostConfig.NanoCpus); err != nil {
			return plan, err
		}
		if err := fixer.waitForPlex(ctx, name, plexClient); err != nil {
			return plan, err
		}
	}
	for _, change := range libraryChanges {
		if err := fixer.applyLibrary(ctx, &plexClient, change); err != nil {
			return plan, err
		}
	}
	plan.Applied = true
	return plan, nil
}

// libraryChange adds folder to the first ordinary (non-4K) library of the
// kind, or creates a library for it. Its Kind is "" when nothing is needed.
func libraryChange(sections []plex.Section, kind, purpose, folder string) Change {
	for _, section := range sections {
		if section.Type != kind || strings.Contains(strings.ToLower(section.Title), "4k") {
			continue
		}
		for _, location := range section.Locations {
			if location.Path == folder {
				return Change{}
			}
		}
		return Change{Kind: ChangeAddLibraryPath, Purpose: purpose, Path: folder, Library: section.Title}
	}
	name := map[string]string{"movie": "Film", "show": "Serier"}[kind]
	return Change{Kind: ChangeCreateLibrary, Purpose: purpose, Path: folder, Library: name}
}

func (fixer Fixer) applyLibrary(ctx context.Context, client *Plex, change Change) error {
	if change.Kind == "" {
		return nil
	}
	if *client == nil {
		opened, err := fixer.Plex(ctx)
		if err != nil {
			return err
		}
		*client = opened
	}
	kind := map[string]string{PurposeMovies: "movie", PurposeTV: "show"}[change.Purpose]
	if change.Kind == ChangeCreateLibrary {
		return (*client).CreateSection(ctx, kind, change.Library, defaultLibraryLanguage, change.Path)
	}
	// Read the library again: it is changed with its current folders.
	sections, err := (*client).Sections(ctx)
	if err != nil {
		return err
	}
	for _, section := range sections {
		if section.Type == kind && section.Title == change.Library {
			return (*client).AddLocation(ctx, section, change.Path)
		}
	}
	return fmt.Errorf("the Plex library %q is gone", change.Library)
}

// waitForPlex waits until Plex's new container runs and its API answers.
func (fixer Fixer) waitForPlex(ctx context.Context, name string, client Plex) error {
	sleep := fixer.Sleep
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
	healthyTimeout, apiTimeout := fixer.HealthyTimeout, fixer.APITimeout
	if healthyTimeout <= 0 {
		healthyTimeout = 3 * time.Minute
	}
	if apiTimeout <= 0 {
		apiTimeout = 2 * time.Minute
	}
	deadline := time.Now().Add(healthyTimeout)
	for {
		container, err := fixer.Cosmos.Container(ctx, name)
		if err == nil && container.Healthy() {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("Plex did not start again within " + healthyTimeout.String() + "; look at it in Cosmos")
		}
		if err := sleep(ctx, 3*time.Second); err != nil {
			return err
		}
	}
	if client == nil {
		return nil
	}
	deadline = time.Now().Add(apiTimeout)
	for {
		if _, err := client.Sections(ctx); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("Plex runs again but does not answer yet; try again in a minute")
		}
		if err := sleep(ctx, 3*time.Second); err != nil {
			return err
		}
	}
}

// giveFolder makes sure Plex sees need's folder. It returns where Plex sees
// it and the change made to mounts ("" when Plex already sees it right).
func giveFolder(self, target cosmos.Container, mounts *[]cosmos.Mount, need Need) (string, string, error) {
	own, ok := covering(self.Mounts, need.Path)
	if !ok {
		return "", "", FolderError{Reason: "Media Manager's folder " + need.Path + " is not a folder from the server, so Plex cannot be given it"}
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(path.Clean(need.Path), path.Clean(own.Destination)), "/")
	// Plex already has the folder (or a folder that holds it)?
	for _, point := range target.Mounts {
		if point.Type != own.Type {
			continue
		}
		var inside string
		var found bool
		switch own.Type {
		case "bind":
			// AltMount's drive is mounted inside Media Manager's folder; Plex
			// must have that whole folder (or one holding it) to follow the
			// drive when AltMount mounts it again.
			source := path.Join(own.Source, rest)
			if need.Purpose == PurposeAltMount {
				source = own.Source
			}
			inside, found = relative(point.Source, source)
			if found && need.Purpose == PurposeAltMount {
				inside = path.Join(inside, rest)
			}
		case "volume":
			found = point.Name != "" && point.Name == own.Name
			inside = rest
		}
		if !found {
			continue
		}
		plexPath := path.Join(point.Destination, inside)
		if need.Purpose == PurposeAltMount && own.Type == "bind" && !follows(point.Propagation) {
			for index := range *mounts {
				if path.Clean((*mounts)[index].Target) == path.Clean(point.Destination) {
					(*mounts)[index].SetPropagation("rslave")
					return plexPath, ChangeSetPropagation, nil
				}
			}
			return "", "", FolderError{Reason: "Plex's folder " + point.Destination + " could not be changed"}
		}
		return plexPath, "", nil
	}
	// Plex gets the same folder at the place Media Manager has it, or below
	// /media-manager when Plex uses that place for something else.
	for _, candidate := range []string{own.Destination, path.Join("/media-manager", own.Destination)} {
		if overlaps(*mounts, candidate) {
			continue
		}
		mount := cosmos.Mount{Type: own.Type, Source: own.Source, Target: candidate, ReadOnly: true}
		if own.Type == "volume" {
			mount.Source = own.Name
		}
		if need.Purpose == PurposeAltMount && own.Type == "bind" {
			mount.SetPropagation("rslave")
		}
		*mounts = append(*mounts, mount)
		return path.Join(candidate, rest), ChangeAddFolder, nil
	}
	return "", "", FolderError{Reason: "Plex already uses " + own.Destination + " for something else"}
}

// covering is the mount that holds folder, the deepest one.
func covering(points []cosmos.MountPoint, folder string) (cosmos.MountPoint, bool) {
	var best cosmos.MountPoint
	found := false
	for _, point := range points {
		if point.Type != "bind" && point.Type != "volume" {
			continue
		}
		if _, ok := relative(point.Destination, folder); ok && (!found || len(point.Destination) > len(best.Destination)) {
			best, found = point, true
		}
	}
	return best, found
}

// relative is child's path below parent.
func relative(parent, child string) (string, bool) {
	parent, child = path.Clean(parent), path.Clean(child)
	if parent == child {
		return "", true
	}
	if parent == "/" {
		return strings.TrimPrefix(child, "/"), true
	}
	if strings.HasPrefix(child, parent+"/") {
		return strings.TrimPrefix(child, parent+"/"), true
	}
	return "", false
}

// overlaps reports whether a new mount at target would cover or sit inside
// one Plex already has.
func overlaps(mounts []cosmos.Mount, target string) bool {
	for _, mount := range mounts {
		if _, inside := relative(mount.Target, target); inside {
			return true
		}
		if _, holds := relative(target, mount.Target); holds {
			return true
		}
	}
	return false
}

func follows(propagation string) bool {
	switch propagation {
	case "rslave", "rshared", "slave", "shared":
		return true
	}
	return false
}

// findPlex picks Plex's container: the one the Plex connection's address
// names (container name, network alias or address), else the only container
// whose image is Plex. With several, it returns them to choose from.
func findPlex(containers []cosmos.ContainerSummary, selfID, host, chosen string) (string, []string) {
	host = strings.ToLower(strings.TrimSpace(host))
	if ip := plexDirectAddress(host); ip != "" {
		host = ip
	}
	var images []string
	for _, container := range containers {
		name := container.Name()
		if name == "" || (selfID != "" && strings.HasPrefix(container.ID, selfID)) {
			continue
		}
		if chosen != "" {
			if name == chosen {
				return name, nil
			}
			continue
		}
		if strings.EqualFold(name, host) {
			return name, nil
		}
		for _, network := range container.NetworkSettings.Networks {
			if host != "" && network.IPAddress == host {
				return name, nil
			}
			for _, alias := range append(append([]string{}, network.Aliases...), network.DNSNames...) {
				if strings.EqualFold(alias, host) {
					return name, nil
				}
			}
		}
		image := strings.ToLower(container.Image)
		if strings.Contains(image, "plex") && !strings.Contains(image, "meta-manager") && !strings.Contains(image, "kometa") {
			images = append(images, name)
		}
	}
	if chosen == "" && len(images) == 1 {
		return images[0], nil
	}
	return "", images
}

// plexDirectAddress reads the address out of a plex.direct name such as
// "172-18-0-6.<hash>.plex.direct".
func plexDirectAddress(host string) string {
	if !strings.HasSuffix(host, ".plex.direct") {
		return ""
	}
	first, _, _ := strings.Cut(host, ".")
	ip := net.ParseIP(strings.ReplaceAll(first, "-", "."))
	if ip == nil {
		return ""
	}
	return ip.String()
}
