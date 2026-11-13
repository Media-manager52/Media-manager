# Media Manager — Cosmos Cloud market listing

This repository is the canonical source for **Media Manager** and hosts the Cosmos
Cloud marketplace listing, install template and icon. The application source is
not a separate private tree.

## Install from the custom source

In Cosmos Cloud open **Market → Sources**, add a source named **Media Manager** with:

```text
https://raw.githubusercontent.com/Media-manager52/Media-manager/main/deploy/cosmos/index.json
```

Then open **Market → Media Manager → Install**. The installer asks for your movie and TV
library folders, a folder for AltMount's files, the existing media network, and
optionally a TMDB token. Plex, news-server and indexer logins are entered on the
website afterwards. It creates the API, UI, PostgreSQL and AltMount services, plus the
UI, `/api` and `/health` routes on one host. The `/api` route injects the owner token
server-side.

If you want the built-in Cosmos market instead of a custom source, the same app is
submitted to `azukaar/cosmos-servapps-official`: [PR #336](https://github.com/azukaar/cosmos-servapps-official/pull/336).
Once accepted it appears without adding a source.

## Requirements

- Cosmos Cloud 0.23.4 or newer.
- Plex.
- A Usenet news-server account (AltMount itself is installed with Media Manager;
  see [AltMount](#altmount)).
- At least one Newznab-compatible indexer.
- A TMDB API read access token, unless you use Seerr. It is free; you can enter it
  in the install form or later on the website.
- A Docker network shared with Plex and Seerr (default `media-stack`).
- Host folders for movies, TV, AltMount's files and encrypted backups.
- `/dev/fuse` on the host (standard on Linux), for AltMount's drive.

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
2. Under **Indstillinger → Forbindelser** choose **Log ind med Plex** and allow
   Media Manager on Plex. Add **TMDB** unless you use Seerr. AltMount is already
   connected.
3. Under **Indstillinger → Usenet-servere** add your news-server login (address,
   user name, password) and choose **Test login**.
4. Add and commission a **Newznab** indexer under **Kilder**.
5. On **Kom i gang** choose **Brug anbefalet opsætning**. It sets up the library
   folders and the "Dansk tekst eller tale" profile after showing you the proposal.
   Then add a title.
6. Optional: configure Seerr, off-site backups and more metadata providers.

The install form no longer asks for the Plex token, an indexer key or the AltMount
secret: these are entered on the website, stored encrypted and tested there. The
`PLEX_TOKEN`, `NEWZNAB_PRIMARY_API_KEY`, `ALTMOUNT_SECRET` and
`BACKUP_ENCRYPTION_KEY` environment variables still work if you set them on the API
service yourself.

## AltMount

AltMount streams Usenet downloads as files instead of downloading them first. By
default the install adds it as the service `<name>-altmount`, built from
[Media-manager52/altmount](https://github.com/Media-manager52/altmount)
(`ghcr.io/media-manager52/altmount:mm-stable`, MIT licensed, upstream
[javi11/altmount](https://github.com/javi11/altmount) plus Media Manager's fixes).

- It runs only on Media Manager's private network, without a login, and gets no
  Cosmos route. Media Manager adds it as its default AltMount connection on the
  first start (`BUNDLED_ALTMOUNT_URL`); if an AltMount connection already exists,
  nothing is changed.
- Its settings are written once, on its first start (`ALTMOUNT_PRESET=media-manager`),
  in the volume `<name>-altmount-config`. News servers are added from Media Manager
  under **Indstillinger → Usenet-servere**; their passwords stay in AltMount.
- It mounts its read-only drive at `altmount` inside the folder you chose for
  AltMount's files; Media Manager sees that folder at `/mnt`. The drive needs
  `/dev/fuse`, `SYS_ADMIN` and an unconfined AppArmor profile, like any FUSE
  container.
- **One manual step: Plex must see the same files.** Media Manager cannot change
  your Plex container, so add the folder you chose for AltMount's files to Plex
  yourself, at `/mnt`, read-only, with bind propagation `rslave`:

  ```json
  { "type": "bind", "source": "<folder for AltMount's files>", "target": "/mnt",
    "readOnly": true, "bindOptions": { "propagation": "rslave" } }
  ```

  Plex then sees the drive at `/mnt/altmount`, and **Brug anbefalet opsætning**
  finds it (with another path it compares the folders both list). The propagation
  matters: with Docker's default (`rprivate`) Plex keeps the old, dead drive after
  every AltMount restart or update ("Socket not connected") until Plex itself is
  restarted. Cosmos' container screens cannot set propagation, so make the change
  in Plex's compose or Cosmos JSON. Putting AltMount's drive inside a folder Plex
  already has does not avoid this; the same rule applies.

Already running your own AltMount? Turn off **Install AltMount together with Media
Manager**, choose the host folder that contains its FUSE mount, put Media Manager on
a network AltMount is on, and add it under **Indstillinger → Forbindelser**.

The owner token is in the install form (and in the API service environment). It is
injected only by the Cosmos `/api` route; do not expose the API port.

## The secret encryption key

Keys you enter on the website (Plex token, AltMount secret, indexer API keys,
OldBoys credentials) are stored encrypted in the database. The Cosmos installer
generates only four passwords, and all four are in use, so the template does not
set the key itself: Media Manager creates a random key on its first start in the
`<app name>-api-state` volume (`SECRET_ENCRYPTION_KEY_FILE`). Keep that volume when
you update or move the app. If it is lost while keys are saved, Media Manager does
not silently create a new one (that could never open the saved keys): the
Connections page says secret storage is off and the API log names the key file.

**Installed before this change?** The old template referenced a fifth password
that Cosmos does not generate, so `SECRET_ENCRYPTION_KEY` is empty and the
Connections page shows a yellow "mangler en krypteringsnøgle" warning. Fix it in
Cosmos: **ServApps → `<app name>-api` → Environment**, set `SECRET_ENCRYPTION_KEY`
to the output of `openssl rand -hex 32`, save, and keep the value somewhere safe.

## The backup key

Backups are encrypted with a key that Media Manager creates on its first start, in
the same volume (`backup-encryption.key`, `BACKUP_ENCRYPTION_KEY_FILE`). The
**System → Backup** page shows where it is. **Save a copy somewhere safe**: without
it a backup cannot be restored on another server. To read it, run
`cat /var/lib/media-manager/state/backup-encryption.key` in the API container's
terminal in Cosmos. If the file is lost after backups were made, Media Manager does
not create a new key (it could not open those backups): backups stay off and the
API log names the file.

## Reinstalling

Cosmos keeps an existing volume when you install an app with the same name again,
but it generates new passwords. The old database then refuses the new database
password and the API never becomes healthy. For a clean start, remove the
`<app name>-postgres-data` and `<app name>-api-state` volumes (Storage → Volumes)
before installing again, or install under a new app name. To keep your data, set
the API's `PGPASSWORD` back to the value from the previous installation.

## Licensing

Media Manager is licensed under the **GNU Affero General Public License, version 3
only** (`AGPL-3.0-only`). The full license text is in the canonical repository at
`https://github.com/Media-manager52/Media-manager/blob/main/LICENSE`; the
corresponding source is `https://github.com/Media-manager52/Media-manager`.
Third-party components remain under their own licenses and notices. The Cosmos
listing, install template, and published images do not grant rights beyond
AGPL-3.0-only.
