import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "vitest";




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
  const { requestLifecycleTone, requestLifecycleLabel, seerrStateLabel } = await import("@/lib/media-manager-requests");

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
  assert.equal(requestLifecycleLabel("not_approved"), "Awaiting approval");
  assert.equal(requestLifecycleLabel("approved_in_seerr"), "Approved in Seerr");

  assert.equal(seerrStateLabel("1"), "Pending approval");
  assert.equal(seerrStateLabel("5"), "Available");
  assert.equal(seerrStateLabel(""), "Unknown");
});

test("filters requests by lifecycle, type, and free text with real API shape", async () => {
  const { filterRequests, requestFilterCounts } = await import("@/lib/media-manager-requests");

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

  // A decision already made in Seerr is not something the owner still has to
  // act on; a request still waiting for that decision is.
  const decided = [
    { ...requests[0], request_id: "9", lifecycle: "declined" },
    { ...requests[0], request_id: "10", lifecycle: "approved_in_seerr" },
    { ...requests[0], request_id: "11", lifecycle: "not_approved" },
  ];
  assert.deepEqual(filterRequests(decided, "attention", "").map((item) => item.request_id), ["11"]);
  assert.deepEqual(filterRequests(decided, "active", "").map((item) => item.request_id), ["10"]);
  assert.deepEqual(filterRequests(decided, "completed", ""), []);
});

test("live API client uses the supported requests endpoints", async () => {
  const source = await readFile(new URL("../api/client.ts", import.meta.url), "utf8");
  assert.match(source, /\/api\/v1\/requests\?limit=/);
  assert.match(source, /\/api\/v1\/requests\/\$\{encodeURIComponent\(id\)\}/);
  assert.match(source, /export const loadRequests/);
  assert.match(source, /export const loadRequest/);
});
