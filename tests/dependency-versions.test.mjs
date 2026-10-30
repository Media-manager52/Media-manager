import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"

const floors = {
  "fast-uri": "3.1.9",
  next: "16.4.3",
  sharp: "0.35.5",
  "source-map-js": "1.2.2",
  undici: "7.30.1",
  vite: "8.3.5",
  zod: "4.6.7",
}

function parse(value) {
  return value.split("-")[0].split(".").map((part) => Number(part))
}

function atLeast(actual, floor) {
  const left = parse(actual)
  const right = parse(floor)
  for (let index = 0; index < Math.max(left.length, right.length); index += 1) {
    const difference = (left[index] ?? 0) - (right[index] ?? 0)
    if (difference !== 0) return difference > 0
  }
  return true
}

test("installed frontend dependencies stay at or above the patched advisory floors", () => {
  for (const [name, floor] of Object.entries(floors)) {
    const packageJSON = JSON.parse(readFileSync(`node_modules/${name}/package.json`, "utf8"))
    assert.ok(atLeast(packageJSON.version, floor), `${name} ${packageJSON.version} is below patched floor ${floor}`)
  }
})
