package acquisition

import (
	"fmt"
	"slices"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
)

// A release whose name says nothing about Danish, from a release group that
// had the required Danish in at most one of every twenty of at least
// danishGroupMinimumDownloads measured downloads, is held instead of tried.
// Measured on this installation, such releases had Danish in 19 of 413
// downloads; trying them cost a grab and a slot of the round each, and the
// title then searched again. A held release stays visible with its reason and
// is never downloaded on its own. Releases with any Danish clue, groups with
// less record, and titles originally in Danish are not affected.
const (
	danishGroupMinimumDownloads = 20
	// danishGroupHoldRatio: held when Verified*danishGroupHoldRatio <= downloads (at most 5 %).
	danishGroupHoldRatio = 20
)

// DanishGroupRuleName names the hold in a release's explanation.
const DanishGroupRuleName = "Danish record of the release group"

// holdUnlikelyDanishGroups holds eligible releases from groups that almost
// never had the required Danish.
func holdUnlikelyDanishGroups(entries []postgres.EvaluatedObservation, policy subtitles.Policy, records postgres.DanishGroupRecords) {
	audio := slices.Contains(policy.RequiredAudio, "da")
	if !audio && !slices.Contains(policy.Required, "da") || policy.OriginalLanguage == "da" {
		return
	}
	record := records.For(audio)
	for index := range entries {
		entry := &entries[index]
		if entry.Evaluation.Verdict != releases.VerdictEligible || releases.DanishEvidence(entry.Observation) > 0 {
			continue
		}
		group := releases.ReleaseGroup(entry.Observation.Title)
		seen, ok := record[group]
		if group == "" || !ok || seen.Downloads() < danishGroupMinimumDownloads || seen.Verified*danishGroupHoldRatio > seen.Downloads() {
			continue
		}
		evaluation := entry.Evaluation
		evaluation.Verdict = releases.VerdictHeld
		evaluation.Explanations = append(append([]releases.Explanation(nil), evaluation.Explanations...), releases.Explanation{
			Rule:          DanishGroupRuleName,
			Fact:          "release_group",
			Decision:      releases.DecisionFail,
			Values:        []string{group},
			Reason:        fmt.Sprintf("release group %s had the required Danish in %d of %d measured downloads", group, seen.Verified, seen.Downloads()),
			RuleKind:      releases.RuleRequired,
			Category:      releases.CategoryDanishLanguage,
			EvidenceState: releases.FactInferred,
		})
		entry.Evaluation = evaluation
	}
}
