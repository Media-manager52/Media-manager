# Third-party notices

Media Manager's original project code is licensed under **AGPL-3.0-only**. This
file records the principal third-party components and their licenses. It does not
relicense any third-party component, and it is not a substitute for the license
text shipped with each dependency.

## Bundled notices

- **shadcn Tailwind CSS 4.13.0** — MIT. The local notice is
  [`third_party/shadcn-tailwind-4.13.0.LICENSE.md`](third_party/shadcn-tailwind-4.13.0.LICENSE.md).
- **Geist and Inter fonts** (`@fontsource-variable/*`) — SIL Open Font License
  1.1. The npm packages contain the applicable OFL notices.
- **sharp / libvips** — the `sharp` JavaScript package is Apache-2.0; the
  platform `@img/sharp-libvips-*` binaries are LGPL-3.0-or-later. The libvips
  notices and source links are included in those packages. Media Manager does not
  modify libvips.
- **shadcn/ui and Radix/Base UI components** — MIT; the component packages retain
  their license notices in `node_modules` and the lockfile.
- **caniuse-lite / caniuse data** — CC-BY-4.0; data derived from
  <https://caniuse.com> and BrowserStack. Attribution is retained here; the
  package retains its own notice.
- **lightningcss and other MPL-2.0 packages** — MPL-2.0; those source files remain
  available under MPL-2.0 and retain their notices.

## Dependency inventory

The exact npm dependency graph and versions are pinned in
[`package-lock.json`](package-lock.json). The reviewed production graph uses MIT,
Apache-2.0, BSD-2-Clause, BSD-3-Clause, ISC, MPL-2.0, LGPL-3.0-or-later,
OFL-1.1, CC0-1.0, CC-BY-4.0, Python-2.0, BlueOak-1.0.0 and 0BSD components. The
Go module graph in [`go.mod`](go.mod)/[`go.sum`](go.sum) uses permissive
MIT/BSD/Apache-style modules. No dependency is presented as relicensed under
AGPL-3.0-only.

## External services and content

TMDB, TheTVDB, Simkl, Plex, AltMount, Seerr, Newznab indexers, and the media
metadata they provide are governed by their own terms. The AGPL grant applies to
Media Manager's code, not to third-party APIs, metadata, artwork, or media.
