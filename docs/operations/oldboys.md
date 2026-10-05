# Native OldBoys indexer

Open **Settings → Connections → OldBoys credentials** in the owner UI. Enter
the **API Token**, **Pass Key (PID)**, and **RSS Key (RID)** as values from the
OldBoys account, rather than full RSS/download URLs. All three are replaced
together. After saving, the fields clear; reloading reports only whether a
credential set is saved. There is no API to read the values back to a browser.

After saving, choose **Validate and enable OldBoys**. Validation makes five
paced requests: a matching Danish TV search, its randomized negative control,
a matching Danish film search, its negative control, and one strictly validated
NZB download. The NZB is discarded; validation creates no AltMount job. A failed
control leaves the provider disabled. The disabled legacy proxy row is converted
to native kind `oldboys`, preserving its provider UUID; the proxy is not started.

Native requests contact only `https://oldboys.pw`: `/api/nzbs/filter` uses the API
Token as a bearer credential, category 1 for films and 2 for TV. The verified
`/nzbs/download/<numeric-ID>.<PID>` route uses the **Pass Key (PID)**. The RSS Key
(RID) is stored with the account set but is not needed by this download contract;
using RID there returned 404 in the bounded live check. Provider links and every
redirect are rejected. Network sends use direct IPv4, `MediaManager/1`, the
shared durable request scheduler, a five-second minimum interval, one request
in flight, and conservative rolling limits of 1,000 searches/100 grabs per 24h.
These fallback limits are not a claim about the owner's subscription allowance.

Only catalogue movies/series with original language `da` use this provider;
release titles must also contain a standalone `DANiSH` token. Each scheduled
scan makes one request, rotating at most four 25-row pages across complete scans.
Native search progress starts at page one independently of older Newznab searches.
TV queries alternate calendar-year and ordinal labels when the workflow has an
unambiguous frozen annual mapping. No website scraping, fake Newznab CAPS,
provider ID invention, guessed media sizes or credential-bearing locators are
used. Missing IDs, sizes and subtitle evidence remain unknown. Native Danish TV
identity can be bound to an exact saved original/canonical title immediately
followed by a scene scope/year token. This is labelled inferred catalogue
binding, never an observed provider ID. Conflicting IDs, wrong titles, remake
years and unresolved episode mapping still fail the existing admission rules.
Output inspection, subtitle policy and exact Plex confirmation remain required.

Saving replacement credentials disables native OldBoys in the same transaction
and requires validation again. Each search/download and final activation checks
the exact credential/configuration snapshot; replaced credentials, superseded
validation attempts and stale search results cannot enable or select releases.
Owner validation uses `POST /api/v1/settings/oldboys/commission`; source/status
screens report native commissioning and pacing separately from Newznab CAPS.

The API needs `OLDBOYS_CREDENTIAL_ENCRYPTION_KEY` (or its `_FILE` reference), a
server-generated 32-byte random key encoded as 64 hexadecimal characters.
Generate and store it privately in `.env.integrations` before deploying.
Missing keys disable the form; invalid keys prevent API startup. Never reuse
the owner token or a provider credential as this key.

Schema 44 adds a single-row `oldboys_credentials` table containing only an
AES-256-GCM nonce/ciphertext and an update time. Authentication binds the payload
to the OldBoys credential format; each replacement uses a fresh nonce. Raw
credentials do not enter integration config, audit details, API responses,
client storage, or logs. The owner-only GET/PUT route is
`/api/v1/settings/oldboys/credentials`; writes are bounded and invalid request
errors do not echo input. Provider clients can decrypt the values server-side.

Back up the encryption key together with the database in protected storage.
Losing or changing it makes saved credentials unreadable; re-entering all three
values under a new key replaces them. A database dump alone does not reveal
their values. Existing encrypted database backups also contain this table;
the deployment broker separately backs up `.env.integrations`. Off-server
backup and access controls remain the owner's operational responsibility.
