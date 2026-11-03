package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/media-manager/media-manager/internal/acquisition"
	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/backups"
	metadataservice "github.com/media-manager/media-manager/internal/metadata"
	"github.com/media-manager/media-manager/internal/oldboyscredentials"
	"github.com/media-manager/media-manager/internal/postgres"
)

type Config struct {
	Streaming              *acquisition.BrowseService
	OldBoysCredentials     *oldboyscredentials.Service
	CommissionOldBoys      func(context.Context) (postgres.Integration, error)
	OwnerToken             string
	SeerrEnabled           bool
	SeerrWebhookSecret     string
	SeerrInstanceID        string
	Version                string
	Commit                 string
	MetadataProvider       *seerr.Client
	MetadataEnricher       *metadataservice.Service
	MetadataProviderStates map[string]string
	CommissionNewznab      func(context.Context, uuid.UUID) (postgres.Integration, error)
	CommissioningV2        CommissioningV2Service
	CommissioningV2Origin  string
	BackupConfigured       bool
	BackupDirectory        string
	BackupOffsite          backups.OffsiteStore
	BackupOffsiteKind      string
	BackupOffsitePrefix    string
	// ServarrAPIKey enables the Radarr/Sonarr-compatible API Seerr uses when
	// Media Manager replaces the *arr applications. Empty disables it.
	ServarrAPIKey string
}

type Server struct {
	config     Config
	store      *postgres.Store
	mux        *http.ServeMux
	readySince time.Time
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		// Hint is the next step, when the server knows one.
		Hint string `json:"hint,omitempty"`
	} `json:"error"`
}

type dataEnvelope struct {
	Data any `json:"data"`
	Meta any `json:"meta,omitempty"`
}

func New(config Config, store *postgres.Store) (*Server, error) {
	if store == nil || len(config.OwnerToken) < 32 {
		return nil, errors.New("owner token must contain at least 32 bytes")
	}
	server := &Server{config: config, store: store, mux: http.NewServeMux(), readySince: time.Now().UTC()}
	server.routes()
	return server, nil
}

func (server *Server) Handler() http.Handler { return server.security(server.mux) }

func (server *Server) requireOwner(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		const prefix = "Bearer "
		authorization := request.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, prefix) || !secureEqual(strings.TrimPrefix(authorization, prefix), server.config.OwnerToken) {
			writeError(writer, http.StatusUnauthorized, "unauthorized", "owner authorization is required")
			return
		}
		next(writer, request)
	}
}

func (server *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(writer, request)
	})
}

func secureEqual(actual, expected string) bool {
	left, right := sha256.Sum256([]byte(actual)), sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(left[:], right[:]) == 1
}

// splitSeerrRequestID splits the instance-qualified request key returned by the
// read model ("<seerr-instance-uuid>:<request-id>") into its parts. A bare
// request ID (for example a legacy bookmark) is not qualified and falls back to
// the configured instance.
func splitSeerrRequestID(value string) (string, string, bool) {
	instance, requestID, found := strings.Cut(value, ":")
	if !found || instance == "" || requestID == "" {
		return "", "", false
	}
	if _, err := uuid.Parse(instance); err != nil {
		return "", "", false
	}
	return instance, requestID, true
}

func optionalUUID(value string) (*uuid.UUID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func decodeJSON(request *http.Request, output any, limit int64) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, limit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON documents")
	}
	return nil
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeErrorHint(writer, status, code, message, "")
}

// writeDatabaseError distinguishes a dependency outage from a genuine server
// defect. Owner screens retry on 503; a 500 would make a stopped database look
// like corrupt application state.
func writeDatabaseError(writer http.ResponseWriter, err error, message string) {
	if databaseUnavailable(err) {
		writeError(writer, http.StatusServiceUnavailable, "database_unavailable", "Database is unavailable")
		return
	}
	writeError(writer, http.StatusInternalServerError, "database_error", message)
}

func databaseUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var connectErr *pgconn.ConnectError
	if errors.As(err, &connectErr) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return strings.HasPrefix(pgErr.Code, "08") || strings.HasPrefix(pgErr.Code, "57")
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	detail := strings.ToLower(err.Error())
	return strings.Contains(detail, "failed to connect") ||
		strings.Contains(detail, "connection refused") ||
		strings.Contains(detail, "no such host") ||
		strings.Contains(detail, "terminating connection")
}

func writeErrorHint(writer http.ResponseWriter, status int, code, message, hint string) {
	response := errorEnvelope{}
	response.Error.Code, response.Error.Message, response.Error.Hint = code, message, hint
	writeJSON(writer, status, response)
}

const workflowActionsHint = "Read available_actions on GET /api/v1/workflows/{id} for what applies now."

// conflictSentinels gives the store's sentinel refusals stable codes and hints.
var conflictSentinels = []struct {
	err        error
	code, hint string
}{
	{postgres.ErrConcurrentModification, "concurrent_modification", "The workflow changed while the action ran. Re-read GET /api/v1/workflows/{id} and retry with its current revision."},
	{postgres.ErrWorkflowAltMountJobActive, "altmount_job_active", "Wait for the active AltMount job to settle, then retry."},
	{postgres.ErrWorkflowNotCancellable, "workflow_not_cancellable", "Workflows that are publishing, published or terminal cannot be cancelled."},
	{postgres.ErrPlexReconfirmationNotTimedOut, "plex_not_timed_out", "Only a workflow in needs_attention with attention_code plex_timeout can be reconfirmed."},
}

// writeConflict reports a refused owner action as 409. A typed ActionRefusal
// or known sentinel keeps a stable code and hint; anything else keeps the
// caller's code and message, with a pointer to the workflow's actions.
func writeConflict(writer http.ResponseWriter, err error, code, message string) {
	var refusal postgres.ActionRefusal
	if errors.As(err, &refusal) {
		writeErrorHint(writer, http.StatusConflict, refusal.Code, refusal.Message, refusal.Hint)
		return
	}
	for _, sentinel := range conflictSentinels {
		if errors.Is(err, sentinel.err) {
			writeErrorHint(writer, http.StatusConflict, sentinel.code, err.Error(), sentinel.hint)
			return
		}
	}
	writeErrorHint(writer, http.StatusConflict, code, message, workflowActionsHint)
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}
