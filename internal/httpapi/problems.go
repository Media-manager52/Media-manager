package httpapi

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/media-manager/media-manager/internal/postgres"
)

const (
	diskLowFreeBytes = int64(10) << 30
	diskLowPercent   = int64(5)
)

func (server *Server) listProblems(writer http.ResponseWriter, request *http.Request) {
	feed, err := server.problemFeed(request)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load the problem list")
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: feed})
}

func (server *Server) supportBundle(writer http.ResponseWriter, request *http.Request) {
	feed, err := server.problemFeed(request)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not build the support bundle")
		return
	}
	health, healthErr := server.store.SystemHealth(request.Context(), time.Now().UTC())
	if healthErr != nil {
		health = postgres.SystemHealthSnapshot{DatabaseState: "unavailable"}
	}
	var body strings.Builder
	fmt.Fprintf(&body, "Media Manager support bundle\n")
	fmt.Fprintf(&body, "Generated: %s\n", feed.CheckedAt.Format(time.RFC3339))
	fmt.Fprintf(&body, "Version: %s\nCommit: %s\n", server.config.Version, server.config.Commit)
	fmt.Fprintf(&body, "Schema: %d/%d\nDatabase: %s (%d ms)\n\n", health.SchemaVersion, health.ExpectedSchema, health.DatabaseState, health.DatabaseLatencyMS)
	fmt.Fprintf(&body, "Problems (%d)\n", len(feed.Problems))
	if len(feed.Problems) == 0 {
		fmt.Fprintf(&body, "- none\n")
	} else {
		for _, problem := range feed.Problems {
			fmt.Fprintf(&body, "- [%s/%s] %s x%d", problem.Level, problem.Area, problem.Title, problem.Count)
			if problem.Detail != "" {
				fmt.Fprintf(&body, ": %s", problem.Detail)
			}
			if problem.LatestAt != nil {
				fmt.Fprintf(&body, " (latest %s)", problem.LatestAt.Format(time.RFC3339))
			}
			fmt.Fprintf(&body, "\n")
		}
	}
	fmt.Fprintf(&body, "\nComponents\n")
	for _, component := range health.Components {
		fmt.Fprintf(&body, "- %s (%s): enabled=%t state=%s failures=%d outcome=%s\n", component.Name, component.Kind, component.Enabled, component.ConfigurationState, component.ConsecutiveFailure, component.LastOutcome)
	}
	backups, err := server.store.ListBackups(request.Context(), 5)
	if err == nil && len(backups) > 0 {
		fmt.Fprintf(&body, "\nRecent backups\n")
		for _, backup := range backups {
			fmt.Fprintf(&body, "- %s state=%s size=%s schema=%d offsite=%s\n", backup.ID, backup.State, humanBytes(backup.SizeBytes), valueOrZero(backup.SchemaVersion), valueOrNone(backup.OffsiteState))
		}
	}
	queue, err := server.store.QueueSnapshot(request.Context(), time.Now().UTC())
	if err == nil {
		fmt.Fprintf(&body, "\nWorker queue: due=%d scheduled=%d leased=%d retrying=%d completed_10m=%d\n", queue.Due, queue.Scheduled, queue.Leased, queue.Retrying, queue.CompletedLast10m)
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("Content-Disposition", `attachment; filename="media-manager-support.txt"`)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = writer.Write([]byte(redactSupportText(body.String())))
}

func (server *Server) problemFeed(request *http.Request) (postgres.ProblemFeed, error) {
	feed, err := server.store.Problems(request.Context(), time.Now().UTC())
	if err != nil {
		return postgres.ProblemFeed{}, err
	}
	feed.Problems = append(feed.Problems, server.diskProblems(request)...)
	sort.SliceStable(feed.Problems, func(i, j int) bool {
		left, right := feed.Problems[i], feed.Problems[j]
		if left.Level != right.Level {
			return left.Level == "error"
		}
		if left.Count != right.Count {
			return left.Count > right.Count
		}
		if left.Area != right.Area {
			return left.Area < right.Area
		}
		return left.Title < right.Title
	})
	return feed, nil
}

// diskProblems checks the backup directory and the managed library roots from
// inside the API container. A missing path is not a disk problem; other health
// checks report unavailable configuration.
func (server *Server) diskProblems(request *http.Request) []postgres.Problem {
	paths := map[string]string{}
	if server.config.BackupDirectory != "" {
		paths[server.config.BackupDirectory] = "Backup directory"
	}
	if roots, err := server.store.ListLibraryRoots(request.Context()); err == nil {
		for _, root := range roots {
			if root.Enabled && root.DestinationRoot != "" {
				paths[root.DestinationRoot] = root.Name
			}
		}
	}
	result := []postgres.Problem{}
	seenDevices := map[syscall.Fsid]bool{}
	for path, name := range paths {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(path, &stat); err != nil {
			continue
		}
		if seenDevices[stat.Fsid] {
			continue
		}
		seenDevices[stat.Fsid] = true
		total := uint64(stat.Blocks) * uint64(stat.Bsize)
		free := uint64(stat.Bavail) * uint64(stat.Bsize)
		if total == 0 {
			continue
		}
		if int64(free) < diskLowFreeBytes || int64(free*100/total) < diskLowPercent {
			now := time.Now().UTC()
			result = append(result, postgres.Problem{
				Code: "system.disk_low", Area: "system", Level: "warning",
				Title:  "Low disk space: " + name,
				Detail: fmt.Sprintf("%s has %s free of %s", path, humanBytesValue(int64(free)), humanBytesValue(int64(total))),
				Count:  1, LatestAt: &now, Href: "/system/backups",
			})
		}
	}
	return result
}

func humanBytes(value *int64) string {
	if value == nil {
		return "unknown"
	}
	return humanBytesValue(*value)
}

func humanBytesValue(size int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	return fmt.Sprintf("%d %s", size, units[unit])
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func valueOrNone(value string) string {
	if strings.TrimSpace(value) == "" {
		return "none"
	}
	return value
}

func redactSupportText(value string) string {
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		lower := strings.ToLower(line)
		for _, marker := range []string{"authorization:", "x-plex-token:", "api_key=", "apikey=", "token=", "secret=", "password=", "bearer "} {
			if position := strings.Index(lower, marker); position >= 0 {
				lines[index] = line[:position+len(marker)] + " [redacted]"
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}
