package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"
)

// Usenet server changes made from Media Manager. The servers themselves live
// in AltMount; Media Manager keeps only this audit trail.
const (
	UsenetServerCreated   = "usenet_server.created"
	UsenetServerChanged   = "usenet_server.changed"
	UsenetServerDeleted   = "usenet_server.deleted"
	UsenetServerReordered = "usenet_server.reordered"
)

// UsenetServerAudit is what the audit trail keeps about one change. It has no
// password field, so a password can never reach the audit trail.
type UsenetServerAudit struct {
	ServerID string   `json:"server_id,omitempty"`
	Host     string   `json:"host,omitempty"`
	Name     string   `json:"name,omitempty"`
	Fields   []string `json:"fields,omitempty"`
	Order    []string `json:"order,omitempty"`
}

// RecordUsenetServerChange audits one change to AltMount's Usenet servers
// against the AltMount integration it was made through.
func (store *Store) RecordUsenetServerChange(ctx context.Context, integrationID uuid.UUID, action string, audit UsenetServerAudit, now time.Time) error {
	switch action {
	case UsenetServerCreated, UsenetServerChanged, UsenetServerDeleted, UsenetServerReordered:
	default:
		return errors.New("unknown Usenet server audit action")
	}
	detail, err := json.Marshal(audit)
	if err != nil {
		return err
	}
	_, err = store.Pool.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner',$2,'integration',$3,$4,$5)`, uuid.NewV7().String(), action, integrationID.String(), string(detail), now.UTC())
	return err
}
