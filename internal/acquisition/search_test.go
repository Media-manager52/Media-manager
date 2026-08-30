package acquisition

import (
	"testing"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
)

func TestConstrainSeriesMatchRequiresPositiveIdentityOrYear(t *testing.T) {
	year := uint16(2005)
	media := catalog.MediaItem{
		Kind:  catalog.KindSeries,
		Title: "The Office",
		Year:  &year,
		Identities: []catalog.ExternalIdentity{{
			Namespace: "tvdb",
			Value:     "73244",
			State:     catalog.IdentityVerified,
		}},
	}
	tests := []struct {
		name        string
		title       string
		facts       []releases.Fact
		wantVerdict releases.Verdict
	}{
		{name: "title only held", title: "The.Office.S01.1080p", wantVerdict: releases.VerdictHeld},
		{name: "canonical year accepted", title: "The.Office.2005.S01.1080p", wantVerdict: releases.VerdictEligible},
		{name: "matching identity accepted", title: "The.Office.S01.1080p", facts: []releases.Fact{{Name: "tvdb_id", Values: []string{"73244"}, State: releases.FactObserved}}, wantVerdict: releases.VerdictEligible},
		{name: "wrong identity rejected", title: "The.Office.S01.1080p", facts: []releases.Fact{{Name: "tvdb_id", Values: []string{"99999"}, State: releases.FactObserved}}, wantVerdict: releases.VerdictRejected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observation := releases.Observation{Title: test.title, Facts: test.facts}
			evaluation := releases.Evaluation{Verdict: releases.VerdictEligible}
			if got := constrainMediaMatch(observation, evaluation, media).Verdict; got != test.wantVerdict {
				t.Fatalf("verdict=%q, want %q", got, test.wantVerdict)
			}
		})
	}
}
