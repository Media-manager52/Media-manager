import assert from "node:assert/strict";
import { readdir, readFile } from "node:fs/promises";
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
  resolve: { alias: { "@": root } },
  server: { middlewareMode: true },
});

after(async () => {
  await vite.close();
});

async function readCssTree(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const contents = await Promise.all(
    entries.map(async (entry) => {
      const entryPath = path.join(directory, entry.name);
      if (entry.isDirectory()) {
        return readCssTree(entryPath);
      }
      return entry.name.endsWith(".css") ? readFile(entryPath, "utf8") : "";
    }),
  );
  return contents.join("\n");
}

async function readTextTree(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const contents = await Promise.all(
    entries.map(async (entry) => {
      const entryPath = path.join(directory, entry.name);
      if (entry.isDirectory()) return readTextTree(entryPath);
      return /\.(?:css|html|js|json|txt)$/.test(entry.name)
        ? readFile(entryPath, "utf8")
        : "";
    }),
  );
  return contents.join("\n");
}

test("emits the catalog's animation and scrolling utilities", async () => {
  const css = await readCssTree(path.join(root, "dist"));

  assert.match(css, /--tw-enter-opacity/);
  assert.match(css, /scrollbar-width:\s*thin/);
  assert.match(css, /scrollbar-width:\s*none/);
  assert.match(css, /scrollbar-gutter:\s*stable/);
  assert.match(css, /scroll-fade-reveal-b/);
  assert.match(css, /mask-image:/);
  assert.match(css, /tw-shimmer/);
  assert.match(css, /prefers-reduced-motion:\s*reduce/);
});

test("forwards progress semantics to the primitive", async () => {
  const { Progress } = await vite.ssrLoadModule("/components/ui/progress.tsx");
  const html = renderToStaticMarkup(React.createElement(Progress, { value: 37 }));

  assert.match(html, /aria-valuenow="37"/);
  assert.match(html, /aria-valuetext="37%"/);
  assert.match(html, /data-state="loading"/);
});

test("emits chart themes for the starter's media dark mode", async () => {
  const { ChartStyle } = await vite.ssrLoadModule("/components/ui/chart.tsx");
  const html = renderToStaticMarkup(
    React.createElement(ChartStyle, {
      id: "contract",
      config: {
        latency: { theme: { light: "#ffffff", dark: "#000000" } },
      },
    }),
  );

  assert.match(html, /\[data-chart=contract\]/);
  assert.match(html, /@media \(prefers-color-scheme: dark\)/);
  assert.doesNotMatch(html, /\.dark/);
});

test("renders sidebar skeletons deterministically", async () => {
  const { SidebarMenuSkeleton } = await vite.ssrLoadModule(
    "/components/ui/sidebar.tsx",
  );
  const first = renderToStaticMarkup(React.createElement(SidebarMenuSkeleton));
  const second = renderToStaticMarkup(React.createElement(SidebarMenuSkeleton));

  assert.equal(first, second);
  assert.match(first, /--skeleton-width:70%/);
});

test("does not present the Indexer Lab preview as a real run", async () => {
  const source = await readFile(
    path.join(root, "components/media-manager/operational-screens.tsx"),
    "utf8",
  );

  assert.match(source, /no indexer requests were sent/i);
  assert.doesNotMatch(source, /commissioning fixture completed/i);
});

test("maps persisted operational read models into the live route switch", async () => {
  const [shell, client, screens] = await Promise.all([
    readFile(path.join(root, "components/media-manager/app-shell.tsx"), "utf8"),
    readFile(path.join(root, "lib/media-manager-api.ts"), "utf8"),
    readFile(path.join(root, "components/media-manager/live-operational-screens.tsx"), "utf8"),
  ]);

  for (const route of [
    "/files",
    "/wanted",
    "/activity/history",
    "/activity/blocklist",
    "/intelligence/releases",
    "/intelligence/groups",
    "/intelligence/evidence",
    "/sources/indexers",
    "/sources/download-clients",
    "/system/health",
    "/system/tasks",
    "/system/security",
    "/system/api",
    "/settings/media-management",
    "/settings/metadata",
    "/system/logs",
  ]) {
    assert.match(shell, new RegExp(`path === ["']${route.replaceAll("/", "\\/")}["']`));
  }
  for (const endpoint of [
    "/api/v1/dashboard",
    "/api/v1/activity",
    "/api/v1/files",
    "/api/v1/intelligence/releases",
    "/api/v1/sources",
    "/api/v1/system/health",
    "/api/v1/media/",
    "/api/v1/wanted",
    "/api/v1/system/tasks",
    "/api/v1/system/info",
    "/api/v1/discovery/search",
    "/api/v1/blocklist",
  ]) {
    assert.match(client, new RegExp(endpoint.replaceAll("/", "\\/")));
  }
  assert.match(screens, /no invented percentages/i);
  assert.match(screens, /does not manufacture connection-test success/i);
});

test("excludes development fixture records from the live artifact", async () => {
  const artifact = await readTextTree(path.join(root, "dist"));

  for (const fixture of [
    "Dune Collection",
    "Blade Runner 2049",
    "Treasure Maps",
    "Foundation.S03.PACK",
  ]) {
    assert.doesNotMatch(artifact, new RegExp(fixture.replaceAll(".", "\\.")));
  }
});
