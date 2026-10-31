import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
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

const {
  audioLabel,
  audioRequirement,
  bestAvailableAudioRules,
  bestAvailableResolutionRules,
  defaultSelections,
  deriveSelections,
  mergeQualityRules,
  resolutionLabel,
  resolutionRequirement,
} = await vite.ssrLoadModule("/lib/quality-profile-rules.ts");

// Real ApplyPreset output from the Go engine (internal/releases/presets_test.go
// keeps it current), over a profile with required resolution and audio gates.
const fixture = JSON.parse(readFileSync(new URL("./browser/fixtures/quality-preset-applications.json", import.meta.url), "utf8"));
const presets = ["balanced", "maximum_quality", "storage_efficient", "compatibility_first"];

const clone = (value) => JSON.parse(JSON.stringify(value));
const required = (rules, fact) => rules.filter((rule) => rule.fact === fact && rule.kind === "required");
const preferred = (rules, fact) => rules.filter((rule) => rule.fact === fact && rule.kind === "preferred");

test("the fixture covers every preset with the gates intact", () => {
  assert.deepEqual(Object.keys(fixture.applied_rules).sort(), [...presets].sort());
  for (const preset of presets) {
    assert.equal(required(fixture.applied_rules[preset], "resolution").length, 1, preset);
    assert.equal(required(fixture.applied_rules[preset], "audio_codec").length, 1, preset);
  }
});

for (const preset of presets) {
  test(`saving a ${preset} profile without changing anything leaves every rule byte-for-byte alone`, () => {
    const applied = fixture.applied_rules[preset];
    const before = clone(applied);
    const selections = deriveSelections(applied);
    assert.equal(selections.resolutionRequirement, "1080p");
    assert.equal(selections.audioRequirement, "custom");

    const merged = mergeQualityRules(applied, selections);

    assert.deepEqual(merged, before);
    assert.deepEqual(applied, before, "the stored rules must not be mutated");
  });

  test(`an unrelated edit on a ${preset} profile keeps the hard gates and the preset ladders`, () => {
    const applied = fixture.applied_rules[preset];
    const merged = mergeQualityRules(applied, deriveSelections(applied));

    assert.deepEqual(required(merged, "resolution"), required(applied, "resolution"));
    assert.deepEqual(required(merged, "audio_codec"), required(applied, "audio_codec"));
    assert.deepEqual(required(merged, "subtitle_or_audio_languages"), required(applied, "subtitle_or_audio_languages"));
    assert.deepEqual(preferred(merged, "subtitle_languages"), preferred(applied, "subtitle_languages"));
    assert.deepEqual(preferred(merged, "resolution"), preferred(applied, "resolution"));
    assert.deepEqual(preferred(merged, "audio_codec"), preferred(applied, "audio_codec"));
    assert.deepEqual(preferred(merged, "source"), preferred(applied, "source"));
  });
}

test("Balanced is shown as best available next to the required gates, the others as custom", () => {
  const balanced = deriveSelections(fixture.applied_rules.balanced);
  assert.equal(balanced.resolutionPreference, "best_available");
  assert.equal(balanced.audioPreference, "best_available");
  assert.equal(balanced.resolutionRequirement, "1080p");
  assert.equal(balanced.audioRequirement, "custom");
  assert.equal(resolutionLabel(fixture.applied_rules.balanced), "Require 1080p, prefer best available");
  assert.equal(audioLabel(fixture.applied_rules.balanced), "Require eac3 / ac3, prefer best available");

  for (const preset of ["maximum_quality", "storage_efficient", "compatibility_first"]) {
    const selections = deriveSelections(fixture.applied_rules[preset]);
    assert.equal(selections.resolutionPreference, "none", preset);
    assert.equal(selections.audioPreference, "none", preset);
    assert.equal(resolutionLabel(fixture.applied_rules[preset]), "Require 1080p", preset);
    assert.equal(audioLabel(fixture.applied_rules[preset]), "Require eac3 / ac3", preset);
  }
});

test("a profile that only prefers, requires nothing and has no rules is described truthfully", () => {
  const ladder = clone(fixture.applied_rules.balanced).filter((rule) => rule.kind !== "required");
  assert.equal(resolutionLabel(ladder), "Best available");
  assert.equal(audioLabel(ladder), "Best available");
  assert.equal(resolutionLabel([]), "None");
  assert.equal(audioLabel([]), "None");
  assert.equal(resolutionLabel(clone(fixture.applied_rules.maximum_quality).filter((rule) => rule.kind !== "required")), "Custom preferences");
});

test("explicitly removing the required resolution removes only that gate", () => {
  for (const preset of presets) {
    const applied = fixture.applied_rules[preset];
    const merged = mergeQualityRules(applied, { ...deriveSelections(applied), resolutionRequirement: "none" });
    assert.equal(required(merged, "resolution").length, 0, preset);
    assert.deepEqual(required(merged, "audio_codec"), required(applied, "audio_codec"), preset);
    assert.deepEqual(preferred(merged, "resolution"), preferred(applied, "resolution"), preset);
    assert.equal(merged.length, applied.length - 1, preset);
  }
});

test("changing the required resolution replaces the gate and keeps the ranking", () => {
  for (const preset of presets) {
    const applied = fixture.applied_rules[preset];
    const merged = mergeQualityRules(applied, { ...deriveSelections(applied), resolutionRequirement: "2160p" });
    assert.deepEqual(required(merged, "resolution").map((rule) => rule.allowed_values), [["2160p"]], preset);
    assert.equal(required(merged, "resolution")[0].unknown, "hold");
    assert.deepEqual(preferred(merged, "resolution"), preferred(applied, "resolution"), preset);
    assert.deepEqual(required(merged, "audio_codec"), required(applied, "audio_codec"), preset);
  }
});

test("explicitly removing the required audio codec removes only that gate", () => {
  const applied = fixture.applied_rules.balanced;
  const merged = mergeQualityRules(applied, { ...deriveSelections(applied), audioRequirement: "none" });
  assert.equal(required(merged, "audio_codec").length, 0);
  assert.deepEqual(preferred(merged, "audio_codec"), preferred(applied, "audio_codec"));
  assert.deepEqual(required(merged, "resolution"), required(applied, "resolution"));
  assert.equal(merged.length, applied.length - 1);
});

test("choosing best available replaces only the soft ladders and never a gate", () => {
  const applied = fixture.applied_rules.maximum_quality;
  const merged = mergeQualityRules(applied, { ...deriveSelections(applied), resolutionPreference: "best_available", audioPreference: "best_available" });

  assert.deepEqual(required(merged, "resolution"), required(applied, "resolution"));
  assert.deepEqual(required(merged, "audio_codec"), required(applied, "audio_codec"));
  assert.deepEqual(preferred(merged, "resolution").map((rule) => [rule.allowed_values[0], rule.weight]), bestAvailableResolutionRules.map((item) => [item.value, item.weight]));
  assert.deepEqual(preferred(merged, "audio_codec").map((rule) => [rule.allowed_values[0], rule.weight]), bestAvailableAudioRules.map((item) => [item.value, item.weight]));
  assert.deepEqual(preferred(merged, "source"), preferred(applied, "source"));
  assert.deepEqual(preferred(merged, "subtitle_languages"), preferred(applied, "subtitle_languages"));
  assert.equal(merged.filter((rule) => rule.fact === "video_codec").length, applied.filter((rule) => rule.fact === "video_codec").length);
});

test("a required resolution the editor has no name for is kept, not narrowed to its first value", () => {
  const rules = [
    { name: "Minimum 1080p", fact: "resolution", kind: "required", allowed_values: ["1080p", "2160p"], weight: 0, unknown: "reject" },
    { name: "Preferred source", fact: "source", kind: "preferred", allowed_values: ["webdl"], weight: 100, unknown: "allow" },
  ];
  const selections = deriveSelections(rules);
  assert.equal(selections.resolutionRequirement, "custom");
  assert.equal(resolutionRequirement(rules), "custom");
  assert.equal(resolutionLabel(rules), "Require 1080p / 2160p");
  assert.deepEqual(mergeQualityRules(rules, selections), rules);

  const several = [...rules, { name: "Also 720p", fact: "resolution", kind: "required", allowed_values: ["720p"], weight: 0 }];
  assert.equal(resolutionRequirement(several), "custom");
  assert.deepEqual(mergeQualityRules(several, deriveSelections(several)), several);
});

test("rules are classified the way the Go evaluator does, ignoring case and whitespace", () => {
  const rules = [
    { name: "Gate", fact: " Resolution ", kind: "Required", allowed_values: [" 1080P "], weight: 0, unknown: "hold" },
    { name: "Audio gate", fact: "AUDIO_CODEC", kind: " required", allowed_values: ["EAC3"], weight: 0 },
  ];
  assert.equal(resolutionRequirement(rules), "1080p");
  assert.equal(audioRequirement(rules), "custom");
  assert.deepEqual(mergeQualityRules(rules, deriveSelections(rules)), rules);
  // Selecting the best-available ladder must not eat a gate that merely differs in case.
  const merged = mergeQualityRules(rules, { ...deriveSelections(rules), resolutionPreference: "best_available", audioPreference: "best_available" });
  assert.deepEqual(merged.slice(0, 2), rules);
});

test("a single preferred source edits in place; a ladder or multi-value source is kept until replaced", () => {
  const single = [{ name: "Preferred source", fact: "source", kind: "preferred", allowed_values: ["webdl"], weight: 100, unknown: "allow" }];
  assert.equal(deriveSelections(single).source, "webdl");
  assert.deepEqual(mergeQualityRules(single, { ...deriveSelections(single), source: "remux" }), [{ ...single[0], allowed_values: ["remux"] }]);

  const multi = [{ name: "Either", fact: "source", kind: "preferred", allowed_values: ["webdl", "bluray"], weight: 100, unknown: "allow" }];
  assert.equal(deriveSelections(multi).source, "none");
  assert.deepEqual(mergeQualityRules(multi, deriveSelections(multi)), multi);

  const ladder = fixture.applied_rules.balanced;
  assert.equal(deriveSelections(ladder).source, "none");
  assert.deepEqual(mergeQualityRules(ladder, deriveSelections(ladder)), ladder);
  const replaced = mergeQualityRules(ladder, { ...deriveSelections(ladder), source: "webrip" });
  assert.deepEqual(preferred(replaced, "source").map((rule) => [rule.name, rule.allowed_values, rule.weight]), [["Preferred source", ["webrip"], 100]]);
  assert.deepEqual(required(replaced, "resolution"), required(ladder, "resolution"));
});

test("a new profile gets the default ladders and source without any gate", () => {
  const merged = mergeQualityRules([], defaultSelections());
  assert.equal(required(merged, "resolution").length, 0);
  assert.equal(required(merged, "audio_codec").length, 0);
  assert.equal(preferred(merged, "resolution").length, bestAvailableResolutionRules.length);
  assert.equal(preferred(merged, "audio_codec").length, bestAvailableAudioRules.length);
  assert.deepEqual(preferred(merged, "source").map((rule) => rule.allowed_values), [["webdl"]]);
  // Creating with a required resolution adds exactly one gate next to the ladders.
  const gated = mergeQualityRules([], { ...defaultSelections(), resolutionRequirement: "1080p" });
  assert.deepEqual(required(gated, "resolution").map((rule) => rule.allowed_values), [["1080p"]]);
  assert.equal(preferred(gated, "resolution").length, bestAvailableResolutionRules.length);
});

test("keeping a custom requirement is a no-op even if the form was opened on other rules", () => {
  // A stale or hand-edited "custom" selection must never invent or remove a gate.
  const rules = [{ name: "Preferred source", fact: "source", kind: "preferred", allowed_values: ["webdl"], weight: 100 }];
  const merged = mergeQualityRules(rules, { ...deriveSelections(rules), resolutionRequirement: "custom", audioRequirement: "custom" });
  assert.deepEqual(merged, rules);
});

test("a selection that is not a known choice never writes or removes a rule", () => {
  // Radix Select can report an empty value while an option mounts; "" must mean "keep".
  for (const preset of presets) {
    const applied = fixture.applied_rules[preset];
    const merged = mergeQualityRules(applied, { resolutionRequirement: "", resolutionPreference: "", audioRequirement: "", audioPreference: "", source: "" });
    assert.deepEqual(merged, applied, preset);
    assert.equal(merged.some((rule) => Array.isArray(rule.allowed_values) && rule.allowed_values.some((value) => !value)), false, preset);
  }
  const stale = mergeQualityRules(fixture.applied_rules.balanced, { ...deriveSelections(fixture.applied_rules.balanced), resolutionRequirement: "4k", audioRequirement: "maybe", source: "   " });
  assert.deepEqual(stale, fixture.applied_rules.balanced);
});
