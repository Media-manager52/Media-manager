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

const {
  workflowStage, stageInfo, titleStatus, attentionTitle, offeredActions,
  awaitingApproval, requestStatus, relativeTime, waitingDetail, languageName, posterTone,
} = await vite.ssrLoadModule("/lib/app-presentation.ts");

const workflow = (state, extra = {}) => ({ id: state, media_id: "m", media_kind: "movie", title: "T", variant: "movie", state, revision: 1, adopted_replacement_confirmation_required: false, created_at: "", updated_at: "", ...extra });
const action = (name, extra = {}) => ({ action: name, operation_id: name, method: "POST", path: `/api/v1/workflows/w/${name}`, effect: "provider", available: true, ...extra });

test("workflow states map to plain stages without inventing progress", () => {
  assert.equal(workflowStage(workflow("searching")), "finding");
  assert.equal(workflowStage(workflow("queued")), "downloading");
  assert.equal(workflowStage(workflow("verifying")), "checking");
  assert.equal(workflowStage(workflow("plex_scanning")), "adding");
  assert.equal(workflowStage(workflow("plex_confirmed")), "in_plex");
  assert.equal(workflowStage(workflow("monitored")), "waiting");
  assert.equal(workflowStage(workflow("needs_attention")), "needs_you");
  assert.equal(workflowStage(workflow("waiting", { owner_action_required: true })), "needs_you");
  assert.equal(workflowStage(workflow("something_new")), "not_started");
});

test("download labels use only the reported percentage", () => {
  assert.equal(stageInfo("downloading", { download_percent: 63.6 }).label, "Downloading 64%");
  assert.equal(stageInfo("downloading", {}).label, "Downloading");
  assert.equal(stageInfo("downloading", { download_percent: 140 }).short, "100%");
  assert.equal(stageInfo("needs_you", { attention_code: "subtitle_unverified" }).step, 3);
  assert.equal(stageInfo("in_plex").step, 5);
});

test("title status prefers owner decisions, then active work, then Plex", () => {
  const needs = workflow("needs_attention", { owner_action_required: true });
  assert.equal(titleStatus([workflow("searching"), needs], true).stage, "needs_you");
  assert.equal(titleStatus([workflow("searching"), workflow("downloading", { download_percent: 10 })], true).label, "Downloading 10%");
  assert.equal(titleStatus([workflow("monitored")], true).stage, "in_plex");
  assert.equal(titleStatus([workflow("monitored")], false).stage, "waiting");
  assert.equal(titleStatus([], false).label, "Not in Plex yet");
});

test("attention headlines are plain words", () => {
  assert.equal(attentionTitle(workflow("needs_attention", { attention_code: "subtitle_unverified" })), "The download didn’t pass the check");
  assert.equal(attentionTitle(workflow("waiting", { retry_discovery_state: "ready" })), "A new copy was found");
  assert.equal(attentionTitle(workflow("needs_attention", { access_rejected_candidate_id: "c" })), "The indexer refused the download");
  assert.equal(attentionTitle(workflow("quarantined")), "A download got stuck");
});

test("only available, phrase-free server actions are offered, cancel last", () => {
  const offered = offeredActions({ available_actions: [
    action("cancel", { effect: "write" }),
    action("search", { confirmation: "REPLACE ADOPTED FILE" }),
    action("discover"),
    action("open_next_round"),
    action("reconfirm_plex", { available: false, reason: "not now" }),
    action("unknown_future_action"),
  ] });
  assert.deepEqual(offered.map((item) => item.label), ["Try the new copy", "Look for new copies", "Stop"]);
  assert.equal(offered[0].primary, true);
  assert.equal(offered.at(-1).confirm, true);
  assert.deepEqual(offeredActions(undefined), []);
  assert.deepEqual(offeredActions({ available_actions: [action("cancel")] }).map((item) => item.primary), [false]);
});

test("request status separates approval, problems, progress and done", () => {
  const request = (extra) => ({ seerr_state: "2", lifecycle: "waiting", attention_count: 0, workflow_count: 1, ...extra });
  assert.equal(awaitingApproval(request({ seerr_state: "1" })), true);
  assert.equal(requestStatus(request({ seerr_state: "1", lifecycle: "not_approved" })).label, "Waiting for your OK");
  assert.equal(requestStatus(request({ lifecycle: "plex_confirmed" })).group, "in_plex");
  assert.equal(requestStatus(request({ lifecycle: "needs_attention", attention_count: 1 })).label, "Needs you");
  assert.equal(requestStatus(request({ lifecycle: "searching" })).label, "Finding a copy");
  assert.equal(requestStatus(request({ lifecycle: "waiting" })).label, "Waiting for a release");
  assert.equal(requestStatus(request({ lifecycle: "approved_in_seerr", workflow_count: 0 })).label, "Approved — being added");
});

test("relative time and waiting detail stay coarse", () => {
  const now = Date.parse("2026-10-29T12:00:00Z");
  assert.equal(relativeTime("2026-10-29T09:00:00Z", now), "3 hours ago");
  assert.equal(relativeTime("2026-10-28T10:00:00Z", now), "yesterday");
  assert.equal(relativeTime("2026-10-29T14:00:00Z", now), "in 2 hours");
  assert.equal(relativeTime("2026-10-29T12:00:20Z", now), "just now");
  assert.equal(relativeTime("not a date", now), "");
  assert.equal(waitingDetail({ next_rescan_at: "2026-10-29T14:00:00Z" }, now), "Next search in 2 hours");
  assert.equal(waitingDetail({ retry_discovery_state: "ready" }, now), "A new copy was found and is waiting for you");
});

test("language names and poster tones are stable", () => {
  assert.equal(languageName("da"), "Danish");
  assert.equal(posterTone("Druk"), posterTone("Druk"));
  assert.match(posterTone("Druk"), /^hsl\(\d+ 24% 26%\)$/);
});
