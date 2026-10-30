# Schema 50 deployment plan (not authorized, not applied)

This plan covers the `0050_search_partial_retries.sql` migration from PR #86.
It has **not** been run against production. Production remains on schema 49 and
must not be migrated without explicit owner authorization.

## Preconditions

- The exact main commit and its successful Verify run are recorded.
- API/UI image index and linux/amd64 digests are recorded.
- A fresh encrypted backup exists and its restore verification succeeded.
- The backup encryption key is stored separately from the backup.
- An off-site copy or an independent backup copy exists; a local backup on the
  same host alone is not disaster recovery.
- The owner has approved a maintenance window and the deployment method.
- **Do not swap only the API image.** API and UI must be the same verified
  commit, and the previous image digests must be recorded for rollback.

## Deployment

1. Record the running commit/schema and previous immutable API/UI digests.
2. Create an encrypted backup through the owner UI/API. Wait for `verified`,
   download the artifact, verify its recorded size/SHA-256, and perform an
   isolated restore check. Copy it off the host if the off-site destination is
   configured.
3. Confirm the release image pair and Verify run. Use the existing verified
   deployment broker/Deploy workflow; do not run an ad-hoc production migration
   command.
4. The API runs the transactional migration before it listens. A failure must
   leave schema 49 and the previous deployment recoverable; capture API logs.
5. Wait for `/health/ready` and `/api/v1/system/info`; confirm the expected
   schema and exact image commit.
6. Verify normal reads (dashboard, wanted, workflows, managed files), then a
   small number of owner-triggered searches. Do not run a bulk search or trigger
   downloads merely to test the migration.
7. Watch provider request counts, worker backlog, workflow revisions, and
   partial-retry state. Existing workflows must remain owner-visible; no
   acquisition or publication policy is changed by schema 50.

## Failure and rollback

- If migration fails before commit, keep the previous schema/images and follow
  the broker failure path; do not retry blindly.
- If migration commits but startup fails, stop and diagnose before serving.
- Schema 50 is forward-only. To return to schema 49, restore the pre-deployment
  backup into a **fresh** database, verify it, and deploy the previous image
  pair. Never run schema-49 images against a schema-50 database.
- After a committed migration, a database restore is required for rollback;
  simply checking out the previous image is unsafe.
- Record the restored schema, image digests, health result, and any lost writes
  between the backup and the failure.

## Post-deployment evidence to retain

- Verify run URL and exact commit.
- Previous and new image digests.
- Backup record, SHA-256, schema, restore-verification result, and off-site
  state (or the explicit owner risk acceptance if off-site is absent).
- `/health/ready` and `/api/v1/system/info` outputs.
- A bounded read-only sample of workflows and partial-retry state.
- Rollback decision and timing, if used.
