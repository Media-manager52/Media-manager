package acquisition

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/media-manager/media-manager/internal/postgres"
	"uuid"
)

// Plex's view of AltMount, as CheckPlexSeesAltMount reports it.
const (
	PlexAltMountOK         = "ok"         // Plex lists every folder Media Manager sees
	PlexAltMountMissing    = "missing"    // Plex lists the folder but not all of AltMount's files
	PlexAltMountUnreadable = "unreadable" // Plex could not list the folder at all
	PlexAltMountEmpty      = "empty"      // AltMount holds nothing yet, so nothing can be compared
	PlexAltMountUnknown    = "unknown"    // Media Manager cannot read AltMount itself
)

// PlexAltMountView says whether one Plex server can read AltMount's files at
// the path the library roots say it sees them.
type PlexAltMountView struct {
	PlexID   uuid.UUID `json:"plex_id"`
	PlexName string    `json:"plex_name"`
	Path     string    `json:"path"`
	State    string    `json:"state"`
	// Missing names up to five of AltMount's top folders that Plex does not
	// list.
	Missing []string `json:"missing,omitempty"`
}

// PlexFolderLister lists the folders a Plex server itself sees in path.
type PlexFolderLister func(ctx context.Context, plex postgres.Integration, path string) ([]string, error)

// PlexFolders lists folders through Plex's own folder browser, with the
// connection's saved token.
func PlexFolders(resolveSecret SecretResolver) PlexFolderLister {
	return func(ctx context.Context, integration postgres.Integration, folder string) ([]string, error) {
		client, err := PlexClient(integration, resolveSecret)
		if err != nil {
			return nil, err
		}
		return client.Browse(ctx, folder)
	}
}

// LocalFolders lists the folder names directly inside root.
func LocalFolders(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// CheckPlexSeesAltMount compares the top folders of AltMount's drive, as
// Media Manager reads them, with what each Plex server lists at the path its
// library roots name for AltMount's files. Plex with a dead copy of the drive
// (a bind without rslave after AltMount restarted) lists nothing there or
// fails, while Media Manager still sees the folders. Nothing is changed.
func CheckPlexSeesAltMount(ctx context.Context, integrations []postgres.Integration, roots []postgres.LibraryRoot, readLocal func(string) ([]string, error), list PlexFolderLister) []PlexAltMountView {
	localRoot := defaultAltMountRoot(integrations)
	if localRoot == "" {
		return []PlexAltMountView{}
	}
	plexByID := map[uuid.UUID]postgres.Integration{}
	for _, integration := range integrations {
		if integration.Kind == "plex" && integration.Enabled {
			plexByID[integration.ID] = integration
		}
	}
	type pair struct {
		plex uuid.UUID
		path string
	}
	seen := map[pair]bool{}
	pairs := []pair{}
	for _, root := range roots {
		if !root.Enabled || root.PlexInstanceID == nil || strings.TrimSpace(root.SourceVisibleRoot) == "" {
			continue
		}
		if _, ok := plexByID[*root.PlexInstanceID]; !ok {
			continue
		}
		key := pair{*root.PlexInstanceID, root.SourceVisibleRoot}
		if !seen[key] {
			seen[key] = true
			pairs = append(pairs, key)
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].plex != pairs[j].plex {
			return pairs[i].plex.String() < pairs[j].plex.String()
		}
		return pairs[i].path < pairs[j].path
	})
	views := make([]PlexAltMountView, 0, len(pairs))
	if len(pairs) == 0 {
		return views
	}
	local, localErr := readLocal(localRoot)
	for _, item := range pairs {
		plex := plexByID[item.plex]
		view := PlexAltMountView{PlexID: plex.ID, PlexName: plex.Name, Path: item.path}
		switch {
		case localErr != nil:
			view.State = PlexAltMountUnknown
		case len(local) == 0:
			view.State = PlexAltMountEmpty
		default:
			folders, err := list(ctx, plex, item.path)
			if err != nil {
				view.State = PlexAltMountUnreadable
				break
			}
			listed := map[string]bool{}
			for _, folder := range folders {
				listed[path.Base(strings.TrimRight(strings.ReplaceAll(folder, "\\", "/"), "/"))] = true
			}
			missing := []string{}
			for _, name := range local {
				if !listed[name] {
					missing = append(missing, name)
				}
			}
			sort.Strings(missing)
			if len(missing) == 0 {
				view.State = PlexAltMountOK
				break
			}
			view.State = PlexAltMountMissing
			if len(missing) > 5 {
				missing = missing[:5]
			}
			view.Missing = missing
		}
		views = append(views, view)
	}
	return views
}

// defaultAltMountRoot is the read-only root of the AltMount connection
// downloads go through: the enabled default, or the only enabled one.
func defaultAltMountRoot(integrations []postgres.Integration) string {
	var chosen []postgres.Integration
	for _, integration := range integrations {
		if integration.Kind == "altmount" && integration.Enabled {
			if integration.Default {
				chosen = []postgres.Integration{integration}
				break
			}
			chosen = append(chosen, integration)
		}
	}
	if len(chosen) != 1 {
		return ""
	}
	var config struct {
		LocalReadOnlyRoot string `json:"local_read_only_root"`
	}
	if err := json.Unmarshal(chosen[0].Config, &config); err != nil {
		return ""
	}
	return strings.TrimSpace(config.LocalReadOnlyRoot)
}
