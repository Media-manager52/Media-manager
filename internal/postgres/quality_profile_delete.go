package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrQualityProfileInUse means a title, file, list or Seerr route still
	// points at the profile, so removing it would change what they require.
	ErrQualityProfileInUse = errors.New("quality profile is in use")
	// ErrQualityProfileIsDefault means the profile is the fallback for its
	// media kind; another profile must become the default first.
	ErrQualityProfileIsDefault = errors.New("quality profile is the default")
)

// ErrQualityProfileReplacementInvalid means the profile chosen to take over
// is missing, the profile being deleted, disabled, or for another media kind.
var ErrQualityProfileReplacementInvalid = errors.New("replacement quality profile is not usable")

// QualityProfileDeletion reports what a delete moved to the replacement.
type QualityProfileDeletion struct {
	Name          string `json:"name"`
	ReplacedBy    string `json:"replaced_by,omitempty"`
	MovedTitles   int64  `json:"moved_titles"`
	MovedFiles    int64  `json:"moved_files"`
	MovedSettings int64  `json:"moved_settings"`
}

// DeleteQualityProfile removes a profile. Without a replacement it only
// removes a profile that nothing refers to: any workflow (finished ones
// included), adopted file, adoption batch, import list or Seerr route keeps it,
// because its language and quality requirements are part of the record of why
// those files were accepted.
//
// With a replacement (an enabled profile of the same media kind) every
// reference moves to the replacement in the same transaction and the profile
// is then removed. The replacement's policy_version is raised above both
// profiles' versions: a round or candidate decision made under the deleted
// profile can therefore never be mistaken for one made under the replacement
// just because the two version numbers happen to be equal. A moved title whose
// frozen round rules differ from the replacement's is stale exactly as after a
// profile edit (roundPolicyCurrentSQL) and waits for the owner to apply the
// policy; rounds whose rules are identical stay current. Nothing is evaluated
// under weaker rules without that explicit step.
func (store *Store) DeleteQualityProfile(ctx context.Context, id uuid.UUID, replaceWith *uuid.UUID, actor string, now time.Time) (QualityProfileDeletion, error) {
	var result QualityProfileDeletion
	if replaceWith != nil && *replaceWith == id {
		return result, ErrQualityProfileReplacementInvalid
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer transaction.Rollback(ctx)
	// Both rows are locked in ID order so two deletes naming each other
	// cannot deadlock.
	ids := []string{id.String()}
	if replaceWith != nil {
		ids = append(ids, replaceWith.String())
	}
	rows, err := transaction.Query(ctx, `SELECT id::text,name,media_kind,is_default,enabled,policy_version FROM quality_profiles WHERE id=ANY($1::uuid[]) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return result, err
	}
	type lockedProfile struct {
		name, kind string
		isDefault  bool
		enabled    bool
		version    int64
	}
	locked := map[string]lockedProfile{}
	for rows.Next() {
		var idText string
		var item lockedProfile
		if err := rows.Scan(&idText, &item.name, &item.kind, &item.isDefault, &item.enabled, &item.version); err != nil {
			rows.Close()
			return result, err
		}
		locked[idText] = item
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, err
	}
	profile, found := locked[id.String()]
	if !found {
		return result, ErrNotFound
	}
	result.Name = profile.name
	if profile.isDefault {
		return result, ErrQualityProfileIsDefault
	}
	if replaceWith != nil {
		replacement, found := locked[replaceWith.String()]
		if !found || !replacement.enabled || replacement.kind != profile.kind {
			return result, ErrQualityProfileReplacementInvalid
		}
		result.ReplacedBy = replacement.name
		moves := []struct {
			statement string
			counter   *int64
		}{
			{`UPDATE workflows SET quality_profile_id=$2 WHERE quality_profile_id=$1`, &result.MovedTitles},
			{`UPDATE adopted_files SET quality_profile_id=$2 WHERE quality_profile_id=$1`, &result.MovedFiles},
			{`UPDATE adoption_batches SET default_quality_profile_id=$2 WHERE default_quality_profile_id=$1`, &result.MovedSettings},
			{`UPDATE adoption_items SET quality_profile_id=$2 WHERE quality_profile_id=$1`, &result.MovedSettings},
			{`UPDATE import_lists SET movie_quality_profile_id=$2 WHERE movie_quality_profile_id=$1`, &result.MovedSettings},
			{`UPDATE import_lists SET series_quality_profile_id=$2 WHERE series_quality_profile_id=$1`, &result.MovedSettings},
			{`UPDATE seerr_library_routes SET quality_profile_id=$2 WHERE quality_profile_id=$1`, &result.MovedSettings},
		}
		for _, move := range moves {
			command, err := transaction.Exec(ctx, move.statement, id.String(), replaceWith.String())
			if err != nil {
				return result, err
			}
			*move.counter += command.RowsAffected()
		}
		if _, err := transaction.Exec(ctx, `UPDATE quality_profiles SET policy_version=GREATEST(policy_version,$2)+1 WHERE id=$1`, replaceWith.String(), profile.version); err != nil {
			return result, err
		}
	} else {
		var inUse bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflows WHERE quality_profile_id=$1)
			OR EXISTS(SELECT 1 FROM adopted_files WHERE quality_profile_id=$1)
			OR EXISTS(SELECT 1 FROM adoption_batches WHERE default_quality_profile_id=$1)
			OR EXISTS(SELECT 1 FROM adoption_items WHERE quality_profile_id=$1)
			OR EXISTS(SELECT 1 FROM import_lists WHERE movie_quality_profile_id=$1 OR series_quality_profile_id=$1)
			OR EXISTS(SELECT 1 FROM seerr_library_routes WHERE quality_profile_id=$1)`, id.String()).Scan(&inUse); err != nil {
			return result, err
		}
		if inUse {
			return result, ErrQualityProfileInUse
		}
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM quality_profiles WHERE id=$1`, id.String()); err != nil {
		// A reference created between the check and the delete.
		if isForeignKeyViolation(err) {
			return result, ErrQualityProfileInUse
		}
		return result, err
	}
	detail, _ := json.Marshal(result)
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,$2,'quality_profile.deleted','quality_profile',$3,$4,$5)`, uuid.NewV7().String(), actor, id.String(), string(detail), now); err != nil {
		return result, err
	}
	return result, transaction.Commit(ctx)
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
