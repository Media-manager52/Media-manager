package postgres

import (
	"context"

	"uuid"
)

// AltMountJobTitle is the title an AltMount upload was made for.
type AltMountJobTitle struct {
	MediaID uuid.UUID `json:"media_id"`
	Kind    string    `json:"kind"`
	Title   string    `json:"title"`
	Year    *int      `json:"year,omitempty"`
	Variant string    `json:"variant,omitempty"`
}

// AltMountJobTitles finds the titles behind AltMount uploads by their job
// IDs (the "--mm-<id>" part of the upload name). Unknown IDs are left out.
func (store *Store) AltMountJobTitles(ctx context.Context, jobIDs []uuid.UUID) (map[uuid.UUID]AltMountJobTitle, error) {
	titles := map[uuid.UUID]AltMountJobTitle{}
	if len(jobIDs) == 0 {
		return titles, nil
	}
	ids := make([]string, 0, len(jobIDs))
	for _, id := range jobIDs {
		ids = append(ids, id.String())
	}
	rows, err := store.Pool.Query(ctx, `SELECT job.id::text,media.id::text,media.kind,media.title,media.year::int,COALESCE(workflow.variant,'')
		FROM altmount_jobs job JOIN workflows workflow ON workflow.id=job.workflow_id JOIN media_items media ON media.id=workflow.media_id
		WHERE job.id=ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var jobText, mediaText string
		var title AltMountJobTitle
		if err := rows.Scan(&jobText, &mediaText, &title.Kind, &title.Title, &title.Year, &title.Variant); err != nil {
			return nil, err
		}
		jobID, err := uuid.Parse(jobText)
		if err != nil {
			return nil, err
		}
		if title.MediaID, err = uuid.Parse(mediaText); err != nil {
			return nil, err
		}
		titles[jobID] = title
	}
	return titles, rows.Err()
}
