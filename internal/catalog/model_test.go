package catalog

import (
	"errors"
	"testing"
	"time"
)

func TestVerifiedProviderIdentityValidation(t *testing.T) {
	_, err := New(KindMovie, "Example", pointer(uint16(2026)), []ExternalIdentity{{Namespace: "tmdb", Value: "../1", State: IdentityVerified, Source: "test"}}, time.Now())
	if !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("unsafe provider identity was accepted: %v", err)
	}
	if _, err := New(KindSeries, "Example", pointer(uint16(2026)), []ExternalIdentity{{Namespace: "tvdb", Value: "12345", State: IdentityVerified, Source: "test"}}, time.Now()); err != nil {
		t.Fatalf("valid TVDB identity was rejected: %v", err)
	}
}

func pointer[T any](value T) *T { return &value }
