package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

var notificationName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var notificationEvent = regexp.MustCompile(`^[a-z0-9_.-]+$`)

type NotificationDestination struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Endpoint  string    `json:"endpoint"`
	AllowHTTP bool      `json:"allow_http"`
	Enabled   bool      `json:"enabled"`
	Events    []string  `json:"events"`
	SecretRef string    `json:"secret_ref,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type NotificationDelivery struct {
	ID             uuid.UUID       `json:"id"`
	DestinationID  uuid.UUID       `json:"destination_id"`
	Destination    string          `json:"destination"`
	AuditEventID   uuid.UUID       `json:"audit_event_id"`
	Payload        json.RawMessage `json:"payload"`
	State          string          `json:"state"`
	Attempts       int             `json:"attempts"`
	ResponseStatus *int            `json:"response_status,omitempty"`
	LastError      string          `json:"last_error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	DeliveredAt    *time.Time      `json:"delivered_at,omitempty"`
}

func validateNotificationDestination(item NotificationDestination) (NotificationDestination, error) {
	item.Name, item.Endpoint, item.SecretRef = strings.TrimSpace(item.Name), strings.TrimSpace(item.Endpoint), strings.TrimSpace(item.SecretRef)
	parsed, err := url.Parse(item.Endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && !(item.AllowHTTP && parsed.Scheme == "http")) {
		return NotificationDestination{}, errors.New("invalid notification endpoint")
	}
	if item.Name == "" || len(item.Name) > 200 || item.SecretRef != "" && !notificationName.MatchString(item.SecretRef) || len(item.Events) < 1 || len(item.Events) > 50 {
		return NotificationDestination{}, errors.New("invalid notification destination")
	}
	seen := map[string]bool{}
	for index, event := range item.Events {
		event = strings.TrimSpace(event)
		if event != "*" && !notificationEvent.MatchString(event) || seen[event] {
			return NotificationDestination{}, errors.New("invalid notification events")
		}
		seen[event], item.Events[index] = true, event
	}
	return item, nil
}

// RedactNotificationEndpoint keeps enough origin information for owner review
// without returning webhook path tokens that some providers treat as secrets.
func RedactNotificationEndpoint(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "redacted"
	}
	return parsed.Scheme + "://" + parsed.Host + "/…"
}

func (store *Store) CreateNotificationDestination(ctx context.Context, item NotificationDestination, now time.Time) (NotificationDestination, error) {
	item, err := validateNotificationDestination(item)
	if err != nil {
		return NotificationDestination{}, err
	}
	item.ID, item.CreatedAt, item.UpdatedAt = uuid.NewV7(), now.UTC(), now.UTC()
	events, _ := json.Marshal(item.Events)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return NotificationDestination{}, err
	}
	defer transaction.Rollback(ctx)
	if _, err := transaction.Exec(ctx, `INSERT INTO notification_destinations(id,name,endpoint,allow_http,enabled,events,secret_ref,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$8)`, item.ID.String(), item.Name, item.Endpoint, item.AllowHTTP, item.Enabled, string(events), item.SecretRef, now.UTC()); err != nil {
		return NotificationDestination{}, err
	}
	detail, _ := json.Marshal(map[string]any{"name": item.Name, "endpoint": RedactNotificationEndpoint(item.Endpoint), "events": item.Events, "secret_ref": item.SecretRef})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','notification.destination_created','notification_destination',$2,$3,$4)`, uuid.NewV7().String(), item.ID.String(), detail, now.UTC()); err != nil {
		return NotificationDestination{}, err
	}
	return item, transaction.Commit(ctx)
}

func (store *Store) ListNotificationDestinations(ctx context.Context) ([]NotificationDestination, error) {
	rows, err := store.Pool.Query(ctx, `SELECT id::text,name,endpoint,allow_http,enabled,events,COALESCE(secret_ref,''),created_at,updated_at FROM notification_destinations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []NotificationDestination{}
	for rows.Next() {
		var item NotificationDestination
		var idText string
		var events []byte
		if err := rows.Scan(&idText, &item.Name, &item.Endpoint, &item.AllowHTTP, &item.Enabled, &events, &item.SecretRef, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		if err := json.Unmarshal(events, &item.Events); err != nil {
			return nil, err
		}
		item.Endpoint = RedactNotificationEndpoint(item.Endpoint)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) SetNotificationDestinationEnabled(ctx context.Context, id uuid.UUID, enabled bool, now time.Time) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	var name string
	if err := transaction.QueryRow(ctx, `UPDATE notification_destinations SET enabled=$2,updated_at=$3 WHERE id=$1 RETURNING name`, id.String(), enabled, now.UTC()).Scan(&name); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"name": name, "enabled": enabled})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','notification.destination_state_changed','notification_destination',$2,$3,$4)`, uuid.NewV7().String(), id.String(), detail, now.UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (store *Store) ListNotificationDeliveries(ctx context.Context, limit int) ([]NotificationDelivery, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	rows, err := store.Pool.Query(ctx, `SELECT delivery.id::text,delivery.destination_id::text,destination.name,delivery.audit_event_id::text,delivery.payload,delivery.state,delivery.attempts,delivery.response_status,COALESCE(delivery.last_error,''),delivery.created_at,delivery.delivered_at FROM notification_deliveries delivery JOIN notification_destinations destination ON destination.id=delivery.destination_id ORDER BY delivery.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []NotificationDelivery{}
	for rows.Next() {
		var item NotificationDelivery
		var idText, destinationText, auditText string
		var payload []byte
		if err := rows.Scan(&idText, &destinationText, &item.Destination, &auditText, &payload, &item.State, &item.Attempts, &item.ResponseStatus, &item.LastError, &item.CreatedAt, &item.DeliveredAt); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.DestinationID, _ = uuid.Parse(destinationText)
		item.AuditEventID, _ = uuid.Parse(auditText)
		item.Payload = append(json.RawMessage(nil), payload...)
		result = append(result, item)
	}
	return result, rows.Err()
}

type NotificationDeliveryContext struct {
	Delivery  NotificationDelivery
	Endpoint  string
	AllowHTTP bool
	SecretRef string
	Enabled   bool
}

func (store *Store) NotificationDeliveryContext(ctx context.Context, id uuid.UUID) (NotificationDeliveryContext, error) {
	var result NotificationDeliveryContext
	var idText, destinationText, auditText string
	var payload []byte
	err := store.Pool.QueryRow(ctx, `SELECT delivery.id::text,delivery.destination_id::text,destination.name,delivery.audit_event_id::text,delivery.payload,delivery.state,delivery.attempts,delivery.response_status,COALESCE(delivery.last_error,''),delivery.created_at,delivery.delivered_at,destination.endpoint,destination.allow_http,COALESCE(destination.secret_ref,''),destination.enabled FROM notification_deliveries delivery JOIN notification_destinations destination ON destination.id=delivery.destination_id WHERE delivery.id=$1`, id.String()).Scan(&idText, &destinationText, &result.Delivery.Destination, &auditText, &payload, &result.Delivery.State, &result.Delivery.Attempts, &result.Delivery.ResponseStatus, &result.Delivery.LastError, &result.Delivery.CreatedAt, &result.Delivery.DeliveredAt, &result.Endpoint, &result.AllowHTTP, &result.SecretRef, &result.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationDeliveryContext{}, ErrNotFound
	}
	if err != nil {
		return NotificationDeliveryContext{}, err
	}
	result.Delivery.ID, _ = uuid.Parse(idText)
	result.Delivery.DestinationID, _ = uuid.Parse(destinationText)
	result.Delivery.AuditEventID, _ = uuid.Parse(auditText)
	result.Delivery.Payload = append(json.RawMessage(nil), payload...)
	return result, nil
}

func (store *Store) MarkNotificationDelivery(ctx context.Context, id uuid.UUID, delivered bool, status int, detail string, now time.Time) error {
	state := "failed"
	var deliveredAt any
	if delivered {
		state, deliveredAt = "delivered", now.UTC()
	}
	_, err := store.Pool.Exec(ctx, `UPDATE notification_deliveries SET state=$2,attempts=attempts+1,response_status=NULLIF($3,0),last_error=NULLIF($4,''),delivered_at=$5 WHERE id=$1`, id.String(), state, status, detail, deliveredAt)
	return err
}
