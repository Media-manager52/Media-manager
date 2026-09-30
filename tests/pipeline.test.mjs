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

const { groupPipeline, pipelineStage } = await vite.ssrLoadModule("/lib/pipeline.ts");

function workflow(id, state, updated, extra = {}) {
  return { id, state, updated_at: updated, owner_action_required: false, ...extra };
}

test("maps persisted workflow states to control-room stages", () => {
  assert.equal(pipelineStage(workflow("a", "monitored")), "requested");
  assert.equal(pipelineStage(workflow("a", "held")), "requested");
  assert.equal(pipelineStage(workflow("a", "candidate_selected")), "searching");
  assert.equal(pipelineStage(workflow("a", "streaming_ready")), "downloading");
  assert.equal(pipelineStage(workflow("a", "import_complete")), "verifying");
  assert.equal(pipelineStage(workflow("a", "plex_scanning")), "publishing");
  assert.equal(pipelineStage(workflow("a", "plex_confirmed")), "in_plex");
  assert.equal(pipelineStage(workflow("a", "needs_attention")), "attention");
  assert.equal(pipelineStage(workflow("a", "cancelled")), "stopped");
});

test("owner action always wins and unknown states never look further along", () => {
  assert.equal(pipelineStage(workflow("a", "waiting", undefined, { owner_action_required: true })), "attention");
  assert.equal(pipelineStage(workflow("a", "some_future_state")), "requested");
});

test("groups workflows by stage with the newest activity first", () => {
  const groups = groupPipeline([
    workflow("old", "searching", "2026-09-01T00:00:00Z"),
    workflow("new", "searching", "2026-09-02T00:00:00Z"),
    workflow("done", "plex_confirmed", "2026-09-01T00:00:00Z"),
  ]);
  assert.deepEqual(groups.searching.map((item) => item.id), ["new", "old"]);
  assert.deepEqual(groups.in_plex.map((item) => item.id), ["done"]);
  assert.equal(groups.attention.length, 0);
});
