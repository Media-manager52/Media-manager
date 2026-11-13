package httpapi

import (
	"context"
	"net/http"
	"path"
	"regexp"
	"sync"
	"time"

	"github.com/media-manager/media-manager/internal/acquisition"
	"github.com/media-manager/media-manager/internal/postgres"
	"uuid"
)

// altMountStatus is what Indstillinger and System show about AltMount without
// opening AltMount's own website: its drive, its news servers with their
// connections and quota, and what is being played from it now.
type altMountStatus struct {
	AltMount struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	} `json:"altmount"`
	Drive                    *acquisition.AltMountDrive `json:"drive,omitempty"`
	DownloadSpeedBytesPerSec float64                    `json:"download_speed_bytes_per_sec"`
	BytesDownloaded24h       int64                      `json:"bytes_downloaded_24h"`
	Servers                  []altMountServerView       `json:"servers"`
	Streams                  []altMountStreamView       `json:"streams"`
	// Unavailable names the parts AltMount did not answer for: drive,
	// servers or streams. The other parts are still shown.
	Unavailable []string  `json:"unavailable"`
	CheckedAt   time.Time `json:"checked_at"`
}

// altMountServerView is one configured news server, in the order AltMount
// tries them, with its live pool status when it is in use.
type altMountServerView struct {
	ID       string                            `json:"id"`
	Name     string                            `json:"name,omitempty"`
	Host     string                            `json:"host"`
	Enabled  bool                              `json:"enabled"`
	Backup   bool                              `json:"is_backup_provider"`
	Live     *acquisition.AltMountServerStatus `json:"live,omitempty"`
	QuotaSet bool                              `json:"quota_set"`
}

// altMountStreamView is one file being read from the drive, with the title
// it was downloaded for when Media Manager made the upload.
type altMountStreamView struct {
	File           string                     `json:"file"`
	Title          *postgres.AltMountJobTitle `json:"title,omitempty"`
	Source         string                     `json:"source"`
	Status         string                     `json:"status"`
	StartedAt      time.Time                  `json:"started_at"`
	LastActivity   time.Time                  `json:"last_activity"`
	TotalSize      int64                      `json:"total_size"`
	CurrentOffset  int64                      `json:"current_offset"`
	BytesPerSecond int64                      `json:"bytes_per_second"`
}

var uploadJobPattern = regexp.MustCompile(`--mm-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)

func (server *Server) getAltMountStatus(writer http.ResponseWriter, request *http.Request) {
	integration, client, ok := server.usenetAltMount(writer, request)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	status := altMountStatus{Servers: []altMountServerView{}, Streams: []altMountStreamView{}, Unavailable: []string{}, CheckedAt: time.Now().UTC()}
	status.AltMount.ID, status.AltMount.Name = integration.ID, integration.Name

	if drive, err := client.Drive(ctx); err == nil {
		status.Drive = &drive
	} else {
		status.Unavailable = append(status.Unavailable, "drive")
	}

	configured, configErr := client.ListProviders(ctx)
	pool, poolErr := client.Pool(ctx)
	if configErr != nil || poolErr != nil {
		status.Unavailable = append(status.Unavailable, "servers")
	}
	live := map[string]acquisition.AltMountServerStatus{}
	if poolErr == nil {
		status.DownloadSpeedBytesPerSec, status.BytesDownloaded24h = pool.DownloadSpeedBytesPerSec, pool.BytesDownloaded24h
		for _, item := range pool.Servers {
			live[item.ID] = item
		}
	}
	for _, provider := range configured {
		view := altMountServerView{ID: provider.ID, Name: provider.Name, Host: provider.Host, Enabled: provider.Enabled, Backup: provider.IsBackupProvider, QuotaSet: provider.QuotaBytes > 0}
		if current, ok := live[provider.ID]; ok {
			view.Live = &current
		}
		status.Servers = append(status.Servers, view)
	}

	streams, streamErr := client.Streams(ctx)
	if streamErr != nil {
		status.Unavailable = append(status.Unavailable, "streams")
	}
	jobIDs := []uuid.UUID{}
	jobOf := make([]*uuid.UUID, len(streams))
	for index, stream := range streams {
		if match := uploadJobPattern.FindStringSubmatch(stream.FilePath); match != nil {
			if id, err := uuid.Parse(match[1]); err == nil {
				jobIDs = append(jobIDs, id)
				jobOf[index] = &id
			}
		}
	}
	titles, err := server.store.AltMountJobTitles(ctx, jobIDs)
	if err != nil {
		titles = nil
	}
	for index, stream := range streams {
		view := altMountStreamView{File: path.Base(stream.FilePath), Source: stream.Source, Status: stream.Status, StartedAt: stream.StartedAt, LastActivity: stream.LastActivity, TotalSize: stream.TotalSize, CurrentOffset: stream.CurrentOffset, BytesPerSecond: stream.BytesPerSecond}
		if jobOf[index] != nil {
			if title, ok := titles[*jobOf[index]]; ok {
				view.Title = &title
			}
		}
		status.Streams = append(status.Streams, view)
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: status})
}

// plexAltMountCheck keeps the newest "does Plex see AltMount" result. The
// check asks Plex to list folders, so it runs at most every two minutes, in
// the background; a page load waits briefly for a running check and
// otherwise shows the previous result.
type plexAltMountCheck struct {
	mu        sync.Mutex
	views     []acquisition.PlexAltMountView
	checkedAt time.Time
	inflight  chan struct{}
	// Replaced in tests.
	listPlex  acquisition.PlexFolderLister
	listLocal func(string) ([]string, error)
	recheck   time.Duration
}

const plexAltMountTTL = 2 * time.Minute

func (server *Server) plexAltMountViews(wait time.Duration) ([]acquisition.PlexAltMountView, time.Time) {
	check := server.plexCheck
	check.mu.Lock()
	if check.inflight == nil && (check.checkedAt.IsZero() || time.Since(check.checkedAt) >= plexAltMountTTL) {
		done := make(chan struct{})
		check.inflight = done
		go func() {
			views, ok := server.runPlexAltMountCheck()
			check.mu.Lock()
			if ok {
				check.views = views
			}
			check.checkedAt, check.inflight = time.Now().UTC(), nil
			check.mu.Unlock()
			close(done)
		}()
	}
	inflight := check.inflight
	check.mu.Unlock()
	if inflight != nil {
		select {
		case <-inflight:
		case <-time.After(wait):
		}
	}
	check.mu.Lock()
	defer check.mu.Unlock()
	views := append([]acquisition.PlexAltMountView{}, check.views...)
	return views, check.checkedAt
}

// runPlexAltMountCheck compares once and, when Plex looks wrong, once more a
// little later, so a folder AltMount created between the two listings does
// not raise a warning.
func (server *Server) runPlexAltMountCheck() ([]acquisition.PlexAltMountView, bool) {
	check := server.plexCheck
	run := func() ([]acquisition.PlexAltMountView, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		integrations, err := server.store.ListIntegrations(ctx)
		if err != nil {
			return nil, false
		}
		roots, err := server.store.ListLibraryRoots(ctx)
		if err != nil {
			return nil, false
		}
		return acquisition.CheckPlexSeesAltMount(ctx, integrations, roots, check.listLocal, check.listPlex), true
	}
	views, ok := run()
	if !ok || !plexAltMountTrouble(views) {
		return views, ok
	}
	time.Sleep(check.recheck)
	return run()
}

// forgetPlexAltMount makes the next page load check Plex again, after Plex
// was changed.
func (server *Server) forgetPlexAltMount() {
	check := server.plexCheck
	check.mu.Lock()
	defer check.mu.Unlock()
	if check.inflight == nil {
		check.checkedAt = time.Time{}
	}
}

func plexAltMountTrouble(views []acquisition.PlexAltMountView) bool {
	for _, view := range views {
		if view.State == acquisition.PlexAltMountMissing || view.State == acquisition.PlexAltMountUnreadable {
			return true
		}
	}
	return false
}
