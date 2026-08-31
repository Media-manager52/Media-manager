package acquisition

import "testing"

func TestReleaseRetrievalRequestLimitIsTwo(t *testing.T) {
	if maxReleaseRetrievalAttempts != 2 {
		t.Fatalf("retrieval request limit=%d, want 2", maxReleaseRetrievalAttempts)
	}
}
