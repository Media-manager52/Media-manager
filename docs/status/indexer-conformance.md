# Newznab provider conformance

Verified 2026-09-01 with the developer-only DRI Indexer Lab
`media-manager` profile at local lab commit `90a5821`. The lab is not a Media
Manager service, dependency, credential store, or runtime fallback.

## Native OldBoys investigation (2026-10-05)

A separate bounded native probe, from Media Manager's production IPv4 network
and durable scheduler, made seven requests: four film/TV positive and randomized
negative controls, one RID retrieval (404), one scoped annual TV query, and one
PID retrieval (200). Positive searches returned Danish Operation X/Klovn rows;
both negative controls were empty. The PID body was NZB XML (122 files, 3,154
segments), despite its misleading bittorrent Content-Type; password metadata
was present and preserved in memory. No download-client job was created.
Inspected rows had no IDs, sizes or media-info evidence. Only sanitized metadata
and counts were retained in the owner's protected evidence report.

The native implementation and dedicated commissioning action are separate from
standard Newznab conformance. Enabling requires the final runtime five-control
commission, plus its own Verify/Deploy evidence. No generic Newznab CAPS or
retired proxy is used. See [native contract](../operations/oldboys.md).

### Native runtime commissioning and collection investigation

PR #43 was deployed through Verify 37337754290 / Deploy 37339782861, and the
supported owner commission enabled OldBoys at 2026-10-05 16:20:19 UTC after all
five controls passed. Normal Danish workflow searches and three deployed browser
viewports succeeded. A normal Operation X S16E06 search found no eligible release;
this is not a claim that OldBoys lacks the series.

The owner supplied release 77005, `Operation.X.S01-S16.DANISH.720p.AAC2.0-PlexLover`.
Six additional guarded paced search diagnostics proved both S16E06 and 2019
filters empty, while title-only searches returned 61 Danish rows across three
pages, including 77005 on page three (the fourth page was empty). One guarded PID
retrieval returned a valid 7,392,857-byte NZB with 124 file subjects, 99 RAR-volume
subjects and no declared episode filenames. The body was inspected in memory
and discarded; no job was submitted by these diagnostics. RAR is an existing
supported analysis path, so the obfuscated archive manifest alone does not prove
it is unsupported or playable. Actual coverage/streams require completed output
inspection. The native title-only/range follow-up addresses the discovery and
scope gap; live pack output/Plex evidence remains pending.

## Supported configured providers

The bounded profile reproduced Media Manager's real `extended=1` request
shapes. It used each provider's own movie and TV feeds to derive candidates,
then required a positive semantic result and an empty randomized negative
control. A TV semantic result required both normalized title tokens and the
requested season; title comparison alone did not pass the check.

| Provider | Verified movie shape | Verified complete-season shape |
| --- | --- | --- |
| abNZB {DK} | `movie`: `q`, `imdbid`, `extended` | `tvsearch`: `q`, `imdbid`, `season`, `extended` |
| altHUB {DK} | `movie`: `q`, `imdbid`, `extended` | `tvsearch`: `q`, `season`, `extended` |
| DrunkenSlug {DK} | `movie`: `q`, `imdbid`, `extended` | `tvsearch`: `q`, `imdbid`, `tmdbid`, `tvdbid`, `season`, `extended` |
| msgnews {DK} | `movie`: `q`, `imdbid`, `extended` | `tvsearch`: `q`, `imdbid`, `season`, `extended` |
| NinjaCentral {DK} | `movie`: `q`, `imdbid`, `extended` | `tvsearch`: `q`, `season`, `extended` |
| NZBFinder {DK} | `movie`: `q`, `imdbid`, `extended` | `tvsearch`: `q`, `imdbid`, `tmdbid`, `tvdbid`, `season`, `extended` |
| NZBgeek {DK} | `movie`: `q`, `imdbid`, `extended` | `tvsearch`: `q`, `tvdbid`, `season`, `extended` |
| Nzb.life {DK} | `movie`: `q`, `imdbid`, `extended` | `tvsearch`: `q`, `season`, `extended` |
| treasure-maps {DK} | `movie`: `q`, `imdbid`, `extended` | `tvsearch`: `q`, `imdbid`, `tvdbid`, `season`, `extended` |

Every run returned authenticated, parseable Newznab XML with zero operation
failures. Parameters not listed above remain CAPS-advertised or unknown; absence
from this table is not evidence that a parameter is unsupported. Existing
runtime allowlists therefore remain unchanged.

### Re-verification 2026-09-30

The same `media-manager` profile (DRI Indexer Lab `0.2.4-mm1`) was re-run from
the production server against all nine enabled providers, one at a time, with
each credential passed only through its process environment: 7 requests per
provider (63 in total), at least 3,000 ms apart, no redirects, no DETAILS,
GETNFO, NZB or website requests. Every provider was reachable and authenticated
with zero operation failures, including Nzb.life and treasure-maps after their
keys were replaced that day. The shapes above were re-verified, with two
additions: altHUB and NinjaCentral now also verify `imdbid` in the
complete-season shape. NZBgeek's movie probe was inconclusive this time because
the sampled candidate's derived `q` was not a title (`BluRayRIP TrueHD Atmos`);
its 2026-09-01 verification stands. A secret scan of all 190 evidence files
(exact and three levels of percent-encoding, all ten keys) found nothing, and no
lab state or secrets store was written. Runtime allowlists are unchanged.

## Bounded annual-season discovery (2026-10-05)

For series with complete, unambiguous annual frozen season metadata, runtime
scans alternate their existing `tvsearch` shape with a bounded ID-only shape
using only identity parameters already selected by that provider's configured
dialect or active recipe. The alternate omits title/year/season/episode filters;
limit, categories, extended parsing, commissioning, durable pacing and budgets
remain unchanged. There is still one request per provider per scan. Providers
whose selected request lacks a supported series ID retain the ordinary shape.
Results still require exact series identity and frozen canonical episode scope.

The Operation X diagnostic made eight guarded, sequential search calls through
Media Manager's shared adapter/scheduler from its configured IPv4 egress with
`MediaManager/1`. Both Nzb.life and msgnews returned Operation X observations
from an ID-only request; constrained season-one requests returned zero. The
result sets contained newer ordinal, broadcast-year and date-named episodes,
not positively identified releases for requested seasons 1–10. No grabs were
made by that diagnostic. These observations do not commission another provider
or prove all returned payloads playable. Local mock/integration tests cover the
alternate and its unchanged request count; rollout needs its own Verify/Deploy
evidence and normal completed-output/Plex checks.

## Observed download locators

NZBFinder search results advertise a same-origin `/api/v1/getnzb` locator whose
`id` value is the release UUID with a `.nzb` suffix. abNZB, altHUB, msgnews,
NinjaCentral, and Nzb.life advertise a same-origin `/getnzb` path containing the
exact 32-character release ID and a numeric account identifier; DrunkenSlug uses
the same path family with a 40-character ID. Those locators are not assumed to
be interchangeable with the generic `t=get` request shape. Media Manager
retains only each verified sanitized origin, path, and release identity, then
adds the configured credential in memory immediately before retrieval. Local
adapter tests verify the request shapes and reject mismatched origins, paths,
IDs, ports, duplicate parameters, and additional query parameters. The DRI
`media-manager` profile did not retrieve an NZB, so the provider responses
remain live-workflow evidence rather than conformance-lab grabs.

treasure-maps (sNZB `1.0.0-snzb`) uses a different contract, verified live on
2026-09-30 from the production server with one request per step: its feed
advertises `https://file.treasure-maps.com/getnzb/<32 hex>?r=<key>` on a
separate file host, with the same 32-character ID as its `/details/<32 hex>`
permalink. `t=get` answers `302` to `https://file.treasure-maps.com/getnzb?r=<key>&id=<id>`;
Media Manager's redirect policy strips the key from that redirect, and the file
host then answers `403` (reproduced), which is why every treasure-maps grab
failed after 2026-08-31. Media Manager therefore requests the advertised file-host
link directly, built from the stored release ID, with the credential only in the
in-memory `r` parameter and no redirect. The contract applies to the exact
`treasure-maps.com` HTTPS endpoint and 32-hex IDs only; local tests pin the
shape, reject other IDs, schemes and ports, and prove every other provider keeps
its own path. The live check made one search, one `t=get` without following the
redirect, one keyless file-host request (403) and one NZB retrieval (200), and
one search plus one retrieval for Nzb.life after its key was replaced.

OldBoys remains disabled and was not contacted. Its bearer/filter interface is
not standard Newznab and must not be enabled through the generic adapter.

## Safety evidence

- At least 3,000 ms separated request starts for each provider.
- Each final conformance run used no more than seven requests; the profile has
  a hard eight-request ceiling.
- Across profile development and final verification, 155 query-class calls and
  zero grab-class calls were made. The highest per-provider total was 34, well
  below the configured 1,000-query rolling fallback.
- Redirects, website mapping, DETAILS, GETNFO, and NZB retrieval were disabled.
- Providers ran sequentially; OldBoys and unrelated integrations were not
  contacted.
- Every private evidence run passed exact and three-level percent-encoded
  configured-secret scans. Evidence is owner-private and excluded from both
  repositories.

## Adding another indexer

1. Create it disabled through Media Manager's owner API or Connections screen,
   referencing a dedicated environment variable for its credential.
2. Run only the lab's `media-manager` profile, one provider at a time. Pass the
   credential through the process environment; do not save or copy it into the
   lab state store.
3. Require valid CAPS and verified movie plus complete-season shapes. Treat
   missing candidates or ambiguous evidence as unknown, never unsupported.
4. Review only the normalized capability result. Do not import raw responses,
   provider URLs, candidate data, or credentials into Media Manager.
5. Use Media Manager's supported **Validate & enable** action. Production
   commissioning and runtime traffic continue through Media Manager's durable
   pacing, rolling budgets, sanitizer, and redirect protections.

## OldBoys credential preparation (2026-10-05)

The owner UI can accept the OldBoys API Token, Pass Key (PID), and RSS Key (RID)
into encrypted server-side storage. This is credential preparation only: no
OldBoys HTTP request is made by saving, and the disabled legacy Newznab proxy
is not commissioned or enabled. Native provider validation remains pending;
the existing provider conformance evidence above is unchanged.
See [OldBoys setup](../operations/oldboys.md).
