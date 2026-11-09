package commissioningstore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
)

func openStore(ctx context.Context, databaseURL string) (*Commissioning, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Commissioning{Pool: pool}, nil
}

func (store *Commissioning) Close() { store.Pool.Close() }

func activatableCapabilityProfileDocument(t *testing.T) json.RawMessage {
	t.Helper()
	return activatableCapabilityProfileDocumentWithContract(t, newznab.ProviderContractRef{})
}

func activatableCapabilityProfileDocumentWithContract(t *testing.T, contract newznab.ProviderContractRef) json.RawMessage {
	t.Helper()
	const evidenceKey = "evidence.00000000000000000000000000000001"
	profile := newznab.CapabilityProfile{
		SchemaVersion: newznab.CapabilityProfileSchemaVersion,
		SearchRecipes: []newznab.SearchRecipe{{
			Key:   "movie-imdb",
			Scope: newznab.SearchScopeMovie,
			Mode:  newznab.SearchModeMovie,
			Query: newznab.QuerySpec{Style: newznab.QueryTitle},
			Bindings: []newznab.WireBinding{{
				Parameter: newznab.WireIMDbID,
				Source:    newznab.BindingIMDbNumeric,
			}},
			Extended:      true,
			Categories:    newznab.CategorySpec{Set: newznab.CategorySelectedMovie},
			VerifiedLimit: 50,
			Role:          newznab.RecipePrimary,
			Priority:      1,
			Verification: newznab.RecipeVerification{
				State:        newznab.VerificationVerified,
				EvidenceKeys: []string{evidenceKey},
			},
		}},
		Operations:  []newznab.OperationCapability{},
		MappingRefs: []newznab.MappingRef{{Key: newznab.MappingNewznabSearchV1, Version: 1}},
		Evidence: []newznab.CommissionEvidence{{
			Key:                    evidenceKey,
			Kind:                   newznab.EvidenceKindControlledProbe,
			Target:                 newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "movie-imdb"},
			ObservedAt:             time.Unix(1, 0).UTC(),
			BodySHA256:             strings.Repeat("a", 64),
			SafeOutcome:            newznab.EvidenceOutcomeSearchSuccess,
			RawResultCount:         1,
			PlausibleIdentityCount: 1,
		}},
	}
	if contract.Key != "" {
		profile.ProviderContract = &contract
	}
	if err := newznab.ValidateCapabilityProfileForActivation(profile); err != nil {
		t.Fatalf("test profile: %v", err)
	}
	body, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
