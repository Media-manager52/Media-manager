[Dansk](#dansk) · [English](#english)

## Dansk

Den otteogtyvende offentlige beta af Media Manager. Den indeholder `main` på `f219944` (databaseskema 70), altså præcis den kode, der kører ejerens bibliotek i dag.

### Rettet

- **System → AltMount siger ikke længere, at AltMount ikke svarede om afspilningerne, når intet afspilles.** AltMount svarer en tom liste som `data: null`, og det læses nu som "intet afspilles".
- **AltMount, der installeres sammen med Media Manager, starter nu sit drev på en ny Cosmos-installation.** Cosmos opretter mappen til AltMounts filer, så kun root må åbne den; AltMount (`mm-v0.4.0-mm.2`, også `mm-stable`) overtager nu selv mappen, når den ikke kan komme ind. Fundet ved en rigtig installation gennem Cosmos.

### Installation

Vejledningen er den samme som i sidste version. Se [docs/operations/install-release.md](https://github.com/Media-manager52/Media-manager/blob/v0.1.0-beta.28/docs/operations/install-release.md).

**Opgradering fra v0.1.0-beta.27:** Ingen ændring af databasen (skema 70).

### Kendte begrænsninger

Som i v0.1.0-beta.11.

## English

The twenty-eighth public beta of Media Manager. It packages `main` at `f219944` (database schema 70), the exact code that runs the owner's production library today.

### Fixed

- **System → AltMount no longer says AltMount did not answer about playback when nothing is playing.** AltMount answers an empty list as `data: null`, which is now read as "nothing plays".
- **The AltMount installed with Media Manager now mounts its drive on a fresh Cosmos install.** Cosmos creates the folder for AltMount's files so only root may enter it; AltMount (`mm-v0.4.0-mm.2`, also `mm-stable`) now takes that folder over when it cannot enter it. Found by a real install through Cosmos.

### Install

```sh
MEDIA_MANAGER_API_IMAGE=ghcr.io/media-manager52/media-manager-api@sha256:cd5081d2f72a235832e15fc0799be010baf1fc276b7b7fdb3dae79c44cd7c0fe
MEDIA_MANAGER_UI_IMAGE=ghcr.io/media-manager52/media-manager-ui@sha256:b565f33ffbd6d3587f32e0416786d6c71f9385811a20f80c9a25e887c3f52729
MEDIA_MANAGER_VERSION=0.1.0-beta.28
MEDIA_MANAGER_COMMIT=f219944588c8e3ab717ea172cca19a529bc8bc91
```

**Upgrading from v0.1.0-beta.27:** no database change (schema 70).

### Known limitations

As in v0.1.0-beta.11.
