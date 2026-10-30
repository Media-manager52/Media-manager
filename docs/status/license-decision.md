# Licensing decision for public beta

Status: **RESOLVED**. The owner selected **GNU AGPL-3.0-only** for Media
Manager. The official, unmodified license text is committed as [`LICENSE`](../../LICENSE)
(SPDX: `AGPL-3.0-only`). `package.json`, the README, the container image labels,
the Cosmos listing, and the release documentation use the same identifier.

This record is not legal advice.

## Owner decision

- License: **GNU Affero General Public License, version 3 only**
- SPDX identifier: `AGPL-3.0-only`
- Canonical corresponding source: <https://github.com/Media-manager52/Media-manager>
- No commercial or proprietary sublicense is granted by this decision.

## Verification performed

- The committed `LICENSE` is the verbatim GNU AGPL v3 text from
  `https://www.gnu.org/licenses/agpl-3.0.txt`; SHA-256
  `0d96a4ff68ad6d4b6f1f30f713b18d5184912ba8dd389f86aa7710db079abcb0`.
- The repository had no previous `LICENSE` and no third-party source was
  relicensed. Existing notices are preserved in
  [`third_party/`](../../third_party) and [`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md).
- The npm lockfile was inspected for the production graph: its components are
  MIT, Apache-2.0, BSD, ISC, MPL-2.0, LGPL-3.0-or-later, OFL-1.1, CC0-1.0,
  CC-BY-4.0, Python-2.0, BlueOak-1.0.0 or 0BSD. sharp/libvips remains
  LGPL-3.0-or-later with its own notices; AGPL-3.0-only does not replace it.
- The Go module graph uses permissive MIT/BSD/Apache-style modules.
- The repository history identifies the owner account `Media-manager52` and
  automation identities. No CLA, copyright-assignment file, or separate
  contributor agreement is present in the repository. The owner authorized this
  license; independent legal confirmation of copyright ownership remains an
  owner/legal review item, not a claim made by this document.

## Distribution wording

- Source and published container images are offered under `AGPL-3.0-only`.
- The Cosmos listing and install template are under the same project license;
  they do not add restrictions.
- Third-party dependencies, fonts, metadata APIs, media, Plex, AltMount, Seerr,
  Newznab indexers, TMDB, TheTVDB, Simkl, and other services remain under their
  own terms.
- Operators of modified network services are responsible for offering the
  corresponding source as required by AGPL section 13.

## Residual legal review

The repository contains no formal copyright assignment or contributor license
agreement. If the owner needs a definitive chain-of-title opinion, legal counsel
must review contribution history and automation-authored commits. No ownership
claim beyond the owner's authorization is asserted here.
