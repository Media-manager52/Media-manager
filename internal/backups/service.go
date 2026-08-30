package backups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
)

const KeyReference = "BACKUP_ENCRYPTION_KEY"

type SecretResolver func(string) (string, error)

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type Service struct {
	Store         *postgres.Store
	Directory     string
	ResolveSecret SecretResolver
	Runner        CommandRunner
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, arguments...).CombinedOutput()
}

type phaseError struct {
	phase string
	err   error
}

func (failure phaseError) Error() string { return "backup " + failure.phase + " failed" }
func (failure phaseError) Unwrap() error { return failure.err }

func (service *Service) Handle(ctx context.Context, event postgres.OutboxEvent) error {
	record, err := service.Store.Backup(ctx, event.AggregateID)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.State == "verified" {
		return nil
	}
	if err := service.Store.StartBackup(ctx, record.ID, time.Now().UTC()); err != nil {
		return err
	}
	filename, size, checksum, schemaVersion, err := service.createAndVerify(ctx, record.ID)
	if err != nil {
		_ = service.Store.FailBackup(ctx, record.ID, safeFailure(err), time.Now().UTC())
		return err
	}
	return service.Store.CompleteBackup(ctx, record.ID, filename, size, checksum, schemaVersion, time.Now().UTC())
}

func (service *Service) createAndVerify(ctx context.Context, id uuid.UUID) (string, int64, string, int64, error) {
	if service.ResolveSecret == nil {
		return "", 0, "", 0, phaseError{phase: "encryption configuration", err: errors.New("secret resolver unavailable")}
	}
	encodedKey, err := service.ResolveSecret(KeyReference)
	if err != nil {
		return "", 0, "", 0, phaseError{phase: "encryption configuration", err: err}
	}
	key, err := ParseKey(encodedKey)
	if err != nil {
		return "", 0, "", 0, phaseError{phase: "encryption configuration", err: err}
	}
	directory, err := service.safeDirectory()
	if err != nil {
		return "", 0, "", 0, phaseError{phase: "storage preparation", err: err}
	}
	runner := service.Runner
	if runner == nil {
		runner = execRunner{}
	}
	rawFile, err := os.CreateTemp(directory, ".backup-*.dump")
	if err != nil {
		return "", 0, "", 0, phaseError{phase: "storage preparation", err: err}
	}
	rawPath := rawFile.Name()
	if err := rawFile.Close(); err != nil {
		return "", 0, "", 0, phaseError{phase: "storage preparation", err: err}
	}
	defer os.Remove(rawPath)
	if _, err := runner.Run(ctx, "pg_dump", "--format=custom", "--compress=6", "--no-owner", "--no-privileges", "--file", rawPath); err != nil {
		return "", 0, "", 0, phaseError{phase: "database dump", err: err}
	}
	if err := os.Chmod(rawPath, 0o600); err != nil {
		return "", 0, "", 0, phaseError{phase: "database dump", err: err}
	}
	filename := id.String() + ".mmbackup"
	finalPath := filepath.Join(directory, filename)
	encryptedFile, err := os.CreateTemp(directory, ".backup-*.encrypted")
	if err != nil {
		return "", 0, "", 0, phaseError{phase: "encryption", err: err}
	}
	encryptedPath := encryptedFile.Name()
	defer os.Remove(encryptedPath)
	if err := os.Chmod(encryptedPath, 0o600); err != nil {
		encryptedFile.Close()
		return "", 0, "", 0, phaseError{phase: "encryption", err: err}
	}
	rawSource, err := os.Open(rawPath)
	if err != nil {
		encryptedFile.Close()
		return "", 0, "", 0, phaseError{phase: "encryption", err: err}
	}
	encryptErr := Encrypt(encryptedFile, rawSource, key)
	closeSourceErr := rawSource.Close()
	syncErr := encryptedFile.Sync()
	closeEncryptedErr := encryptedFile.Close()
	if encryptErr != nil || closeSourceErr != nil || syncErr != nil || closeEncryptedErr != nil {
		return "", 0, "", 0, phaseError{phase: "encryption", err: errors.Join(encryptErr, closeSourceErr, syncErr, closeEncryptedErr)}
	}
	checksum, size, err := FileDigest(encryptedPath)
	if err != nil {
		return "", 0, "", 0, phaseError{phase: "checksum", err: err}
	}
	if err := os.Rename(encryptedPath, finalPath); err != nil {
		return "", 0, "", 0, phaseError{phase: "storage finalization", err: err}
	}
	schemaVersion, err := service.verifyRestore(ctx, runner, finalPath, key, directory, id)
	if err != nil {
		return "", 0, "", 0, err
	}
	return filename, size, checksum, schemaVersion, nil
}

func (service *Service) verifyRestore(ctx context.Context, runner CommandRunner, encryptedPath string, key []byte, directory string, id uuid.UUID) (int64, error) {
	decrypted, err := os.CreateTemp(directory, ".verify-*.dump")
	if err != nil {
		return 0, phaseError{phase: "restore verification", err: err}
	}
	decryptedPath := decrypted.Name()
	defer os.Remove(decryptedPath)
	if err := os.Chmod(decryptedPath, 0o600); err != nil {
		decrypted.Close()
		return 0, phaseError{phase: "restore verification", err: err}
	}
	encrypted, err := os.Open(encryptedPath)
	if err != nil {
		decrypted.Close()
		return 0, phaseError{phase: "restore verification", err: err}
	}
	decryptErr := Decrypt(decrypted, encrypted, key)
	closeEncryptedErr := encrypted.Close()
	syncErr := decrypted.Sync()
	closeDecryptedErr := decrypted.Close()
	if decryptErr != nil || closeEncryptedErr != nil || syncErr != nil || closeDecryptedErr != nil {
		return 0, phaseError{phase: "restore verification", err: errors.Join(decryptErr, closeEncryptedErr, syncErr, closeDecryptedErr)}
	}
	verificationDatabase := "mm_verify_" + strings.ReplaceAll(id.String(), "-", "")
	if _, err := runner.Run(ctx, "createdb", "--template=template0", verificationDatabase); err != nil {
		return 0, phaseError{phase: "verification database creation", err: err}
	}
	dropVerification := func() error {
		_, dropErr := runner.Run(context.Background(), "dropdb", "--if-exists", verificationDatabase)
		return dropErr
	}
	if _, err := runner.Run(ctx, "pg_restore", "--exit-on-error", "--no-owner", "--no-privileges", "--dbname", verificationDatabase, decryptedPath); err != nil {
		_ = dropVerification()
		return 0, phaseError{phase: "restore verification", err: err}
	}
	output, err := runner.Run(ctx, "psql", "--no-align", "--tuples-only", "--dbname", verificationDatabase, "--command", "SELECT COALESCE(MAX(version),0) FROM schema_migrations")
	if err != nil {
		_ = dropVerification()
		return 0, phaseError{phase: "schema verification", err: err}
	}
	version, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil || version != postgres.CurrentSchemaVersion {
		_ = dropVerification()
		return 0, phaseError{phase: "schema verification", err: fmt.Errorf("restored schema version %d", version)}
	}
	if err := dropVerification(); err != nil {
		return 0, phaseError{phase: "verification cleanup", err: err}
	}
	return version, nil
}

func (service *Service) safeDirectory() (string, error) {
	directory := filepath.Clean(strings.TrimSpace(service.Directory))
	if !filepath.IsAbs(directory) {
		return "", errors.New("backup directory must be absolute")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("backup directory is not a real directory")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", err
	}
	return directory, nil
}

func FileDigest(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func safeFailure(err error) string {
	var failure phaseError
	if errors.As(err, &failure) {
		return failure.phase + " failed"
	}
	return "backup operation failed"
}
