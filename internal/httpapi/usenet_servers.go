package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/acquisition"
	"github.com/media-manager/media-manager/internal/postgres"
)

// Usenet servers are AltMount's NNTP providers, managed from Media Manager so
// the owner only types each news server's login. The servers and their
// passwords live in AltMount alone: Media Manager passes a password straight
// through, never stores, logs, audits or returns it, and shows only whether
// AltMount holds one.

type usenetServersView struct {
	AltMount usenetServersConnection    `json:"altmount"`
	Servers  []acquisition.UsenetServer `json:"servers"`
}

type usenetServersConnection struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type usenetServerOrderInput struct {
	IDs []string `json:"ids"`
}

type usenetServerTestResult struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	RTTMs  int64  `json:"rtt_ms,omitempty"`
}

const usenetServerBodyLimit = 16 << 10

var (
	usenetHostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	usenetDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// usenetAltMount opens the AltMount connection the servers are managed
// through: the enabled default AltMount connection, or the only enabled one.
func (server *Server) usenetAltMount(writer http.ResponseWriter, request *http.Request) (postgres.Integration, *acquisition.UsenetServerClient, bool) {
	integrations, err := server.store.ListIntegrations(request.Context())
	if err != nil {
		writeDatabaseError(writer, err, "could not load integrations")
		return postgres.Integration{}, nil, false
	}
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
		writeErrorHint(writer, http.StatusConflict, "altmount_not_configured", "Usenet servers are managed through AltMount, and no single enabled AltMount connection was found", "Add AltMount under Indstillinger → Forbindelser, enable it and make it the default.")
		return postgres.Integration{}, nil, false
	}
	client, err := acquisition.AltMountProviderClient(request.Context(), chosen[0], server.resolveSecret, nil)
	if err != nil {
		writeError(writer, http.StatusBadGateway, "altmount_unavailable", safeConnectionError(err))
		return postgres.Integration{}, nil, false
	}
	return chosen[0], client, true
}

// writeUsenetServerError turns AltMount's answer into Media Manager's error
// shape. AltMount's refusal reason is kept; it never carries a password.
func writeUsenetServerError(writer http.ResponseWriter, err error) {
	status, message, refused := acquisition.UsenetServerRefusal(err)
	switch {
	case errors.Is(err, acquisition.ErrInvalidUsenetServerID):
		writeError(writer, http.StatusBadRequest, "invalid_id", "Usenet server ID is invalid")
	case refused && status == http.StatusNotFound:
		writeError(writer, http.StatusNotFound, "not_found", "Usenet server was not found in AltMount")
	case refused && status >= 400 && status < 500:
		if message == "" {
			message = "AltMount refused the change"
		}
		writeError(writer, http.StatusUnprocessableEntity, "usenet_server_rejected", message)
	default:
		writeError(writer, http.StatusBadGateway, "altmount_unavailable", safeConnectionError(err))
	}
}

func usenetContext(request *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(request.Context(), 60*time.Second)
}

func (server *Server) listUsenetServers(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := usenetContext(request)
	defer cancel()
	integration, client, ok := server.usenetAltMount(writer, request.WithContext(ctx))
	if !ok {
		return
	}
	defer client.Logout(ctx)
	servers, err := client.ListProviders(ctx)
	if err != nil {
		writeUsenetServerError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: usenetServersView{AltMount: usenetServersConnection{ID: integration.ID, Name: integration.Name}, Servers: servers}})
}

func (server *Server) createUsenetServer(writer http.ResponseWriter, request *http.Request) {
	var input acquisition.UsenetServerInput
	if err := decodeJSON(request, &input, usenetServerBodyLimit); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must be one Usenet server")
		return
	}
	if message := validateUsenetServer(&input, true); message != "" {
		writeError(writer, http.StatusBadRequest, "invalid_usenet_server", message)
		return
	}
	ctx, cancel := usenetContext(request)
	defer cancel()
	integration, client, ok := server.usenetAltMount(writer, request.WithContext(ctx))
	if !ok {
		return
	}
	defer client.Logout(ctx)
	created, err := client.CreateProvider(ctx, input)
	if err != nil {
		writeUsenetServerError(writer, err)
		return
	}
	server.auditUsenetServer(ctx, integration.ID, postgres.UsenetServerCreated, postgres.UsenetServerAudit{ServerID: created.ID, Host: created.Host, Name: created.Name})
	writeJSON(writer, http.StatusCreated, dataEnvelope{Data: created})
}

func (server *Server) updateUsenetServer(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	var input acquisition.UsenetServerInput
	if err := decodeJSON(request, &input, usenetServerBodyLimit); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must be the Usenet server fields to change")
		return
	}
	if message := validateUsenetServer(&input, false); message != "" {
		writeError(writer, http.StatusBadRequest, "invalid_usenet_server", message)
		return
	}
	ctx, cancel := usenetContext(request)
	defer cancel()
	integration, client, ok := server.usenetAltMount(writer, request.WithContext(ctx))
	if !ok {
		return
	}
	defer client.Logout(ctx)
	if input.Enabled != nil && !*input.Enabled {
		if !server.keepsAnActiveUsenetServer(ctx, writer, client, id) {
			return
		}
	}
	updated, err := client.UpdateProvider(ctx, id, input)
	if err != nil {
		writeUsenetServerError(writer, err)
		return
	}
	server.auditUsenetServer(ctx, integration.ID, postgres.UsenetServerChanged, postgres.UsenetServerAudit{ServerID: updated.ID, Host: updated.Host, Name: updated.Name, Fields: changedUsenetFields(input)})
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: updated})
}

func (server *Server) deleteUsenetServer(writer http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	ctx, cancel := usenetContext(request)
	defer cancel()
	integration, client, ok := server.usenetAltMount(writer, request.WithContext(ctx))
	if !ok {
		return
	}
	defer client.Logout(ctx)
	servers, err := client.ListProviders(ctx)
	if err != nil {
		writeUsenetServerError(writer, err)
		return
	}
	var removed *acquisition.UsenetServer
	for index := range servers {
		if servers[index].ID == id {
			removed = &servers[index]
		}
	}
	if removed == nil {
		writeError(writer, http.StatusNotFound, "not_found", "Usenet server was not found in AltMount")
		return
	}
	if removed.Enabled && enabledUsenetServers(servers) == 1 {
		writeLastActiveUsenetServer(writer)
		return
	}
	if err := client.DeleteProvider(ctx, id); err != nil {
		writeUsenetServerError(writer, err)
		return
	}
	server.auditUsenetServer(ctx, integration.ID, postgres.UsenetServerDeleted, postgres.UsenetServerAudit{ServerID: removed.ID, Host: removed.Host, Name: removed.Name})
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]string{"status": "removed"}})
}

func (server *Server) reorderUsenetServers(writer http.ResponseWriter, request *http.Request) {
	var input usenetServerOrderInput
	if err := decodeJSON(request, &input, usenetServerBodyLimit); err != nil || len(input.IDs) == 0 {
		writeError(writer, http.StatusBadRequest, "invalid_json", "ids must list every Usenet server once, first to last")
		return
	}
	seen := map[string]bool{}
	for _, id := range input.IDs {
		if seen[id] {
			writeError(writer, http.StatusBadRequest, "invalid_usenet_server", "ids lists a Usenet server twice")
			return
		}
		seen[id] = true
	}
	ctx, cancel := usenetContext(request)
	defer cancel()
	integration, client, ok := server.usenetAltMount(writer, request.WithContext(ctx))
	if !ok {
		return
	}
	defer client.Logout(ctx)
	if err := client.ReorderProviders(ctx, input.IDs); err != nil {
		writeUsenetServerError(writer, err)
		return
	}
	server.auditUsenetServer(ctx, integration.ID, postgres.UsenetServerReordered, postgres.UsenetServerAudit{Order: input.IDs})
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: map[string]string{"status": "reordered"}})
}

func (server *Server) testUsenetServer(writer http.ResponseWriter, request *http.Request) {
	var input acquisition.UsenetServerTest
	if err := decodeJSON(request, &input, usenetServerBodyLimit); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", "request body must be the login to test")
		return
	}
	input.Host, input.Username = strings.TrimSpace(input.Host), strings.TrimSpace(input.Username)
	switch {
	case !usenetHostPattern.MatchString(input.Host):
		writeError(writer, http.StatusBadRequest, "invalid_usenet_server", "host must be the news server's name, such as news.example.com")
		return
	case input.Port < 1 || input.Port > 65535:
		writeError(writer, http.StatusBadRequest, "invalid_usenet_server", "port must be between 1 and 65535")
		return
	case input.Username == "" || input.Password == "":
		writeError(writer, http.StatusBadRequest, "invalid_usenet_server", "type the username and password to test them")
		return
	}
	ctx, cancel := usenetContext(request)
	defer cancel()
	_, client, ok := server.usenetAltMount(writer, request.WithContext(ctx))
	if !ok {
		return
	}
	defer client.Logout(ctx)
	result, err := client.TestProvider(ctx, input)
	if err != nil {
		writeUsenetServerError(writer, err)
		return
	}
	detail := strings.ReplaceAll(result.ErrorMessage, input.Password, "***")
	if len(detail) > 300 {
		detail = detail[:300]
	}
	writeJSON(writer, http.StatusOK, dataEnvelope{Data: usenetServerTestResult{OK: result.Success, Detail: detail, RTTMs: result.RTTMs}})
}

// keepsAnActiveUsenetServer refuses to turn off the last enabled server:
// without one AltMount cannot download anything.
func (server *Server) keepsAnActiveUsenetServer(ctx context.Context, writer http.ResponseWriter, client *acquisition.UsenetServerClient, id string) bool {
	servers, err := client.ListProviders(ctx)
	if err != nil {
		writeUsenetServerError(writer, err)
		return false
	}
	for _, candidate := range servers {
		if candidate.ID == id && candidate.Enabled && enabledUsenetServers(servers) == 1 {
			writeLastActiveUsenetServer(writer)
			return false
		}
	}
	return true
}

func enabledUsenetServers(servers []acquisition.UsenetServer) int {
	count := 0
	for _, candidate := range servers {
		if candidate.Enabled {
			count++
		}
	}
	return count
}

func writeLastActiveUsenetServer(writer http.ResponseWriter) {
	writeErrorHint(writer, http.StatusConflict, "usenet_server_last_active", "This is the only active Usenet server; without one nothing can be downloaded", "Add or turn on another Usenet server first.")
}

func (server *Server) auditUsenetServer(ctx context.Context, integrationID uuid.UUID, action string, audit postgres.UsenetServerAudit) {
	if err := server.store.RecordUsenetServerChange(ctx, integrationID, action, audit, time.Now().UTC()); err != nil {
		slog.WarnContext(ctx, "could not audit a Usenet server change", "action", action, "error", err)
	}
}

// validateUsenetServer checks what the owner typed before AltMount sees it.
// A new server needs host, port, username and password; a change checks only
// the fields it sets. Text fields are trimmed in place.
func validateUsenetServer(input *acquisition.UsenetServerInput, creating bool) string {
	trim := func(value *string) {
		if value != nil {
			*value = strings.TrimSpace(*value)
		}
	}
	trim(input.Name)
	trim(input.Host)
	trim(input.Username)
	trim(input.AccountExpirationDate)
	if creating {
		switch {
		case input.Host == nil || *input.Host == "":
			return "host is required"
		case input.Port == nil:
			return "port is required"
		case input.Username == nil || *input.Username == "":
			return "username is required"
		case input.Password == nil || *input.Password == "":
			return "password is required"
		}
		if input.MaxConnections == nil {
			connections := 10
			input.MaxConnections = &connections
		}
		if input.Enabled == nil {
			enabled := true
			input.Enabled = &enabled
		}
	}
	switch {
	case input.Name != nil && len(*input.Name) > 100:
		return "name is at most 100 characters"
	case input.Host != nil && !usenetHostPattern.MatchString(*input.Host):
		return "host must be the news server's name, such as news.example.com, without http:// or a port"
	case input.Port != nil && (*input.Port < 1 || *input.Port > 65535):
		return "port must be between 1 and 65535"
	case input.Username != nil && (*input.Username == "" || len(*input.Username) > 200):
		return "username cannot be empty"
	case input.Password != nil && len(*input.Password) > 200:
		return "password is at most 200 characters"
	case input.MaxConnections != nil && (*input.MaxConnections < 1 || *input.MaxConnections > 500):
		return "max_connections must be between 1 and 500"
	case input.QuotaBytes != nil && *input.QuotaBytes < 0:
		return "quota_bytes cannot be negative"
	case input.QuotaPeriodHours != nil && (*input.QuotaPeriodHours < 0 || *input.QuotaPeriodHours > 24*366):
		return "quota_period_hours must be between 0 and 8784"
	case input.AccountExpirationDate != nil && *input.AccountExpirationDate != "" && !validUsenetDate(*input.AccountExpirationDate):
		return "account_expiration_date must be a date such as 2027-01-31"
	}
	return ""
}

func validUsenetDate(value string) bool {
	if !usenetDatePattern.MatchString(value) {
		return false
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

// changedUsenetFields names the fields a change set, for the audit trail. A
// new password is recorded as "password", never its value.
func changedUsenetFields(input acquisition.UsenetServerInput) []string {
	fields := []string{}
	add := func(set bool, name string) {
		if set {
			fields = append(fields, name)
		}
	}
	add(input.Name != nil, "name")
	add(input.Host != nil, "host")
	add(input.Port != nil, "port")
	add(input.Username != nil, "username")
	add(input.Password != nil && *input.Password != "", "password")
	add(input.MaxConnections != nil, "max_connections")
	add(input.TLS != nil, "tls")
	add(input.InsecureTLS != nil, "insecure_tls")
	add(input.Enabled != nil, "enabled")
	add(input.IsBackupProvider != nil, "is_backup_provider")
	add(input.QuotaBytes != nil, "quota_bytes")
	add(input.QuotaPeriodHours != nil, "quota_period_hours")
	add(input.AccountExpirationDate != nil, "account_expiration_date")
	return fields
}
