# Acquisition and publication safety rules

These are the product rules Media Manager follows from search to Plex. The
owner-level guarantees and the tests that prove them are in
[../invariants.md](../invariants.md); the lifecycle itself is described in
[workflows.md](workflows.md).

- One in-flight request per indexer; persisted pacing, `Retry-After`, and bounded backoff apply to normal work and the Indexer Lab.
- Repeated Newznab attributes retain their original order and values.
- Commissioning snapshots each provider's advertised movie and TV search dialect. Runtime searches send only supported parameters, prefer verified external identities, normalize IMDb IDs to their numeric Newznab form, and retain general search as a capability-backed fallback.
- Every Newznab call reserves a durable per-provider rolling budget before network I/O. Prowlarr-imported query/grab limits are preserved; otherwise the conservative fallback is 1,000 queries and 100 grabs per 24 hours, in addition to the three-second minimum interval and one-in-flight lane.
- Ambiguous AltMount upload results are reconciled by a unique filename and are never blindly resubmitted.
- AltMount output is read-only. Media Manager publishes only contained symlinks.
- A season pack remains one AltMount job and one immutable expectation set. Completed seasons observed only as relevant episode releases use separate exact-episode workflows. Annual broadcast-year/date names are mapped only with positive series identity and complete, unambiguous frozen season metadata; output coverage still uses canonical episode keys.
- AltMount completion is not import completion; import completion is not Plex completion.
- Completed output is inspected only inside the authorized AltMount job root. Required subtitle languages must be verified on every selected video before publication; Danish variants are stored canonically as `da` with raw evidence retained.
- Seerr provider metadata may route a title to an enabled media-kind profile by canonical original language. Danish variants route as `da`; missing or unreliable metadata remains on the default fallback. This routing is not evidence that the acquired file contains a Danish audio track.
- A quality profile may use best-available ranking instead of a hard resolution floor. Positive evidence ranks `2160p`, `1080p`, `720p`, `576p`, then `480p`; reliable provider dimensions are canonicalized while their raw values remain evidence. Positive release-title audio evidence ranks common lossless, object-based, and lossy codecs within the selected resolution tier. Unknown resolution or audio evidence remains eligible and receives no preference score.
- A verified external subtitle is published beside its video with Plex-compatible naming and shares that publication generation. It becomes current only after the video completes exact Plex confirmation.
- Every acquisition round snapshots a bounded attempt limit from startup configuration (default two, range one–six), while lifetime attempt numbers remain monotonic. Only a definitive release-attributable output failure closes a slot and excludes that exact release plus its canonical NZB package inside the workflow; infrastructure failures recover the same job and do not blame the release.
- After the round's slots fail, retained output stays owner-visible and undeleted. Low-priority discovery resumes after a deterministic 24-hour ±20% delay, backs off only after complete no-new scans, and can expose a newly observed eligible exact identity without opening another round. Only the owner can open the next round, using the configured limit. The configured retained failed-output limit (default four, range one–twelve) stops further acquisitions pending storage review.
- An owner may reverify each exact retained output once after a probe correction. Reverification reuses the frozen inventory and its original subtitle policy, makes no AltMount request, and resumes publication only when the existing verification and Plex gates pass.
- Plex confirmation requires a verified provider GUID and every exact expected path.
- An upgrade receives a distinct release-version filename. The old symlink is removed only after Plex confirms the replacement; the AltMount source is retained.
- Replacing an adopted real file requires an exact owner confirmation before every new or delayed search unless that workflow already holds an exact fingerprint-bound authorization. The old file is moved only after Plex confirms the new publication, retained for 30 days, and never physically purged without a second exact owner confirmation after retention.
- Rolling an adopted upgrade back restores the entire retained cycle with exact fingerprints. Replacement links remain current until Plex re-confirms every restored path, then are safely displaced and removed.
- Reviewed adoption monitoring is applied only after the renamed files pass Plex path and identity confirmation. Immediate search additionally requires the adopted replacement phrase and preserves an existing exact TV episode snapshot for one complete-pack workflow.
