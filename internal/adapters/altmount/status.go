package altmount

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// DriveStatus is AltMount's own view of its FUSE drive.
type DriveStatus struct {
	Status  string `json:"status"`
	Healthy bool   `json:"healthy"`
	Path    string `json:"path"`
}

// ServerStatus is one news server as AltMount's connection pool sees it now.
type ServerStatus struct {
	ID                      string     `json:"id"`
	Host                    string     `json:"host"`
	State                   string     `json:"state"`
	UsedConnections         int        `json:"used_connections"`
	MaxConnections          int        `json:"max_connections"`
	ErrorCount              int64      `json:"error_count"`
	FailureReason           string     `json:"failure_reason"`
	CurrentSpeedBytesPerSec float64    `json:"current_speed_bytes_per_sec"`
	PingMs                  int64      `json:"ping_ms"`
	MissingWarning          bool       `json:"missing_warning"`
	ByteCount24h            int64      `json:"byte_count_24h"`
	QuotaBytes              int64      `json:"quota_bytes"`
	QuotaUsed               int64      `json:"quota_used"`
	QuotaResetAt            *time.Time `json:"quota_reset_at,omitempty"`
	QuotaExceeded           bool       `json:"quota_exceeded"`
}

// PoolStatus is AltMount's download pool: totals and every server.
type PoolStatus struct {
	BytesDownloaded24h       int64          `json:"bytes_downloaded_24h"`
	DownloadSpeedBytesPerSec float64        `json:"download_speed_bytes_per_sec"`
	Servers                  []ServerStatus `json:"providers"`
}

// Stream is one file being read from AltMount's drive right now. Only the
// fields Media Manager shows are decoded; the viewer's address and player
// are left out.
type Stream struct {
	FilePath       string    `json:"file_path"`
	StartedAt      time.Time `json:"started_at"`
	LastActivity   time.Time `json:"last_activity"`
	Source         string    `json:"source"`
	TotalSize      int64     `json:"total_size"`
	CurrentOffset  int64     `json:"current_offset"`
	BytesPerSecond int64     `json:"bytes_per_second"`
	Status         string    `json:"status"`
}

// Drive reads the FUSE drive status.
func (client *HTTPClient) Drive(ctx context.Context) (DriveStatus, error) {
	var status DriveStatus
	err := client.json(ctx, http.MethodGet, "/api/fuse/status", nil, &status)
	return status, err
}

// Pool reads the download pool and its servers.
func (client *HTTPClient) Pool(ctx context.Context) (PoolStatus, error) {
	var pool PoolStatus
	if err := client.json(ctx, http.MethodGet, "/api/system/pool/metrics", nil, &pool); err != nil {
		return PoolStatus{}, err
	}
	if pool.Servers == nil {
		pool.Servers = []ServerStatus{}
	}
	for index := range pool.Servers {
		if reset := pool.Servers[index].QuotaResetAt; reset != nil && reset.IsZero() {
			pool.Servers[index].QuotaResetAt = nil
		}
	}
	return pool, nil
}

// Streams lists the files being read from the drive now.
func (client *HTTPClient) Streams(ctx context.Context) ([]Stream, error) {
	var streams []Stream
	if err := client.json(ctx, http.MethodGet, "/api/files/active-streams", nil, &streams); err != nil {
		return nil, err
	}
	if streams == nil {
		streams = []Stream{}
	}
	return streams, nil
}

// Version is the version AltMount reports, such as "v0.4.0", or
// "v0.4.0-mm.3" for Media Manager's build. A build without a version says
// "dev".
func (client *HTTPClient) Version(ctx context.Context) (string, error) {
	var stats struct {
		System struct {
			Version string `json:"version"`
		} `json:"system"`
	}
	if err := client.json(ctx, http.MethodGet, "/api/system/stats", nil, &stats); err != nil {
		return "", err
	}
	return strings.TrimSpace(stats.System.Version), nil
}
