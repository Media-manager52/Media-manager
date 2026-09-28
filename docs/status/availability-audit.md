# Availability audit — 2026-09-28

Answers the owner's question from
[the availability brief](../onboarding/agent-briefs/claude-availability-audit.md):
are the missing titles truly unavailable with Danish subtitles, or blocked by
the pipeline? Everything below was measured read-only against the live
database, AltMount logs, and Plex on 2026-09-28 at about 19:50–21:30 UTC
(`main` at `399dcf4` deployed, schema 40). No live search, grab, round,
blocklist, or provider change was made.

## Answer

Of 169 non-terminal workflows, **5 are "tried every eligible release, no
Danish found"**. The rest are blocked by pipeline behavior or have not been
tried. Seven defects were found (D1–D7); five are fixed on `main` (not yet
deployed), and one (D7) needs an owner design decision.

| Verdict (before the fixes below) | Workflows |
| --- | ---: |
| blocked by provider | 54 |
| blocked by policy/round limit | 23 |
| blocked by archives | 4 |
| blocked by pipeline attention | 11 |
| not yet tried | 9 |
| held (no eligible release) | 8 |
| no eligible release | 19 |
| no release observed anywhere | 32 |
| no da release observed anywhere | 5 |
| request revoked (owner) | 4 |
| verified da | 0 |
| verified without da | 0 |
| **total** | **169** |

Verdicts are assigned by explicit rules, first match wins: verified with `da`
→ verified without `da` → Seerr revoked → pipeline attention code → pending
retrieval deferred by a grab budget or `release_retrieval_failed` (provider) →
no candidate ever observed → no attempt yet (untried eligible → "not yet
tried"; held candidates → "held"; otherwise "no eligible release") → attempts
made with untried eligible releases left and the round exhausted → archive
exclusions only → otherwise "no da release observed anywhere".

Key facts behind the table:

- 960 eligible, never-attempted releases with **explicit Danish-subtitle
  evidence** sit in 22 open workflows (871 of them in provider-blocked ones).
- Explicit `da` evidence is decisive: all 88 past downloads with an explicit
  `da` fact verified Danish subtitles; 21 of 204 (10%) without any marker did.
- The subtitle verifier has no false negatives (H3), so every recorded
  "no Danish" is true for that release.

## Defects

| # | Defect | Evidence | Status |
| --- | --- | --- | --- |
| D1 | A selection stays pinned to an indexer whose grab budget (100/24h) is exhausted | 45 workflows waiting until the next morning (44 on NZBFinder); at least 23 had identical releases at the same score on indexers with 1–63 of 100 grabs used | Fixed `3486ab3`: move to the identical release on an indexer with budget |
| D2 | No-selection searches repeat every 30 minutes on all nine indexers | Every indexer hit its 1,000-query budget in 24h; zero-result titles used 2,144 of ~8,970 queries; one title ran 49 times a day | Fixed `d34e7bf`: back off 30m → 12h per round |
| D3 | Danish letters break title matching and the query | No title with æ/ø/å ever confirmed; 9 of 10 never observed a release; 7 genuine `Forraeder.Under.Kutten…DANiSH` releases rejected as wrong title | Fixed `cc57d7e`: scene spellings for match and query |
| D4 | Movie year hold ignores an exact provider identity | 1,041 held candidates in 23 workflows, **all** with an IMDb/TMDB attribute matching the verified identity (off-by-one years such as A Monster Calls 2017 for 2016); 31 with explicit `da` | Fixed `f9c8b2a`: exact identity satisfies the year check |
| D5 | Pre-upload package rejections consume a round attempt | 60 of 67 archive failures never reached AltMount yet used a slot (46 workflows; A Monster Calls exhausted with 341 untried, 3 explicit `da`) | Mostly moot after D6; follow-up below |
| D6 | Pre-upload screen rejects every RAR/7-Zip set | AltMount analyzed 84 RAR sets in 72h, 77 succeeded (plus nested RAR and RAR-in-7z), including Media Manager's own DKsubs/NORDiC uploads. The screen then rejected NCIS S13/S14/S16 NORDiC, NCIS S19 DKsubs, Lucifer S03 NORDiC, … | Fixed `23c95a3`: RAR/7z submitted; AltMount failures still excluded |
| D7 | Complete-season workflows accept only season packs; Danish TV is posted per episode | Forbrydelser der rystede Danmark S01–S06: 187 episode releases (47 explicit `da`) all rejected; Extreme Makeover S01–S05: 85 explicit `da`; Rivalerne S01: 10 of 11 | **Owner decision** (touches hard rule 3) |

## Hypotheses

**H1 — ranking.** Explicit `da` facts add 100,000 and always outrank
no-evidence releases (Lanterns NORDiC: 153,300 vs 53,300). Ranking is not the
problem; *which* evidence is visible is. Subtitle attributes come from the
indexer that returned them (mostly NZBFinder), so the identical release title
on another indexer scores lower. D1's cross-post switch uses the identical
title regardless of score. Title markers are weak evidence: Resident Evil's
no-Danish release was from group `DKS`, and too few NORDiC/DANiSH-marked
downloads without an attribute exist to measure their hit rate (1). Keeping
them out of the fact set is correct; a tie-breaker is not justified yet.

**H2 — archives.** Confirmed and fixed as D6; D5 covers the slot accounting.
The pre-upload screen had rejected 60 releases since it shipped; AltMount
itself streams about 92% of RAR sets. Archives AltMount cannot stream still
fail at queue time and are excluded as release-attributable. Storage: a failed
archive import produces no output inventory, so it adds nothing to the
four-output retention cap.

**H3 — verifier false negatives.** None. Criminal Minds S16 is a correct
failure: a mixed-source pack whose E01–E09 (Amazon) carry only `eng` + `eng`
SDH tracks, while E10 (Disney+) has a `dan` SubRip track. The `da` in
`observed_languages` is the union across files. Across all 188 unverified
verifications: no Danish-titled track under a non-Danish tag, no unpaired
subtitle file in any failed output. The only untagged tracks (23) are one per
episode in an English-only iVy pack. Cosmetic follow-up: show per-file
coverage ("da in 1 of 10 files").

**H4 — query construction.** No indexer has an active capability profile, so
all nine use the legacy dialect: `q=<title>` plus `imdbid`/`tmdbid` where
supported, a single request, `limit` 100 (treasure-maps 500). D3 fixes the
title spelling. The 32 titles with zero results split into: 10 with Danish
letters (D3); 3 airing seasons whose episodes run as separate workflows (by
design); NCIS S24, which premieres on 2026-10-06; 9 other Danish originals
(revues, stand-up, small Danish films, for example Cirkusrevyen 2006, Masser
af Passer, Simon Talbot: Optur); and 9 English or niche titles (Jackass
specials, featurettes, WWE SummerSlam 2026: Saturday, Grinebidder). The last
two groups were queried 10–13 times today on all nine indexers with the
spelling unaffected by D3 and returned nothing: plausibly genuinely absent
from these indexers. Latent issue: capability-profile
fallback recipes are planned but never executed; harmless until a profile is
activated.

**H5 — providers.** Per indexer over 7 days: treasure-maps is structurally
broken for grabs (32 HTTP 403s across 22 workflows, zero successful
downloads, yet it still spent 34 grabs in 24h); msgnews works since the
canonical-host fix (no failures after 16:08 UTC; its recent downloads were all
lost to the D6 archive screen); nzb.life (14 downloads) and altHUB (1) work;
DrunkenSlug/NZBFinder had 2 invalid-NZB failures each. Grab budgets, not
failures, are the dominant provider constraint (D1).

**H6 — known candidates cannot open a round.** Confirmed: 23 workflows have
eligible untried releases (6 with explicit `da`) behind an exhausted round
and wait for a *newly* observed release. Proposed owner action (not
implemented): `POST /api/v1/workflows/{id}/acquisition-rounds/from-known`
opens one two-slot round from existing never-attempted, never-excluded
eligible candidates, ordered explicit `da` → Danish/Nordic title marker →
score. Storage math: each failed output is retained; the cap is 4 per
workflow, so a sweep can spend at most `4 − retained` more downloads before
storage review. Archive and retrieval failures produce no output and do not
count. For the 23 workflows at most 23 × 2 = 46 downloads per sweep, bounded
by the existing grab budgets.

**H7 — parsing holds.** Hold causes on open workflows: `requested movie
year` 1,041 candidates (23 workflows) — fixed by D4; `series identity
evidence` 541 (62); `Complete season pack` 176 (39); `Requested season` 12
(10). Season-word forms (`NCIS 2003 Season 14`, `Seizoen 22`) are a real
parser gap, but nearly every example is a Dutch-subtitle pack (`NL Subs
ingebakken`, burned-in Dutch), so fixing it would mostly yield downloads that
fail verification. Low value; not changed.

## Owner decisions

1. **Deploy** `23c95a3`, `f9c8b2a`, `cc57d7e`, `d34e7bf`, `3486ab3` (no schema
   change). Expected effect: the 45 grab-budget waits move to other indexers,
   query budgets recover, Danish-letter titles and year-offset movies become
   selectable, and NORDiC/DKsubs RAR releases are submitted.
2. **Revive the 60 screen-rejected archive releases.** Their exclusions and
   attempt records predate D6 and keep them disqualified. Scope: exclusions
   with `failure_kind='archive_unsupported'` whose job never reached AltMount
   (`external_queue_id IS NULL`, detail starting "release package rejected
   before AltMount submission"). This is a live data change and needs
   approval and a supported path (a migration or an owner endpoint).
3. **D7: per-episode acquisition for finished seasons without packs.**
   Extend `DispatchAiringSeason` (already used for airing seasons) to
   completed seasons where only episode releases are observed. Unblocks
   Forbrydelser der rystede Danmark, Extreme Makeover, Rivalerne, Reacher.
4. **H6: open a round from known candidates** (design above).
5. **treasure-maps:** re-commission the account/credentials or disable grabs
   there; it spends budget and fails every download with HTTP 403.
6. **D5 follow-up:** screen packages before the submission intent so the
   remaining pre-upload rejections (zip/tar, ISO) do not consume a slot.
7. Budgets: every indexer uses the conservative default (1,000 queries / 100
   grabs per 24h). Raising them to the providers' real limits is an owner
   configuration choice.

## Truth table (snapshot before the fixes)

| Title | Scope | Kind | Profile | State | Observed | Explicit da | Eligible untried (da) | Attempts | Languages seen | Exclusions | Verdict |
| --- | --- | --- | --- | --- | ---: | ---: | ---: | ---: | --- | --- | --- |
| 1000 Men and Me: The Bonnie Blue Story | default | movie 2025 | requires da | candidate_selected | 92 | 0 | 88 (0) | 4 | eng | archive_unsupported=1,subtitle_verification=3 | blocked by provider — grab budget: NZBFinder {DK} |
| An American Crime | default | movie 2007 | requires da | candidate_selected | 221 | 0 | 185 (0) | 2 | eng,ger | subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| Bedeviled | default | movie 2017 | requires da | candidate_selected | 124 | 4 | 8 (0) | 2 | — | archive_unsupported=1,subtitle_verification=1 | blocked by provider — grab budget: NZBFinder {DK} |
| Chum | default | movie 2026 | requires da | candidate_selected | 144 | 0 | 141 (0) | 3 | eng,fre | subtitle_verification=3 | blocked by provider — grab budget: NZBFinder {DK} |
| Criminal Minds | season:16 | series 2005 | requires da | candidate_selected | 601 | 28 | 17 (0) | 3 | chi,cze,da,dut,eng,fin,fre,ger,gre,hun,ita,jpn,kor,nor,pol,por,rum,slo,spa,swe,tur | archive_unsupported=1,release_retrieval=1,season_pack_incomplete=1,subtitle_verification=1 | blocked by provider — grab budget: NZBFinder {DK} |
| CSI: Crime Scene Investigation | season:01 | series 2000 | requires da | candidate_selected | 528 | 1 | 10 (0) | 2 | eng | archive_unsupported=1,release_retrieval=2,subtitle_verification=1 | blocked by provider — grab budget: NZBFinder {DK} |
| CSI: Crime Scene Investigation | season:02 | series 2000 | requires da | candidate_selected | 515 | 0 | 10 (0) | 4 | eng | archive_unsupported=2,release_incomplete=1,release_retrieval=1,subtitle_verification=1 | blocked by provider — grab budget: NZBFinder {DK} |
| CSI: Crime Scene Investigation | season:03 | series 2000 | requires da | candidate_selected | 502 | 0 | 5 (0) | 2 | eng | release_retrieval=1,subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| CSI: Crime Scene Investigation | season:04 | series 2000 | requires da | candidate_selected | 503 | 0 | 11 (0) | 2 | eng | season_pack_incomplete=1,subtitle_verification=1 | blocked by provider — grab budget: NZBFinder {DK} |
| CSI: Crime Scene Investigation | season:07 | series 2000 | requires da | needs_attention (release_retrieval_failed) | 527 | 0 | 10 (0) | 2 | eng | release_incomplete=1,release_retrieval=2,subtitle_verification=1 | blocked by provider |
| CSI: Crime Scene Investigation | season:13 | series 2000 | requires da | needs_attention (release_retrieval_failed) | 522 | 0 | 9 (0) | 2 | ara,chi,dut,eng,fin,hin,ita,kor,pol,rus,swe,tur | release_incomplete=1,release_retrieval=2,subtitle_verification=1 | blocked by provider |
| CSI: Crime Scene Investigation | season:14 | series 2000 | requires da | needs_attention (release_retrieval_failed) | 528 | 0 | 6 (0) | 2 | ara,chi,dut,eng,fre,hin,ita,kor,pol,rus,tur | release_incomplete=1,release_retrieval=2,subtitle_verification=1 | blocked by provider |
| CSI: Crime Scene Investigation | season:15 | series 2000 | requires da | candidate_selected | 512 | 0 | 8 (0) | 2 | ara,chi,dut,eng,fre,hin,ita,kor,pol,rus,tur | release_incomplete=1,release_retrieval=1,subtitle_verification=1 | blocked by provider — grab budget: NZBFinder {DK} |
| Dexter | season:06 | series 2006 | requires da | candidate_selected | 980 | 36 | 21 (1) | 3 | eng,ger,ita | archive_unsupported=1,release_retrieval=1,subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| Dexter | season:07 | series 2006 | requires da | needs_attention (release_retrieval_failed) | 973 | 2 | 23 (1) | 4 | eng | archive_unsupported=1,output_verification=1,release_incomplete=1,release_retrieval=1,subtitle_verification=1 | blocked by provider |
| Dexter | season:08 | series 2006 | requires da | candidate_selected | 985 | 2 | 22 (1) | 3 | eng | archive_unsupported=1,output_verification=1,subtitle_verification=1 | blocked by provider — grab budget: NZBFinder {DK} |
| Game of Thrones | season:05 | series 2011 | requires da | candidate_selected | 700 | 146 | 25 (0) | 3 | eng | no_playable_media=1,subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| Grand Theft Auto VI: An Extended Look | default | movie 2026 | requires da | candidate_selected | 136 | 0 | 131 (0) | 3 | chi,eng,fre,ger,ita,jpn,kor,pol,por,rus,spa | release_retrieval=1,subtitle_verification=3 | blocked by provider — grab budget: NZBFinder {DK} |
| Hadestown: The Musical | default | movie 2026 | requires da | candidate_selected | 55 | 0 | 53 (0) | 2 | eng | subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| Harold & Kumar Go to Amsterdam | default | movie 2008 | requires da | needs_attention (release_retrieval_failed) | 7 | 0 | 3 (0) | 2 | — | release_retrieval=1,subtitle_verification=2 | blocked by provider |
| I Want Your Sex | default | movie 2026 | requires da | candidate_selected | 119 | 0 | 103 (0) | 4 | eng,fre | subtitle_verification=4 | blocked by provider — grab budget: DrunkenSlug {DK} |
| If These Walls Could Rock | default | movie 2025 | requires da | candidate_selected | 8 | 0 | 6 (0) | 2 | — | subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| Kinsey | default | movie 2004 | requires da | candidate_selected | 91 | 0 | 78 (0) | 2 | cze,eng,fre,ger,por,spa | subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| Knights of Badassdom | default | movie 2014 | requires da | needs_attention (release_retrieval_failed) | 214 | 1 | 0 (0) | 0 | — | release_retrieval=2 | blocked by provider |
| Lanterns | season:01:episode:01 | series 2026 | requires da | candidate_selected | 428 | 183 | 354 (154) | 0 | — | — | blocked by provider — grab budget: NZBFinder {DK} |
| Lanterns | season:01:episode:02 | series 2026 | requires da | candidate_selected | 382 | 152 | 315 (130) | 0 | — | — | blocked by provider — grab budget: NZBFinder {DK} |
| Lanterns | season:01:episode:03 | series 2026 | requires da | candidate_selected | 348 | 144 | 285 (127) | 0 | — | — | blocked by provider — grab budget: NZBFinder {DK} |
| Lanterns | season:01:episode:04 | series 2026 | requires da | candidate_selected | 350 | 153 | 291 (130) | 0 | — | — | blocked by provider — grab budget: NZBFinder {DK} |
| Lanterns | season:01:episode:05 | series 2026 | requires da | candidate_selected | 370 | 173 | 316 (146) | 0 | — | — | blocked by provider — grab budget: NZBFinder {DK} |
| Lanterns | season:01:episode:06 | series 2026 | requires da | candidate_selected | 340 | 140 | 284 (119) | 0 | — | — | blocked by provider — grab budget: NZBFinder {DK} |
| Lanterns | season:01:episode:07 | series 2026 | requires da | candidate_selected | 215 | 70 | 194 (59) | 0 | — | — | blocked by provider — grab budget: NZBFinder {DK} |
| Lucifer | season:01 | series 2016 | requires da | needs_attention (release_retrieval_failed) | 602 | 0 | 11 (0) | 3 | chi,eng | release_retrieval=2,subtitle_verification=3 | blocked by provider |
| Lucifer | season:02 | series 2016 | requires da | candidate_selected | 602 | 0 | 11 (0) | 1 | chi,eng | release_retrieval=1,subtitle_verification=1 | blocked by provider — grab budget: NZBFinder {DK} |
| NCIS | season:06 | series 2003 | requires da | candidate_selected | 701 | 0 | 5 (0) | 3 | eng,hin,tam,tel | subtitle_verification=3 | blocked by provider — grab budget: NZBFinder {DK} |
| NCIS | season:07 | series 2003 | requires da | needs_attention (release_retrieval_failed) | 700 | 0 | 3 (0) | 4 | — | archive_unsupported=1,output_verification=1,release_retrieval=2,subtitle_verification=2 | blocked by provider |
| NCIS | season:08 | series 2003 | requires da | candidate_selected | 701 | 0 | 5 (0) | 4 | eng,hin,tam,tel | archive_unsupported=1,output_verification=1,release_retrieval=1,subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| NCIS | season:09 | series 2003 | requires da | candidate_selected | 700 | 0 | 12 (0) | 2 | eng | output_verification=1,subtitle_verification=1 | blocked by provider — grab budget: NZBFinder {DK} |
| NCIS | season:12 | series 2003 | requires da | candidate_selected | 702 | 0 | 12 (0) | 2 | eng,hin,tam,tel | subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| NCIS | season:15 | series 2003 | requires da | candidate_selected | 726 | 0 | 10 (0) | 2 | eng,hin,tam,tel | subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| NCIS | season:18 | series 2003 | requires da | candidate_selected | 702 | 0 | 14 (0) | 2 | eng | subtitle_verification=1 | blocked by provider — grab budget: NZBFinder {DK} |
| NCIS | season:20 | series 2003 | requires da | candidate_selected | 733 | 20 | 16 (0) | 2 | eng | release_retrieval=1,subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| NCIS | season:21 | series 2003 | requires da | needs_attention (release_retrieval_failed) | 710 | 2 | 20 (0) | 2 | eng | output_verification=1,release_retrieval=1,subtitle_verification=1 | blocked by provider |
| NCIS | season:23 | series 2003 | requires da | candidate_selected | 768 | 0 | 9 (0) | 2 | eng | subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| Once Upon a Deadpool | default | movie 2018 | requires da | candidate_selected | 67 | 1 | 47 (0) | 3 | — | archive_unsupported=1,duplicate_package=2,release_retrieval=1,subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| One-Eyed Monster | default | movie 2008 | requires da | candidate_selected | 40 | 0 | 35 (0) | 1 | — | release_retrieval=1,subtitle_verification=1 | blocked by provider — grab budget: NZBFinder {DK} |
| Resident Evil | default | movie 2026 | requires da | candidate_selected | 25 | 0 | 18 (0) | 1 | — | subtitle_verification=1 | blocked by provider — grab budget: NZBFinder {DK} |
| Supernatural | season:06 | series 2005 | requires da | candidate_selected | 701 | 23 | 17 (1) | 3 | eng | archive_unsupported=1,subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| Supernatural | season:09 | series 2005 | requires da | candidate_selected | 701 | 57 | 13 (2) | 2 | eng | subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| Tayuan 2 | default | movie 2026 | requires da | candidate_selected | 5 | 0 | 2 (0) | 3 | eng | subtitle_verification=3 | blocked by provider — grab budget: NZBFinder {DK} |
| The Amazing Digital Circus: The Last Act | default | movie 2026 | requires da | candidate_selected | 14 | 0 | 11 (0) | 3 | — | subtitle_verification=3 | blocked by provider — grab budget: NZBFinder {DK} |
| The Fantastic Four | default | movie 1994 | requires da | candidate_selected | 23 | 0 | 13 (0) | 3 | — | archive_unsupported=1,subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| This Is Not a Test | default | movie 2026 | requires da | candidate_selected | 76 | 14 | 8 (0) | 2 | eng | subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| V/H/S/94 | default | movie 2021 | requires da | candidate_selected | 168 | 1 | 154 (0) | 2 | eng,spa | archive_unsupported=1,release_retrieval=2,subtitle_verification=1 | blocked by provider — grab budget: NZBFinder {DK} |
| Wolf Creek | season:01 | series 2016 | requires da | candidate_selected | 518 | 0 | 18 (0) | 2 | eng | release_retrieval=1,subtitle_verification=2 | blocked by provider — grab budget: NZBFinder {DK} |
| A Monster Calls | default | movie 2016 | requires da | waiting (round_exhausted_waiting_new_identity) | 417 | 7 | 341 (3) | 2 | — | archive_unsupported=2 | blocked by policy/round limit |
| CSI: Crime Scene Investigation | season:05 | series 2000 | requires da | waiting (round_exhausted_waiting_new_identity) | 526 | 1 | 21 (0) | 2 | — | archive_unsupported=1,release_incomplete=1 | blocked by policy/round limit |
| CSI: Crime Scene Investigation | season:08 | series 2000 | requires da | waiting (round_exhausted_waiting_new_identity) | 489 | 0 | 5 (0) | 2 | — | archive_unsupported=1,release_incomplete=1 | blocked by policy/round limit |
| CSI: Crime Scene Investigation | season:09 | series 2000 | requires da | waiting (round_exhausted_waiting_new_identity) | 500 | 0 | 4 (0) | 2 | — | archive_unsupported=1,release_incomplete=1,release_retrieval=1 | blocked by policy/round limit |
| CSI: Crime Scene Investigation | season:10 | series 2000 | requires da | waiting (round_exhausted_waiting_new_identity) | 503 | 23 | 3 (0) | 6 | eng | archive_unsupported=4,release_incomplete=1,subtitle_verification=1 | blocked by policy/round limit |
| CSI: Crime Scene Investigation | season:12 | series 2000 | requires da | waiting (round_exhausted_waiting_new_identity) | 549 | 208 | 8 (1) | 2 | — | archive_unsupported=1,release_incomplete=1 | blocked by policy/round limit |
| Lucifer | season:03 | series 2016 | requires da | waiting (round_exhausted_waiting_new_identity) | 602 | 0 | 2 (0) | 4 | chi,dut,eng | archive_unsupported=2,release_retrieval=2,subtitle_verification=2 | blocked by policy/round limit |
| NCIS | season:01 | series 2003 | requires da | waiting (round_exhausted_waiting_new_identity) | 760 | 0 | 6 (0) | 5 | eng,hin,tam,tel | archive_unsupported=1,release_incomplete=1,release_retrieval=1,subtitle_verification=3 | blocked by policy/round limit |
| NCIS | season:02 | series 2003 | requires da | waiting (round_exhausted_waiting_new_identity) | 682 | 0 | 11 (0) | 2 | — | output_verification=1 | blocked by policy/round limit |
| NCIS | season:05 | series 2003 | requires da | waiting (round_exhausted_waiting_new_identity) | 716 | 0 | 14 (0) | 5 | eng,ger,hin,tam,tel | archive_unsupported=2,output_verification=1,subtitle_verification=2 | blocked by policy/round limit |
| NCIS | season:10 | series 2003 | requires da | waiting (round_exhausted_waiting_new_identity) | 701 | 0 | 7 (0) | 2 | — | output_verification=1 | blocked by policy/round limit |
| NCIS | season:11 | series 2003 | requires da | waiting (round_exhausted_waiting_new_identity) | 700 | 0 | 4 (0) | 4 | eng,hin,tam,tel | archive_unsupported=1,subtitle_verification=3 | blocked by policy/round limit |
| NCIS | season:13 | series 2003 | requires da | waiting (round_exhausted_waiting_new_identity) | 703 | 0 | 6 (0) | 5 | eng,hin,tam,tel | archive_unsupported=2,output_verification=1,subtitle_verification=2 | blocked by policy/round limit |
| NCIS | season:14 | series 2003 | requires da | waiting (round_exhausted_waiting_new_identity) | 703 | 0 | 9 (0) | 5 | eng,hin,tam,tel | archive_unsupported=2,output_verification=1,subtitle_verification=2 | blocked by policy/round limit |
| NCIS | season:16 | series 2003 | requires da | waiting (round_exhausted_waiting_new_identity) | 778 | 0 | 7 (0) | 5 | eng,hin,tam,tel | archive_unsupported=2,output_verification=1,subtitle_verification=2 | blocked by policy/round limit |
| NCIS | season:17 | series 2003 | requires da | waiting (round_exhausted_waiting_new_identity) | 702 | 0 | 11 (0) | 4 | eng | archive_unsupported=1,output_verification=1,release_incomplete=1,release_retrieval=3,subtitle_verification=1 | blocked by policy/round limit |
| NCIS | season:19 | series 2003 | requires da | waiting (round_exhausted_waiting_new_identity) | 729 | 27 | 11 (1) | 4 | eng | archive_unsupported=2,subtitle_verification=2 | blocked by policy/round limit |
| NCIS | season:22 | series 2003 | requires da | waiting (round_exhausted_waiting_new_identity) | 729 | 0 | 18 (0) | 4 | eng | archive_unsupported=1,release_retrieval=3,subtitle_verification=3 | blocked by policy/round limit |
| Never Back Down | default | movie 2008 | requires da | waiting (round_exhausted_waiting_new_identity) | 234 | 0 | 174 (0) | 5 | eng,spa | archive_unsupported=3,subtitle_verification=2 | blocked by policy/round limit |
| SLC Punk | default | movie 1998 | requires da | waiting (round_exhausted_waiting_new_identity) | 155 | 0 | 123 (0) | 5 | eng | archive_unsupported=3,subtitle_verification=2 | blocked by policy/round limit |
| The Nurse | season:01 | series 2023 | Danish original | waiting (round_exhausted_waiting_new_identity) | 663 | 34 | 3 (1) | 2 | chi,eng | release_retrieval=2,subtitle_verification=2 | blocked by policy/round limit |
| The Return | default | movie 2024 | requires da | waiting (round_exhausted_waiting_new_identity) | 356 | 0 | 332 (0) | 5 | eng,spa | archive_unsupported=2,duplicate_package=1,subtitle_verification=3 | blocked by policy/round limit |
| Under the Skin | default | movie 2014 | requires da | waiting (round_exhausted_waiting_new_identity) | 387 | 4 | 16 (0) | 5 | aze,baq,cat,chi,dut,eng,est,fre,ger,heb,lav,lit,pol,por,rum,rus,spa,tur | archive_unsupported=1,subtitle_verification=4 | blocked by policy/round limit |
| Dexter | season:01 | series 2006 | requires da | waiting (round_exhausted_waiting_new_identity) | 702 | 28 | 0 (0) | 2 | — | archive_unsupported=2 | blocked by archives |
| Linie 3: Hvor er de 2 andre? | default | movie 1994 | Danish original | waiting | 3 | 0 | 0 (0) | 1 | — | archive_unsupported=1,release_retrieval=2 | blocked by archives |
| Linie 3: TV i teltet | default | movie 1980 | Danish original | waiting (round_exhausted_waiting_new_identity) | 4 | 0 | 0 (0) | 2 | — | archive_unsupported=2,release_retrieval=1 | blocked by archives |
| Not Made for Politics | default | movie 2025 | requires da | held | 8 | 2 | 0 (0) | 2 | — | no_playable_media=2 | blocked by archives |
| A Patch of Fog | default | movie 2015 | requires da | needs_attention (output_inspection_failed) | 50 | 0 | 43 (0) | 2 | eng | subtitle_verification=1 | blocked by pipeline attention |
| CSI: Crime Scene Investigation | season:06 | series 2000 | requires da | needs_attention (output_inspection_failed) | 327 | 0 | 3 (0) | 2 | — | release_incomplete=1 | blocked by pipeline attention |
| Jackass Presents: Bad Grandpa .5 | default | movie 2014 | requires da | needs_attention (output_inspection_failed) | 86 | 1 | 39 (0) | 3 | eng,fre,spa | subtitle_verification=2 | blocked by pipeline attention |
| Jackass: The Lost Tapes | default | movie 2009 | requires da | needs_attention (altmount_job_attention) | 34 | 0 | 27 (0) | 2 | — | movie_primary_ambiguous=1 | blocked by pipeline attention |
| Michael Jackson: Searching for Neverland | default | movie 2017 | requires da | needs_attention (altmount_job_attention) | 55 | 0 | 48 (0) | 2 | eng | subtitle_verification=1 | blocked by pipeline attention |
| NCIS | season:03 | series 2003 | requires da | needs_attention (output_inspection_failed) | 767 | 0 | 6 (0) | 3 | eng,ger,hin,tam,tel | subtitle_verification=2 | blocked by pipeline attention |
| NCIS | season:04 | series 2003 | requires da | needs_attention (output_inspection_failed) | 754 | 0 | 8 (0) | 3 | eng,ger,hin,tam,tel | release_retrieval=1,subtitle_verification=2 | blocked by pipeline attention |
| Not Another Not Another Movie | default | movie 2011 | requires da | needs_attention (altmount_job_attention) | 80 | 0 | 64 (0) | 5 | eng | archive_unsupported=2,subtitle_verification=2 | blocked by pipeline attention |
| Reacher | season:03 | series 2022 | requires da | needs_attention (subtitle_retry_no_alternate) | 650 | 0 | 3 (0) | 1 | eng | subtitle_verification=1 | blocked by pipeline attention |
| Supernatural | season:05 | series 2005 | requires da | needs_attention (altmount_job_attention) | 701 | 23 | 33 (1) | 3 | eng | output_verification=1,subtitle_verification=1 | blocked by pipeline attention |
| The Legend of Bagger Vance | default | movie 2000 | requires da | needs_attention (altmount_job_attention) | 272 | 0 | 237 (0) | 4 | eng,fre | archive_unsupported=1,subtitle_verification=2 | blocked by pipeline attention |
| Comedy Central Roast of Bruce Willis | default | movie 2018 | requires da | waiting | 57 | 0 | 1 (0) | 1 | eng | subtitle_verification=1 | not yet tried |
| Comedy Central Roast of Justin Bieber | default | movie 2015 | requires da | waiting | 32 | 0 | 1 (0) | 4 | eng | subtitle_verification=4 | not yet tried |
| Harold & Kumar Escape from Guantanamo Bay | default | movie 2008 | requires da | waiting | 222 | 0 | 31 (0) | 3 | eng,spa | archive_unsupported=1,release_incomplete=1,subtitle_verification=1 | not yet tried |
| Linie 3: Rundrejsen 2001 | default | movie 2001 | Danish original | waiting | 4 | 0 | 1 (0) | 1 | — | archive_unsupported=1,release_retrieval=1 | not yet tried |
| Mortal Game | default | movie 2025 | requires da | held | 33 | 0 | 1 (0) | 3 | eng | subtitle_verification=3 | not yet tried |
| Power Ballad | default | movie 2026 | requires da | candidate_selected | 374 | 0 | 369 (0) | 4 | eng,fre | subtitle_verification=4 | not yet tried |
| Reacher | season:01 | series 2022 | requires da | held | 647 | 127 | 1 (0) | 1 | — | release_retrieval=1,season_pack_incomplete=1 | not yet tried |
| Rivalerne | season:01:episode:03 | series 2026 | Danish original | waiting | 9 | 9 | 4 (4) | 1 | — | archive_unsupported=1,release_retrieval=2 | not yet tried |
| Supernatural | season:03 | series 2005 | requires da | waiting | 705 | 117 | 80 (41) | 3 | eng | release_incomplete=1,subtitle_verification=2 | not yet tried |
| Drabscheferne - de uopklarede sager | season:01 | series 2020 | Danish original | held | 1 | 0 | 0 (0) | 0 | — | — | held (no eligible release) |
| Drabscheferne - de uopklarede sager | season:02 | series 2020 | Danish original | waiting | 1 | 0 | 0 (0) | 0 | — | — | held (no eligible release) |
| Graphic Desires | default | movie 2023 | requires da | held | 114 | 0 | 0 (0) | 0 | — | — | held (no eligible release) |
| Hard Breakers | default | movie 2011 | requires da | held | 177 | 1 | 0 (0) | 0 | — | — | held (no eligible release) |
| Jackass Volume One | default | movie 2005 | requires da | held | 3 | 0 | 0 (0) | 0 | — | — | held (no eligible release) |
| Jackass Volume Two | default | movie 2004 | requires da | held | 2 | 0 | 0 (0) | 0 | — | — | held (no eligible release) |
| Michael Jackson: 30th Anniversary Celebration | default | movie 2001 | requires da | held | 1 | 0 | 0 (0) | 0 | — | — | held (no eligible release) |
| Reacher | season:04 | series 2022 | requires da | waiting | 638 | 0 | 0 (0) | 0 | — | — | held (no eligible release) |
| Cirkusrevyen 2018 | default | movie 2018 | Danish original | waiting | 1 | 0 | 0 (0) | 0 | — | — | no eligible release |
| Extreme Makeover: Home Edition | season:01 | series 2003 | requires da | waiting | 37 | 11 | 0 (0) | 0 | — | — | no eligible release |
| Extreme Makeover: Home Edition | season:02 | series 2003 | requires da | waiting | 6 | 0 | 0 (0) | 0 | — | — | no eligible release |
| Extreme Makeover: Home Edition | season:03 | series 2003 | requires da | waiting | 52 | 24 | 0 (0) | 0 | — | — | no eligible release |
| Extreme Makeover: Home Edition | season:04 | series 2003 | requires da | waiting | 50 | 25 | 0 (0) | 0 | — | — | no eligible release |
| Extreme Makeover: Home Edition | season:05 | series 2003 | requires da | waiting | 50 | 25 | 0 (0) | 0 | — | — | no eligible release |
| Forbrydelser der rystede Danmark | season:01 | series 2019 | Danish original | waiting | 67 | 0 | 0 (0) | 0 | — | — | no eligible release |
| Forbrydelser der rystede Danmark | season:02 | series 2019 | Danish original | waiting | 31 | 5 | 0 (0) | 0 | — | — | no eligible release |
| Forbrydelser der rystede Danmark | season:03 | series 2019 | Danish original | waiting | 25 | 0 | 0 (0) | 0 | — | — | no eligible release |
| Forbrydelser der rystede Danmark | season:04 | series 2019 | Danish original | waiting | 5 | 0 | 0 (0) | 0 | — | — | no eligible release |
| Forbrydelser der rystede Danmark | season:05 | series 2019 | Danish original | waiting | 9 | 0 | 0 (0) | 0 | — | — | no eligible release |
| Forbrydelser der rystede Danmark | season:06 | series 2019 | Danish original | waiting | 50 | 42 | 0 (0) | 0 | — | — | no eligible release |
| Forræder: Under kutten | season:01 | series 2026 | Danish original | waiting | 7 | 0 | 0 (0) | 0 | — | — | no eligible release |
| Monster: The Josef Fritzl Story | default | movie 2010 | requires da | waiting | 2 | 0 | 0 (0) | 0 | — | release_retrieval=2 | no eligible release |
| Pokémon the Movie: Mewtwo Strikes Back - Evoluti | default | movie 2019 | requires da | waiting | 8 | 1 | 0 (0) | 0 | — | — | no eligible release |
| Rivalerne | season:01 | series 2026 | Danish original | waiting | 11 | 10 | 0 (0) | 0 | — | — | no eligible release |
| S/H/V | default | movie 2027 | requires da | waiting | 387 | 14 | 0 (0) | 0 | — | — | no eligible release |
| WWE Clash in Italy | default | movie 2026 | requires da | waiting | 1 | 0 | 0 (0) | 0 | — | release_retrieval=1 | no eligible release |
| WWE SummerSlam 2026: Sunday | default | movie 2026 | requires da | waiting | 5 | 0 | 0 (0) | 0 | — | — | no eligible release |
| "That's Awesome!": The Story of 'Dumb and Dumber | default | movie 2015 | requires da | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| 80 års fødselsdagen | default | movie 1999 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| A Look Inside Eternal Sunshine of the Spotless M | default | movie 2004 | requires da | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| BROS - Så Blev der Mørkt | default | movie 2024 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Cirkusrevyen 2006 | default | movie 2006 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Dante: 500 Miles on Psychedelics | default | movie 2026 | requires da | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Final Hour | default | movie 1995 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Forræder | season:04 | series 2023 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Forræder: Under kutten | season:04 | series 2026 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Grinebidder: Hele Danmarks Dirch | default | movie 2004 | requires da | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Hjørring Revyen 2024 | default | movie 2024 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Huxi Bach - Hjælp | default | movie 2014 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Jackass Backyard BBQ | default | movie 2003 | requires da | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Jackass Volume Three | default | movie 2002 | requires da | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Jackass: The Beginning | default | movie 2011 | requires da | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Klar Til Krig? | season:01 | series 2026 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Lanterns | season:01 | series 2026 | requires da | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Linie 3 - 10 års jubilæumsshow | default | movie 1989 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Linie 3: 25 års jubilæumsshow | default | movie 2004 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Little Virgil and Freddy Frogface | default | movie 1980 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Love Thy Neighbour | default | movie 1967 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Magt - Supermagt | default | movie 2023 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Masser af Passer | default | movie 1955 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Mikkel Klint Thorius: 100% Måske | default | movie 2024 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| NCIS | season:24 | series 2003 | requires da | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Outlander: Blood of My Blood | season:02 | series 2025 | requires da | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| P.I.S. - Politiets indsatsstyrke | season:03 | series 2000 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Simon Talbot: Optur | default | movie 2022 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Tre brødre - På fælleskabets regning | default | movie 2026 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Weightless | default | movie 2026 | Danish original | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| WWE SummerSlam 2026: Saturday | default | movie 2026 | requires da | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Your Heart Will Be Broken | default | movie 2026 | requires da | waiting | 0 | 0 | 0 (0) | 0 | — | — | no release observed anywhere |
| Comedy Central Roast of Alec Baldwin | default | movie 2019 | requires da | waiting | 44 | 0 | 0 (0) | 2 | eng | subtitle_verification=2 | no da release observed anywhere |
| Jackass: Too Hot For MTV | default | movie 2002 | requires da | waiting | 1 | 0 | 0 (0) | 1 | — | subtitle_verification=1 | no da release observed anywhere |
| Pokémon 3: The Movie | default | movie 2000 | requires da | waiting | 5 | 0 | 0 (0) | 1 | — | subtitle_verification=1 | no da release observed anywhere |
| Pokémon the Movie 2000 | default | movie 1999 | requires da | waiting | 6 | 0 | 0 (0) | 1 | — | subtitle_verification=1 | no da release observed anywhere |
| Pokémon: The First Movie | default | movie 1998 | requires da | waiting | 17 | 0 | 0 (0) | 3 | eng,jpn | subtitle_verification=3 | no da release observed anywhere |
| Lioness | season:01 | series 2023 | requires da | needs_attention (seerr_request_revoked) | 618 | 18 | 13 (0) | 0 | — | release_retrieval=1 | request revoked (owner) |
| Lioness | season:02 | series 2023 | requires da | needs_attention (seerr_request_revoked) | 702 | 33 | 30 (2) | 0 | — | release_retrieval=1 | request revoked (owner) |
| One Night Only | default | movie 2026 | requires da | needs_attention (seerr_request_revoked) | 213 | 30 | 195 (29) | 0 | — | release_retrieval=1 | request revoked (owner) |
| Strung | default | movie 2026 | requires da | needs_attention (seerr_request_revoked) | 157 | 7 | 149 (6) | 1 | eng | release_retrieval=1,subtitle_verification=1 | request revoked (owner) |
