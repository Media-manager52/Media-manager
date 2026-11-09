/**
 * A short name for a library file. The full path is long and made for Plex,
 * so the list shows the episode or the film and keeps the path one tap away.
 */
export function fileLabel(destinationRelative: string): string {
  const base = (destinationRelative.split("/").pop() ?? destinationRelative).replace(/\.[a-z0-9]{2,4}$/i, "")
  const clean = (text: string) => text.replace(/\[[^\]]*\]/g, " ").replace(/\{[^}]*\}/g, " ").replace(/\s+/g, " ").replace(/^[\s-]+|[\s-]+$/g, "")
  const episode = /\bS(\d{2,4})E(\d{2,3})((?:-?E\d{2,3})*)\b/i.exec(base)
  if (!episode) return clean(base) || destinationRelative
  const code = `S${episode[1]}E${episode[2]}${episode[3].toUpperCase().replace(/^E/, "-E")}`
  const title = clean(base.slice(episode.index + episode[0].length))
  return title ? `${code} · ${title}` : code
}

/**
 * Languages for one line: Danish first, then at most `max` names and a count
 * of the rest, so a file with thirty subtitle tracks stays readable.
 */
export function shortLanguages(names: string[], max = 2): string {
  const ordered = [...names].sort((a, b) => Number(b === "Dansk") - Number(a === "Dansk"))
  if (ordered.length <= max) return ordered.join(", ")
  const rest = ordered.length - max
  return `${ordered.slice(0, max).join(", ")} + ${rest} ${rest === 1 ? "andet" : "andre"}`
}
