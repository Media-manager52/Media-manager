"use client"

import * as React from "react"
import Link from "next/link"
import {
  Bell,
  BookOpenCheck,
  Cable,
  CalendarDays,
  CheckCircle2,
  Clock3,
  CloudCog,
  Code2,
  DatabaseBackup,
  FileClock,
  FolderCog,
  FolderInput,
  Gauge,
  Globe2,
  HardDrive,
  HeartPulse,
  Image,
  Layers3,
  ListChecks,
  LockKeyhole,
  Play,
  Plus,
  Save,
  ScanSearch,
  ServerCog,
  ShieldCheck,
  Sparkles,
  TerminalSquare,
  Users,
  Webhook,
  type LucideIcon,
} from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { InlineNotice, KeyValue, MetricCard, Panel, StatusChip, WorkProgress } from "./ui"

type PageSpec = {
  icon: LucideIcon
  metrics: Array<{ label: string; value: string; note: string; tone?: "good" | "info" | "warn" | "bad" | "neutral" }>
  panelTitle: string
  panelSubtitle: string
  columns: string[]
  rows: string[][]
  notice?: { tone: "good" | "info" | "warn" | "bad"; title: string; copy: string }
  action?: string
}

const specs: Record<string, PageSpec> = {
  "/collections": { icon: Layers3, metrics: [{ label: "Collections", value: "18", note: "7 smart · 11 manual" }, { label: "Movies grouped", value: "226", note: "Across 14 franchises", tone: "info" }, { label: "Rule matches", value: "94", note: "Current catalog", tone: "good" }], panelTitle: "Collections", panelSubtitle: "Rules are previewed before a collection changes.", columns: ["Collection", "Type", "Items", "Rule", "State"], rows: [["Dune Collection", "Franchise", "2", "Verified TMDB collection", "Current"], ["Blade Runner", "Manual", "2", "Owner selected", "Current"], ["Nordic UHD", "Smart", "47", "Profile + subtitle policy", "Current"], ["Needs Danish subtitles", "Smart", "8", "Subtitle gap", "Dynamic"]], action: "New collection" },
  "/calendar": { icon: CalendarDays, metrics: [{ label: "Next 7 days", value: "9", note: "Airing or releasing", tone: "info" }, { label: "Monitored", value: "8", note: "One intentionally unmonitored", tone: "good" }, { label: "Pack candidates", value: "2", note: "Season completion windows", tone: "info" }], panelTitle: "Upcoming availability", panelSubtitle: "Calendar dates never cause a search before the item is eligible.", columns: ["Date", "Title", "Type", "Policy", "State"], rows: [["Sep 3", "Foundation · S03E08", "Episode", "TV UHD", "Monitored"], ["Sep 5", "Example Movie", "Digital", "UHD Balanced", "Waiting"], ["Sep 8", "Silo · S03E01", "Episode", "TV UHD", "Monitored"], ["Sep 12", "Andor · Season 02", "Season end", "Pack preferred", "Monitoring"]] },
  "/wanted": { icon: Sparkles, metrics: [{ label: "Missing", value: "14", note: "No acceptable file", tone: "bad" }, { label: "Video upgrades", value: "27", note: "Cutoff unmet", tone: "warn" }, { label: "Language gaps", value: "8", note: "Audio or subtitles", tone: "warn" }], panelTitle: "Policy gaps", panelSubtitle: "Video, audio, language, and subtitle goals remain independent.", columns: ["Title", "Gap", "Current", "Target", "Search"], rows: [["Blade Runner 2049", "Video + audio", "1080p · DTS-HD", "2160p DV · Atmos", "Candidate ready"], ["Arrival", "Subtitle", "English", "Danish full", "Feeds"], ["Foundation S03E08", "Missing", "—", "TV UHD", "When aired"], ["Example Movie", "Missing", "—", "UHD Balanced", "When released"]] },
  "/activity/history": { icon: FileClock, metrics: [{ label: "Today", value: "24", note: "Durable events" }, { label: "Successful", value: "19", note: "Verified outcomes", tone: "good" }, { label: "Needs attention", value: "2", note: "Owner review", tone: "warn" }], panelTitle: "History", panelSubtitle: "The event journal is append-only and keeps precise failure scope.", columns: ["Time", "Event", "Object", "Result", "Evidence"], rows: [["00:42", "Plex confirmation", "Dune: Part Two", "Available", "Path + identity"], ["00:38", "Import verified", "Dune: Part Two", "Success", "MediaInfo + link"], ["00:30", "Release selected", "Dune: Part Two", "Score 94", "12 facts"], ["Yesterday", "Indexer result held", "One malformed item", "Scoped", "Raw response retained"]] },
  "/activity/blocklist": { icon: ShieldCheck, metrics: [{ label: "Release blocks", value: "6", note: "Exact native releases", tone: "warn" }, { label: "Pack blocks", value: "2", note: "Show + season scope", tone: "warn" }, { label: "Global blocks", value: "0", note: "No broad poisoning", tone: "good" }], panelTitle: "Blocklist", panelSubtitle: "A bad pack or result cannot silently damage an indexer or unrelated release.", columns: ["Release", "Scope", "Reason", "Added", "Expires"], rows: [["Foundation.S03.PACK-GROUP", "Foundation · S03", "Missing E07", "Aug 26", "Manual"], ["Example.Movie.2160p-GROUP", "Exact GUID", "Passworded payload", "Aug 25", "90 days"], ["Andor.S02.PACK-GROUP2", "Andor · S02", "Ambiguous mapping", "Aug 22", "Manual"]], action: "Add block" },
  "/intelligence/groups": { icon: Users, metrics: [{ label: "Observed groups", value: "86", note: "Evidence only" }, { label: "High confidence", value: "17", note: "Enough observations", tone: "good" }, { label: "Contradictions", value: "5", note: "Visible, not averaged away", tone: "warn" }], panelTitle: "Observed group behavior", panelSubtitle: "Trust scores are evidence summaries, never permanent allowlists.", columns: ["Group", "Observations", "Useful facts", "Contradictions", "Confidence"], rows: [["GROUP", "184", "Reliable source/audio tags", "2", "High"], ["GROUP2", "73", "Compact WEB releases", "0", "High"], ["GROUP3", "18", "MULTi labeling", "5", "Low"], ["UNKNOWN", "3", "Insufficient evidence", "0", "Low"]] },
  "/intelligence/evidence": { icon: BookOpenCheck, metrics: [{ label: "Claims", value: "1,482", note: "Recent candidate set" }, { label: "Observed", value: "91%", note: "Direct source fields", tone: "good" }, { label: "Conflicting", value: "14", note: "Held for policy", tone: "warn" }], panelTitle: "Evidence claims", panelSubtitle: "Each normalized fact points back to the exact raw source value.", columns: ["Claim", "Value", "Source", "Confidence", "State"], rows: [["Resolution", "2160p", "Release title + DETAILS", "High", "Observed"], ["Dynamic range", "Dolby Vision", "Newznab attr #8", "High", "Observed"], ["Danish subtitles", "Full", "NFO + provider attr", "High", "Observed"], ["Release group", "GROUP", "Title suffix", "Medium", "Inferred"]] },
  "/sources/indexers": { icon: Globe2, metrics: [{ label: "Indexers", value: "3", note: "All independently scheduled" }, { label: "Ready", value: "3", note: "Auth and TLS healthy", tone: "good" }, { label: "Cooling down", value: "0", note: "No rate limits", tone: "good" }], panelTitle: "Indexer connections", panelSubtitle: "Connection health and operation support are intentionally separate.", columns: ["Indexer", "Connection", "Coverage", "Budget", "Latest outcome"], rows: [["Treasure Maps", "Ready", "96% mapped", "783 remaining", "Success"], ["Nordic Index", "Ready", "89% mapped", "Unknown · conservative", "No results"], ["Archive NZB", "Ready", "74% mapped", "2s interval", "Success"]], action: "Add indexer" },
  "/sources/download-clients": { icon: ServerCog, metrics: [{ label: "Clients", value: "1", note: "Usenet first" }, { label: "AltMount", value: "Ready", note: "Native queue + read-only mount", tone: "good" }, { label: "Fallback", value: "Off", note: "Never automatic", tone: "good" }], panelTitle: "Download clients", panelSubtitle: "Media Manager submits only to the explicitly selected capability-compatible client.", columns: ["Client", "Transport", "Capabilities", "Connection", "Default"], rows: [["AltMount", "Usenet streaming", "Queue · SSE · mounted output", "Ready", "Movies + TV"], ["SABnzbd", "Usenet", "Not configured", "—", "No"], ["qBittorrent", "BitTorrent", "Not configured", "—", "No"]], action: "Add client" },
  "/sources/import-lists": { icon: ListChecks, metrics: [{ label: "Lists", value: "2", note: "External watch intent" }, { label: "Last sync", value: "7m", note: "No errors", tone: "good" }, { label: "Added today", value: "4", note: "Monitoring only", tone: "info" }], panelTitle: "Import lists", panelSubtitle: "Lists create monitoring intent; they never become release evidence.", columns: ["List", "Source", "Items", "Profile", "Last sync"], rows: [["IMDb Watchlist", "IMDb", "36", "Type defaults", "7m ago"], ["Trakt UHD", "Trakt", "14", "UHD Reference", "31m ago"]], action: "Add list" },
  "/settings/profiles": { icon: Gauge, metrics: [{ label: "Movie profiles", value: "3", note: "One default" }, { label: "TV profiles", value: "2", note: "One default" }, { label: "Upgrades enabled", value: "4", note: "Per profile", tone: "info" }], panelTitle: "Quality profiles", panelSubtitle: "Hard requirements reject first; preferences score only eligible candidates.", columns: ["Profile", "Type", "Cutoff", "Preferred target", "Default"], rows: [["UHD Reference", "Movie", "2160p REMUX", "DV + Atmos + Danish", "Yes"], ["UHD Balanced", "Movie", "2160p WEB", "HDR + Danish", "No"], ["HD Compact", "Movie", "1080p WEB", "Efficient codec", "No"], ["TV UHD", "Series", "2160p", "Pack + Danish", "Yes"]], action: "New profile" },
  "/settings/metadata": { icon: Image, metrics: [{ label: "Providers", value: "2", note: "TMDB + TVDB" }, { label: "Identity conflicts", value: "0", note: "No unresolved mappings", tone: "good" }, { label: "Artwork locks", value: "23", note: "Owner choices retained", tone: "info" }], panelTitle: "Metadata providers", panelSubtitle: "Verified aliases describe catalog identity without becoming release evidence.", columns: ["Provider", "Media", "Priority", "Connection", "Last refresh"], rows: [["TMDB", "Movies + TV", "Primary movie", "Ready", "18m ago"], ["TVDB", "TV", "Episode order", "Ready", "22m ago"]], action: "Configure" },
  "/settings/media-management": { icon: FolderCog, metrics: [{ label: "Recycle", value: "30 days", note: "Verified operation time" }, { label: "Plex roots", value: "Read-only", note: "To Plex", tone: "good" }, { label: "AltMount", value: "Read-only", note: "To Media Manager", tone: "good" }], panelTitle: "Lifecycle policy", panelSubtitle: "Preview, containment, journaling, and verification wrap every filesystem change.", columns: ["Policy", "Default", "Safety boundary", "State"], rows: [["Delete", "Recycle", "Source retained", "Enabled"], ["Expired symlink recycle", "Auto purge", "Final containment check", "Enabled"], ["Real media purge", "Confirm", "New preview required", "Enabled"], ["Upgrade replacement", "Verify then delete old", "Plex confirmation", "Enabled"]] },
  "/settings/notifications": { icon: Bell, metrics: [{ label: "Destinations", value: "1", note: "Owner channel" }, { label: "Events enabled", value: "8", note: "Failures + completions" }, { label: "Delivery failures", value: "0", note: "Last 30 days", tone: "good" }], panelTitle: "Notification destinations", panelSubtitle: "Operational detail stays in Media Manager; notifications link to the exact event.", columns: ["Destination", "Events", "State", "Last delivery"], rows: [["Owner webhook", "Failure · attention · complete", "Ready", "42m ago"]], action: "Add destination" },
  "/system/tasks": { icon: Clock3, metrics: [{ label: "Tasks", value: "11", note: "Scheduled workers" }, { label: "Running", value: "1", note: "Seerr reconciliation", tone: "info" }, { label: "Failed", value: "0", note: "Last 24 hours", tone: "good" }], panelTitle: "Scheduled tasks", panelSubtitle: "Leased, resumable workers never burst after a restart.", columns: ["Task", "Schedule", "Latest", "Duration", "State"], rows: [["Seerr reconciliation", "Every 5m + jitter", "Now", "1.2s", "Running"], ["Indexer feeds", "Per learned budget", "2m ago", "3.8s", "Success"], ["Plex reconciliation", "Every 10m", "6m ago", "2.1s", "Success"], ["Recycle eligibility", "Daily", "8h ago", "0.4s", "Success"]] },
  "/system/backups": { icon: DatabaseBackup, metrics: [{ label: "Retained", value: "14", note: "Encrypted backups" }, { label: "Latest", value: "3h", note: "Database + config", tone: "good" }, { label: "Restore test", value: "7d", note: "Verified dev restore", tone: "good" }], panelTitle: "Backups", panelSubtitle: "A backup is healthy only after restore verification succeeds.", columns: ["Created", "Contents", "Size", "Verification", "Retention"], rows: [["Today · 21:00", "DB + config + audit", "84 MB", "Checksum passed", "14 days"], ["Yesterday · 21:00", "DB + config + audit", "83 MB", "Restore tested", "14 days"], ["Aug 27 · 21:00", "DB + config + audit", "82 MB", "Checksum passed", "14 days"]], action: "Back up now" },
  "/system/security": { icon: LockKeyhole, metrics: [{ label: "Owner accounts", value: "1", note: "No household roles" }, { label: "Secrets", value: "Encrypted", note: "Redacted from logs", tone: "good" }, { label: "Public edge", value: "Cosmos", note: "Outside app boundary", tone: "info" }], panelTitle: "Security boundaries", panelSubtitle: "Private service connectivity is explicit; hostile input never reaches files or requests unchecked.", columns: ["Boundary", "Protection", "Latest check", "State"], rows: [["Owner session", "Same-origin + secure cookie", "Now", "Ready"], ["Outbound HTTP", "Resolved-IP + redirect policy", "Today", "Ready"], ["AltMount source", "Read-only mount + containment", "Now", "Ready"], ["Plex roots", "Plex read-only", "Now", "Ready"]] },
  "/system/api": { icon: Code2, metrics: [{ label: "API keys", value: "2", note: "Machine identities" }, { label: "Requests today", value: "184", note: "All audited" }, { label: "Rate violations", value: "0", note: "No rejected bursts", tone: "good" }], panelTitle: "Scoped API keys", panelSubtitle: "Machine keys can never become owner browser sessions.", columns: ["Key", "Scopes", "Created", "Last used", "State"], rows: [["Seerr webhook", "requests:write", "Aug 20", "2m ago", "Active"], ["Support read-only", "health:read · logs:read", "Aug 24", "Yesterday", "Active"]], action: "Create key" },
  "/system/health": { icon: HeartPulse, metrics: [{ label: "Overall", value: "Healthy", note: "No global false outage", tone: "good" }, { label: "Components", value: "12/12", note: "Ready", tone: "good" }, { label: "Degraded operations", value: "1", note: "TVMaze search inconclusive", tone: "warn" }], panelTitle: "Component health", panelSubtitle: "A malformed result affects that result; a failed operation affects only that operation.", columns: ["Component", "Connection", "Operations", "Latency", "State"], rows: [["PostgreSQL", "Ready", "Read + write", "4ms", "Healthy"], ["AltMount", "Ready", "Queue + output", "22ms", "Healthy"], ["Plex", "Ready", "Scan + reconcile", "31ms", "Healthy"], ["Treasure Maps", "Ready", "One inconclusive mode", "184ms", "Healthy"]] },
  "/system/logs": { icon: TerminalSquare, metrics: [{ label: "Events today", value: "1,284", note: "Structured logs" }, { label: "Audit writes", value: "84", note: "Append-only" }, { label: "Secret leaks", value: "0", note: "Canary scan", tone: "good" }], panelTitle: "Recent logs", panelSubtitle: "Logs explain operations without exposing credentials or raw signed URLs.", columns: ["Time", "Level", "Component", "Event", "Correlation"], rows: [["00:44:12", "INFO", "plex", "appearance confirmed", "wf_2049"], ["00:43:58", "INFO", "library", "symlink verified", "wf_2049"], ["00:43:42", "WARN", "indexer", "one result malformed; item held", "search_71"], ["00:42:09", "INFO", "altmount", "queue snapshot reconciled", "job_1274"]] },
}

export function OperationalScreen({ path }: { path: string }) {
  if (path === "/import") return <ImportScreen />
  if (path === "/sources/indexer-lab") return <IndexerLabScreen />
  if (path === "/settings/storage") return <StorageScreen />
  if (path === "/settings/connections") return <ConnectionsScreen />
  const spec = specs[path]
  if (!spec) return <NotFoundScreen />
  return <SpecScreen spec={spec} path={path} />
}

function SpecScreen({ spec, path }: { spec: PageSpec; path: string }) {
  const Icon = spec.icon
  return (
    <div className="space-y-4">
      <div className="grid gap-3 sm:grid-cols-3">
        {spec.metrics.map((metric, index) => <MetricCard key={metric.label} {...metric} icon={index === 0 ? Icon : undefined} />)}
      </div>
      {spec.notice ? <InlineNotice {...spec.notice}>{spec.notice.copy}</InlineNotice> : null}
      <Panel title={spec.panelTitle} subtitle={spec.panelSubtitle} action={spec.action ? <ActionDialog label={spec.action} path={path} /> : undefined} className="!p-0">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader><TableRow>{spec.columns.map((column) => <TableHead key={column}>{column}</TableHead>)}</TableRow></TableHeader>
            <TableBody>{spec.rows.map((row, rowIndex) => <TableRow key={`${row[0]}-${rowIndex}`}>{row.map((cell, index) => <TableCell key={`${cell}-${index}`} className={index === 0 ? "font-semibold text-[#e0e5eb]" : undefined}>{index === row.length - 1 ? <StatusChip tone={toneFor(cell)}>{cell}</StatusChip> : cell}</TableCell>)}</TableRow>)}</TableBody>
          </Table>
        </div>
        <div className="border-t border-white/[0.06] px-4 py-3 text-[9.5px] text-[#778291]">Development records demonstrate the complete information model; connected deployments replace them with API data.</div>
      </Panel>
    </div>
  )
}

function toneFor(value: string): "good" | "info" | "warn" | "bad" | "neutral" {
  if (/ready|healthy|active|success|current|enabled|available/i.test(value)) return "good"
  if (/held|attention|missing|low|manual|waiting|inconclusive|dynamic/i.test(value)) return "warn"
  if (/failed|blocked|error|down/i.test(value)) return "bad"
  if (/monitor|running|verified|observed|high/i.test(value)) return "info"
  return "neutral"
}

function ActionDialog({ label }: { label: string; path: string }) {
  return (
    <Dialog>
      <DialogTrigger asChild><Button size="sm"><Plus /> {label}</Button></DialogTrigger>
      <DialogContent className="border-[#2d3541] bg-[#10141a] text-[#edf1f5]">
        <DialogHeader><DialogTitle>{label}</DialogTitle><DialogDescription className="text-[#8b96a5]">This production form will validate the complete object before saving.</DialogDescription></DialogHeader>
        <div className="grid gap-3"><label className="space-y-1.5 text-[10px] text-[#8b96a5]">Name<Input className="mm-input" placeholder="Required name" /></label><label className="space-y-1.5 text-[10px] text-[#8b96a5]">Enabled<div className="flex h-10 items-center justify-between rounded-lg border border-white/[0.08] bg-[#0b0f14] px-3"><span>Use after verification</span><Switch defaultChecked /></div></label></div>
        <DialogFooter><Button variant="outline">Cancel</Button><Button onClick={() => toast.success(`${label} saved in the development workflow`)}><Save /> Save</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ImportScreen() {
  const [scanned, setScanned] = React.useState(false)
  return (
    <div className="space-y-4">
      <div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Discovery" value={scanned ? "Complete" : "Not started"} note="Strictly read-only" tone={scanned ? "good" : "neutral"} icon={FolderInput} /><MetricCard label="Matched" value={scanned ? "238" : "—"} note="Confident identity" tone="good" /><MetricCard label="Needs review" value={scanned ? "7" : "—"} note="No guesses allowed" tone={scanned ? "warn" : "neutral"} /></div>
      <Panel title="Adopt an existing Plex library" subtitle="Discovery changes nothing. Approval executes a journaled, verified plan.">
        <div className="grid gap-4 lg:grid-cols-[1fr_300px]"><div className="rounded-xl border border-white/[0.07] bg-[#0c1015] p-4"><div className="text-[11px] font-bold text-[#e3e8ef]">Movies · Existing</div><div className="mt-2 font-mono text-[9.5px] text-[#8792a1]">/libraries/existing/movies</div><div className="mt-4 flex flex-wrap gap-2"><StatusChip tone="good">Plex reconciled</StatusChip><StatusChip tone="info">Read-only discovery</StatusChip><StatusChip tone="neutral">Monitoring default off</StatusChip></div><Button className="mt-4" disabled={scanned} onClick={() => { setScanned(true); toast.success("Read-only discovery scan completed") }}><ScanSearch /> {scanned ? "Scan complete" : "Scan and review"}</Button></div><div><KeyValue label="Rename during adoption" value="Yes" /><KeyValue label="Recognized companions" value="Preserve + rename" /><KeyValue label="Unknown companions" value="Preserve + flag" /><KeyValue label="Monitoring" value="Choose per batch · off" /><KeyValue label="Immediate search" value="Choose per batch · off" /></div></div>
      </Panel>
      {scanned ? <Panel title="Review plan" subtitle="Every source entry has an explicit result before approval." className="!p-0"><Table><TableHeader><TableRow><TableHead>Class</TableHead><TableHead>Items</TableHead><TableHead>Proposed action</TableHead><TableHead>State</TableHead></TableRow></TableHeader><TableBody><TableRow><TableCell className="font-semibold">Confident movies</TableCell><TableCell>238</TableCell><TableCell>Rename in place</TableCell><TableCell><StatusChip tone="good">Ready</StatusChip></TableCell></TableRow><TableRow><TableCell className="font-semibold">Ambiguous identities</TableCell><TableCell>3</TableCell><TableCell>Owner match required</TableCell><TableCell><StatusChip tone="warn">Review</StatusChip></TableCell></TableRow><TableRow><TableCell className="font-semibold">Unknown companions</TableCell><TableCell>4</TableCell><TableCell>Preserve and flag</TableCell><TableCell><StatusChip tone="warn">Review</StatusChip></TableCell></TableRow></TableBody></Table><div className="flex justify-end border-t border-white/[0.06] p-3"><Button disabled>Approve after 7 reviews</Button></div></Panel> : null}
    </div>
  )
}

function IndexerLabScreen() {
  const [running, setRunning] = React.useState(false)
  const [progress, setProgress] = React.useState(0)
  React.useEffect(() => {
    if (!running || progress >= 100) return
    const timer = window.setTimeout(() => setProgress((current) => Math.min(100, current + 8)), 260)
    return () => window.clearTimeout(timer)
  }, [running, progress])
  React.useEffect(() => { if (progress === 100) toast.info("Layout preview complete — no indexer requests were sent") }, [progress])
  const phase = progress < 18 ? "Connection and authentication" : progress < 38 ? "CAPS snapshot" : progress < 58 ? "Recent-release sampling" : progress < 72 ? "Search semantics" : progress < 86 ? "DETAILS and GETNFO" : "NZB retrieval verification"
  return (
    <div className="grid gap-4 xl:grid-cols-[1fr_340px]">
      <div className="space-y-4"><Panel title="Indexer Lab layout preview" subtitle="Development-only sequence preview. This screen never contacts an indexer or reports real test results."><div className="grid gap-3 sm:grid-cols-2"><label className="space-y-1.5 text-[10px] text-[#8792a1]">Example Newznab API URL<Input className="mm-input" readOnly defaultValue="https://indexer.example/api" /></label><label className="space-y-1.5 text-[10px] text-[#8792a1]">API key<Input className="mm-input" type="password" readOnly placeholder="Not collected in preview" autoComplete="off" /></label></div><div className="mt-4 flex flex-wrap gap-2"><Button disabled={running} onClick={() => { setRunning(true); setProgress(6) }}><Play /> Preview run layout</Button><Button variant="outline">Preview Prowlarr import</Button></div></Panel>{running ? <Panel title="Planned diagnostic sequence" subtitle={phase}><WorkProgress value={progress} label={`${progress}% · visual preview only`} /><div className="mt-4 grid gap-2 sm:grid-cols-2">{["HTTPS + TLS", "API authentication", "Newznab response", "CAPS advertisement", "Positive/negative search", "Pagination uniqueness", "Extended metadata", "DETAILS", "GETNFO", "NZB document"].map((check, index) => <div key={check} className="flex items-center gap-2 rounded-lg border border-white/[0.06] bg-[#0c1015] px-3 py-2 text-[9.5px] text-[#aab3bf]">{progress > index * 10 ? <CheckCircle2 className="size-3.5 text-[#8199f4]" /> : <Clock3 className="size-3.5 text-[#657183]" />}{check} <span className="ml-auto text-[8px] uppercase tracking-wider text-[#657183]">preview</span></div>)}</div></Panel> : null}</div>
      <Panel title="Safety budget" subtitle="The Lab never tries to force a rate limit." className="h-fit xl:sticky xl:top-24"><KeyValue label="Max in flight" value="1" /><KeyValue label="Minimum interval" value="3 seconds" /><KeyValue label="Maximum sample" value="5 pages" /><KeyValue label="Download probes" value="Explicit final phase" /><KeyValue label="Retry-After" value="Always honored" /><InlineNotice tone="info" title="Capability rule">HTTP 200, CAPS advertisement, or an empty result cannot by itself become “verified.”</InlineNotice></Panel>
    </div>
  )
}

function StorageScreen() {
  return (
    <div className="space-y-4">
      <div className="grid gap-3 sm:grid-cols-3"><MetricCard label="Movie roots" value="2" note="Primary + archive" icon={HardDrive} /><MetricCard label="TV roots" value="2" note="Primary + archive" /><MetricCard label="Path checks" value="Passed" note="AltMount → Plex visible" tone="good" /></div>
      <div className="grid gap-4 xl:grid-cols-2"><RootCard title="Movies · Primary" type="Movie default" path="/libraries/movies" usage={68} /><RootCard title="TV · Primary" type="TV default" path="/libraries/tv" usage={74} /></div>
      <Panel title="AltMount path mapping" subtitle="Reported, container-visible, and Plex-visible paths are explicit and containment-checked."><div className="overflow-hidden rounded-xl border border-white/[0.06]"><Table><TableHeader><TableRow><TableHead>AltMount reports</TableHead><TableHead>Media Manager sees</TableHead><TableHead>Plex sees</TableHead><TableHead>State</TableHead></TableRow></TableHeader><TableBody><TableRow><TableCell className="font-mono">/complete</TableCell><TableCell className="font-mono">/mnt/altmount</TableCell><TableCell className="font-mono">/mnt/altmount</TableCell><TableCell><StatusChip tone="good">Contained</StatusChip></TableCell></TableRow></TableBody></Table></div><Button variant="outline" className="mt-4" onClick={() => toast.success("Mapping created a temporary test link and Plex-visible target passed")}>Validate with test link</Button></Panel>
      <Panel title="Plex naming preview" subtitle="The rendered path is checked before a symlink is written."><div className="rounded-xl border border-white/[0.06] bg-[#090d12] p-3 font-mono text-[10px] leading-5 text-[#aeb8c5]">/libraries/movies/Blade Runner 2049 (2017) &#123;tmdb-335984&#125;/Blade Runner 2049 (2017) &#123;tmdb-335984&#125; - [2160p UHD BluRay REMUX HEVC DolbyVision TrueHD Atmos GROUP].mkv</div></Panel>
    </div>
  )
}

function RootCard({ title, type, path, usage }: { title: string; type: string; path: string; usage: number }) {
  return <Panel title={title} subtitle={type} action={<StatusChip tone="good">Writable by Media Manager</StatusChip>}><div className="font-mono text-[10px] text-[#9ca7b4]">{path}</div><div className="mt-4"><WorkProgress value={usage} label={`${usage}% used · 8.4 TB free`} /></div><div className="mt-4"><KeyValue label="Plex access" value="Read-only" /><KeyValue label="Per-item override" value="Allowed" /><KeyValue label="Naming template" value="Plex default" /></div></Panel>
}

function ConnectionsScreen() {
  return <div className="space-y-4"><div className="grid gap-4 xl:grid-cols-3"><ConnectionCard icon={CloudCog} title="Plex" copy="Scan requests and exact appearance confirmation." facts={[["Libraries", "Movies + TV"], ["Connection", "Ready"], ["Media roots", "Read-only"]]} /><ConnectionCard icon={Webhook} title="Seerr" copy="Approved requests, webhook hints, and five-minute reconciliation." facts={[["Webhook", "Verified"], ["Latest poll", "2m ago"], ["Request scope", "Approved only"]]} /><ConnectionCard icon={Cable} title="Owner webhook" copy="Selected operational notifications only." facts={[["Events", "8 enabled"], ["Latest", "Delivered"], ["Failures", "0"]]} /></div><InlineNotice tone="info" title="Public access stays outside Media Manager">Cosmos Cloud owns the public domain, HTTPS certificates, reverse proxy, and public access protection.</InlineNotice></div>
}

function ConnectionCard({ icon: Icon, title, copy, facts }: { icon: LucideIcon; title: string; copy: string; facts: string[][] }) {
  return <Panel title={title} subtitle={copy} action={<Icon className="size-5 text-[#94aaff]" />}>{facts.map(([label, value]) => <KeyValue key={label} label={label} value={value} valueClassName={/ready|verified|delivered/i.test(value) ? "text-[#69d3a3]" : undefined} />)}<Button variant="outline" className="mt-4 w-full" onClick={() => toast.success(`${title} connection test passed in the development fixture`)}>Test connection</Button></Panel>
}

function NotFoundScreen() {
  return <Panel title="Screen not found" subtitle="The requested route is not part of the locked product map."><Button asChild><Link href="/">Return to overview</Link></Button></Panel>
}
