package postgres

import (
	"context"
	"encoding/json"
	"time"

	"uuid"
)

// PlexAccessAudit is what "Ret Plex" changed. The Cosmos token is never part
// of it.
type PlexAccessAudit struct {
	Container string   `json:"container"`
	Changes   []string `json:"changes"`
}

// RecordPlexAccessFix audits a "Ret Plex" against the Plex connection.
func (store *Store) RecordPlexAccessFix(ctx context.Context, plexIntegrationID uuid.UUID, audit PlexAccessAudit, now time.Time) error {
	detail, err := json.Marshal(audit)
	if err != nil {
		return err
	}
	_, err = store.Pool.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','plex.access_fixed','integration',$2,$3,$4)`, uuid.NewV7().String(), plexIntegrationID.String(), string(detail), now.UTC())
	return err
}
