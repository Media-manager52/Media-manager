package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"uuid"

	"github.com/media-manager/media-manager/internal/releases"
)

// indexerReliabilityWindow is how far back finished downloads count towards an
// indexer's reliability share.
const indexerReliabilityWindow = "30 days"

// indexerReliabilitySQL yields one row per indexer with finished downloads in
// the window: the smoothed share that ended in verified output. One verified
// and three other pseudo-downloads keep an indexer with little history near
// releases.DefaultIndexerReliability instead of at 0 or 1.
const indexerReliabilitySQL = `SELECT reliability_candidate.indexer_instance_id,
	(count(*) FILTER (WHERE reliability_job.acquisition_outcome='output_verified')+1)::float8/(count(*)+4) AS share
	FROM altmount_jobs reliability_job
	JOIN release_candidates reliability_candidate ON reliability_candidate.id=reliability_job.candidate_id
	WHERE reliability_job.acquisition_outcome IS NOT NULL AND reliability_job.submitted_at>now()-interval '` + indexerReliabilityWindow + `'
	GROUP BY reliability_candidate.indexer_instance_id`

// indexerReliabilityJoinSQL left-joins the reliability rows to a candidate
// alias under the fixed alias "reliability".
func indexerReliabilityJoinSQL(candidate string) string {
	return " LEFT JOIN (" + indexerReliabilitySQL + ") reliability ON reliability.indexer_instance_id=" + candidate + ".indexer_instance_id "
}

// indexerReliabilityOrderSQL orders the more reliable indexer first. It only
// breaks ties after the policy score.
const indexerReliabilityOrderSQL = "COALESCE(reliability.share,0.25) DESC"

// providerUnusableSQL is true for a candidate whose stored provider fields mark
// a bad or password-protected archive. It mirrors
// releases.ProviderMarksUnusable; such candidates are tried last.
func providerUnusableSQL(candidate string) string {
	return "EXISTS(SELECT 1 FROM release_observations marked CROSS JOIN LATERAL jsonb_array_elements(marked.raw_fields) field WHERE marked.id=" + candidate + ".observation_id AND lower(btrim(field->>'name'))='password' AND btrim(field->>'value') IN ('2','10'))"
}

// loadIndexerReliability reads the same shares for in-process ranking.
func loadIndexerReliability(ctx context.Context, transaction pgx.Tx) (releases.RankOptions, error) {
	rows, err := transaction.Query(ctx, indexerReliabilitySQL)
	if err != nil {
		return releases.RankOptions{}, err
	}
	defer rows.Close()
	options := releases.RankOptions{IndexerReliability: map[uuid.UUID]float64{}}
	for rows.Next() {
		var indexer string
		var share float64
		if err := rows.Scan(&indexer, &share); err != nil {
			return releases.RankOptions{}, err
		}
		id, err := uuid.Parse(indexer)
		if err != nil {
			return releases.RankOptions{}, err
		}
		options.IndexerReliability[id] = share
	}
	return options, rows.Err()
}
