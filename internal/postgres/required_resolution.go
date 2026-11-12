package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/releases"
)

// RequiredResolutionsForJob returns the allowed values of every required
// resolution rule that applies to an AltMount job: the rules of the round's
// policy snapshot plus any the live profile adds (a live profile can only add
// a requirement here, never remove one). Each inner list is one rule; the
// output must satisfy every rule.
func (store *Store) RequiredResolutionsForJob(ctx context.Context, jobID uuid.UUID) ([][]string, error) {
	var roundPolicy, profilePolicy []byte
	err := store.Pool.QueryRow(ctx, `SELECT round.policy_snapshot,profile.policy
		FROM altmount_jobs job
		JOIN acquisition_rounds round ON round.id=job.round_id AND round.workflow_id=job.workflow_id
		JOIN workflows workflow ON workflow.id=job.workflow_id
		JOIN quality_profiles profile ON profile.id=workflow.quality_profile_id
		WHERE job.id=$1`, jobID.String()).Scan(&roundPolicy, &profilePolicy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var result [][]string
	seen := map[string]bool{}
	for _, document := range [][]byte{roundPolicy, profilePolicy} {
		var policy struct {
			Rules []releases.Rule `json:"rules"`
		}
		if len(document) == 0 || json.Unmarshal(document, &policy) != nil {
			continue
		}
		for _, rule := range policy.Rules {
			if rule.Kind != releases.RuleRequired || rule.Fact != "resolution" || len(rule.AllowedValues) == 0 {
				continue
			}
			key := strings.Join(rule.AllowedValues, "\x00")
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, append([]string(nil), rule.AllowedValues...))
		}
	}
	return result, nil
}
