[Dansk](#dansk) · [English](#english)

## Dansk

Den niogtyvende offentlige beta af Media Manager. Den indeholder `main` på `89af0bf` (databaseskema 70), altså præcis den kode, der kører ejerens bibliotek i dag.

### Nyt

- **"Ret Plex": Media Manager klarer selv Plex-trinnet.** Under Kom i gang (og under System, hvis Plex har mistet AltMounts drev) står **Lad Media Manager rette Plex**. Du indsætter en API-nøgle fra Cosmos (Settings → API → New Token), ser præcis hvad der bliver ændret, og trykker **Ret Plex**. Plex får AltMounts drev med `rslave` (så det ikke forsvinder, når AltMount genstarter) og Media Managers biblioteksmapper, og mapperne lægges i dine Plex-biblioteker (eller "Film" og "Serier" oprettes). Alt andet i Plex bliver som før, og nøglen bliver aldrig gemt.
- **Brug anbefalet opsætning prøver alle mapper i et Plex-bibliotek**, ikke kun den første.

### Installation

Vejledningen er den samme som i sidste version. Se [docs/operations/install-release.md](https://github.com/Media-manager52/Media-manager/blob/v0.1.0-beta.29/docs/operations/install-release.md).

**Opgradering fra v0.1.0-beta.28:** Ingen ændring af databasen (skema 70).

### Kendte begrænsninger

Som i v0.1.0-beta.11.

## English

The twenty-ninth public beta of Media Manager. It packages `main` at `89af0bf` (database schema 70), the exact code that runs the owner's production library today.

### Added

- **"Ret Plex": Media Manager takes care of the Plex step.** On Kom i gang (and on System when Plex lost AltMount's drive), **Lad Media Manager rette Plex** takes a Cosmos API token (Settings → API → New Token), shows exactly what will change and, on **Ret Plex**, gives Plex AltMount's drive with `rslave` (so it survives AltMount restarts) and Media Manager's library folders, which are added to your Plex libraries (or Film and Serier are created). Everything else in Plex stays as it was, and the token is never stored.
- **Brug anbefalet opsætning tries every folder of a Plex library**, not only the first.

### Install

```sh
MEDIA_MANAGER_API_IMAGE=ghcr.io/media-manager52/media-manager-api@sha256:3001e251045cb801ccc10a59b9eca0219b5dac3fbbfb6f85738e1c42a86efd26
MEDIA_MANAGER_UI_IMAGE=ghcr.io/media-manager52/media-manager-ui@sha256:4530c7ece654a22242753944318ba9d00092418d249284ef3be1665a8feded05
MEDIA_MANAGER_VERSION=0.1.0-beta.29
MEDIA_MANAGER_COMMIT=89af0bfdc24c9be28d5b1aef281b9a754ce56d19
```

**Upgrading from v0.1.0-beta.28:** no database change (schema 70).

### Known limitations

As in v0.1.0-beta.11.
