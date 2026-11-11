# Third-party notices

Media Manager's original project code is licensed under **AGPL-3.0-only**. This
file records the principal third-party components and their licenses. It does not
relicense any third-party component, and it is not a substitute for the license
text shipped with each dependency.

## Bundled notices

- **Inter font** (`@fontsource-variable/inter`) — SIL Open Font License 1.1,
  bundled into the web UI build. The npm package contains the OFL notice.
- **Web UI runtime libraries** bundled into `web/dist`: React and React DOM
  (MIT), React Router (MIT), TanStack Query (MIT) and Lucide icons (ISC). Their
  notices are in the packages pinned by `web/package-lock.json`.
- **Build-only tooling** (Vite, Tailwind CSS, lightningcss under MPL-2.0,
  TypeScript, ESLint, Vitest, Playwright) runs at build or test time and is not
  copied into the UI runtime image.
- **Alpine runtime packages** — the API image installs `ffmpeg` 8.0.1
  (`GPL-2.0-or-later AND LGPL-2.1-or-later`), `postgresql18-client`
  (`PostgreSQL`), and `ca-certificates` (`MPL-2.0 AND MIT`) from Alpine. The
  GPL-2.0, LGPL-2.1 and LGPL-3.0 texts are preserved under `third_party/`.
  ffmpeg 8.0.1 source is at
  <https://ffmpeg.org/releases/ffmpeg-8.0.1.tar.xz>; Alpine package recipes
  and exact source references are at
  <https://gitlab.alpinelinux.org/alpine/aports/-/tree/master/community/ffmpeg>.
  These are separate programs aggregated in the image, not relicensed as part
  of Media Manager.

## Adapted design

- **HolyArrs** (<https://github.com/HolyArrs/HolyArrs>, web UI licensed
  **AGPL-3.0-only**, the same license as Media Manager). The web UI's colour,
  surface, text and shadow values in `web/src/styles.css`, the grouped menu and
  the overview's picture tiles and count cards, the library's poster grid and
  the title page's wide picture, facts and cast row, and the picture cards on
  Kræver handling and Anmodninger, the calendar tiles and Arbejde's dense list
  are adapted from HolyArrs'
  design. No HolyArrs source file is copied; Media Manager's own components
  are written in React. HolyArrs' name and logo are not used.

## Dependency inventory

The exact npm dependency graph and versions are pinned in
[`web/package-lock.json`](web/package-lock.json). The UI runtime image contains
only the built bundle and a dependency-free Node server. The full npm graph,
including dev/build tooling, uses MIT, Apache-2.0,
BSD-2-Clause, BSD-3-Clause, ISC, MPL-2.0, LGPL-3.0-or-later, OFL-1.1, CC0-1.0,
CC-BY-4.0, Python-2.0, BlueOak-1.0.0 and 0BSD components. The Go module graph in
[`go.mod`](go.mod)/[`go.sum`](go.sum) uses permissive MIT/BSD/Apache-style
modules. No dependency is presented as relicensed under AGPL-3.0-only.

## External services and content

TMDB, TheTVDB, Simkl, Plex, AltMount, Seerr, Newznab indexers, and the media
metadata they provide are governed by their own terms. The AGPL grant applies to
Media Manager's code, not to third-party APIs, metadata, artwork, or media.
