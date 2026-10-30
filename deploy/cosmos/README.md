# Media Manager — Cosmos Cloud market listing

This public repository is the Cosmos Cloud marketplace listing for **Media Manager**.
The application source is maintained separately; this repository exists so Cosmos can
resolve the public listing, install template and icon.

## Install from the custom source

In Cosmos Cloud open **Market → Sources**, add a source named **Media Manager** with:

```text
https://raw.githubusercontent.com/Media-manager52/Media-manager/main/deploy/cosmos/index.json
```

Then open **Market → Media Manager → Install**. The installer asks for your movie and TV
library folders, the AltMount parent folder, the existing media network, and optional
Plex/Newznab/TMDB credentials. It creates the API, UI and PostgreSQL services, plus the
UI, `/api` and `/health` routes on one host. The `/api` route injects the owner token
server-side.

If you want the built-in Cosmos market instead of a custom source, the same app is
submitted to `azukaar/cosmos-servapps-official`: [PR #336](https://github.com/azukaar/cosmos-servapps-official/pull/336).
Once accepted it appears without adding a source.

## Requirements

- Cosmos Cloud 0.23.4 or newer.
- AltMount (Usenet acquisition) and Plex.
- At least one Newznab-compatible indexer.
- A TMDB API read access token (recommended).
- A Docker network shared with AltMount, Plex and Seerr (default `media-stack`).
- Host folders for movies, TV, AltMount's parent mount and encrypted backups.

## Images

The API and UI images come from the canonical application project
(`https://github.com/Media-manager52/Media-manager`) via GHCR:

```text
ghcr.io/media-manager52/media-manager-api:main
ghcr.io/media-manager52/media-manager-ui:main
```

They are built for `linux/amd64`. If a pull fails, the GHCR packages may still be
private; make them public once at:

- https://github.com/users/Media-manager52/packages/container/media-manager-api/settings
- https://github.com/users/Media-manager52/packages/container/media-manager-ui/settings

The project's publish workflow sets both packages public after a successful main
publish.

## After install

1. Open the installed Media Manager app from Cosmos (Cosmos OpenID handles the login).
2. Add **AltMount** under Sources → Download clients.
3. Add **Plex** under Settings → Connections.
4. Add and commission a **Newznab** indexer under Sources → Indexers.
5. Add a library root and quality profile, then add a title.
6. Optional: configure Seerr, off-site backups and more metadata providers.

The owner token is in the install form (and in the API service environment). It is
injected only by the Cosmos `/api` route; do not expose the API port.

## Licensing

Media Manager is licensed under the **GNU Affero General Public License, version 3
only** (`AGPL-3.0-only`). The full license text is in the canonical repository at
`https://github.com/Media-manager52/Media-manager/blob/main/LICENSE`; the
corresponding source is `https://github.com/Media-manager52/Media-manager`.
Third-party components remain under their own licenses and notices. The Cosmos
listing, install template, and published images do not grant rights beyond
AGPL-3.0-only.
