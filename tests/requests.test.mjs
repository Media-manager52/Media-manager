import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test, { after } from "node:test";
import { fileURLToPath } from "node:url";

import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer } from "vite";

const root = fileURLToPath(new URL("..", import.meta.url));
const vite = await createServer({
  appType: "custom",
  configFile: false,
  root,
  resolve: {
    alias: [
      { find: "next/image", replacement: path.join(root, "tests/next-image-stub.mjs") },
      { find: "@", replacement: root },
    ],
  },
  server: { middlewareMode: true },
});

after(async () => {
  await vite.close();
});

function request(overrides) {
  return {
    id: overrides.request_id,
    request_id: overrides.request_id,
    seerr_instance_id: "11111111-1111-4111-8111-111111111111",
    media_kind: "movie",
    title: "Untitled",
    requested_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-01T00:00:00Z",
    seerr_state: "2",
    workflow_count: 0,
    confirmed_count: 0,
    attention_count: 0,
    lifecycle: "monitored",
    season_count: 0,
    available_seasons: 0,
    ...overrides,
  };
}

const requests = [
  request({ request_id: "1", title: "Completed Movie", year: 2024, requester: "owner", lifecycle: "plex_confirmed", current_state: "plex_confirmed", workflow_count: 1, confirmed_count: 1, media_id: "11111111-1111-4111-8111-111111111111", quality_profile: "Movie UHD" }),
  request({ request_id: "2", title: "Danish Series", media_kind: "series", requester: "danish-owner", lifecycle: "needs_attention", attention_count: 1, attention_detail: "required Danish subtitles were not verified", season_count: 2, available_seasons: 0 }),
  request({ request_id: "3", title: "Searching Movie", lifecycle: "searching", current_state: "searching", workflow_count: 1, confirmed_count: 0 }),
  request({ request_id: "4", title: "Already Available", lifecycle: "available", seerr_state: "5" }),
];

test("maps durable request lifecycles to owner-facing tones and labels", async () => {
  const { requestLifecycleTone, requestLifecycleLabel, seerrStateLabel } = await vite.ssrLoadModule("/lib/media-manager-requests.ts");

  assert.equal(requestLifecycleTone("plex_confirmed"), "good");
  assert.equal(requestLifecycleTone("available"), "good");
  assert.equal(requestLifecycleTone("needs_attention"), "warn");
  assert.equal(requestLifecycleTone("failed"), "bad");
  assert.equal(requestLifecycleTone("searching"), "info");
  assert.equal(requestLifecycleTone("monitored"), "info");
  assert.equal(requestLifecycleTone("unknown"), "neutral");

  assert.equal(requestLifecycleLabel("plex_confirmed"), "Plex confirmed");
  assert.equal(requestLifecycleLabel("needs_attention"), "Needs Attention");
  assert.equal(requestLifecycleLabel("searching"), "Searching");
  assert.equal(requestLifecycleLabel("not_approved"), "Not approved");

  assert.equal(seerrStateLabel("1"), "Pending approval");
  assert.equal(seerrStateLabel("5"), "Available");
  assert.equal(seerrStateLabel(""), "Unknown");
});

test("filters requests by lifecycle, type, and free text with real API shape", async () => {
  const { filterRequests, requestFilterCounts } = await vite.ssrLoadModule("/lib/media-manager-requests.ts");

  assert.deepEqual(filterRequests(requests, "all", "").map((item) => item.request_id), ["1", "2", "3", "4"]);
  assert.deepEqual(filterRequests(requests, "completed", "").map((item) => item.request_id), ["1", "4"]);
  assert.deepEqual(filterRequests(requests, "attention", "").map((item) => item.request_id), ["2"]);
  assert.deepEqual(filterRequests(requests, "active", "").map((item) => item.request_id), ["3"]);
  assert.deepEqual(filterRequests(requests, "series", "").map((item) => item.request_id), ["2"]);
  assert.deepEqual(filterRequests(requests, "movie", "").map((item) => item.request_id), ["1", "3", "4"]);
  assert.deepEqual(filterRequests(requests, "all", "danish").map((item) => item.request_id), ["2"]);
  assert.deepEqual(filterRequests(requests, "all", "2024").map((item) => item.request_id), ["1"]);
  assert.deepEqual(filterRequests(requests, "attention", "completed"), []);
  assert.deepEqual(filterRequests([], "all", ""), []);
  assert.deepEqual(requestFilterCounts(requests), { total: 4, active: 1, completed: 2, attention: 1, movie: 3, series: 1 });
});

test("renders a Plex-confirmed request without inventing extra success", async () => {
  const { RequestInspector } = await vite.ssrLoadModule("/components/media-manager/live-operational-screens.tsx");
  const movie = requests[0];
  const detail = {
    request: movie,
    workflows: [{ id: "22222222-2222-4222-8222-222222222222", media_id: movie.media_id, media_kind: "movie", title: movie.title, variant: "default", state: "plex_confirmed", revision: 3, acquisition_attempts: 1, round_ordinal: 1, round_slots_used: 1, round_attempt_limit: 2, complete_no_new_scans: 0, new_release_ready: false, open_next_round_allowed: false, retained_output_count: 0, retained_output_bytes: 0, excluded_release_count: 0, search_allowed: false, adopted_replacement_confirmation_required: false, created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-02T00:00:00Z" }],
    seasons: [],
    files: [{ id: "33333333-3333-4333-8333-333333333333", variant: "default", state: "current", plex_confirmed: true, destination_relative: "Completed Movie (2024)/Completed Movie.mkv" }],
  };
  const html = renderToStaticMarkup(React.createElement(RequestInspector, { request: movie, detail, loading: false, error: "" }));

  assert.match(html, /Completed Movie/);
  assert.match(html, /Plex confirmed/);
  assert.match(html, /owner/);
  assert.match(html, /Attempts 1/);
  assert.match(html, /Seerr request ID/);
});

test("renders a needs-attention request truthfully", async () => {
  const { RequestInspector } = await vite.ssrLoadModule("/components/media-manager/live-operational-screens.tsx");
  const series = requests[1];
  const detail = {
    request: series,
    workflows: [{ id: "44444444-4444-4444-8444-444444444444", media_id: "55555555-5555-4555-8555-555555555555", media_kind: "series", title: series.title, variant: "season:01", state: "needs_attention", revision: 4, acquisition_attempts: 2, round_ordinal: 1, round_slots_used: 2, round_attempt_limit: 2, complete_no_new_scans: 0, new_release_ready: false, open_next_round_allowed: false, retained_output_count: 1, retained_output_bytes: 1024, excluded_release_count: 0, search_allowed: false, adopted_replacement_confirmation_required: false, attention_detail: "required Danish subtitles were not verified", created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-02T00:00:00Z" }],
    seasons: [{ number: 1, availability: 1, requested: true, workflow_state: "needs_attention" }],
    files: [],
  };
  const html = renderToStaticMarkup(React.createElement(RequestInspector, { request: series, detail, loading: false, error: "" }));

  assert.match(html, /Needs Attention/);
  assert.match(html, /required Danish subtitles were not verified/);
  assert.match(html, /Season 01/);
  assert.doesNotMatch(html, /Plex confirmed/);
});

test("live API client uses the supported requests endpoints", async () => {
  const source = await readFile(path.join(root, "lib/media-manager-api.ts"), "utf8");
  assert.match(source, /\/api\/v1\/requests\?limit=/);
  assert.match(source, /\/api\/v1\/requests\/\$\{encodeURIComponent\(id\)\}/);
  assert.match(source, /export const loadRequests/);
  assert.match(source, /export const loadRequest/);
});
