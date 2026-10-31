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

const { fallbackEvidenceRows, formatFactValue, evidenceStateNotRecorded } = await vite.ssrLoadModule("/lib/release-evidence.ts");

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
