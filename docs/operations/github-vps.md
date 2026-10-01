# GitHub builds, uploads and updates on the VPS

GitHub coordinates the work; a dedicated VPS account performs builds and tests.
The runner is registered only to the private Media Manager repository as
`media-manager-vps`. It runs one job at a time, with an aggregate ceiling of
five CPU cores and 12 GiB RAM across its runner, Docker engine and containers.
Production has the remaining capacity. The CI user has no sudo or production
Docker-group membership and cannot read the owner's home or production secrets.

## Normal use

1. Push a feature branch and open a pull request. **Verify** runs frontend
   lint/typecheck/unit tests, browser tests, full Go vet/race tests with its
   disposable PostgreSQL, deployment-boundary regressions and container smoke.
   Fork pull requests and public repositories cannot use this workflow's runner.
2. Merge the passing change into `main`. Verification runs again for the actual
   merged commit, then **publish** uploads the API and UI images to GitHub
   Container Registry. Images carry full commit tags and source/revision labels:
   `ghcr.io/media-manager52/media-manager-api:sha-<commit>` and
   `ghcr.io/media-manager52/media-manager-ui:sha-<commit>`. The `main` tags point
   to the most recently verified upload; deployments use the full commit tags.
3. Open **Actions → Deploy → Run workflow**, select `main`, and run it. Leave
   the verification run ID blank to select the successful run for that exact
   commit. Production deployment is manual; pushes alone do not update it.
4. Read the Deploy job summary for its result and running schema. A failed
   deployment stays failed and requires review; no automatic database rollback
   or failed-media deletion occurs.

No long-lived registry secret is added to GitHub. Publishing uses the job's
short-lived `GITHUB_TOKEN` with package-write permission. Registry login data
is kept in a temporary CI directory and removed afterward. The packages remain
private and linked to the repository. Source code still changes through Git;
database backups, indexer credentials, NZBs and media are never uploaded.

## Isolation and deployment checks

CI runs on its own rootless Docker daemon under `mm-ci`; it cannot use the
production Docker socket. Disposable PostgreSQL uses loopback port 35432;
container smoke uses 38081, unique project/network names, empty integration
credentials, temporary mount roots and an internal integration network.
HTTP smoke probes run from the disposable UI on its internal frontend network;
the entirely internal stack does not need host port forwarding for these probes.
Neither check uses production port 28081 or production containers/mounts.
Stopped CI containers and unused networks/volumes are cleaned after jobs;
local compiler/tool/image caches remain for subsequent builds.

The root-owned deployment broker accepts only a commit and two numeric run
identities. It independently checks GitHub for current `main`, a successful
Verify run and all five successful jobs on this VPS runner, and an active
owner-triggered Deploy run from the same repository/main commit. It checks
image labels before transferring the already-tested images from the CI engine.
The runner receives no production Docker access or integration credentials.

Before replacing API/UI, the broker saves protected environment files, previous
image identities and archives, a PostgreSQL custom dump with a readable table
data inventory, and the proposed images. It rechecks current `main` after the
backup, advances the clean production checkout with a fast-forward, then starts
only API/UI with the tested images. Compose health checks and an authenticated
running-commit check must pass. PostgreSQL, AltMount media and existing failed
outputs stay under their normal lifecycle protections.
The broker publishes the job result atomically after completing its JSON and
read permissions, so the waiting runner cannot observe an incomplete result.

The deployment image override is ignored local state at
`.sites-runtime/github-deploy-images.json`. When starting the stack manually
after a GitHub deployment, include it:

```sh
sudo docker compose -f compose.yaml -f .sites-runtime/github-deploy-images.json up -d --no-build
```

Backups remain on the server; off-server copies and retention review are still
needed. A schema change can prevent starting an old binary, so rollback needs
a compatible binary or a separately reviewed database restore.

## Host administration

The CI account is `/var/lib/media-manager-ci`, with 65,536 non-overlapping
subordinate user/group IDs, `uidmap`, rootless Docker extras and lingering
enabled. Its user Docker service and `mm-github-runner.service` start on boot.
The runner package is downloaded from the official Actions runner release and
checksum-verified before registration. Runner updates remain enabled.

After reviewing changes to the broker/units, an owner can install them with:

```sh
sudo bash deploy/github/install-host-services.sh
```

These privileged files are not automatically replaced by CI. Their source is
in [deploy/github](../../deploy/github/); the installer requires the dedicated
account and active rootless engine to exist. Inspect status with
`gh api repos/Media-manager52/Media-manager/actions/runners`,
`systemctl status media-manager-github-deploy.path`, and the CI user's
`systemctl --user status mm-github-runner docker`. Deployment logs/results stay
under `/var/lib/media-manager-deploy`, with protected logs and read-only results.
Pause new build work by stopping the CI user's runner service; production
continues independently. Let a running deployment finish before changing its
broker or stopping services.

This account's current GitHub plan does not provide branch protection for a
private repository. Review and merge only passing PRs; the broker enforces
successful verification again before production can change. No paid-plan or
repository-visibility change is required for this setup.

[GitHub's billing documentation](https://docs.github.com/en/actions/concepts/billing-and-usage)
currently lists self-hosted execution as free. Container Registry storage and
bandwidth are also currently free under
[GitHub Packages billing](https://docs.github.com/en/billing/concepts/product-billing/github-packages).
Actions artifact storage is separate; failure screenshots have seven-day
retention and upload failure cannot replace the original test result.
The isolation follows [Docker's rootless guidance](https://docs.docker.com/engine/security/rootless/).
