/** Danish formatting helpers. Dates are shown in the viewer's time zone. */
const dateFormat = new Intl.DateTimeFormat("da-DK", { day: "numeric", month: "short", year: "numeric" })
const dateTimeFormat = new Intl.DateTimeFormat("da-DK", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" })
const weekdayFormat = new Intl.DateTimeFormat("da-DK", { weekday: "long", day: "numeric", month: "long" })
const relative = new Intl.RelativeTimeFormat("da-DK", { numeric: "auto" })
const numberFormat = new Intl.NumberFormat("da-DK")

export function formatDate(value: string | undefined | null): string {
  if (!value) return "—"
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? "—" : dateFormat.format(date)
}

export function formatDateTime(value: string | undefined | null): string {
  if (!value) return "—"
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? "—" : dateTimeFormat.format(date)
}

export function formatWeekday(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : weekdayFormat.format(date)
}

/** "om 3 timer" / "for 2 dage siden". `now` is injectable for tests. */
export function formatRelative(value: string | undefined | null, now = Date.now()): string {
  if (!value) return "—"
  const time = new Date(value).getTime()
  if (Number.isNaN(time)) return "—"
  const seconds = Math.round((time - now) / 1000)
  const abs = Math.abs(seconds)
  if (abs < 45) return "lige nu"
  if (abs < 3600) return relative.format(Math.round(seconds / 60), "minute")
  if (abs < 86400) return relative.format(Math.round(seconds / 3600), "hour")
  if (abs < 86400 * 30) return relative.format(Math.round(seconds / 86400), "day")
  return formatDate(value)
}

export function formatNumber(value: number | undefined | null): string {
  return value === undefined || value === null ? "—" : numberFormat.format(value)
}

export function formatBytes(value: number | undefined | null): string {
  if (!value || value <= 0) return "—"
  const units = ["B", "KB", "MB", "GB", "TB"]
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  return `${size.toLocaleString("da-DK", { maximumFractionDigits: size >= 10 ? 0 : 1 })} ${units[unit]}`
}

export function pluralize(count: number, one: string, many: string): string {
  return `${formatNumber(count)} ${count === 1 ? one : many}`
}
