package newznab

import (
	"slices"
	"testing"
)

// TestSecretSplitAcrossCommentAndProcInst proves the guarded transport's
// credential-echo guarantee: a credential split across an XML comment or a
// processing instruction is ONE logical text entry presented to InspectSecret
// containing the joined secret — not two separate entries that a naive
// per-entry inspector could miss. The test asserts on individual entries
// (never by concatenating all entries), so it fails against the old
// implementation that flushed separate text runs around comments/PIs.
func TestSecretSplitAcrossCommentAndProcInst(t *testing.T) {
	t.Parallel()
	secret := "SECRET_PART_1SECRET_PART_2"
	commentBody := []byte(`<caps><server title="local">SECRET_PART_1<!-- -->SECRET_PART_2</server></caps>`)
	_, texts, err := strictSingleRootScan(commentBody)
	if err != nil {
		t.Fatalf("comment-split scan: %v", err)
	}
	if !slices.ContainsFunc(texts, func(entry []byte) bool { return string(entry) == secret }) {
		t.Fatalf("comment split did not produce one joined logical entry: texts=%q", texts)
	}

	procBody := []byte(`<caps><server title="local">SECRET_PART_1<?pi data?>SECRET_PART_2</server></caps>`)
	_, texts, err = strictSingleRootScan(procBody)
	if err != nil {
		t.Fatalf("procinst-split scan: %v", err)
	}
	if !slices.ContainsFunc(texts, func(entry []byte) bool { return string(entry) == secret }) {
		t.Fatalf("procinst split did not produce one joined logical entry: texts=%q", texts)
	}
}

// TestNestedElementBoundariesRemainSeparate proves StartElement/EndElement
// remain logical text boundaries: element text around a nested child must
// NOT collapse into one "AAAINNERZZZ" run.
func TestNestedElementBoundariesRemainSeparate(t *testing.T) {
	t.Parallel()
	body := []byte(`<caps><a>AAA<c>INNER</c>ZZZ</a></caps>`)
	_, texts, err := strictSingleRootScan(body)
	if err != nil {
		t.Fatalf("nested scan: %v", err)
	}
	joined := func() []string {
		out := make([]string, 0, len(texts))
		for _, entry := range texts {
			out = append(out, string(entry))
		}
		return out
	}()
	if slices.Contains(joined, "AAAINNERZZZ") {
		t.Fatalf("nested boundaries collapsed into one run: texts=%q", texts)
	}
	for _, want := range []string{"AAA", "INNER", "ZZZ"} {
		if !slices.Contains(joined, want) {
			t.Fatalf("missing separate logical entry %q: texts=%q", want, texts)
		}
	}
}
