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

const mediaID = "11111111-1111-4111-8111-111111111111";

function workflow(variant, state, overrides = {}) {
  return {
    id: `workflow-${variant}-${state}-${overrides.idSuffix ?? "a"}`,
    media_id: mediaID,
    media_kind: "series",
    title: "Airing Series",
    year: 2024,
    variant,
    state,
    revision: 1,
    adopted_replacement_confirmation_required: false,
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-02T00:00:00Z",
    ...overrides,
  };
}

function file(variant, state = "current", plexConfirmed = true) {
  return {
    id: `file-${variant}`,
    media_id: mediaID,
    media_kind: "series",
    title: "Airing Series",
    root_name: "TV Primary",
    destination_relative: `Airing Series/${variant}.mkv`,
    source_relative: `job/${variant}.mkv`,
    plex_visible_target: `/tv/${variant}.mkv`,
    state,
    generation: 1,
    plex_confirmed: plexConfirmed,
    variant,
    origin: "altmount",
    quality_profile: "TV 1080p Danish",
    quality_evidence_state: "media_info",
    quality_evidence: { facts: { resolution: "1080p" } },
  };
}

test("parses supported series variants and rejects unknown scopes", async () => {
  const { parseSeriesVariant } = await vite.ssrLoadModule("/lib/series-variants.ts");

  assert.deepEqual(parseSeriesVariant("season:01"), { kind: "season", season: 1 });
  assert.deepEqual(parseSeriesVariant("season:1"), { kind: "season", season: 1 });
  assert.deepEqual(parseSeriesVariant("season:12"), { kind: "season", season: 12 });
  assert.deepEqual(parseSeriesVariant("season:01:episode:01"), { kind: "episode", season: 1, episode: 1 });
  assert.deepEqual(parseSeriesVariant("season:02:episode:12"), { kind: "episode", season: 2, episode: 12 });

  for (const value of ["", "default", "movie", "season:", "season:abc", "season:01:episode:00", "season:01:episode", "season:01:special:01", "season:9999"]) {
    assert.equal(parseSeriesVariant(value).kind, "other", `${value} must not parse as a series scope`);
  }
});

test("rolls a normal season-pack workflow into its own season", async () => {
  const { rollupSeriesSeasons } = await vite.ssrLoadModule("/lib/series-variants.ts");
  const seasons = rollupSeriesSeasons([workflow("season:01", "plex_confirmed")], [file("season:01")]);

  assert.equal(seasons.length, 1);
  assert.equal(seasons[0].number, 1);
  assert.equal(seasons[0].packWorkflow?.state, "plex_confirmed");
  assert.equal(seasons[0].episodeWorkflows.length, 0);
});

test("rolls airing episode workflows into the matching season instead of a new season", async () => {
  const { rollupSeriesSeasons } = await vite.ssrLoadModule("/lib/series-variants.ts");
  const seasons = rollupSeriesSeasons([
    workflow("season:01", "waiting"),
    workflow("season:01:episode:01", "searching"),
  ]);

  assert.equal(seasons.length, 1, "season:01 and season:01:episode:01 are one season");
  assert.equal(seasons[0].packWorkflow?.state, "waiting");
  assert.equal(seasons[0].episodeWorkflows.length, 1);
  assert.deepEqual(seasons[0].acquiringEpisodes, [1]);
  assert.deepEqual(seasons[0].acquiredEpisodes, []);
});

test("aggregates multiple episode scopes and a mixed season plus episode history", async () => {
  const { rollupSeriesSeasons } = await vite.ssrLoadModule("/lib/series-variants.ts");
  const seasons = rollupSeriesSeasons([
    workflow("season:01", "waiting"),
    workflow("season:01:episode:01", "plex_confirmed"),
    workflow("season:01:episode:02", "searching"),
    workflow("season:01:episode:03", "waiting"),
    workflow("season:02:episode:01", "searching"),
  ], [file("season:01:episode:01")]);

  assert.equal(seasons.length, 2);
  const [first, second] = seasons;
  assert.equal(first.number, 1);
  assert.equal(first.packWorkflow?.state, "waiting");
  assert.deepEqual(first.acquiredEpisodes, [1]);
  assert.deepEqual(first.acquiringEpisodes, [2]);
  assert.deepEqual(first.waitingEpisodes, [3]);
  assert.equal(first.episodeFiles.length, 1);
  assert.equal(second.number, 2);
  assert.deepEqual(second.acquiringEpisodes, [1]);
});

test("reports future episode waiting and genuine needs-attention episodes separately", async () => {
  const { rollupSeriesSeasons } = await vite.ssrLoadModule("/lib/series-variants.ts");
  const seasons = rollupSeriesSeasons([
    workflow("season:03:episode:04", "waiting", { next_rescan_at: "2026-11-20T00:00:00Z" }),
    workflow("season:03:episode:05", "needs_attention", { owner_action_required: true, attention_detail: "Required Danish subtitles were not verified" }),
  ]);

  assert.equal(seasons.length, 1);
  assert.deepEqual(seasons[0].waitingEpisodes, [4]);
  assert.deepEqual(seasons[0].attentionEpisodes, [5]);
  assert.equal(seasons[0].attentionWorkflows[0].attention_detail, "Required Danish subtitles were not verified");
  assert.equal(seasons[0].nextSearchAt, "2026-11-20T00:00:00Z");
});

test("a confirmed episode is never demoted by a later cancelled duplicate", async () => {
  const { rollupSeriesSeasons } = await vite.ssrLoadModule("/lib/series-variants.ts");
  const seasons = rollupSeriesSeasons([
    workflow("season:01:episode:01", "plex_confirmed", { updated_at: "2026-09-01T00:00:00Z" }),
    workflow("season:01:episode:01", "cancelled", { updated_at: "2026-09-05T00:00:00Z" }),
  ]);

  assert.deepEqual(seasons[0].acquiredEpisodes, [1]);
  assert.equal(seasons[0].episodeWorkflows.length, 1);
});

test("overview classification separates active, waiting, completed, and attention", async () => {
  const { isActivelyAcquiring, isWaiting, isCompleted, requiresOwnerAttention, collectOwnerAttention } = await vite.ssrLoadModule("/lib/workflow-states.ts");

  for (const state of ["searching", "candidate_selected", "queued", "downloading", "verifying", "publishing", "plex_scanning"]) {
    assert.equal(isActivelyAcquiring(state), true, `${state} must be actively acquiring`);
  }
  for (const state of ["waiting", "monitored", "held", "planned", "retained"]) {
    assert.equal(isActivelyAcquiring(state), false, `${state} must not be actively acquiring`);
    assert.equal(isWaiting(state), true, `${state} must be waiting/monitored`);
  }
  assert.equal(isCompleted("plex_confirmed"), true);
  assert.equal(isWaiting("needs_attention"), false);
  assert.equal(requiresOwnerAttention("needs_attention"), true);
  assert.equal(requiresOwnerAttention("waiting"), false);
  assert.equal(requiresOwnerAttention("waiting", true), true);

  const attention = collectOwnerAttention([
    { workflow_id: "w1", media_id: mediaID, media_kind: "series", title: "Wanted Series", year: 2024, gap: "missing", variant: "season:01", current_state: "needs_attention", target_profile: "TV 1080p", upgrades_enabled: true, owner_action_required: true, attention_detail: "Subtitles missing", updated_at: "2026-09-02T00:00:00Z" },
  ], [
    workflow("season:01", "waiting"),
    workflow("season:01:episode:01", "needs_attention", { idSuffix: "b" }),
  ]);

  assert.equal(attention.length, 2);
  assert.equal(attention[0].title, "Wanted Series");
  assert.equal(attention[0].reason, "Subtitles missing");
  assert.equal(attention[1].variant, "season:01:episode:01");
  assert.match(attention[1].href, /^\/media\/series\//);
});

test("overview queue separates actively acquiring from waiting and includes wanted rows", async () => {
  const { isActivelyAcquiring, isWaiting, requiresOwnerAttention } = await vite.ssrLoadModule("/lib/workflow-states.ts");
  const { overviewQueueItems } = await vite.ssrLoadModule("/components/media-manager/live-operational-screens.tsx");

  const workflows = [
    workflow("season:01:episode:01", "searching", { idSuffix: "search" }),
    workflow("season:01", "waiting", { idSuffix: "pack" }),
    workflow("season:01:episode:02", "needs_attention", { idSuffix: "attn", owner_action_required: true }),
  ];
  const wanted = [
    { workflow_id: "wanted-waiting", media_id: mediaID, media_kind: "series", title: "Wanted Waiting", year: 2024, gap: "missing", variant: "season:01:episode:03", current_state: "waiting", target_profile: "TV 1080p", upgrades_enabled: true, owner_action_required: false, updated_at: "2026-09-02T00:00:00Z" },
    { workflow_id: "wanted-attention", media_id: mediaID, media_kind: "movie", title: "Wanted Attention", year: 2024, gap: "missing", variant: "default", current_state: "needs_attention", target_profile: "Movie 1080p", upgrades_enabled: true, owner_action_required: true, updated_at: "2026-09-02T00:00:00Z" },
  ];

  const queue = overviewQueueItems(workflows, wanted);
  const active = queue.filter((item) => isActivelyAcquiring(item.state)).map((item) => item.title);
  const waiting = queue.filter((item) => isWaiting(item.state) && !requiresOwnerAttention(item.state, item.ownerActionRequired)).map((item) => item.title);

  assert.deepEqual(active, ["Airing Series"]);
  assert.ok(waiting.includes("Wanted Waiting"), "a wanted waiting row must appear in the waiting group");
  assert.ok(!waiting.includes("Wanted Attention"), "owner attention must never appear as normal waiting");
  assert.equal(queue.length, workflows.length + wanted.length, "no duplicate workflow is listed twice");
});

test("episode status comes only from persisted workflows, files, and air dates", async () => {
  const { episodeStatus, rollupSeriesSeasons } = await vite.ssrLoadModule("/lib/series-variants.ts");
  const workflow = (id, variant, state, extra = {}) => ({ id, variant, state, media_id: "m", media_kind: "series", title: "S", revision: 1, adopted_replacement_confirmation_required: false, created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z", ...extra });
  const [rollup] = rollupSeriesSeasons([
    workflow("a", "season:01:episode:01", "plex_confirmed"),
    workflow("b", "season:01:episode:02", "downloading"),
    workflow("c", "season:01:episode:03", "needs_attention", { owner_action_required: true, attention_detail: "Danish subtitles were not verified" }),
  ], []);
  const today = new Date("2026-09-30T12:00:00");
  assert.equal(episodeStatus({ number: 1, air_date: "2026-09-01" }, rollup, today).status, "acquired");
  assert.equal(episodeStatus({ number: 2, air_date: "2026-09-08" }, rollup, today).status, "acquiring");
  const attention = episodeStatus({ number: 3, air_date: "2026-09-15" }, rollup, today);
  assert.equal(attention.status, "attention");
  assert.equal(attention.workflow.attention_detail, "Danish subtitles were not verified");
  assert.equal(episodeStatus({ number: 4, air_date: "2026-09-22" }, rollup, today).status, "missing");
  assert.equal(episodeStatus({ number: 5, air_date: "2026-10-06" }, rollup, today).status, "unaired");
  // Without an air date an episode is undated, never unaired or missing.
  assert.equal(episodeStatus({ number: 6 }, rollup, today).status, "undated");
  const [pack] = rollupSeriesSeasons([workflow("p", "season:02", "plex_confirmed")], []);
  assert.equal(episodeStatus({ number: 9, air_date: "2020-01-01" }, pack, today).status, "acquired");
});
