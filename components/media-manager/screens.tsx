"use client"

import * as React from "react"
import Image from "next/image"
import Link from "next/link"
import {
  Activity,
  ArrowRight,
  ChevronDown,
  Database,
  FileSymlink,
  Filter,
  Grid2X2,
  Library,
  List,
  MoreHorizontal,
  Play,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  Sparkles,
  Target,
} from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { cn } from "@/lib/utils"
import { liveMode, loadMedia } from "@/lib/media-manager-api"
import {
  dashboardMetrics,
  mediaRecords,
  releaseCandidates,
  type QueueRecord,
  type ReleaseCandidate,
} from "@/lib/media-manager-data"
import { InlineNotice, KeyValue, MetricCard, Panel, StatusChip, WorkProgress } from "./ui"

const statusTone = {
  Available: "good",
  Monitoring: "info",
  Wanted: "warn",
  Importing: "info",
  Cataloged: "neutral",
  "Streaming ready": "good",
  Processing: "info",
  Queued: "neutral",
  "Needs attention": "warn",
  Eligible: "good",
  Held: "warn",
  Rejected: "bad",
} as const

export function DashboardScreen({ queue }: { queue: QueueRecord[] }) {
  return (
    <div className="space-y-4">
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {dashboardMetrics.map((metric, index) => {
          const icons = [Library, Target, Activity, ShieldCheck]
          return <MetricCard key={metric.label} {...metric} icon={icons[index]} />
        })}
      </div>

      <div className="grid gap-4 xl:grid-cols-[1.35fr_.65fr]">
        <Panel
          title="Library pulse"
          subtitle="The next useful decisions, already reduced to evidence"
          action={<Button asChild variant="outline" size="sm"><Link href="/wanted">Open wanted</Link></Button>}
        >
          <div className="grid gap-3 sm:grid-cols-3">
            <MetricCard label="Video upgrades" value="27" note="Cutoff not met" tone="warn" />
            <MetricCard label="Language upgrades" value="8" note="Danish policy gaps" tone="warn" />
            <MetricCard label="Unmapped files" value="6" note="Review required" tone="bad" />
          </div>
          <div className="mt-4">
            <InlineNotice
              tone="info"
              title="A stronger Blade Runner 2049 candidate is ready"
              action={<Button asChild size="sm"><Link href="/intelligence/releases">Compare evidence <ArrowRight /></Link></Button>}
            >
              2160p UHD REMUX · Dolby Vision · TrueHD Atmos · verified Danish subtitles · score 96.
            </InlineNotice>
          </div>
        </Panel>

        <Panel
          title="Connected system"
          subtitle="Each boundary stays independently observable"
          action={<Button asChild variant="ghost" size="sm"><Link href="/system/health">Health</Link></Button>}
        >
          <KeyValue label="AltMount" value="Ready · streaming" valueClassName="text-[#69d3a3]" />
          <KeyValue label="Plex" value="Connected · 2 libraries" />
          <KeyValue label="Seerr" value="Reconciled 2m ago" />
          <KeyValue label="PostgreSQL" value="Ready · migrations current" />
          <KeyValue label="Indexer scheduler" value="3 lanes · no cooldowns" />
        </Panel>
      </div>

      <div className="grid gap-4 xl:grid-cols-2">
        <Panel
          title="Active work"
          subtitle="Transport, verification, import, and publication remain separate states"
          action={<Button asChild variant="outline" size="sm"><Link href="/activity/queue">Open queue</Link></Button>}
        >
          <div className="space-y-3">
            {queue.slice(0, 3).map((item) => (
              <div key={item.id} className="rounded-xl border border-white/[0.06] bg-[#0c1015] p-3">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="truncate text-[11px] font-bold text-[#e8edf4]">{item.title}</div>
                    <div className="mt-1 text-[9.5px] text-[#7f8a9a]">{item.client} · {item.stage}</div>
                  </div>
                  <StatusChip tone={statusTone[item.state]}>{item.state}</StatusChip>
                </div>
                <div className="mt-3"><WorkProgress value={item.progress} /></div>
              </div>
            ))}
          </div>
        </Panel>

        <Panel
          title="Upcoming"
          subtitle="Feed observations are matched to every monitored need"
          action={<Button asChild variant="outline" size="sm"><Link href="/calendar">Calendar</Link></Button>}
        >
          <div className="mm-timeline">
            <TimelineEvent title="Foundation · S03E08" meta="September 3 · Apple TV+" copy="Future episode monitored with the TV UHD profile." />
            <TimelineEvent title="Example Movie · digital release" meta="September 5" copy="Eligible feed observations will be evaluated when available." />
            <TimelineEvent title="Andor · season-pack verification" meta="Now" copy="Twelve expected episodes found; per-file checks are running." />
          </div>
        </Panel>
      </div>
    </div>
  )
}

function TimelineEvent({ title, meta, copy }: { title: string; meta: string; copy: string }) {
  return (
    <div className="mm-timeline-event">
      <div className="text-[11px] font-bold text-[#e5eaf0]">{title}</div>
      <div className="mt-1 text-[9px] text-[#778291]">{meta}</div>
      <div className="mt-1.5 text-[10px] leading-5 text-[#9ca6b3]">{copy}</div>
    </div>
  )
}

export function LibraryScreen() {
  const [query, setQuery] = React.useState("")
  const [kind, setKind] = React.useState<"All" | "Movie" | "Series">("All")
  const [view, setView] = React.useState<"grid" | "list">("grid")
  const [records, setRecords] = React.useState(liveMode ? [] : mediaRecords)
  const [loading, setLoading] = React.useState(liveMode)
  const [loadError, setLoadError] = React.useState("")
  React.useEffect(() => {
    if (!liveMode) return
    const controller = new AbortController()
    loadMedia(controller.signal).then(setRecords).catch((error: unknown) => { if (!controller.signal.aborted) setLoadError(error instanceof Error ? error.message : "Could not load media") }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [])
  const filtered = records.filter((item) =>
    (kind === "All" || item.kind === kind) &&
    `${item.title} ${item.year} ${item.externalIds.join(" ")}`.toLowerCase().includes(query.toLowerCase()),
  )

  return (
    <div className="space-y-4">
      {loadError ? <InlineNotice tone="bad" title="Live catalog unavailable">{loadError}</InlineNotice> : null}
      <Panel className="!p-3">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
          <div className="relative min-w-0 flex-1 lg:max-w-md">
            <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-[#758091]" aria-hidden />
            <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search title, year, TMDB, TVDB, IMDb…" className="mm-input pl-9" />
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {(["All", "Movie", "Series"] as const).map((value) => (
              <Button key={value} size="sm" variant={kind === value ? "default" : "outline"} onClick={() => setKind(value)}>{value}</Button>
            ))}
            <Button variant="outline" size="icon" aria-label="Grid view" onClick={() => setView("grid")} data-active={view === "grid"}><Grid2X2 /></Button>
            <Button variant="outline" size="icon" aria-label="List view" onClick={() => setView("list")} data-active={view === "list"}><List /></Button>
            <Button variant="outline" size="sm"><Filter /> Filters</Button>
          </div>
        </div>
      </Panel>

      <div className="flex items-center justify-between px-1 text-[10px] text-[#7f8a9a]">
        <span>{filtered.length} of {records.length} {liveMode ? "live records" : "development records"}</span>
        <span>Sorted by title</span>
      </div>

      {loading ? <Panel><div className="py-12 text-center text-[11px] text-[#7f8a9a]">Loading the live catalog…</div></Panel> : filtered.length === 0 ? <Panel><div className="py-12 text-center"><div className="text-sm font-bold text-white">{records.length === 0 ? "Your catalog is empty" : "No media matched"}</div><div className="mt-2 text-[10px] text-[#7f8a9a]">{records.length === 0 ? "Add a movie or an exact TV season pack to begin." : "Change the search or media type filter."}</div>{records.length === 0 ? <Button asChild className="mt-4" size="sm"><Link href="/discover"><Plus /> Add media</Link></Button> : null}</div></Panel> : view === "grid" ? (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4 2xl:grid-cols-6">
          {filtered.map((item) => <MediaCard key={item.id} item={item} />)}
        </div>
      ) : (
        <Panel className="!p-0">
          <Table>
            <TableHeader><TableRow><TableHead>Title</TableHead><TableHead>Type</TableHead><TableHead>Profile</TableHead><TableHead>Root</TableHead><TableHead>State</TableHead></TableRow></TableHeader>
            <TableBody>
              {filtered.map((item) => (
                <TableRow key={item.id}>
                  <TableCell><Link className="font-semibold text-[#e9edf3] hover:text-[#aebdff]" href={item.kind === "Movie" ? `/media/movies/${item.id}` : `/media/series/${item.id}`}>{item.title} <span className="text-[#788392]">({item.year})</span></Link><div className="mt-1 text-[9px] text-[#737e8c]">{item.externalIds.join(" · ")}</div></TableCell>
                  <TableCell>{item.kind}</TableCell><TableCell>{item.profile}</TableCell><TableCell>{item.root}</TableCell>
                  <TableCell><StatusChip tone={statusTone[item.state]}>{item.state}</StatusChip></TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Panel>
      )}
    </div>
  )
}

function MediaCard({ item }: { item: (typeof mediaRecords)[number] }) {
  const href = item.kind === "Movie" ? `/media/movies/${item.id}` : `/media/series/${item.id}`
  return (
    <Link href={href} className="group overflow-hidden rounded-[15px] border border-white/[0.07] bg-[#0e1217] transition hover:-translate-y-0.5 hover:border-[#445064] hover:shadow-2xl hover:shadow-black/30">
      <div className="relative aspect-[2/3] overflow-hidden bg-[#161b22]">
        {/* User-provided concept artwork; production uses configured metadata artwork. */}
        <Image src={item.poster} alt="" width={600} height={900} className="h-full w-full object-cover transition duration-300 group-hover:scale-[1.025]" />
        <div className="absolute inset-x-0 bottom-0 h-20 bg-gradient-to-t from-[#090c10] to-transparent" />
        <div className="absolute bottom-2 left-2"><StatusChip tone={statusTone[item.state]}>{item.state}</StatusChip></div>
      </div>
      <div className="p-3">
        <div className="truncate text-[11px] font-extrabold text-[#edf1f6]">{item.title}</div>
        <div className="mt-1 text-[9.5px] text-[#7f8997]">{item.year || "Year unknown"} · {item.kind}</div>
        <div className="mt-2 line-clamp-1 text-[9px] text-[#8e98a6]">{item.quality}</div>
      </div>
    </Link>
  )
}

export function DiscoverScreen() {
  const [query, setQuery] = React.useState("Blade Runner 2049")
  const [added, setAdded] = React.useState(false)
  return (
    <div className="space-y-4">
      <Panel title="Find a movie or series" subtitle="Title, year, TMDB, TVDB, and IMDb IDs and URLs are accepted.">
        <div className="flex flex-col gap-2 sm:flex-row">
          <div className="relative flex-1">
            <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-[#758091]" />
            <Input className="mm-input pl-9" value={query} onChange={(event) => setQuery(event.target.value)} aria-label="Discover query" />
          </div>
          <Button onClick={() => toast.success("Metadata search completed from the development fixture")}>Search metadata</Button>
        </div>
      </Panel>

      <Panel title="Best identity match" subtitle="External identities are aliases; Media Manager keeps its own stable ID.">
        <div className="grid gap-5 md:grid-cols-[130px_1fr]">
          <Image src="/media/blade.svg" alt="Blade Runner 2049 concept poster" width={600} height={900} className="aspect-[2/3] w-full rounded-xl border border-white/10 object-cover" />
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2"><h2 className="text-xl font-extrabold tracking-tight text-white">Blade Runner 2049</h2><StatusChip tone="neutral">Movie · 2017</StatusChip></div>
            <p className="mt-3 max-w-3xl text-[11px] leading-6 text-[#9ba5b2]">A young blade runner unearths a long-buried secret that leads him to track down former LAPD blade runner Rick Deckard.</p>
            <div className="mt-4 flex flex-wrap gap-2"><StatusChip tone="info">TMDB 335984</StatusChip><StatusChip tone="info">IMDb tt1856101</StatusChip><StatusChip tone="good">Identity verified</StatusChip></div>
            <div className="mt-5 grid gap-3 sm:grid-cols-3">
              <SettingSelect label="Profile" value="UHD Reference" options={["UHD Reference", "UHD Balanced", "HD Compact"]} />
              <SettingSelect label="Library root" value="Movies · Primary" options={["Movies · Primary", "Movies · Archive"]} />
              <SettingSelect label="Monitoring" value="Movie" options={["Movie", "None"]} />
            </div>
            <div className="mt-5 flex flex-wrap gap-2">
              <Button disabled={added} onClick={() => { setAdded(true); toast.success("Blade Runner 2049 added and its workflow created") }}><Plus /> {added ? "Added" : "Add and search"}</Button>
              <Button variant="outline" onClick={() => { setAdded(true); toast.success("Added without immediate backlog search") }}>Add without search</Button>
              {added ? <Button asChild variant="ghost"><Link href="/media/movies/mov_blade_runner_2049">Open movie <ArrowRight /></Link></Button> : null}
            </div>
          </div>
        </div>
      </Panel>
    </div>
  )
}

function SettingSelect({ label, value, options }: { label: string; value: string; options: string[] }) {
  return (
    <label className="space-y-1.5 text-[9.5px] font-semibold uppercase tracking-wider text-[#778291]">
      {label}
      <Select defaultValue={value}>
        <SelectTrigger className="mm-input w-full normal-case tracking-normal text-[#dce2ea]"><SelectValue /></SelectTrigger>
        <SelectContent>{options.map((option) => <SelectItem key={option} value={option}>{option}</SelectItem>)}</SelectContent>
      </Select>
    </label>
  )
}

export function MovieScreen() {
  const [monitored, setMonitored] = React.useState(true)
  return (
    <div className="space-y-4">
      <section className="mm-media-hero">
        <div className="absolute inset-0 bg-[radial-gradient(circle_at_75%_25%,rgba(220,92,53,.28),transparent_32%),linear-gradient(115deg,#090c10_4%,rgba(9,12,16,.9)_46%,rgba(10,12,17,.35))]" />
        <div className="relative grid min-h-[350px] gap-5 p-5 sm:grid-cols-[145px_1fr] sm:items-end sm:p-7">
          <Image src="/media/blade.svg" alt="Blade Runner 2049 concept poster" width={600} height={900} className="hidden aspect-[2/3] w-full rounded-xl border border-white/15 object-cover shadow-2xl sm:block" />
          <div className="self-end">
            <div className="flex flex-wrap gap-2"><StatusChip tone="warn">Upgrade wanted</StatusChip><StatusChip tone="good">Plex available</StatusChip></div>
            <h2 className="mt-3 text-3xl font-[860] tracking-[-0.045em] text-white sm:text-[42px]">Blade Runner 2049</h2>
            <div className="mt-2 flex flex-wrap items-center gap-2 text-[10px] text-[#9aa5b2]"><span>2017</span><span>·</span><span>2h 44m</span><span>·</span><span>TMDB 335984</span><span>·</span><span>IMDb tt1856101</span></div>
            <p className="mt-4 max-w-3xl text-[11px] leading-6 text-[#aab3be]">Current 1080p file remains active while Media Manager evaluates a verified UHD replacement. Nothing is removed until the replacement passes file checks and Plex confirms it.</p>
            <div className="mt-5 flex flex-wrap items-center gap-2">
              <Button asChild><Link href="/intelligence/releases"><Sparkles /> Compare 4 candidates</Link></Button>
              <Button variant="outline" onClick={() => toast.success("A paced targeted search was queued")}>Search now</Button>
              <div className="ml-auto flex items-center gap-2 rounded-lg border border-white/[0.08] bg-black/20 px-3 py-2 text-[10px] text-[#aab3be]"><Switch checked={monitored} onCheckedChange={setMonitored} /> Monitored</div>
            </div>
          </div>
        </div>
      </section>

      <Tabs defaultValue="overview" className="space-y-4">
        <TabsList className="mm-tabs"><TabsTrigger value="overview">Overview</TabsTrigger><TabsTrigger value="files">Files</TabsTrigger><TabsTrigger value="history">History</TabsTrigger><TabsTrigger value="metadata">Metadata</TabsTrigger></TabsList>
        <TabsContent value="overview" className="space-y-4">
          <div className="grid gap-4 xl:grid-cols-[1.35fr_.65fr]">
            <Panel title="Current version" subtitle="The active Plex file is never displaced before the upgrade verifies.">
              <div className="overflow-hidden rounded-xl border border-white/[0.06]">
                <Table><TableHeader><TableRow><TableHead>Quality</TableHead><TableHead>Video</TableHead><TableHead>Audio</TableHead><TableHead>Subtitles</TableHead><TableHead>State</TableHead></TableRow></TableHeader><TableBody><TableRow><TableCell className="font-semibold">1080p BluRay</TableCell><TableCell>HEVC · 10-bit</TableCell><TableCell>DTS-HD MA 5.1</TableCell><TableCell>Danish full</TableCell><TableCell><StatusChip tone="good">Active</StatusChip></TableCell></TableRow></TableBody></Table>
              </div>
              <InlineNotice tone="info" title="Upgrade candidate selected by policy">
                The UHD REMUX adds Dolby Vision and TrueHD Atmos while keeping the required Danish subtitle evidence.
              </InlineNotice>
            </Panel>
            <Panel title="Monitoring" subtitle="Persisted per item; global defaults never silently rewrite it.">
              <KeyValue label="Quality profile" value="UHD Reference" />
              <KeyValue label="Library root" value="Movies · Primary" />
              <KeyValue label="Availability" value="Released" />
              <KeyValue label="Search policy" value="Immediate + feeds" />
              <KeyValue label="Upgrade state" value="Enabled · cutoff unmet" valueClassName="text-[#efc979]" />
            </Panel>
          </div>
        </TabsContent>
        <TabsContent value="files"><FileRelationship /></TabsContent>
        <TabsContent value="history"><HistoryTimeline /></TabsContent>
        <TabsContent value="metadata"><MetadataPanel /></TabsContent>
      </Tabs>
    </div>
  )
}

function FileRelationship() {
  return <Panel title="File relationship" subtitle="Every managed path retains its source, evidence, and Plex observation."><div className="grid gap-2 md:grid-cols-5 md:items-center"><RelationNode title="AltMount source" copy="Read-only virtual file" /><ArrowRight className="mx-auto hidden size-4 text-[#5f6b7c] md:block" /><RelationNode title="Managed symlink" copy="Plex-safe name" /><ArrowRight className="mx-auto hidden size-4 text-[#5f6b7c] md:block" /><RelationNode title="Plex item" copy="Identity confirmed" /></div><div className="mt-4 rounded-xl border border-white/[0.06] bg-[#090d12] p-3 font-mono text-[10px] leading-5 text-[#aeb8c5]">Blade Runner 2049 (2017) &#123;tmdb-335984&#125;/Blade Runner 2049 (2017) &#123;tmdb-335984&#125; - [1080p BluRay HEVC DTS-HD-MA-5.1 GROUP].mkv</div></Panel>
}

function RelationNode({ title, copy }: { title: string; copy: string }) {
  return <div className="rounded-xl border border-white/[0.07] bg-[#0e1319] p-4 text-center"><FileSymlink className="mx-auto size-5 text-[#91a8ff]" /><div className="mt-2 text-[10px] font-bold text-[#e2e7ed]">{title}</div><div className="mt-1 text-[9px] text-[#778291]">{copy}</div></div>
}

function HistoryTimeline() {
  return <Panel title="Durable history" subtitle="Every transition and manual decision remains auditable."><div className="mm-timeline"><TimelineEvent title="Better candidate selected" meta="Today · policy UHD Reference" copy="Hard requirements passed; score 96. Selection has not changed the active library file." /><TimelineEvent title="Current file adopted" meta="August 12 · import batch 14" copy="Physical file renamed after reviewed plan; Plex identity preserved." /><TimelineEvent title="Monitoring enabled" meta="August 12 · owner" copy="Backlog search was enabled separately for this item." /></div></Panel>
}

function MetadataPanel() {
  return <Panel title="Verified identity" subtitle="Metadata describes the title. It does not prove release facts."><div className="grid gap-4 sm:grid-cols-2"><div><KeyValue label="Media Manager ID" value="mov_blade_runner_2049" /><KeyValue label="TMDB" value="335984 · verified" /><KeyValue label="IMDb" value="tt1856101 · verified" /><KeyValue label="Type" value="Movie" /></div><div><KeyValue label="Title" value="Blade Runner 2049" /><KeyValue label="Year" value="2017" /><KeyValue label="Runtime" value="164 minutes" /><KeyValue label="Metadata refreshed" value="18 minutes ago" /></div></div></Panel>
}

export function ReleasesScreen({ onGrab }: { onGrab: (candidate: ReleaseCandidate) => void }) {
  const [selected, setSelected] = React.useState(releaseCandidates[0].id)
  const active = releaseCandidates.find((candidate) => candidate.id === selected) ?? releaseCandidates[0]
  return (
    <div className="grid gap-4 xl:grid-cols-[1fr_360px]">
      <div className="space-y-3">
        <Panel className="!p-3">
          <div className="flex flex-wrap items-center gap-2"><StatusChip tone="info">Blade Runner 2049</StatusChip><span className="text-[10px] text-[#7f8a9a]">4 candidates · observed across 3 indexers · newest evidence first</span><Button className="ml-auto" variant="outline" size="sm" onClick={() => toast.success("Search refresh queued within indexer limits")}><RefreshCw /> Refresh safely</Button></div>
        </Panel>
        {releaseCandidates.map((candidate) => (
          <button key={candidate.id} onClick={() => setSelected(candidate.id)} className={cn("w-full rounded-2xl border bg-[#0e1217] p-4 text-left transition", selected === candidate.id ? "border-[#647ee0] shadow-[0_0_0_3px_rgba(100,126,224,.1)]" : "border-white/[0.07] hover:border-[#3d4858]") }>
            <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
              <div className="min-w-0"><div className="break-all text-[11px] font-bold leading-5 text-[#e8edf3]">{candidate.title}</div><div className="mt-1 text-[9.5px] text-[#75808f]">{candidate.indexer} · {candidate.age} · {candidate.size}</div></div>
              <div className="flex shrink-0 items-center gap-2"><span className="text-lg font-[850] text-[#c2cdfb]">{candidate.score}</span><StatusChip tone={statusTone[candidate.decision]}>{candidate.decision}</StatusChip></div>
            </div>
            <div className="mt-3 flex flex-wrap gap-1.5">{candidate.facts.map((fact) => <StatusChip key={fact} tone={candidate.decision === "Rejected" ? "bad" : "neutral"}>{fact}</StatusChip>)}</div>
            <p className="mt-3 text-[10px] leading-5 text-[#939eab]">{candidate.reason}</p>
          </button>
        ))}
      </div>
      <Panel title="Decision evidence" subtitle="Hard requirements run before preferences." className="h-fit xl:sticky xl:top-24">
        <div className="text-[12px] font-bold leading-5 text-[#e9edf3]">{active.title}</div>
        <div className="mt-4"><KeyValue label="Eligibility" value={<StatusChip tone={statusTone[active.decision]}>{active.decision}</StatusChip>} /><KeyValue label="Policy score" value={`${active.score} / 100`} /><KeyValue label="Indexer" value={active.indexer} /><KeyValue label="Native identity" value="Preserved" /></div>
        <InlineNotice tone={active.decision === "Eligible" ? "good" : active.decision === "Held" ? "warn" : "bad"} title={active.decision === "Eligible" ? "Safe to select" : active.decision === "Held" ? "Evidence incomplete" : "Hard requirement failed"}>{active.reason}</InlineNotice>
        <Button className="mt-4 w-full" disabled={active.decision !== "Eligible"} onClick={() => onGrab(active)}><Play /> Grab with AltMount</Button>
        <Button className="mt-2 w-full" variant="outline" onClick={() => toast.info("Manual overrides require a reason and remain audited")}>Manual override…</Button>
      </Panel>
    </div>
  )
}

export function QueueScreen({ queue }: { queue: QueueRecord[] }) {
  return (
    <Panel className="!p-0">
      <Table>
        <TableHeader><TableRow><TableHead>Work</TableHead><TableHead>Client</TableHead><TableHead>Stage</TableHead><TableHead>Progress</TableHead><TableHead>State</TableHead><TableHead className="text-right">Actions</TableHead></TableRow></TableHeader>
        <TableBody>
          {queue.map((item) => (
            <TableRow key={item.id}>
              <TableCell><div className="max-w-sm text-[10.5px] font-bold text-[#e4e9ef]">{item.title}</div><div className="mt-1 text-[9px] text-[#717c89]">{item.kind} · {item.id}</div></TableCell>
              <TableCell>{item.client}</TableCell><TableCell><div className="max-w-[240px] text-[10px] text-[#a2acb8]">{item.stage}</div></TableCell>
              <TableCell className="min-w-36"><WorkProgress value={item.progress} label={item.progress ? `${item.progress}% · ETA ${item.eta}` : item.eta} /></TableCell>
              <TableCell><StatusChip tone={statusTone[item.state]}>{item.state}</StatusChip></TableCell>
              <TableCell className="text-right"><Button variant="ghost" size="icon" aria-label={`Actions for ${item.title}`}><MoreHorizontal /></Button></TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <div className="border-t border-white/[0.06] px-4 py-3 text-[9.5px] text-[#778291]">AltMount “streaming ready,” “completed,” file verification, and Plex confirmation are intentionally separate.</div>
    </Panel>
  )
}

export function SeriesScreen() {
  const [expanded, setExpanded] = React.useState(true)
  const airDates = ["Aug 4, 2026", "Aug 11, 2026", "Aug 18, 2026", "Aug 25, 2026", "Sep 1, 2026", "Sep 8, 2026", "Sep 15, 2026", "Sep 22, 2026"]
  return (
    <div className="space-y-4">
      <section className="mm-media-hero min-h-[315px]">
        <div className="absolute inset-0 bg-[radial-gradient(circle_at_75%_25%,rgba(109,119,190,.3),transparent_34%),linear-gradient(115deg,#090c10_4%,rgba(9,12,16,.88)_48%,rgba(10,12,17,.3))]" />
        <div className="relative grid min-h-[315px] gap-5 p-5 sm:grid-cols-[125px_1fr] sm:items-end sm:p-7">
          <Image src="/media/foundation.svg" alt="Foundation concept poster" width={600} height={900} className="hidden aspect-[2/3] rounded-xl border border-white/15 object-cover shadow-2xl sm:block" />
          <div><div className="flex flex-wrap gap-2"><StatusChip tone="info">Future monitored</StatusChip><StatusChip tone="good">S01–S02 available</StatusChip></div><h2 className="mt-3 text-3xl font-[860] tracking-[-0.045em] text-white sm:text-[40px]">Foundation</h2><div className="mt-2 text-[10px] text-[#9aa5b2]">2021 · TVDB 366972 · TMDB 93740 · Standard numbering</div><p className="mt-4 max-w-3xl text-[11px] leading-6 text-[#aab3be]">Season packs are preferred. One selected pack becomes one AltMount job; Media Manager verifies every expected episode before the season becomes complete.</p><div className="mt-5 flex gap-2"><Button onClick={() => toast.success("Season-pack search queued within indexer limits")}><Search /> Search monitored seasons</Button><Button variant="outline">Edit monitoring</Button></div></div>
        </div>
      </section>

      <div className="grid gap-4 xl:grid-cols-[1.4fr_.6fr]">
        <Panel title="Seasons & episodes" subtitle="Monitoring and coverage are persisted at series, season, and episode level.">
          <div className="flex w-full items-center gap-3 rounded-xl border border-white/[0.07] bg-[#0c1015] p-3"><button className="flex min-w-0 flex-1 items-center gap-3 text-left" onClick={() => setExpanded(!expanded)} aria-expanded={expanded}><ChevronDown className={cn("size-4 text-[#8da6ff] transition", !expanded && "-rotate-90")} /><div><div className="text-[11px] font-bold text-[#e4e9ef]">Season 03</div><div className="mt-1 text-[9px] text-[#778291]">10 episodes · 8 aired · pack preferred</div></div></button><div className="flex items-center gap-2"><StatusChip tone="info">Monitored</StatusChip><Switch defaultChecked aria-label="Monitor season 3" /></div></div>
          {expanded ? <div className="mt-2 overflow-hidden rounded-xl border border-white/[0.06]"><Table><TableHeader><TableRow><TableHead>Episode</TableHead><TableHead>Air date</TableHead><TableHead>Coverage</TableHead><TableHead>Monitor</TableHead></TableRow></TableHeader><TableBody>{Array.from({ length: 8 }, (_, index) => <TableRow key={index}><TableCell className="font-semibold">S03E{String(index + 1).padStart(2, "0")} · {index === 7 ? "The Last Empress" : `Episode ${index + 1}`}</TableCell><TableCell>{airDates[index]}</TableCell><TableCell>{index < 7 ? <StatusChip tone="good">Available</StatusChip> : <StatusChip tone="info">In pack queue</StatusChip>}</TableCell><TableCell><Checkbox defaultChecked aria-label={`Monitor episode ${index + 1}`} /></TableCell></TableRow>)}</TableBody></Table></div> : null}
        </Panel>
        <Panel title="Pack coverage" subtitle="AltMount completion only starts enumeration.">
          <div className="grid grid-cols-3 gap-2"><MetricCard label="Expected" value="10" note="Season snapshot" /><MetricCard label="Mapped" value="8" note="Unique coverage" tone="info" /><MetricCard label="Issues" value="0" note="Blocking" tone="good" /></div>
          <div className="mt-4"><WorkProgress value={80} label="Verifying files · 8 of 10 mapped" /></div>
          <div className="mt-4"><KeyValue label="AltMount job" value="One pack job" /><KeyValue label="Inventory digest" value="Frozen" /><KeyValue label="Multi-episode file" value="E03–E04 mapped" /><KeyValue label="Publication" value="Waiting for all checks" valueClassName="text-[#efc979]" /></div>
          <InlineNotice tone="info" title="Coverage rule">Every expected episode must map to exactly one verified content group. Ambiguity never becomes a guess.</InlineNotice>
        </Panel>
      </div>
    </div>
  )
}

export function FilesScreen() {
  const [selected, setSelected] = React.useState(0)
  const rows = [
    { id: "file_blade_2049", name: "Blade Runner 2049 … REMUX.mkv", kind: "Managed symlink", root: "Movies · Primary", size: "72.4 GB", state: "Verified" },
    { id: "file_dune_part_two", name: "Dune Part Two … DV Atmos.mkv", kind: "Managed symlink", root: "Movies · Primary", size: "64.7 GB", state: "Verified" },
    { id: "file_foundation_s03e01", name: "Foundation - S03E01.mkv", kind: "AltMount virtual", root: "TV · Primary", size: "8.2 GB", state: "Read-only" },
    { id: "file_unknown_companion", name: "unknown-companion.bin", kind: "Unknown companion", root: "Import review", size: "4.1 MB", state: "Held" },
  ]
  return (
    <div className="grid gap-4 xl:grid-cols-[1fr_320px]">
      <Panel className="!p-0">
        <div className="flex gap-2 border-b border-white/[0.06] p-3"><div className="relative flex-1"><Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-[#758091]" /><Input className="mm-input pl-9" placeholder="Search path, title, source, or state…" /></div><Button variant="outline"><Filter /> Filter</Button></div>
        <Table><TableHeader><TableRow><TableHead>File</TableHead><TableHead>Type</TableHead><TableHead>Root</TableHead><TableHead>Size</TableHead><TableHead>State</TableHead></TableRow></TableHeader><TableBody>{rows.map((row, index) => <TableRow key={row.id} onClick={() => setSelected(index)} data-state={selected === index ? "selected" : undefined} className="cursor-pointer"><TableCell className="max-w-sm truncate font-semibold">{row.name}</TableCell><TableCell>{row.kind}</TableCell><TableCell>{row.root}</TableCell><TableCell>{row.size}</TableCell><TableCell><StatusChip tone={row.state === "Verified" ? "good" : row.state === "Held" ? "warn" : "info"}>{row.state}</StatusChip></TableCell></TableRow>)}</TableBody></Table>
      </Panel>
      <Panel title="Inspector" subtitle={rows[selected].name} className="h-fit xl:sticky xl:top-24">
        <KeyValue label="State" value={rows[selected].state} /><KeyValue label="Storage" value={rows[selected].root} /><KeyValue label="Kind" value={rows[selected].kind} /><KeyValue label="Size" value={rows[selected].size} /><KeyValue label="Containment" value="Inside configured root" valueClassName="text-[#67d1a2]" /><KeyValue label="Source mutations" value="Forbidden" />
        <Button asChild className="mt-4 w-full"><Link href={`/files/${rows[selected].id}`}>Inspect relationship</Link></Button>
        <Button className="mt-2 w-full" variant="outline" onClick={() => toast.info("A preview is required before any file operation")}>Plan operation…</Button>
      </Panel>
    </div>
  )
}

export function FileDetailScreen() {
  return <div className="space-y-4"><FileRelationship /><div className="grid gap-4 xl:grid-cols-2"><Panel title="MediaInfo" subtitle="Measured from the actual file, never inferred from its name."><KeyValue label="Container" value="Matroska" /><KeyValue label="Video" value="HEVC Main 10 · 3840×2160" /><KeyValue label="Dynamic range" value="Dolby Vision Profile 7 + HDR10" /><KeyValue label="Audio" value="TrueHD Atmos · 7.1" /><KeyValue label="Subtitles" value="Danish full · English SDH" /><KeyValue label="Duration" value="2:43:47" /></Panel><Panel title="Safety and provenance" subtitle="The read-only AltMount target remains outside Media Manager’s mutation authority."><KeyValue label="Source" value="AltMount job 1274" /><KeyValue label="Target mapping" value="Contained · verified" /><KeyValue label="Link state" value="Healthy" valueClassName="text-[#68d2a2]" /><KeyValue label="Plex identity" value="TMDB 335984" /><KeyValue label="Last verified" value="2 minutes ago" /><Button variant="outline" className="mt-4 w-full" onClick={() => toast.info("A complete operation plan would be shown before execution")}>Plan rename or move</Button></Panel></div></div>
}

export function IndexerDetailScreen() {
  const operations = [["CAPS", "Verified", "Aug 28"], ["General text search", "Verified", "Aug 28"], ["Movie IMDb search", "Verified", "Aug 28"], ["Movie TMDB search", "Advertised", "—"], ["TVDB + season + episode", "Inconclusive", "Aug 28"], ["DETAILS", "Verified", "Aug 28"], ["GETNFO", "Verified", "Aug 28"], ["NZB retrieval", "Verified", "Aug 28"]]
  return <div className="grid gap-4 xl:grid-cols-[1fr_330px]"><Panel title="Operation verification" subtitle="Advertisement, verification, and the last request outcome remain separate."><Table><TableHeader><TableRow><TableHead>Operation</TableHead><TableHead>Capability</TableHead><TableHead>Last proof</TableHead></TableRow></TableHeader><TableBody>{operations.map((row) => <TableRow key={row[0]}><TableCell className="font-semibold">{row[0]}</TableCell><TableCell><StatusChip tone={row[1] === "Verified" ? "good" : row[1] === "Inconclusive" ? "warn" : "info"}>{row[1]}</StatusChip></TableCell><TableCell>{row[2]}</TableCell></TableRow>)}</TableBody></Table></Panel><div className="space-y-4"><Panel title="Runtime limits" subtitle="Testing never bypasses these limits."><KeyValue label="Max in flight" value="1" /><KeyValue label="Minimum interval" value="3 seconds" /><KeyValue label="Cooldown" value="None" /><KeyValue label="Daily request budget" value="783 remaining" /><KeyValue label="Latest outcome" value="Success" valueClassName="text-[#69d3a3]" /></Panel><Panel title="Observed coverage"><KeyValue label="Recent sample" value="500 unique" /><KeyValue label="Pagination" value="5 verified pages" /><KeyValue label="Duplicate overlap" value="0.8%" /><KeyValue label="Raw attributes" value="Preserved with order" /></Panel></div></div>
}

export function PreviewBadge() {
  return <div className="inline-flex items-center gap-2 rounded-full border border-[#334260] bg-[#11192b] px-2.5 py-1 text-[9px] font-bold text-[#b8c6f7]"><Database className="size-3" /> {liveMode ? "Live API" : "Development data"}</div>
}
