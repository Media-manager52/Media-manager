package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/prowlarrbackup"
)

var secretReference = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,127}$`)

type ProwlarrBackupImport struct {
	ID            uuid.UUID                  `json:"id"`
	State         string                     `json:"state"`
	ParserVersion string                     `json:"parser_version"`
	CreatedAt     time.Time                  `json:"created_at"`
	CompletedAt   *time.Time                 `json:"completed_at,omitempty"`
	ErrorDetail   string                     `json:"error_detail,omitempty"`
	Candidates    []ProwlarrIndexerCandidate `json:"candidates"`
}

type ProwlarrIndexerCandidate struct {
	ID                uuid.UUID       `json:"id"`
	ImportID          uuid.UUID       `json:"import_id"`
	Name              string          `json:"name"`
	Implementation    string          `json:"implementation"`
	BaseURL           string          `json:"base_url"`
	Categories        []int           `json:"categories"`
	SanitizedConfig   json.RawMessage `json:"sanitized_config"`
	SecretRef         string          `json:"secret_ref,omitempty"`
	ReviewState       string          `json:"review_state"`
	AttentionDetail   string          `json:"attention_detail,omitempty"`
	CreatedInstanceID *uuid.UUID      `json:"created_instance_id,omitempty"`
}

func (store *Store) CreateProwlarrImport(ctx context.Context, digest []byte, parsed []prowlarrbackup.Candidate, now time.Time) (ProwlarrBackupImport, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return ProwlarrBackupImport{}, err
	}
	defer transaction.Rollback(ctx)
	var existing string
	if err := transaction.QueryRow(ctx, `SELECT id::text FROM prowlarr_backup_imports WHERE backup_sha256=$1`, digest).Scan(&existing); err == nil {
		if err := transaction.Commit(ctx); err != nil {
			return ProwlarrBackupImport{}, err
		}
		id, _ := uuid.Parse(existing)
		return store.ProwlarrImport(ctx, id)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ProwlarrBackupImport{}, err
	}
	importID := uuid.NewV7()
	if _, err := transaction.Exec(ctx, `INSERT INTO prowlarr_backup_imports(id,backup_sha256,state,parser_version,created_at) VALUES($1,$2,'review',$3,$4)`, importID.String(), digest, prowlarrbackup.ParserVersion, now.UTC()); err != nil {
		return ProwlarrBackupImport{}, err
	}
	for _, candidate := range parsed {
		candidateID := uuid.NewV7()
		categories, _ := json.Marshal(candidate.Categories)
		if _, err := transaction.Exec(ctx, `INSERT INTO prowlarr_indexer_candidates(id,import_id,name,implementation,base_url,categories,sanitized_config,secret_ref,review_state,attention_detail) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,NULLIF($10,''))`, candidateID.String(), importID.String(), candidate.Name, candidate.Implementation, candidate.BaseURL, string(categories), string(candidate.SanitizedConfig), candidate.SuggestedSecretRef, candidate.ReviewState, candidate.AttentionDetail); err != nil {
			return ProwlarrBackupImport{}, err
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return ProwlarrBackupImport{}, err
	}
	return store.ProwlarrImport(ctx, importID)
}

func (store *Store) ProwlarrImport(ctx context.Context, importID uuid.UUID) (ProwlarrBackupImport, error) {
	var result ProwlarrBackupImport
	var idText string
	err := store.Pool.QueryRow(ctx, `SELECT id::text,state,parser_version,created_at,completed_at,COALESCE(error_detail,'') FROM prowlarr_backup_imports WHERE id=$1`, importID.String()).Scan(&idText, &result.State, &result.ParserVersion, &result.CreatedAt, &result.CompletedAt, &result.ErrorDetail)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProwlarrBackupImport{}, ErrNotFound
	}
	if err != nil {
		return ProwlarrBackupImport{}, err
	}
	result.ID, _ = uuid.Parse(idText)
	rows, err := store.Pool.Query(ctx, `SELECT id::text,import_id::text,name,implementation,base_url,categories,sanitized_config,COALESCE(secret_ref,''),review_state,COALESCE(attention_detail,''),created_instance_id::text FROM prowlarr_indexer_candidates WHERE import_id=$1 ORDER BY name,id`, importID.String())
	if err != nil {
		return ProwlarrBackupImport{}, err
	}
	defer rows.Close()
	result.Candidates = []ProwlarrIndexerCandidate{}
	for rows.Next() {
		var candidate ProwlarrIndexerCandidate
		var candidateText, candidateImportText string
		var categories, config []byte
		var createdText *string
		if err := rows.Scan(&candidateText, &candidateImportText, &candidate.Name, &candidate.Implementation, &candidate.BaseURL, &categories, &config, &candidate.SecretRef, &candidate.ReviewState, &candidate.AttentionDetail, &createdText); err != nil {
			return ProwlarrBackupImport{}, err
		}
		candidate.ID, _ = uuid.Parse(candidateText)
		candidate.ImportID, _ = uuid.Parse(candidateImportText)
		if err := json.Unmarshal(categories, &candidate.Categories); err != nil {
			return ProwlarrBackupImport{}, err
		}
		candidate.SanitizedConfig = append(json.RawMessage(nil), config...)
		if createdText != nil {
			created, parseErr := uuid.Parse(*createdText)
			if parseErr != nil {
				return ProwlarrBackupImport{}, parseErr
			}
			candidate.CreatedInstanceID = &created
		}
		result.Candidates = append(result.Candidates, candidate)
	}
	return result, rows.Err()
}

func (store *Store) ApproveProwlarrImport(ctx context.Context, importID uuid.UUID, approvals map[uuid.UUID]string, now time.Time) (ProwlarrBackupImport, error) {
	transaction, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return ProwlarrBackupImport{}, err
	}
	defer transaction.Rollback(ctx)
	var state string
	if err := transaction.QueryRow(ctx, `SELECT state FROM prowlarr_backup_imports WHERE id=$1 FOR UPDATE`, importID.String()).Scan(&state); errors.Is(err, pgx.ErrNoRows) {
		return ProwlarrBackupImport{}, ErrNotFound
	} else if err != nil {
		return ProwlarrBackupImport{}, err
	}
	if state == "complete" {
		if err := transaction.Commit(ctx); err != nil {
			return ProwlarrBackupImport{}, err
		}
		return store.ProwlarrImport(ctx, importID)
	}
	if state != "review" && state != "approved" {
		return ProwlarrBackupImport{}, errors.New("Prowlarr import is not reviewable")
	}
	rows, err := transaction.Query(ctx, `SELECT id::text,name,base_url,sanitized_config,review_state FROM prowlarr_indexer_candidates WHERE import_id=$1 ORDER BY id FOR UPDATE`, importID.String())
	if err != nil {
		return ProwlarrBackupImport{}, err
	}
	type candidateRow struct {
		id, name, baseURL, review string
		config                    []byte
	}
	var candidates []candidateRow
	for rows.Next() {
		var candidate candidateRow
		if err := rows.Scan(&candidate.id, &candidate.name, &candidate.baseURL, &candidate.config, &candidate.review); err != nil {
			rows.Close()
			return ProwlarrBackupImport{}, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ProwlarrBackupImport{}, err
	}
	rows.Close()
	approved := 0
	for _, candidate := range candidates {
		if candidate.review != "pending" {
			continue
		}
		candidateID, _ := uuid.Parse(candidate.id)
		secretRef := strings.TrimSpace(approvals[candidateID])
		if secretRef == "" {
			if _, err := transaction.Exec(ctx, `UPDATE prowlarr_indexer_candidates SET review_state='rejected' WHERE id=$1`, candidate.id); err != nil {
				return ProwlarrBackupImport{}, err
			}
			continue
		}
		if !secretReference.MatchString(secretRef) || candidate.baseURL == "" || !json.Valid(candidate.config) {
			return ProwlarrBackupImport{}, errors.New("approved Prowlarr candidate has invalid configuration")
		}
		canonicalConfig, canonicalURL, err := canonicalNewznabConfig(candidate.config, false)
		if err != nil {
			return ProwlarrBackupImport{}, err
		}
		if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "newznab:"+canonicalURL); err != nil {
			return ProwlarrBackupImport{}, err
		}
		var duplicate bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integration_instances WHERE kind='newznab' AND config->>'base_url'=$1)`, canonicalURL).Scan(&duplicate); err != nil {
			return ProwlarrBackupImport{}, err
		}
		if duplicate {
			return ProwlarrBackupImport{}, fmt.Errorf("%w: %s", ErrDuplicateIndexerURL, canonicalURL)
		}
		instanceID := uuid.NewV7()
		if _, err := transaction.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,'newznab',$2,false,false,$3,$4,$5,$5)`, instanceID.String(), candidate.name, string(canonicalConfig), secretRef, now.UTC()); err != nil {
			return ProwlarrBackupImport{}, err
		}
		if _, err := transaction.Exec(ctx, `UPDATE prowlarr_indexer_candidates SET review_state='approved',secret_ref=$2,created_instance_id=$3 WHERE id=$1`, candidate.id, secretRef, instanceID.String()); err != nil {
			return ProwlarrBackupImport{}, err
		}
		approved++
	}
	if _, err := transaction.Exec(ctx, `UPDATE prowlarr_backup_imports SET state='complete',completed_at=$2 WHERE id=$1`, importID.String(), now.UTC()); err != nil {
		return ProwlarrBackupImport{}, err
	}
	detail, _ := json.Marshal(map[string]any{"approved": approved, "reviewed": len(candidates)})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','prowlarr.backup_imported','prowlarr_import',$2,$3,$4)`, uuid.NewV7().String(), importID.String(), string(detail), now.UTC()); err != nil {
		return ProwlarrBackupImport{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return ProwlarrBackupImport{}, err
	}
	return store.ProwlarrImport(ctx, importID)
}
