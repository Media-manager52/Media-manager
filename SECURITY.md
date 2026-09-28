# Security policy

Media Manager runs an owner-operated media stack with real credentials
(Newznab providers, Seerr, Plex, AltMount, PostgreSQL) and write access to
library paths. Security reports are taken seriously.

## Reporting a vulnerability

- Use **GitHub private vulnerability reporting** on this repository
  (Security → Report a vulnerability), or contact the repository owner
  directly. Do not open a public issue for a suspected vulnerability.
- Include: affected commit/version, the deployment shape (Docker Compose or
  otherwise), reproduction steps, impact, and whether any secret or media path
  could be exposed.
- You will get an acknowledgement as soon as possible. Please allow time for a
  fix before any public disclosure.

## In scope

- Authentication or authorization bypass on the owner API, webhook intake, or
  the Radarr/Sonarr-compatible surface.
- Exposure of secrets or credentials in logs, responses, repository history, or
  error messages.
- Redirect, TLS, or credential-forwarding weaknesses in NZB retrieval or
  provider handling.
- Symlink or path escapes that could write outside the configured library
  roots or into the read-only AltMount output.
- Publication without the exact Plex identity/path confirmation, or a way to
  bypass required-subtitle verification.
- Injection or tampering through the Seerr webhook, intake payloads, or
  import-list handling.

## Out of scope

- Requests for help configuring providers, indexers, or the media stack.
- Vulnerabilities in third-party services (Plex, Seerr, AltMount, indexers)
  that are not caused by Media Manager code; report those upstream.
- Findings that require an already-compromised host or database.

## Hard safety invariants

These are enforced by the code and must never be weakened by a fix:

1. AltMount output is read-only; nothing writes, moves, or deletes inside it.
2. Publication uses contained Plex-safe symlinks through `internal/library`
   only; no raw rename/symlink operations.
3. A season pack is one AltMount job with one immutable expectation.
4. Nothing succeeds without exact Plex identity and path confirmation.
5. Newznab pacing, retry budgets, redirect pinning, TLS checks, credential
   stripping, and secret sanitization are never relaxed.
6. Failed outputs stay retained and owner-visible unless the owner approves a
   storage policy.
7. Only the owner opens acquisition rounds; recovery is never faked.
8. Secrets are never printed, logged, or committed; `.env` files and provider
   secret files stay out of git.

See [`docs/onboarding/README.md`](docs/onboarding/README.md) and
[`AGENTS.md`](AGENTS.md) for the full contract.
