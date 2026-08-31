package acquisition

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
)

func TestReleaseRetrievalRequestLimitIsTwo(t *testing.T) {
	if maxReleaseRetrievalAttempts != 2 {
		t.Fatalf("retrieval request limit=%d, want 2", maxReleaseRetrievalAttempts)
	}
}

func TestDeferredIndexerPacingDoesNotConsumeRetrievalAttemptLimit(t *testing.T) {
	cause := indexers.CooldownError{Until: time.Now().Add(time.Minute)}
	err := (&RetrievalService{}).retryOrRecordRetrievalFailure(context.Background(), postgres.OutboxEvent{Attempts: 99}, postgres.RetrievalContext{}, cause)
	var deferred indexers.CooldownError
	if !errors.As(err, &deferred) || deferred.RetryAt() != cause.RetryAt() {
		t.Fatalf("deferred pacing became a release failure: %v", err)
	}
}
