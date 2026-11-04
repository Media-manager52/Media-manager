import type { Page } from "@playwright/test"

import { mockApi } from "../browser/mock-api"

// Invented titles, people and numbers for the README screenshots. Nothing here
// comes from a real library, and the posters are drawn locally (posterSVG), so
// generating the images contacts no external service.

export const now = new Date("2026-11-04T10:30:00+01:00")
const at = (minutesAgo: number) => new Date(now.getTime() - minutesAgo * 60_000).toISOString()
const day = (offset: number) => new Date(now.getTime() + offset * 86_400_000).toISOString().slice(0, 10)

type DemoTitle = { id: string; kind: "movie" | "series"; title: string; year: number; hue: number; genres: string[]; overview: string }

const titles: DemoTitle[] = ([
  { kind: "movie", title: "Nordlys over Skagen", year: 2025, hue: 205, genres: ["Drama"], overview: "En maler vender hjem til Skagen og finder en familiehemmelighed i det gamle atelier." },
  { kind: "series", title: "Havnefogeden", year: 2023, hue: 18, genres: ["Krimi"], overview: "En tidligere drabsefterforsker bliver havnefoged i en lille fiskerby, hvor intet er så stille, som det ser ud." },
  { kind: "movie", title: "Den sidste færge", year: 2024, hue: 260, genres: ["Thriller"], overview: "Elleve passagerer, én storm og en færge, der aldrig når frem til Ærø." },
  { kind: "series", title: "Kolonihaven", year: 2022, hue: 120, genres: ["Komedie"], overview: "Naboskab, hækkeklipning og helt for mange udvalgsmøder i en københavnsk haveforening." },
  { kind: "movie", title: "Isbjørnens rejse", year: 2025, hue: 190, genres: ["Familie", "Animation"], overview: "En lille isbjørn driver væk på et isflag og må finde vejen hjem til Grønland." },
  { kind: "series", title: "Byens lys", year: 2021, hue: 330, genres: ["Drama"], overview: "Fire venner prøver at få livet til at hænge sammen i Aarhus' natteliv." },
  { kind: "movie", title: "Stormfloden", year: 2023, hue: 225, genres: ["Drama", "Historie"], overview: "Vestkysten, 1872. En landsby forbereder sig på den værste storm i mands minde." },
  { kind: "series", title: "Sporet i sneen", year: 2024, hue: 175, genres: ["Krimi", "Mysterie"], overview: "En efterforsker fra Nuuk opklarer forsvindinger langs den østgrønlandske kyst." },
  { kind: "movie", title: "Sommeren på Fanø", year: 2022, hue: 42, genres: ["Romantik"], overview: "To fremmede deler et feriehus en hel sommer, fordi udlejeren har dobbeltbooket." },
  { kind: "series", title: "Tidsrejsende", year: 2020, hue: 280, genres: ["Familie", "Eventyr"], overview: "To søskende finder et ur, der sender dem tilbage til Christian IV's København." },
  { kind: "movie", title: "Skyggespil", year: 2024, hue: 0, genres: ["Thriller"], overview: "En dukkefører opdager, at hans forestillinger bliver til virkelighed." },
  { kind: "movie", title: "Fyrtårnet", year: 2021, hue: 35, genres: ["Gyser"], overview: "Et ungt par overtager et fyrtårn, som ingen har villet bo i siden 1953." },
  { kind: "series", title: "Rigets kok", year: 2025, hue: 95, genres: ["Reality"], overview: "Ti amatørkokke kæmper om at lave nytårsmenuen på Amalienborg." },
  { kind: "movie", title: "Vinterbørn", year: 2023, hue: 210, genres: ["Familie"], overview: "Tre børn bygger en snehule, der viser sig at være meget større indeni." },
  { kind: "series", title: "Kystvagten", year: 2019, hue: 200, genres: ["Action"], overview: "Redningsfolk på Bornholm døgnet rundt, sommer som vinter." },
  { kind: "movie", title: "Bogtyven", year: 2020, hue: 25, genres: ["Drama"], overview: "En pensioneret bibliotekar stjæler én bog om ugen. Ingen ved hvorfor." },
] satisfies Omit<DemoTitle, "id">[]).map((title, index) => ({ ...title, id: `d0000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}` }))

const poster = (title: DemoTitle) => `https://image.tmdb.org/t/p/w342/demo-${titles.indexOf(title)}.svg`

const media = titles.map((title) => ({
  id: title.id, kind: title.kind, title: title.title, year: title.year,
  identities: [{ namespace: "tmdb", value: String(900_000 + titles.indexOf(title)) }],
  metadata: { overview: title.overview, original_language: "da", genres: title.genres, poster_url: poster(title), season_count: title.kind === "series" ? 3 : undefined },
}))

const byTitle = (name: string) => titles.find((title) => title.title === name)!

function workflow(name: string, variant: string, state: string, minutesAgo: number, extra: Record<string, unknown> = {}) {
  const title = byTitle(name)
  return {
    id: `e0000000-0000-4000-8000-${String(titles.indexOf(title) + 1).padStart(6, "0")}${String(minutesAgo).padStart(6, "0")}`,
    media_id: title.id, media_kind: title.kind, title: title.title, year: title.year, variant, state, revision: 4,
    adopted_replacement_confirmation_required: false, created_at: at(minutesAgo + 600), updated_at: at(minutesAgo),
    quality_profile: title.kind === "movie" ? "Film med danske undertekster" : "Serier med danske undertekster",
    available_actions: [], ...extra,
  }
}

const attention = [
  workflow("Den sidste færge", "default", "needs_attention", 95, { attention_code: "retained_failed_output_limit", owner_action_required: true }),
  workflow("Kystvagten", "season:2", "needs_attention", 240, { attention_code: "altmount_job_attention", owner_action_required: true }),
]

const recent = [
  workflow("Nordlys over Skagen", "default", "plex_confirmed", 4, { terminal_at: at(4) }),
  workflow("Havnefogeden", "season:3:episode:6", "verifying", 9),
  workflow("Isbjørnens rejse", "default", "downloading", 14),
  workflow("Sporet i sneen", "season:1", "plex_confirmed", 38, { terminal_at: at(38) }),
  workflow("Rigets kok", "season:1:episode:4", "searching", 52),
  workflow("Stormfloden", "default", "plex_confirmed", 75, { terminal_at: at(75) }),
  workflow("Kolonihaven", "season:2", "waiting", 130),
  workflow("Vinterbørn", "default", "publishing", 160),
]

const calendar = [
  { title: "Havnefogeden", season: 3, episode: 7, name: "Lavvande", offset: 1 },
  { title: "Rigets kok", season: 1, episode: 5, name: "Smørrebrød i særklasse", offset: 2 },
  { title: "Sporet i sneen", season: 2, episode: 1, name: "Den lange nat", offset: 4 },
  { title: "Byens lys", season: 4, episode: 3, name: "Sidste runde", offset: 6 },
].map((entry) => ({
  media_id: byTitle(entry.title).id, media_kind: "series", media_title: entry.title, workflow_id: byTitle(entry.title).id,
  variant: `season:${entry.season}:episode:${entry.episode}`, season: entry.season, episode: entry.episode, episode_title: entry.name,
  air_date: day(entry.offset), monitored: true, workflow_state: "waiting",
}))

const series = byTitle("Havnefogeden")
const seriesDetail = {
  media: media.find((item) => item.id === series.id),
  workflows: [
    workflow("Havnefogeden", "season:1", "plex_confirmed", 20_000, { terminal_at: at(20_000) }),
    workflow("Havnefogeden", "season:2", "plex_confirmed", 9_000, { terminal_at: at(9_000) }),
    ...[1, 2, 3, 4, 5].map((episode) => workflow("Havnefogeden", `season:3:episode:${episode}`, "plex_confirmed", 7_000 - episode * 1_000, { terminal_at: at(7_000 - episode * 1_000) })),
    workflow("Havnefogeden", "season:3:episode:6", "verifying", 9),
  ],
  seasons: [1, 2, 3].map((number) => ({
    number, monitored: true, prefer_season_pack: true, search_immediately: true,
    state: number < 3 ? "plex_confirmed" : "waiting", expected_episodes: number < 3 ? 8 : 10, updated_at: at(number * 1000),
    episodes: Array.from({ length: number < 3 ? 8 : 10 }, (_, index) => ({ number: index + 1, title: ["Højvande", "Fortøjet", "Natfiskeren", "Tågehornet", "Bundgarn", "Strømkæntring", "Lavvande", "Molen", "Fyrskibet", "Sidste tur"][index] })),
  })),
  files: [],
}

/** A flat, abstract poster: a two-tone gradient with the title, drawn locally. */
function posterSVG(index: number): string {
  const title = titles[index] ?? titles[0]
  const words = title.title.split(" ")
  const lines: string[] = []
  for (const word of words) {
    const last = lines.at(-1)
    if (last && (last + " " + word).length <= 12) lines[lines.length - 1] = last + " " + word
    else lines.push(word)
  }
  const text = lines.map((line, i) => `<text x="24" y="${300 + i * 34}" font-family="Inter, Arial, sans-serif" font-size="30" font-weight="700" fill="#fff">${line}</text>`).join("")
  return `<svg xmlns="http://www.w3.org/2000/svg" width="342" height="513" viewBox="0 0 342 513">
<defs><linearGradient id="g" x1="0" y1="0" x2="0.4" y2="1"><stop offset="0" stop-color="hsl(${title.hue} 55% 58%)"/><stop offset="1" stop-color="hsl(${(title.hue + 40) % 360} 60% 18%)"/></linearGradient></defs>
<rect width="342" height="513" fill="url(#g)"/>
<circle cx="250" cy="120" r="70" fill="#fff" fill-opacity="0.12"/>
<path d="M0 420 Q 90 370 171 410 T 342 400 V 513 H 0 Z" fill="#000" fill-opacity="0.25"/>
${text}
<text x="24" y="480" font-family="Inter, Arial, sans-serif" font-size="16" fill="#fff" fill-opacity="0.8">${title.year}</text>
</svg>`
}

/** Serves the shared browser-test mock, overridden with the demo library. */
export async function mockDemo(page: Page): Promise<void> {
  await page.clock.setFixedTime(now)
  await mockApi(page)
  const routes: Array<[RegExp, (url: URL) => unknown]> = [
    [/^\/api\/v1\/dashboard$/, () => ({ counts: { movies: 412, series: 87, active_workflows: 23, needs_attention: attention.length, current_files: 3_948, recycle_entries: 0, pending_events: 12 }, recent_workflows: recent, recent_activity: [], generated_at: now.toISOString() })],
    [/^\/api\/v1\/workflows$/, (url) => (url.searchParams.get("attention") === "true" ? attention : [...attention, ...recent].filter((item) => item.state !== "plex_confirmed"))],
    [/^\/api\/v1\/calendar$/, () => calendar],
    [/^\/api\/v1\/media$/, (url) => (url.searchParams.get("offset") === "0" || !url.searchParams.get("offset") ? media : [])],
    [/^\/api\/v1\/media\/[0-9a-f-]+\/detail$/, () => seriesDetail],
    [/^\/api\/v1\/system\/health$/, () => ({
      database_state: "ready", database_latency_ms: 2, schema_version: 51, expected_schema: 51, outbox: { pending: 12, due: 0, retrying: 0 },
      components: [], mounts: [{ integration_id: "a", name: "AltMount", path: "/mnt/altmount", state: "available" }], checked_at: now.toISOString(),
    })],
  ]
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url())
    const match = route.request().method() === "GET" ? routes.find(([pattern]) => pattern.test(url.pathname)) : undefined
    if (!match) return route.fallback()
    return route.fulfill({ json: { data: match[1](url) } })
  })
  await page.route("https://image.tmdb.org/**", (route) => {
    const index = Number(/demo-(\d+)\.svg$/.exec(new URL(route.request().url()).pathname)?.[1] ?? 0)
    return route.fulfill({ contentType: "image/svg+xml", body: posterSVG(index) })
  })
}

export const demoSeriesID = series.id
