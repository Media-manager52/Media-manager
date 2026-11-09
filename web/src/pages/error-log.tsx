import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { Copy, Download } from "lucide-react"
import { useDeferredValue, useState } from "react"
import { Link } from "react-router"

import { loadErrorLog, type APIErrorLogEntry } from "@/api/client"
import { Badge, Button, Card, CardHeader, Empty, ErrorBox, PageHeader, SearchField, Segmented } from "@/components/ui"
import { useToast } from "@/components/toast"
import { formatDateTime, formatRelative } from "@/format"
import { errorAreaLabel, errorDetail, errorEntryText, errorExplanation, errorLogAreas, errorLogText, type ErrorLogArea } from "@/lib/error-log"
import { describeVariant, titleHref, workflowHref } from "@/lib/links"
import { ListSkeleton } from "@/pages/overview"

function scopeOf(entry: APIErrorLogEntry): string {
  if (!entry.variant || entry.variant === "default") return ""
  return describeVariant({ variant: entry.variant, media_kind: entry.media_kind === "movie" ? "movie" : "series" })
}

function linkOf(entry: APIErrorLogEntry): string {
  if (entry.workflow_id) return workflowHref({ id: entry.workflow_id })
  if (entry.media_id) return titleHref(entry.media_id)
  if (entry.area === "backup") return "/system/backup"
  if (entry.area === "connection" || entry.area === "search") return "/kilder"
  return ""
}

export function ErrorLogPage() {
  const toast = useToast()
  const [area, setArea] = useState<ErrorLogArea>("")
  const [input, setInput] = useState("")
  const search = useDeferredValue(input.trim())
  const log = useQuery({
    queryKey: ["error-log", area, search],
    queryFn: ({ signal }) => loadErrorLog({ area, q: search }, signal),
    placeholderData: keepPreviousData,
    refetchInterval: 60_000,
  })
  const data = log.data
  const entries = data?.entries ?? []
  const copy = async (text: string, done: string) => {
    try {
      await navigator.clipboard.writeText(text)
      toast("good", done)
    } catch {
      toast("bad", "Browseren tillod ikke kopiering. Brug Hent som tekstfil i stedet.")
    }
  }
  const download = () => {
    const link = document.createElement("a")
    link.href = URL.createObjectURL(new Blob([errorLogText(entries, data?.total ?? 0, scopeOf)], { type: "text/plain;charset=utf-8" }))
    link.download = "media-manager-fejllog.txt"
    link.click()
    URL.revokeObjectURL(link.href)
  }
  const allCount = Object.values(data?.areas ?? {}).reduce((sum, count) => sum + count, 0)
  return (
    <>
      <PageHeader
        title="Fejllog"
        description="Alt, der er gået galt de seneste 30 dage, nyeste først. Åbne sager står her, indtil de er løst. Nøgler og adgangskoder er fjernet."
        actions={<SearchField value={input} onChange={setInput} placeholder="Søg i titel eller fejl" label="Søg i fejlloggen" />}
      />
      {log.error ? <ErrorBox error={log.error} retry={() => log.refetch()} /> : null}
      <div className="mb-4">
        <Segmented
          label="Vis fejl fra"
          value={area}
          onChange={setArea}
          options={errorLogAreas.map((entry) => ({ value: entry.value, label: entry.label, count: entry.value === "" ? allCount : data?.areas[entry.value] ?? 0 }))}
        />
      </div>
      <Card>
        <CardHeader
          title={data && data.total > entries.length ? `De ${entries.length} nyeste af ${data.total}` : `${entries.length} fejl`}
          description="Kopiér teksten, hvis du vil bede om hjælp eller melde en fejl. Læs den igennem først: den kan indeholde navne på dine titler."
          action={
            <div className="flex flex-wrap gap-2">
              <Button disabled={entries.length === 0} onClick={() => void copy(errorLogText(entries, data?.total ?? 0, scopeOf), "Fejlloggen er kopieret")}><Copy aria-hidden className="size-4" />Kopiér det viste</Button>
              <Button disabled={entries.length === 0} onClick={download}><Download aria-hidden className="size-4" />Hent som tekstfil</Button>
            </div>
          }
        />
        {log.isPending ? <ListSkeleton rows={6} /> : null}
        {data && entries.length === 0 ? <Empty title={search || area ? "Ingen fejl passer til din søgning" : "Ingen fejl de seneste 30 dage"} /> : null}
        <ul className="divide-y divide-line">
          {entries.map((entry) => {
            const { what, next } = errorExplanation(entry)
            const scope = scopeOf(entry)
            const href = linkOf(entry)
            return (
              <li key={`${entry.area}:${entry.id}`} className="px-5 py-3">
                <div className="flex flex-wrap items-center gap-2 text-xs text-text-3">
                  <span title={formatRelative(entry.occurred_at)}>{formatDateTime(entry.occurred_at)}</span>
                  <Badge>{errorAreaLabel(entry.area)}</Badge>
                  {entry.open ? <Badge tone="bad">Kræver dig</Badge> : null}
                  {entry.count > 1 ? <Badge tone="warn">{entry.count} gange</Badge> : null}
                </div>
                <div className="mt-1 font-medium">{what}</div>
                {entry.title ? <div className="text-sm">{href ? <Link to={href} className="font-medium text-accent-strong hover:underline">{entry.title}</Link> : entry.title}{scope ? <span className="text-text-3"> · {scope}</span> : null}</div> : null}
                {entry.subject ? <div className="break-all font-mono text-xs text-text-2">{entry.subject}</div> : null}
                <div className="mt-1 text-sm text-text-2">Det sker nu: {next}{!entry.title && href ? <> <Link to={href} className="font-medium text-accent-strong hover:underline">Åbn</Link></> : null}</div>
                <details className="mt-1 text-xs text-text-3">
                  <summary className="cursor-pointer">Teknisk detalje</summary>
                  <div className="mt-1 break-words">Kode: <span className="font-mono">{entry.code}</span></div>
                  {entry.detail ? <div className="mt-1 whitespace-pre-wrap break-words">{errorDetail(entry)}</div> : null}
                  <button type="button" className="mt-1 font-medium text-accent-strong hover:underline" onClick={() => void copy(errorEntryText(entry, scope), "Fejlen er kopieret")}>Kopiér denne fejl</button>
                </details>
              </li>
            )
          })}
        </ul>
      </Card>
    </>
  )
}
