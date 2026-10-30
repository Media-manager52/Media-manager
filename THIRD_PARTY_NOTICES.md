# Third-party notices

Media Manager's original project code is licensed under **AGPL-3.0-only**. This
file records the principal third-party components and their licenses. It does not
relicense any third-party component, and it is not a substitute for the license
text shipped with each dependency.

## Bundled notices

- **shadcn Tailwind CSS 4.13.0** — MIT. The local notice is
  [`third_party/shadcn-tailwind-4.13.0.LICENSE.md`](third_party/shadcn-tailwind-4.13.0.LICENSE.md).
- **Geist Mono and Inter fonts** (`@fontsource-variable/geist-mono`,
  `@fontsource-variable/inter`) — SIL Open Font License 1.1. The npm packages
  contain the applicable OFL notices.
- **sharp / libvips** — the `sharp` JavaScript package is Apache-2.0; the
  platform `@img/sharp-libvips-*` binaries are LGPL-3.0-or-later. The prebuilt
  npm packages do not ship a license file, so the LGPL-3.0 text is preserved in
  [`third_party/LGPL-3.0.txt`](third_party/LGPL-3.0.txt) (with
  [`GPL-3.0.txt`](third_party/GPL-3.0.txt) by reference); libvips source is at
  <https://github.com/libvips/libvips> and the prebuilt source at
  <https://github.com/lovell/sharp-libvips>. Media Manager does not modify
  libvips; the library remains dynamically loaded.
- **shadcn/ui and Radix/Base UI components** — MIT; the component packages retain
  their license notices in `node_modules` and the lockfile.
- **caniuse-lite / caniuse data** — CC-BY-4.0; data derived from
  <https://caniuse.com> and BrowserStack. Attribution is retained here; the
  package retains its own notice.
- **lightningcss and other MPL-2.0 packages** — MPL-2.0; those source files remain
  available under MPL-2.0 and retain their notices.
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

## Dependency inventory

The exact npm dependency graph and versions are pinned in
[`package-lock.json`](package-lock.json). The full npm graph, including
dev/build tooling copied into the UI runtime image, uses MIT, Apache-2.0,
BSD-2-Clause, BSD-3-Clause, ISC, MPL-2.0, LGPL-3.0-or-later, OFL-1.1, CC0-1.0,
CC-BY-4.0, Python-2.0, BlueOak-1.0.0 and 0BSD components. The Go module graph in
[`go.mod`](go.mod)/[`go.sum`](go.sum) uses permissive MIT/BSD/Apache-style
modules. No dependency is presented as relicensed under AGPL-3.0-only.

## External services and content

TMDB, TheTVDB, Simkl, Plex, AltMount, Seerr, Newznab indexers, and the media
metadata they provide are governed by their own terms. The AGPL grant applies to
Media Manager's code, not to third-party APIs, metadata, artwork, or media.
