package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/backups"
	"github.com/media-manager/media-manager/internal/postgres"
)

func (server *Server) listBackups(writer http.ResponseWriter, request *http.Request) {
	items, err := server.store.ListBackups(request.Context(), 100)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load backups")
		return
	}
	markDeletedBackupFiles(server.config.BackupDirectory, items)
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]any{
		"configured":           server.config.BackupConfigured,
		"key_file":             server.config.BackupKeyFile,
		"format":               "PostgreSQL custom format encrypted with chunked AES-256-GCM",
		"restore_verification": "isolated temporary database and exact schema version",
		"automatic_deletion":   false,
		"production_restore":   false,
		"offsite": map[string]any{
			"configured": server.config.BackupOffsite != nil,
			"kind":       server.config.BackupOffsiteKind,
			"prefix":     server.config.BackupOffsitePrefix,
		},
		"records": items,
	}})
}

// markDeletedBackupFiles flags verified backups whose local file is gone, so a
// removed file is never presented as a usable backup. Only a confirmed absence
// counts: a missing backup directory or an unreadable path says nothing about
// the individual files.
func markDeletedBackupFiles(directory string, items []postgres.BackupRecord) {
	if directory == "" {
		return
	}
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return
	}
	for index := range items {
		item := &items[index]
		if item.State != "verified" || item.Filename == "" || filepath.Base(item.Filename) != item.Filename {
			continue
		}
		if _, err := os.Lstat(filepath.Join(directory, item.Filename)); errors.Is(err, os.ErrNotExist) {
			item.LocalFileDeleted = true
		}
	}
}

func (server *Server) createBackup(writer http.ResponseWriter, request *http.Request) {
	if !server.config.BackupConfigured {
		writeError(writer, http.StatusConflict, "backup_not_configured", "BACKUP_ENCRYPTION_KEY is not configured")
		return
	}
	item, err := server.store.CreateBackup(request.Context(), time.Now().UTC())
	if errors.Is(err, postgres.ErrBackupInProgress) {
		writeError(writer, http.StatusConflict, "backup_in_progress", "a backup is already queued or running")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "backup_failed", "could not queue backup")
		return
	}
	writer.Header().Set("Location", "/api/v1/backups")
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: item})
}

func (server *Server) downloadBackup(writer http.ResponseWriter, request *http.Request) {
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "backup ID is invalid")
		return
	}
	record, err := server.store.Backup(request.Context(), id)
	if errors.Is(err, postgres.ErrNotFound) || err == nil && record.State != "verified" {
		writeError(writer, http.StatusNotFound, "not_found", "verified backup was not found")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not load backup")
		return
	}
	if filepath.Base(record.Filename) != record.Filename || !strings.HasSuffix(record.Filename, ".mmbackup") {
		writeError(writer, http.StatusInternalServerError, "invalid_backup", "backup storage reference is invalid")
		return
	}
	path := filepath.Join(server.config.BackupDirectory, record.Filename)
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		checksum, size, digestErr := backups.FileDigest(path)
		if digestErr != nil || checksum != record.SHA256 || record.SizeBytes == nil || size != *record.SizeBytes {
			writeError(writer, http.StatusConflict, "backup_integrity_failed", "backup checksum or size no longer matches its verified record")
			return
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			writeError(writer, http.StatusNotFound, "not_found", "backup file is unavailable")
			return
		}
		defer file.Close()
		serveBackup(writer, request, file, info, record.Filename)
		return
	}
	// The local copy is gone or was never restored; a verified off-site copy is
	// still a valid restore source. The downloaded object is checked against the
	// same size and SHA-256 recorded before the local copy was accepted.
	if record.OffsiteKey == "" || server.config.BackupOffsite == nil {
		writeError(writer, http.StatusNotFound, "not_found", "backup file is unavailable")
		return
	}
	if !backups.ValidOffsiteKey(record.OffsiteKey) {
		writeError(writer, http.StatusInternalServerError, "invalid_backup", "off-site backup reference is invalid")
		return
	}
	file, info, fetchErr := server.fetchOffsiteBackup(request.Context(), record)
	if fetchErr != nil {
		writeError(writer, http.StatusBadGateway, "offsite_unavailable", "off-site backup could not be read or failed verification")
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	serveBackup(writer, request, file, info, record.Filename)
}

func (server *Server) fetchOffsiteBackup(ctx context.Context, record postgres.BackupRecord) (*os.File, os.FileInfo, error) {
	if record.SizeBytes == nil || *record.SizeBytes < 1 || record.SHA256 == "" {
		return nil, nil, errors.New("verified backup metadata is incomplete")
	}
	body, err := server.config.BackupOffsite.Get(ctx, record.OffsiteKey)
	if err != nil {
		return nil, nil, err
	}
	defer body.Close()
	temp, err := os.CreateTemp(server.config.BackupDirectory, ".offsite-*.mmbackup")
	if err != nil {
		return nil, nil, err
	}
	name := temp.Name()
	fail := func(cause error) (*os.File, os.FileInfo, error) {
		_ = temp.Close()
		_ = os.Remove(name)
		return nil, nil, cause
	}
	if err := temp.Chmod(0o600); err != nil {
		return fail(err)
	}
	written, err := io.Copy(temp, io.LimitReader(body, *record.SizeBytes+1))
	if err != nil || written != *record.SizeBytes {
		return fail(errors.New("off-site backup size mismatch"))
	}
	if err := temp.Sync(); err != nil {
		return fail(err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(name)
		return nil, nil, err
	}
	checksum, size, err := backups.FileDigest(name)
	if err != nil || checksum != record.SHA256 || size != *record.SizeBytes {
		_ = os.Remove(name)
		return nil, nil, errors.New("off-site backup checksum mismatch")
	}
	file, err := os.Open(name)
	if err != nil {
		_ = os.Remove(name)
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return nil, nil, err
	}
	return file, info, nil
}

func serveBackup(writer http.ResponseWriter, request *http.Request, file *os.File, info os.FileInfo, filename string) {
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(writer, request, filename, info.ModTime(), file)
}

func (server *Server) retryBackupOffsite(writer http.ResponseWriter, request *http.Request) {
	if server.config.BackupOffsite == nil {
		writeError(writer, http.StatusConflict, "offsite_not_configured", "off-site backup is not configured")
		return
	}
	id, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_id", "backup ID is invalid")
		return
	}
	record, err := server.store.QueueBackupOffsite(request.Context(), id, time.Now().UTC())
	if errors.Is(err, postgres.ErrNotFound) {
		writeError(writer, http.StatusConflict, "backup_not_verified", "only a verified backup can be copied off-site")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "database_error", "could not queue the off-site copy")
		return
	}
	writeJSON(writer, http.StatusAccepted, dataEnvelope{Data: map[string]any{"id": record.ID, "state": "queued"}})
}
