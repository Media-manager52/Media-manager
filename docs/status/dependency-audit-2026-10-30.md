# Dependency security audit — 2026-10-30

Scope: the public UI/frontend dependency graph on `main` (`31789f7`) as
reported by GitHub Dependabot and `npm audit`. Nine open high/critical alerts
were reviewed; this is an audit of known advisories, not proof that every
runtime code path is exploitable.

## High and critical advisories

| Package | GHSA / CVE | Installed | Patched | Relationship | Scope | Assessment and action |
|---|---|---|---|---|---|---|
| `next` | GHSA-673q-mjm6-r5xr | 16.3.6 | 16.4.1 | direct | runtime | Critical unauthenticated RCE in `use cache` typed-array results. Upgraded to 16.4.3. |
| `next` | GHSA-2464-fhr4-w9xw | 16.3.6 | 16.4.1 | direct | runtime | Critical unauthenticated RCE in Image Optimization with SVG. Upgraded to 16.4.3. The UI uses `next/image`; do not expose the optimizer to untrusted SVG. |
| `next` | GHSA-p3m5-59mp-jv5r (CVE-2026-94482) | 16.3.6 | 16.4.1 | direct | runtime | Server Action denial of service. Upgraded to 16.4.3. |
| `next` | GHSA-cjq9-62q9-8jv4 (CVE-2026-94483) | 16.3.6 | 16.3.8 | direct | runtime | Image Optimization SSRF. Upgraded to 16.4.3. |
| `zod` | GHSA-mrhx-c7xc-m3w5 (CVE-2023-54404) | 3.25.76 | 4.6.6 | direct | runtime | Oversized constrained collections can exhaust memory. There are no `zod` imports in application source; it is kept for resolver compatibility and upgraded to 4.6.7. |
| `source-map-js` | GHSA-68fv-2mgg-jv7q (CVE-2026-93749) | 1.2.1 | 1.2.2 | transitive (PostCSS/Next) | runtime | Source-map section offsets can cause event-loop denial of service. Pinned to 1.2.2. |
| `sharp` | GHSA-wq5f-xc86-pv6w | 0.35.4 | 0.35.5 | transitive (Next optional, Miniflare) | runtime/development | librsvg image-decoding issue. Pinned to 0.35.5; production image optimization must not accept attacker-controlled SVG. |
| `undici` | GHSA-xjj6-f33q-929c (CVE-2026-109869) | 7.29.1 | 7.30.1 | transitive (Wrangler/Miniflare) | development | Content-encoding denial of service. Development tooling only; pinned to 7.30.1 and Wrangler/plugin updated. |
| `vite` | GHSA-587h-7wmj-h5pm (CVE-2026-112942) | 8.3.1 | 8.3.3 | direct | development | `launch-editor` path check on Windows; development server only. Upgraded to 8.3.5. |

The remaining moderate/low advisories were resolved by the same lockfile
refresh, including `fast-uri` (pinned to 3.1.9) and the newer Cloudflare
Wrangler/Miniflare tree.

## Verification performed for this audit

- `npm audit` and `npm audit --omit=dev`: **0 vulnerabilities** after the
  upgrade.
- `npm run typecheck`, `npm run lint`, full unit suite (48 tests), and full
  Playwright Chromium suite (159 tests): pass.
- `npm test` builds the production UI and runs the unit suite.
- No production deployment, container restart, provider query, or media
  download was performed.

## Residual risk

The frontend still exposes Next Image Optimization for configured remote
artwork hosts. The patched Next release closes the reported SVG/SSRF issues,
but the deployment must keep the optimizer behind the authenticated Cosmos
edge and must not add arbitrary remote image hosts. `zod` is present only for
resolver compatibility; it should be removed if no future code uses it.
The current UI image copies the full `node_modules` tree, so development and
build tooling (Vite, Wrangler/Miniflare, undici) is present in the final image
even when its libraries are not on the primary request path. Those advisories are
therefore fixed rather than dismissed as development-only. Pruning development
dependencies from the runtime image is a separate hardening follow-up and must
not be done without verifying that the vinext/Wrangler runtime still starts.
