package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/media-manager/media-manager/internal/postgres"
)

// pruneObservations runs one pass of postgres.PruneRepeatedObservations.
//
//	media-manager prune-observations [-older-than-days 14] [-batch 500]
//	media-manager prune-observations -apply -archive - > observations.jsonl.gz
//
// Without -apply it only counts. With -apply every removed row is first
// written to the gzip JSON-lines archive ("-" is standard output), and a batch
// is deleted only after its rows were flushed to the archive.
func pruneObservations(ctx context.Context, store *postgres.Store, args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("prune-observations", flag.ContinueOnError)
	flags.SetOutput(stderr)
	apply := flags.Bool("apply", false, "delete repeats (default: only count them)")
	olderThanDays := flags.Int("older-than-days", 14, "keep every observation newer than this many days (minimum 7)")
	batch := flags.Int("batch", 500, "provider releases per transaction (1-5000)")
	archivePath := flags.String("archive", "", `gzip JSON-lines archive of removed rows; "-" writes to standard output (required with -apply)`)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *olderThanDays < 7 {
		return errors.New("-older-than-days must be at least 7")
	}
	if *apply && *archivePath == "" {
		return errors.New("-apply requires -archive")
	}

	options := postgres.ObservationPruneOptions{
		ObservedBefore:   time.Now().UTC().AddDate(0, 0, -*olderThanDays),
		ReleasesPerBatch: *batch,
		Apply:            *apply,
	}
	var archive *observationArchive
	if *apply {
		var err error
		archive, err = openObservationArchive(*archivePath, stdout)
		if err != nil {
			return err
		}
		defer archive.Close()
		options.Archive = archive.Write
	}
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	started := time.Now()
	options.Progress = func(progress postgres.ObservationPruneProgress) {
		if progress.Batches%50 == 0 {
			logger.Info("pruning observations", "releases", progress.Releases, "repeats", progress.Repeats, "deleted", progress.Deleted, "elapsed", time.Since(started).Round(time.Second).String())
		}
	}
	result, err := store.PruneRepeatedObservations(ctx, options)
	if archive != nil {
		if closeErr := archive.Close(); err == nil {
			err = closeErr
		}
		if err == nil && archive.rows != result.Deleted {
			err = fmt.Errorf("archive holds %d rows but %d were deleted", archive.rows, result.Deleted)
		}
	}
	logger.Info("observation pruning finished", "apply", *apply, "completed", result.Completed, "releases", result.Releases, "repeats", result.Repeats, "deleted", result.Deleted, "elapsed", time.Since(started).Round(time.Second).String())
	return err
}

type observationArchive struct {
	file   *os.File
	buffer *bufio.Writer
	gzip   *gzip.Writer
	rows   int64
	closed bool
}

func openObservationArchive(path string, stdout io.Writer) (*observationArchive, error) {
	archive := &observationArchive{}
	var target io.Writer = stdout
	if path != "-" {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("create archive: %w", err)
		}
		archive.file = file
		target = file
	}
	archive.buffer = bufio.NewWriterSize(target, 1<<20)
	archive.gzip = gzip.NewWriter(archive.buffer)
	return archive, nil
}

// Write stores one batch and returns only after it left this process (and, for
// a file, reached the disk).
func (archive *observationArchive) Write(rows []postgres.ArchivedObservation) error {
	encoder := json.NewEncoder(archive.gzip)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	if err := archive.gzip.Flush(); err != nil {
		return err
	}
	if err := archive.buffer.Flush(); err != nil {
		return err
	}
	if archive.file != nil {
		if err := archive.file.Sync(); err != nil {
			return err
		}
	}
	archive.rows += int64(len(rows))
	return nil
}

func (archive *observationArchive) Close() error {
	if archive.closed {
		return nil
	}
	archive.closed = true
	err := archive.gzip.Close()
	if flushErr := archive.buffer.Flush(); err == nil {
		err = flushErr
	}
	if archive.file != nil {
		if syncErr := archive.file.Sync(); err == nil {
			err = syncErr
		}
		if closeErr := archive.file.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}
