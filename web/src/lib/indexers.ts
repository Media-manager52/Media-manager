/**
 * Newznab indexers whose API address is known, so a beginner only picks the
 * name and pastes the API key. Every address is the one Media Manager uses in
 * production. Limits are not filled in: they depend on the account.
 */
export const indexerPresets: Array<{ name: string; url: string }> = [
  { name: "abNZB", url: "https://abnzb.com/api" },
  { name: "altHUB", url: "https://api.althub.co.za/api" },
  { name: "DrunkenSlug", url: "https://drunkenslug.com/api" },
  { name: "msgnews", url: "https://msgnews.live/api" },
  { name: "NinjaCentral", url: "https://ninjacentral.co.za/api" },
  { name: "NZBFinder", url: "https://nzbfinder.ws/api" },
  { name: "NZBgeek", url: "https://api.nzbgeek.info/api" },
  { name: "Nzb.life", url: "https://api.nzb.life/api" },
  { name: "treasure-maps", url: "https://treasure-maps.com/api" },
]

/** The preset whose address is url, if any. */
export function indexerPresetFor(url: string): string {
  const clean = url.trim().replace(/\/+$/, "").toLowerCase()
  return indexerPresets.find((preset) => preset.url.toLowerCase() === clean)?.name ?? ""
}
