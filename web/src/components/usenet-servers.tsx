import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ArrowDown, ArrowUp, CheckCircle2, XCircle } from "lucide-react"
import { useState, type ReactNode } from "react"
import { Link } from "react-router"

import {
  ApiError, createUsenetServer, deleteUsenetServer, loadUsenetServers, reorderUsenetServers, testUsenetServer, updateUsenetServer,
  type APIUsenetServer, type APIUsenetServerTest,
} from "@/api/client"
import { fieldClass } from "@/components/setup-forms"
import { useToast } from "@/components/toast"
import { Badge, Button, Card, CardHeader, Dialog, Empty, ErrorBox } from "@/components/ui"
import { formatNumber, pluralize } from "@/format"
import { errorMessage } from "@/i18n"
import {
  draftFromServer, draftToCreate, draftToPatch, isLastActive, moveServer, newUsenetServerDraft, splitServerAddress, usenetDraftProblem,
  type UsenetServerDraft,
} from "@/lib/usenet-servers"
import { ListSkeleton } from "@/pages/overview"

const queryKey = ["settings", "usenet-servers"]

/** AltMount's refusal carries its own reason; everything else is shown in Danish. */
function usenetError(error: unknown): string {
  if (error instanceof ApiError && error.code === "usenet_server_rejected") return `AltMount afviste ændringen: ${error.message}`
  return errorMessage(error)
}

function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <label className="block">
      {label}
      {children}
      {hint ? <span className="mt-1 block text-xs text-text-3">{hint}</span> : null}
    </label>
  )
}

function Check({ checked, onChange, children, disabled }: { checked: boolean; onChange: (value: boolean) => void; children: ReactNode; disabled?: boolean }) {
  return <label className="flex items-center gap-2 text-text"><input type="checkbox" checked={checked} disabled={disabled} onChange={(event) => onChange(event.target.checked)} className="size-4" />{children}</label>
}

/** Usenet (news) servers: the logins AltMount downloads with. They live in AltMount; Media Manager never keeps a password. */
export function UsenetServersSection() {
  const servers = useQuery({ queryKey, queryFn: ({ signal }) => loadUsenetServers(signal), retry: false })
  const [adding, setAdding] = useState(false)
  const intro = (
    <p className="text-sm text-text-2">
      Skriv login til dine news-servere (Usenet-udbydere) her. AltMount henter med dem. Adgangskoden sendes direkte til AltMount og gemmes ikke i Media Manager. Serverne prøves i rækkefølgen herunder; en backup-server bruges kun, når de andre mangler en del af en fil.
    </p>
  )
  if (servers.error) {
    if (servers.error instanceof ApiError && servers.error.code === "altmount_not_configured") {
      return (
        <div className="flex flex-col gap-6">
          {intro}
          <Card><Empty title="AltMount er ikke sat op endnu">Usenet-servere gemmes i AltMount. Tilføj AltMount under <Link className="underline" to="/indstillinger/forbindelser">Forbindelser</Link>, og kom tilbage hertil.</Empty></Card>
        </div>
      )
    }
    return <div className="flex flex-col gap-6">{intro}<ErrorBox error={new Error(usenetError(servers.error))} retry={() => servers.refetch()} /></div>
  }
  if (servers.isPending) return <Card><ListSkeleton /></Card>
  const list = servers.data.servers ?? []
  const active = list.filter((server) => server.enabled).length
  return (
    <div className="flex flex-col gap-6">
      {intro}
      <Card>
        <CardHeader
          title="Usenet-servere"
          description={list.length === 0 ? `Gemmes i ${servers.data.altmount.name}.` : `${pluralize(active, "aktiv server", "aktive servere")} · gemmes i ${servers.data.altmount.name}.`}
          action={<Button variant="primary" onClick={() => setAdding(true)}>Tilføj server</Button>}
        />
        {list.length === 0 ? (
          <Empty title="Ingen Usenet-server endnu">Uden en server kan intet hentes. Tilføj login fra din Usenet-udbyder.</Empty>
        ) : (
          <ol className="divide-y divide-line">
            {list.map((server, index) => <ServerRow key={server.id} server={server} servers={list} index={index} />)}
          </ol>
        )}
      </Card>
      {adding ? <ServerDialog onClose={() => setAdding(false)} /> : null}
    </div>
  )
}

function ServerRow({ server, servers, index }: { server: APIUsenetServer; servers: APIUsenetServer[]; index: number }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const refresh = () => queryClient.invalidateQueries({ queryKey })
  const lastActive = isLastActive(servers, server)
  const label = server.name || server.host
  const move = useMutation({
    mutationFn: (delta: -1 | 1) => reorderUsenetServers(moveServer(servers.map((entry) => entry.id), index, delta)),
    onSuccess: refresh,
    onError: (error) => toast("bad", usenetError(error)),
  })
  const toggle = useMutation({
    mutationFn: () => updateUsenetServer(server.id, { enabled: !server.enabled }),
    onSuccess: async () => {
      toast("good", server.enabled ? `${label} er slået fra.` : `${label} er slået til.`)
      await refresh()
    },
    onError: (error) => toast("bad", usenetError(error)),
  })
  const details = [
    `${server.host}:${server.port}`,
    server.username,
    pluralize(server.max_connections, "forbindelse", "forbindelser"),
    server.last_rtt_ms > 0 ? `svartid ${formatNumber(server.last_rtt_ms)} ms` : "",
  ].filter(Boolean).join(" · ")
  return (
    <li className="px-5 py-3">
      <div className="flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-center">
        <div className="min-w-0 sm:flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs tabular-nums text-text-3">{index + 1}.</span>
            <span className="font-medium break-all">{label}</span>
            {!server.enabled ? <Badge>Slået fra</Badge> : null}
            {server.is_backup_provider ? <Badge tone="info">Backup</Badge> : null}
            {server.tls ? <Badge tone="good">Krypteret</Badge> : <Badge tone="warn">Ikke krypteret</Badge>}
            {server.password_set ? <Badge tone="good">Adgangskode gemt</Badge> : <Badge tone="warn">Mangler adgangskode</Badge>}
          </div>
          <div className="mt-0.5 break-all text-xs text-text-3">{details}</div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="ghost" className="h-8 px-2" aria-label={`Flyt ${label} op`} disabled={index === 0 || move.isPending} onClick={() => move.mutate(-1)}><ArrowUp aria-hidden className="size-4 shrink-0" /></Button>
          <Button variant="ghost" className="h-8 px-2" aria-label={`Flyt ${label} ned`} disabled={index === servers.length - 1 || move.isPending} onClick={() => move.mutate(1)}><ArrowDown aria-hidden className="size-4 shrink-0" /></Button>
          <Button
            variant="ghost"
            className="h-8 text-xs"
            busy={toggle.isPending}
            disabled={lastActive}
            title={lastActive ? "Den eneste aktive server kan ikke slås fra." : undefined}
            onClick={() => toggle.mutate()}
          >
            {server.enabled ? "Slå fra" : "Slå til"}
          </Button>
          <Button variant="ghost" className="h-8 text-xs" onClick={() => setEditing(true)}>Rediger</Button>
          <Button variant="ghost" className="h-8 text-xs" aria-label={`Slet ${label}`} disabled={lastActive} title={lastActive ? "Den eneste aktive server kan ikke slettes." : undefined} onClick={() => setDeleting(true)}>Slet</Button>
        </div>
      </div>
      {lastActive && servers.length === 1 ? <p className="mt-1 text-xs text-text-3">Det er din eneste server. Tilføj en ny, før du sletter eller slår denne fra.</p> : null}
      {editing ? <ServerDialog server={server} onClose={() => setEditing(false)} /> : null}
      {deleting ? <DeleteServerDialog server={server} onClose={() => setDeleting(false)} /> : null}
    </li>
  )
}

function DeleteServerDialog({ server, onClose }: { server: APIUsenetServer; onClose: () => void }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const label = server.name || server.host
  const mutation = useMutation({
    mutationFn: () => deleteUsenetServer(server.id),
    onSuccess: async () => {
      toast("good", `${label} er slettet.`)
      onClose()
      await queryClient.invalidateQueries({ queryKey })
    },
  })
  return (
    <Dialog open onClose={() => { if (!mutation.isPending) onClose() }} title={`Slet ${label}?`} footer={<><Button variant="ghost" disabled={mutation.isPending} onClick={onClose}>Fortryd</Button><Button variant="danger" busy={mutation.isPending} onClick={() => mutation.mutate()}>Slet server</Button></>}>
      <div className="space-y-3">
        <p>Serveren og dens login fjernes fra AltMount. Vil du bruge den igen, skal du skrive login igen. Vil du bare holde pause, kan du slå den fra i stedet.</p>
        {mutation.error ? <p role="alert" className="text-sm text-bad">{usenetError(mutation.error)}</p> : null}
      </div>
    </Dialog>
  )
}

function TestResult({ result }: { result: APIUsenetServerTest }) {
  return (
    <div role="status" className={`flex items-start gap-2 text-sm ${result.ok ? "text-good" : "text-bad"}`}>
      {result.ok ? <CheckCircle2 aria-hidden className="mt-0.5 size-4 shrink-0" /> : <XCircle aria-hidden className="mt-0.5 size-4 shrink-0" />}
      <span>
        {result.ok
          ? `Login virker${result.rtt_ms ? ` (svartid ${formatNumber(result.rtt_ms)} ms)` : ""}.`
          : `Login virker ikke. Tjek adresse, port, kryptering, brugernavn og adgangskode.${result.detail ? ` Serverens svar: ${result.detail}` : ""}`}
      </span>
    </div>
  )
}

/** Adds a server, or changes one; "Test login" has AltMount try the typed login without saving it. */
function ServerDialog({ server, onClose }: { server?: APIUsenetServer; onClose: () => void }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const creating = !server
  const original = server ? draftFromServer(server) : newUsenetServerDraft
  const [draft, setDraft] = useState<UsenetServerDraft>(original)
  const [touched, setTouched] = useState(false)
  const problem = usenetDraftProblem(draft, creating)
  const set = (change: Partial<UsenetServerDraft>) => {
    setDraft((current) => ({ ...current, ...change }))
    test.reset()
  }
  const test = useMutation({
    mutationFn: () => testUsenetServer({ host: draft.host.trim(), port: Number(draft.port), username: draft.username.trim(), password: draft.password, tls: draft.tls, insecure_tls: draft.tls && draft.insecureTLS }),
  })
  const save = useMutation({
    mutationFn: async () => {
      if (creating) return createUsenetServer(draftToCreate(draft))
      const patch = draftToPatch(original, draft)
      if (Object.keys(patch).length === 0) return null
      return updateUsenetServer(server.id, patch)
    },
    onSuccess: async () => {
      toast("good", creating ? `${draft.name.trim() || draft.host.trim()} er tilføjet.` : `${draft.name.trim() || draft.host.trim()} er gemt.`)
      onClose()
      await queryClient.invalidateQueries({ queryKey })
    },
  })
  const onAddress = (value: string) => {
    const split = splitServerAddress(value)
    set({ host: split.port || split.tls !== undefined ? split.host : value, ...(split.port ? { port: split.port } : {}), ...(split.tls !== undefined ? { tls: split.tls } : {}) })
  }
  const onTLS = (tls: boolean) => set({ tls, port: tls && draft.port === "119" ? "563" : !tls && draft.port === "563" ? "119" : draft.port })
  const canTest = !usenetDraftProblem({ ...draft, password: draft.password || "x" }, false) && draft.password !== ""
  return (
    <Dialog
      open
      onClose={() => { if (!save.isPending) onClose() }}
      title={creating ? "Tilføj Usenet-server" : `Rediger ${server.name || server.host}`}
      footer={<>
        <Button variant="ghost" disabled={save.isPending} onClick={onClose}>Fortryd</Button>
        <Button busy={test.isPending} disabled={!canTest || save.isPending} title={canTest ? undefined : "Udfyld adresse, port, brugernavn og adgangskode for at teste."} onClick={() => test.mutate()}>Test login</Button>
        <Button variant="primary" busy={save.isPending} onClick={() => { setTouched(true); if (!problem) save.mutate() }}>Gem</Button>
      </>}
    >
      <div className="space-y-3">
        <Field label="Adresse" hint="Står i mailen fra din Usenet-udbyder, fx news.eksempel.dk.">
          <input autoComplete="off" spellCheck={false} placeholder="news.eksempel.dk" value={draft.host} onChange={(event) => onAddress(event.target.value)} className={`${fieldClass} font-mono`} />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field label="Port" hint="Oftest 563 med kryptering.">
            <input inputMode="numeric" value={draft.port} onChange={(event) => set({ port: event.target.value.replace(/\D/g, "") })} className={fieldClass} />
          </Field>
          <Field label="Forbindelser" hint="Højst det, din aftale tillader.">
            <input inputMode="numeric" value={draft.connections} onChange={(event) => set({ connections: event.target.value.replace(/\D/g, "") })} className={fieldClass} />
          </Field>
        </div>
        <Check checked={draft.tls} onChange={onTLS}>Krypteret forbindelse (SSL/TLS) – anbefalet</Check>
        <Field label="Brugernavn">
          <input autoComplete="off" spellCheck={false} value={draft.username} onChange={(event) => set({ username: event.target.value })} className={`${fieldClass} font-mono`} />
        </Field>
        <Field label="Adgangskode" hint={creating ? "Sendes kun til AltMount og vises aldrig igen." : server.password_set ? "Lad feltet stå tomt for at beholde den gemte adgangskode. Skriv den for at teste login." : "AltMount har ingen adgangskode for serveren. Skriv den."}>
          <input type="password" autoComplete="new-password" value={draft.password} onChange={(event) => set({ password: event.target.value })} className={`${fieldClass} font-mono`} />
        </Field>
        <Field label="Navn (valgfrit)" hint="Kun til at kende serveren igen, fx udbyderens navn.">
          <input autoComplete="off" value={draft.name} onChange={(event) => set({ name: event.target.value })} className={fieldClass} />
        </Field>
        <Check checked={draft.backup} onChange={(backup) => set({ backup })}>Backup-server: bruges kun, når de andre mangler en del af en fil</Check>
        <details className="rounded-lg border border-line px-3 py-2">
          <summary className="cursor-pointer text-sm text-text-2">Avanceret</summary>
          <div className="mt-3 space-y-3">
            <div className="grid grid-cols-2 gap-3">
              <Field label="Kvote (GB)" hint="Tomt = ingen kvote.">
                <input inputMode="decimal" value={draft.quotaGB} onChange={(event) => set({ quotaGB: event.target.value })} className={fieldClass} />
              </Field>
              <Field label="Kvoteperiode (timer)" hint="Fx 720 for en måned.">
                <input inputMode="numeric" value={draft.quotaHours} onChange={(event) => set({ quotaHours: event.target.value.replace(/\D/g, "") })} className={fieldClass} />
              </Field>
            </div>
            <Field label="Abonnementet udløber" hint="Valgfrit, så du kan se det her.">
              <input type="date" value={draft.expires} onChange={(event) => set({ expires: event.target.value })} className={fieldClass} />
            </Field>
            <Check checked={draft.tls && draft.insecureTLS} disabled={!draft.tls} onChange={(insecureTLS) => set({ insecureTLS })}>Spring certifikatkontrol over (kun hvis udbyderen beder om det)</Check>
          </div>
        </details>
        {test.data ? <TestResult result={test.data} /> : null}
        {test.error ? <p role="alert" className="text-sm text-bad">{usenetError(test.error)}</p> : null}
        {touched && problem ? <p role="alert" className="text-sm text-bad">{problem}</p> : null}
        {save.error ? <p role="alert" className="text-sm text-bad">{usenetError(save.error)}</p> : null}
      </div>
    </Dialog>
  )
}
