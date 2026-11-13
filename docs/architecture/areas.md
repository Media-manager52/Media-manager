# Area map

Where to go for a change or a bug. Each area is one subject of the product,
from the page a person sees to the code and tests behind it.

The table of files per area is `scripts/dev/areas.conf`, and
`internal/architecture` fails the build if it drifts from the code.

## Test one area

```bash
scripts/dev/test-area.sh            # list the areas
scripts/dev/test-area.sh search     # run that area's Go tests
```

This is for working quickly. It starts a throwaway PostgreSQL in Docker unless
`MEDIA_MANAGER_TEST_DATABASE_URL` is set. CI still runs everything.

## Areas

| Area | What it does | Service code | Web page | In Fejllog |
| --- | --- | --- | --- | --- |
| `search` | Asks indexers, reads release names, approves or rejects releases | `internal/acquisition/search*.go`, `internal/releases`, `internal/indexers` | Arbejde, Kilder | Søgning |
| `rounds` | Attempts per title: which release is tried next, when a new round opens | `internal/acquisition`, `internal/workflows` | Arbejde, Kræver handling | Titler |
| `publication` | Sends to AltMount, verifies, links into the library, confirms in Plex | `internal/acquisition`, `internal/publication`, `internal/packs`, `internal/adapters/altmount`, `internal/adapters/plex` | Arbejde, titelside | Download |
| `language` | Proves language in the actual file | `internal/subtitles`, `internal/mediainfo`, `internal/languages` | Arbejde (sprogkontrol) | Sprog |
| `repairs` | Replaces files AltMount reports broken or that lack required audio | `internal/acquisition/altmount_repair.go` | System → Reparationer | Reparation |
| `library` | Titles, seasons, moving between libraries, manual add, airing dates | `internal/library`, `internal/metadata`, `internal/catalog` | Bibliotek, Tilføj, Kalender | Titler |
| `commissioning` | Tests and approves an indexer before it may be used | `internal/commissioning`, `internal/adapters/newznab`, `internal/adapters/oldboys` | Kilder | Forbindelser |
| `seerr` | Requests from Seerr, routing, status sent back | `internal/intake`, `internal/adapters/seerr` | Anmodninger, Indstillinger → Seerr | Titler |
| `adoption` | Takes over existing files | `internal/adoption`, `internal/adoptionexec` | Indstillinger → Overtag | Titler |
| `recycle` | Deleted titles, restore, archive | `internal/recycle` | System → Papirkurv | Titler |
| `overview` | Read-only views: dashboard, activity, problems, Fejllog, calendar data | `internal/httpapi` | Oversigt, Fejllog, System | all |
| `settings` | Connections, libraries, quality profiles, keys | `internal/quicksetup`, `internal/integrationsecrets` | Indstillinger, Kom i gang | Forbindelser |
| `backups` | Encrypted backups | `internal/backups` | System → Backup | Backup |
| `notifications` | Messages to the owner's destinations | `internal/notifications` | Indstillinger → Notifikationer | Forbindelser |
| `lists` | Import lists and collections | `internal/importlists` | Kilder | Forbindelser |
| `servarr` | Radarr/Sonarr-style API, Prowlarr backup import | `internal/servarr`, `internal/prowlarrbackup` | Kilder | Forbindelser |
| `core` | Database start-up, migrations, background queue | `internal/postgres`, `cmd/media-manager` | System → Baggrundskø | Forbindelser |

The website itself is in `web/`: `src/pages` has one file per page and
`src/i18n.ts` is the only place API codes become words. Test it from `web/` with
`npx vitest run` and `npx playwright test`. See [frontend.md](frontend.md).

## Finding a bug

1. Open **Fejllog** on the website and note the area and the code under
   *Teknisk detalje*.
2. Find the area above, and search for the code in that area's service code.
3. Fix it, then run `scripts/dev/test-area.sh <area>`.
4. Add a test that fails without the fix.

## Adding something new

Find the area it belongs to and work there. Rules that apply everywhere are in
[boundaries.md](boundaries.md) and [../invariants.md](../invariants.md). If a
new file is added to `internal/postgres`, add it to `scripts/dev/areas.conf`.
How storage is divided is explained in [boundaries.md](boundaries.md#storage-subjects).
