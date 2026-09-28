# Newznab provider conformance

Verified 2026-09-01 with the developer-only DRI Indexer Lab
`media-manager` profile at local lab commit `90a5821`. The lab is not a Media
Manager service, dependency, credential store, or runtime fallback.

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
