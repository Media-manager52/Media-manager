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

const { systemTiles } = await vite.ssrLoadModule("/components/media-manager/overview-screen.tsx");

const altmount = { id: "a", kind: "altmount", name: "AltMount", enabled: true, is_default: true, config: {}, configuration_state: "configured", consecutive_failure: 0, created_at: "", updated_at: "" };
const health = (mounts) => ({ database_state: "ready", database_latency_ms: 1, schema_version: 44, expected_schema: 44, outbox: { pending: 0, due: 0, retrying: 0 }, components: [altmount], mounts, checked_at: "" });
const tile = (value) => systemTiles(value).find((item) => item.key === "altmount");

test("AltMount tile stays green only while its output mount is readable", () => {
  assert.equal(tile(health(undefined)).title, "AltMount connected");
  assert.equal(tile(health([{ integration_id: "a", name: "AltMount", path: "/mnt/altmount", state: "available" }])).tone, "good");
});

test("an unreadable output mount turns the AltMount tile into a warning", () => {
  const dead = tile(health([{ integration_id: "a", name: "AltMount", path: "/mnt/altmount", state: "unavailable", detail: "socket not connected" }]));
  assert.equal(dead.tone, "warn");
  assert.equal(dead.title, "AltMount output unavailable");
  assert.match(dead.detail, /\/mnt\/altmount/);
});
