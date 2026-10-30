# License decision for public beta

Status: **OWNER DECISION REQUIRED**. No license file has been added and the
repository must not be described as licensed until the owner chooses. This is
not legal advice.

## Current inconsistency

- The repository has no `LICENSE`.
- `README.md` asks the owner before redistributing and does not grant rights.
- `deploy/cosmos/README.md` says the application source is **private** and
  **all rights reserved**, while the source repository and images are publicly
  accessible.
- Public beta distribution, container images, and the Cosmos listing need one
  consistent grant.

## Options

| License | Practical effect | Main obligations | Fit for Media Manager |
| --- | --- | --- | --- |
| **Apache-2.0** | Permissive: anyone may use, modify, redistribute, and run a hosted service. | Preserve copyright/license notices; include NOTICE if present; state changes; patent grant. | Easiest public adoption and commercial/self-hosted reuse; does not force derivative source disclosure. |
| **AGPL-3.0** | Strong copyleft, including network use: modified versions offered as a service must offer corresponding source. | Source availability for network users; derivative works under AGPL; notices and installation information where applicable. | Best fit if the project must remain community-owned and hosted modifications must stay open; some companies avoid AGPL. |
| **MIT** | Very short permissive license; broad reuse with minimal conditions. | Preserve copyright and permission notice. | Simplest, but no explicit patent grant and less explicit contributor terms than Apache-2.0. |

A separate commercial license would require a contributor/CLA strategy and is a
larger business decision; it should not be layered onto the beta unexpectedly.

## Documentation changes after selection

- Add the chosen `LICENSE` and any `NOTICE` file.
- Replace the “private / all rights reserved” text in `deploy/cosmos/README.md`
  and the “ask before redistributing” wording in `README.md`.
- Add the license and source URL to the Cosmos listing description and image
  labels where supported.
- Keep third-party notices for dependencies and bundled fonts/assets.
- State whether the license applies to source, published images, and the Cosmos
  listing separately, without contradicting them.
- Review the `private` flag/metadata in `package.json` and any distribution
  metadata before publishing a package.

## Recommendation framing

Apache-2.0 is the lowest-friction public-beta choice if the owner wants broad
self-hosting and ecosystem adoption. AGPL-3.0 is the coherent choice if network
operators must share modifications. The owner must choose; do not infer one from
the current public repository.
