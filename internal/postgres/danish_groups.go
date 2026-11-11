package postgres

import (
	"context"
	"regexp"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/releases"
)

// Danish release-group record.
//
// Whether a release carries Danish subtitles or audio is only known after it
// is downloaded and measured. Nearly half of all downloads ended without the
// required Danish, and the release group behind a name predicts it well: some
// groups had Danish in every one of hundreds of downloads, others in almost
// none. This file learns each group's record from this installation's own
// measured outputs, separately for profiles that accept Danish subtitles or
// audio and for profiles that require Danish audio.

// danishGroupWindow is how far back measured outputs count.
const danishGroupWindow = "90 days"

// danishGroupCacheLifetime is how long one learned record is reused.
const danishGroupCacheLifetime = 30 * time.Minute

// DanishGroupRecord counts one release group's measured downloads under a
// Danish requirement: Verified had the required Danish, Missing did not.
type DanishGroupRecord struct {
	Verified int
	Missing  int
}

// Downloads is the number of measured downloads.
func (record DanishGroupRecord) Downloads() int { return record.Verified + record.Missing }

// Share is the smoothed share of downloads that had the required Danish.
func (record DanishGroupRecord) Share() float64 {
	return float64(record.Verified+1) / float64(record.Downloads()+4)
}

// DanishGroupRecords holds the records per release group, for profiles that
// accept Danish subtitles or audio (Subtitles) and for profiles that require
// Danish audio (Audio).
type DanishGroupRecords struct {
	Subtitles map[string]DanishGroupRecord
	Audio     map[string]DanishGroupRecord
}

// For returns the records that apply to a policy requiring Danish audio
// (audio) or Danish subtitles or audio (otherwise).
func (records DanishGroupRecords) For(audio bool) map[string]DanishGroupRecord {
	if audio {
		return records.Audio
	}
	return records.Subtitles
}

// Shares returns the smoothed share per group, for ranking.
func (records DanishGroupRecords) Shares(audio bool) map[string]float64 {
	shares := map[string]float64{}
	for group, record := range records.For(audio) {
		shares[group] = record.Share()
	}
	return shares
}

type danishGroupCache struct {
	mutex   sync.Mutex
	at      time.Time
	records DanishGroupRecords
}

var danishGroupCaches sync.Map // *pgxpool.Pool -> *danishGroupCache

var danishNameHint = regexp.MustCompile(releases.DanishSubtitleHintPattern)

// DanishGroupRecords returns the learned records, read at most every
// danishGroupCacheLifetime. Releases whose name already says Danish are left
// out: they are tried first anyway and would make a group look better than
// its untagged releases are. Titles originally in Danish are left out too:
// their own audio is Danish whatever the group.
func (store *Store) DanishGroupRecords(ctx context.Context) (DanishGroupRecords, error) {
	value, _ := danishGroupCaches.LoadOrStore(store.Pool, &danishGroupCache{})
	cache := value.(*danishGroupCache)
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	if !cache.at.IsZero() && time.Since(cache.at) < danishGroupCacheLifetime {
		return cache.records, nil
	}
	records, err := loadDanishGroupRecords(ctx, store.Pool)
	if err != nil {
		return DanishGroupRecords{}, err
	}
	cache.records, cache.at = records, time.Now()
	return records, nil
}

func loadDanishGroupRecords(ctx context.Context, pool *pgxpool.Pool) (DanishGroupRecords, error) {
	records := DanishGroupRecords{Subtitles: map[string]DanishGroupRecord{}, Audio: map[string]DanishGroupRecord{}}
	rows, err := pool.Query(ctx, `SELECT candidate.title,job.acquisition_outcome='output_verified',COALESCE(profile.policy->'required_audio_languages' ? 'da',false)
		FROM altmount_jobs job
		JOIN release_candidates candidate ON candidate.id=job.candidate_id
		JOIN workflows workflow ON workflow.id=job.workflow_id
		JOIN quality_profiles profile ON profile.id=workflow.quality_profile_id
		LEFT JOIN media_metadata metadata ON metadata.media_id=workflow.media_id
		WHERE job.acquisition_outcome IN ('output_verified','subtitle_requirements_unverified')
		  AND job.submission_started_at>now()-interval '`+danishGroupWindow+`'
		  AND (COALESCE(profile.policy->'required_subtitle_languages' ? 'da',false) OR COALESCE(profile.policy->'required_audio_languages' ? 'da',false))
		  AND COALESCE(metadata.data->>'original_language','') NOT IN ('da','dan')`)
	if err != nil {
		return records, err
	}
	defer rows.Close()
	for rows.Next() {
		var title string
		var verified, audio bool
		if err := rows.Scan(&title, &verified, &audio); err != nil {
			return records, err
		}
		group := releases.ReleaseGroup(title)
		if group == "" || danishNameHint.MatchString(title) {
			continue
		}
		target := records.Subtitles
		if audio {
			target = records.Audio
		}
		record := target[group]
		if verified {
			record.Verified++
		} else {
			record.Missing++
		}
		target[group] = record
	}
	return records, rows.Err()
}

// rankForDanish ranks releases for a profile that requires Danish, putting
// releases of groups that usually carry the required Danish first.
func (store *Store) rankForDanish(ctx context.Context, observations []releases.Observation, evaluations []releases.Evaluation, options releases.RankOptions, audio bool) ([]releases.Evaluation, error) {
	groups, err := store.DanishGroupRecords(ctx)
	if err != nil {
		return nil, err
	}
	options.DanishGroupShare = groups.Shares(audio)
	return releases.RankForDanishSubtitlesWith(observations, evaluations, options), nil
}
