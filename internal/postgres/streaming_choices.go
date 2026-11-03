package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"uuid"
)

// StreamingChoice is one release a viewer may pick from a streaming stream
// list. It survives a Media Manager restart so a list the player still shows
// keeps working until it expires. It never holds a credential: the indexer is
// named by id and a fingerprint of its configuration, and the locator is the
// sanitized reference retrieval already persists for release candidates.
type StreamingChoice struct {
	Token                  uuid.UUID
	Request                json.RawMessage
	IntegrationID          uuid.UUID
	IntegrationFingerprint [32]byte
	NativeGUID             string
	Title                  string
	LocatorRef             string
	CreatedAt              time.Time
	ExpiresAt              time.Time
}

// IntegrationFingerprint identifies an integration's exact retrieval
// configuration. A stored choice is usable only while the instance still has
// this fingerprint, matching the in-memory rule that an edited indexer
// invalidates its open stream lists.
func IntegrationFingerprint(integration Integration) [32]byte {
	hash := sha256.New()
	for _, part := range []string{integration.Kind, integration.SecretRef, string(integration.Config)} {
		hash.Write([]byte(part))
		hash.Write([]byte{0})
	}
	var sum [32]byte
	copy(sum[:], hash.Sum(nil))
	return sum
}

// SaveStreamingChoices stores one stream list's choices and prunes expired
// rows. The insert is one statement, so a list is stored whole or not at all.
func (store *Store) SaveStreamingChoices(ctx context.Context, choices []StreamingChoice) error {
	if len(choices) == 0 {
		return nil
	}
	tokens := make([]string, len(choices))
	requests := make([]string, len(choices))
	integrations := make([]string, len(choices))
	fingerprints := make([][]byte, len(choices))
	guids := make([]string, len(choices))
	titles := make([]string, len(choices))
	locators := make([]string, len(choices))
	created := make([]time.Time, len(choices))
	expires := make([]time.Time, len(choices))
	for index, choice := range choices {
		fingerprint := choice.IntegrationFingerprint
		tokens[index] = choice.Token.String()
		requests[index] = string(choice.Request)
		integrations[index] = choice.IntegrationID.String()
		fingerprints[index] = fingerprint[:]
		guids[index] = choice.NativeGUID
		titles[index] = choice.Title
		locators[index] = choice.LocatorRef
		created[index] = choice.CreatedAt.UTC()
		expires[index] = choice.ExpiresAt.UTC()
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	if _, err := transaction.Exec(ctx, `DELETE FROM streaming_choices WHERE expires_at <= $1`, time.Now().UTC()); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO streaming_choices(token,request,integration_id,integration_fingerprint,native_guid,title,locator_ref,created_at,expires_at)
		SELECT * FROM unnest($1::uuid[],$2::jsonb[],$3::uuid[],$4::bytea[],$5::text[],$6::text[],$7::text[],$8::timestamptz[],$9::timestamptz[])
		ON CONFLICT (token) DO NOTHING`, tokens, requests, integrations, fingerprints, guids, titles, locators, created, expires); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

// StreamingChoiceByToken returns an unexpired stored choice.
func (store *Store) StreamingChoiceByToken(ctx context.Context, token uuid.UUID, now time.Time) (StreamingChoice, bool, error) {
	var choice StreamingChoice
	var tokenText, integrationText string
	var fingerprint []byte
	err := store.Pool.QueryRow(ctx, `SELECT token::text,request,integration_id::text,integration_fingerprint,native_guid,title,locator_ref,created_at,expires_at
		FROM streaming_choices WHERE token=$1 AND expires_at > $2`, token.String(), now.UTC()).
		Scan(&tokenText, &choice.Request, &integrationText, &fingerprint, &choice.NativeGUID, &choice.Title, &choice.LocatorRef, &choice.CreatedAt, &choice.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StreamingChoice{}, false, nil
	}
	if err != nil {
		return StreamingChoice{}, false, err
	}
	if choice.Token, err = uuid.Parse(tokenText); err != nil {
		return StreamingChoice{}, false, err
	}
	if choice.IntegrationID, err = uuid.Parse(integrationText); err != nil {
		return StreamingChoice{}, false, err
	}
	if len(fingerprint) != len(choice.IntegrationFingerprint) {
		return StreamingChoice{}, false, errors.New("stored streaming choice has an invalid integration fingerprint")
	}
	copy(choice.IntegrationFingerprint[:], fingerprint)
	return choice, true, nil
}
