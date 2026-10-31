import assert from "node:assert/strict";
import test, { after } from "node:test";
import { fileURLToPath } from "node:url";

import { createServer } from "vite";

const root = fileURLToPath(new URL("..", import.meta.url));
const vite = await createServer({
  appType: "custom",
  configFile: false,
  root,
  resolve: { alias: [{ find: "@", replacement: root }] },
  server: { middlewareMode: true },
});

after(async () => {
  await vite.close();
});

const { fallbackEvidenceRows, formatFactValue, evidenceStateNotRecorded, releaseGroupEvidence, summarizeReleaseGroups } = await vite.ssrLoadModule("/lib/release-evidence.ts");

test("stored fact arrays keep their own name, values and recorded state", () => {
  const rows = fallbackEvidenceRows([
    { name: "resolution", values: ["1080p"], state: "observed", evidence_at: [0] },
    { name: "audio_codec", values: ["eac3", "ac3"], state: "inferred" },
    { name: "subtitle_languages", values: [], state: "unknown" },
    { name: "source", values: ["webdl"], state: "conflicting" },
  ]);
  assert.deepEqual(rows, [
    { fact: "resolution", value: "1080p", state: "observed" },
    { fact: "audio_codec", value: "eac3, ac3", state: "inferred" },
    { fact: "subtitle_languages", value: "Unknown", state: "unknown" },
    { fact: "source", value: "webdl", state: "conflicting" },
  ]);
});

test("an array entry without a usable name never becomes a row, and an unrecognised state is not recorded", () => {
  const rows = fallbackEvidenceRows([
    { values: ["orphan"], state: "observed" },
    { name: "   ", values: ["blank"] },
    { name: 7, values: ["numeric"] },
    null,
    "text",
    { name: "resolution", values: ["720p"], state: "certain" },
    { name: "group" },
  ]);
  assert.deepEqual(rows, [
    { fact: "resolution", value: "720p", state: evidenceStateNotRecorded },
    { fact: "group", value: "Unknown", state: evidenceStateNotRecorded },
  ]);
});

test("the legacy object shape has values but no recorded state", () => {
  assert.deepEqual(fallbackEvidenceRows({ resolution: "720p", subtitle_languages: ["da", "en"], flag: true }), [
    { fact: "resolution", value: "720p", state: "not_recorded" },
    { fact: "subtitle_languages", value: "da, en", state: "not_recorded" },
    { fact: "flag", value: "true", state: "not_recorded" },
  ]);
});

test("empty or absent stored facts produce no rows", () => {
  for (const empty of [null, undefined, {}, [], "", 0, false]) assert.deepEqual(fallbackEvidenceRows(empty), []);
});

test("the row limit applies to both shapes", () => {
  const array = Array.from({ length: 30 }, (_, index) => ({ name: `fact-${index}`, values: ["v"], state: "observed" }));
  const object = Object.fromEntries(array.map((entry) => [entry.name, "v"]));
  assert.equal(fallbackEvidenceRows(array).length, 20);
  assert.equal(fallbackEvidenceRows(object).length, 20);
  assert.equal(fallbackEvidenceRows(array, 3).length, 3);
  // Skipped entries do not consume the limit.
  const mixed = [{ values: ["x"] }, ...array];
  assert.equal(fallbackEvidenceRows(mixed, 2).length, 2);
});

test("values are rendered without losing structure", () => {
  assert.equal(formatFactValue("a"), "a");
  assert.equal(formatFactValue(3), "3");
  assert.equal(formatFactValue(["a", "b"]), "a, b");
  assert.equal(formatFactValue([]), "Unknown");
  assert.equal(formatFactValue({ nested: 1 }), '{"nested":1}');
  assert.equal(formatFactValue(null), "Unknown");
});

const fact = (name, values, state = "observed") => ({ name, values, state, evidence_at: [], observed_at: "2026-09-02T00:00:00Z" });

test("the release group is read from the stored fact array, not from object properties", () => {
  assert.deepEqual(releaseGroupEvidence([fact("resolution", ["1080p"]), fact("group", ["GROUP"])]), { kind: "group", name: "GROUP" });
  assert.deepEqual(releaseGroupEvidence([fact("group", ["  padded  "], "inferred")]), { kind: "group", name: "padded" });
  assert.deepEqual(releaseGroupEvidence([fact("Group ", ["CASE"])]), { kind: "group", name: "CASE" });
  // release_group outranks group, but only when it actually names a group.
  assert.deepEqual(releaseGroupEvidence([fact("group", ["newsgroup"]), fact("release_group", ["REAL"])]), { kind: "group", name: "REAL" });
  assert.deepEqual(releaseGroupEvidence([fact("release_group", [], "unknown"), fact("group", ["FALLBACK"])]), { kind: "group", name: "FALLBACK" });
  // The old object-property reading found nothing in the real shape; it must not be accepted back.
  assert.deepEqual(releaseGroupEvidence({ group: "GROUP", release_group: "GROUP" }), { kind: "unknown" });
});

test("unknown group evidence stays unknown and is never invented", () => {
  for (const unknown of [
    undefined, null, {}, [], "group", 7,
    [fact("resolution", ["1080p"])],
    [fact("group", [], "unknown")],
    [fact("group", null, "unknown")],
    [fact("group", ["HIDDEN"], "unknown")],
    [fact("group", ["   ", ""])],
    [null, "text", { values: ["GROUP"] }, { name: 7, values: ["GROUP"] }],
  ]) assert.deepEqual(releaseGroupEvidence(unknown), { kind: "unknown" });
});

test("conflicting group evidence is neither a group nor unknown", () => {
  assert.deepEqual(releaseGroupEvidence([fact("group", ["A", "B"], "conflicting")]), { kind: "conflicting", values: ["A", "B"] });
  assert.deepEqual(releaseGroupEvidence([fact("group", ["A", "B"])]), { kind: "conflicting", values: ["A", "B"] });
  assert.deepEqual(releaseGroupEvidence([fact("group", ["A", "a"])]), { kind: "group", name: "A" });
  // A conflicting release_group is reported as such; a lower-priority group fact does not overrule it.
  assert.deepEqual(releaseGroupEvidence([fact("release_group", ["X", "Y"], "conflicting"), fact("group", ["Z"])]), { kind: "conflicting", values: ["X", "Y"] });
  assert.deepEqual(releaseGroupEvidence([fact("release_group", ["X", "Y"], "conflicting")]), { kind: "conflicting", values: ["X", "Y"] });
});

test("the release group summary counts verdicts and keeps evidence buckets apart", () => {
  const summary = summarizeReleaseGroups([
    { normalized_facts: [fact("group", ["B"])], verdict: "eligible" },
    { normalized_facts: [fact("group", ["A"])], verdict: "held" },
    { normalized_facts: [fact("group", ["A"])], verdict: "rejected" },
    { normalized_facts: [fact("group", ["A"])], verdict: "eligible" },
    { normalized_facts: [], verdict: "held" },
    { normalized_facts: {}, verdict: "rejected" },
    { normalized_facts: [fact("group", ["X", "Y"], "conflicting")], verdict: "held" },
    // A real group that happens to be called "Unknown" is not the unknown bucket.
    { normalized_facts: [fact("group", ["Unknown"])] },
  ]);
  assert.deepEqual(summary.map((row) => [row.key, row.kind, row.name, row.observations, row.eligible, row.held, row.rejected]), [
    ["group:A", "group", "A", 3, 1, 1, 1],
    ["group:B", "group", "B", 1, 1, 0, 0],
    ["group:Unknown", "group", "Unknown", 1, 0, 0, 0],
    ["conflicting", "conflicting", "Conflicting evidence", 1, 0, 1, 0],
    ["unknown", "unknown", "Unknown", 2, 0, 1, 1],
  ]);
  assert.deepEqual(summarizeReleaseGroups([]), []);
});
