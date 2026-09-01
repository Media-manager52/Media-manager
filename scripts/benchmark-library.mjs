import { performance } from "node:perf_hooks";
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

const median = (values) => [...values].sort((left, right) => left - right)[Math.floor(values.length / 2)];
const duration = (action) => {
  const started = performance.now();
  const result = action();
  return { milliseconds: performance.now() - started, result };
};

function fixtures(count) {
  const media = Array.from({ length: count }, (_, index) => ({
    id: `media-${index}`,
    kind: "movie",
    title: `Movie ${String(index).padStart(5, "0")}`,
    year: 2000 + (index % 25),
    identities: [{ namespace: "tmdb", value: String(index), state: "verified" }],
  }));
  const files = media.map((item, index) => ({
    id: `file-${index}`,
    media_id: item.id,
    root_name: "Movies",
    state: "current",
    plex_confirmed: true,
    quality_profile: "1080p",
    quality_evidence: { facts: { resolution: "1080p", subtitle_languages: ["da"] } },
  }));
  const wanted = media.map((item, index) => ({
    workflow_id: `wanted-${index}`,
    media_id: item.id,
    gap: "upgrade",
    target_profile: "1080p",
  }));
  const workflows = media.map((item, index) => ({
    id: `workflow-${index}`,
    media_id: item.id,
    state: "plex_confirmed",
  }));
  return { media, files, wanted, workflows };
}

try {
  const { buildMovieLibraryRecords, filterMovieLibraryRecords, initialMovieFilters } = await vite.ssrLoadModule("/lib/movie-library.ts");
  const rows = [];
  for (const count of [500, 1_000, 2_000, 4_000, 10_000]) {
    const data = fixtures(count);
    const builds = [];
    const filters = [];
    for (let run = 0; run < 5; run += 1) {
      const built = duration(() => buildMovieLibraryRecords(data.media, data.files, data.wanted, data.workflows));
      builds.push(built.milliseconds);
      filters.push(duration(() => filterMovieLibraryRecords(built.result, {
        ...initialMovieFilters,
        query: `tmdb ${count - 1}`,
      })).milliseconds);
    }
    rows.push({
      records: count,
      "build median (ms)": median(builds).toFixed(1),
      "filter median (ms)": median(filters).toFixed(1),
    });
  }
  console.table(rows);
} finally {
  await vite.close();
}
