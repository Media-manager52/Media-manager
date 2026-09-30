import assert from "node:assert/strict";
import test, { after } from "node:test";
import { fileURLToPath } from "node:url";

import { createServer } from "vite";

const root = fileURLToPath(new URL("..", import.meta.url));
const vite = await createServer({
  appType: "custom",
  configFile: false,
  root,
  resolve: { alias: { "@": root } },
  server: { middlewareMode: true },
});

after(async () => {
  await vite.close();
});

const { workflowStep, attentionHeadline, retryPlan } = await vite.ssrLoadModule("/lib/workflow-presentation.ts");

test("steps follow persisted states and only Plex confirmation completes", () => {
  assert.deepEqual(workflowStep("searching"), { current: 0, complete: false });
  assert.deepEqual(workflowStep("streaming_ready"), { current: 3, complete: false });
  assert.deepEqual(workflowStep("plex_scanning"), { current: 6, complete: false });
  assert.deepEqual(workflowStep("plex_confirmed"), { current: 7, complete: true });
});

test("non-acquisition and unknown states never look further along", () => {
  assert.equal(workflowStep("waiting").current, -1);
  assert.equal(workflowStep("needs_attention").current, -1);
  assert.equal(workflowStep("some_future_state").current, -1);
});

test("attention headlines name the kind of problem", () => {
  assert.equal(attentionHeadline("subtitle_requirements_unverified").label, "Verification failed");
  assert.equal(attentionHeadline("round_exhausted_waiting_new_identity").label, "No acceptable release");
  assert.equal(attentionHeadline("altmount_job_attention").label, "Download client needs you");
  assert.deepEqual(attentionHeadline(undefined, "quarantined"), { label: "Owner action required", tone: "bad" });
  assert.equal(attentionHeadline("something_new").label, "Needs your decision");
});

test("retry plan uses only persisted schedule facts", () => {
  const now = Date.parse("2026-09-30T06:00:00Z");
  assert.equal(retryPlan({ next_rescan_at: "2026-09-30T10:12:00Z" }, now), "Auto-retry in 4h 12m");
  assert.equal(retryPlan({ next_rescan_at: "2026-09-30T06:20:00Z" }, now), "Auto-retry in 20m");
  assert.equal(retryPlan({ next_rescan_at: "2026-09-30T05:00:00Z" }, now), "Automatic retry due now");
  assert.equal(retryPlan({ retry_discovery_state: "ready" }, now), "New release found · waiting for your approval");
  assert.equal(retryPlan({ retry_discovery_state: "waiting" }, now), "Waiting for a new eligible release");
  assert.equal(retryPlan({}, now), "Will not retry automatically");
  assert.equal(retryPlan(undefined, now), "Will not retry automatically");
});
