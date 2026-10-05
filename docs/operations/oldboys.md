# OldBoys credential setup

Open **Settings → Connections → OldBoys credentials** in the owner UI. Enter
the **API Token**, **Pass Key (PID)**, and **RSS Key (RID)** as values from the
OldBoys account, rather than full RSS/download URLs. All three are replaced
together. After saving, the fields clear; reloading reports only whether a
credential set is saved. There is no API to read the values back to a browser.

This setup step does not validate, commission, or enable OldBoys. The existing
disabled legacy proxy connection stays disabled. Native search/download support
and bounded validation remain separate work; the intended scope is Danish TV
and films. Do not validate this provider with generic Newznab CAPS.

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
