import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { CheckCircle2, XCircle } from "lucide-react"
import { useState, type ReactNode } from "react"

import {
  checkIntegration, commissionIntegration, configureSeerrWebhook, createIntegration, createLibraryRoot, createNotificationDestination, createQualityProfile,
  loadIntegrations, loadLibraryRoots, loadPlexLibraries, loadQualityProfilePresets, loadQualityProfiles, loadSeerrWebhook, loadSetupStatus,
  saveIntegrationSecret, updateIntegrationConnection,
  type APIConnectionCheck, type APIConnectionPatch, type APIIntegration, type APIQualityProfile,
} from "@/api/client"
import { useToast } from "@/components/toast"
import { Badge, Button, Card, CardHeader, Dialog, Empty, ErrorBox } from "@/components/ui"
import { languageName } from "@/i18n"
import { checkText, suggestDestination, suggestSourceRoot } from "@/lib/setup"
import { ListSkeleton } from "@/pages/overview"

export const fieldClass = "mt-1 h-9 w-full rounded-lg border border-line bg-surface px-3 text-text"

export function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
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

/** The outcome of a connection test, exactly as the server reported it. */
export function CheckResult({ result }: { result: APIConnectionCheck }) {
  return (
    <div role="status" className={`mt-2 flex items-start gap-2 text-sm ${result.ok ? "text-good" : "text-bad"}`}>
      {result.ok ? <CheckCircle2 aria-hidden className="mt-0.5 size-4 shrink-0" /> : <XCircle aria-hidden className="mt-0.5 size-4 shrink-0" />}
      <span>{result.ok ? "Forbindelsen virker. " : "Forbindelsen virker ikke. "}{checkText(result.detail)}</span>
    </div>
  )
}

type ConnectionKind = "plex" | "altmount" | "tmdb" | "seerr"

const connectionKinds: Array<{ kind: ConnectionKind; title: string; description: string; placeholder: string }> = [
  { kind: "plex", title: "Plex", description: "Plex viser dine film og serier. Media Manager beder Plex om at opdatere, når en fil er klar.", placeholder: "http://plex:32400" },
  { kind: "altmount", title: "AltMount", description: "AltMount henter udgivelserne fra Usenet og gør filerne synlige for Plex.", placeholder: "http://altmount:8080" },
  { kind: "tmdb", title: "TMDB", description: "Søgning efter film og serier, plakater og beskrivelser. Nødvendig, når du ikke bruger Seerr; gratis at oprette.", placeholder: "" },
  { kind: "seerr", title: "Seerr", description: "Valgfrit. Media Manager tager imod anmodninger fra Seerr.", placeholder: "http://seerr:5055" },
]

type SecretValues = { token: string; username: string; password: string }
const emptySecret: SecretValues = { token: "", username: "", password: "" }

function needsSecret(kind: string, authentication: string | undefined): boolean {
  return !(kind === "altmount" && authentication === "anonymous_admin")
}

/** The value the server stores for a connection: a token, or AltMount's login as JSON. */
function secretValue(kind: string, values: SecretValues): string {
  if (kind === "altmount") return values.username || values.password ? JSON.stringify({ username: values.username, password: values.password }) : ""
  return values.token.trim()
}

function SecretFields({ kind, values, onChange }: { kind: string; values: SecretValues; onChange: (values: SecretValues) => void }) {
  const secretField = `${fieldClass} font-mono`
  if (kind === "altmount") {
    return (
      <>
        <Field label="AltMount-brugernavn"><input autoComplete="off" value={values.username} onChange={(event) => onChange({ ...values, username: event.target.value })} className={secretField} /></Field>
        <Field label="AltMount-adgangskode"><input type="password" autoComplete="new-password" value={values.password} onChange={(event) => onChange({ ...values, password: event.target.value })} className={secretField} /></Field>
      </>
    )
  }
  const label = kind === "plex" ? "Plex-token" : kind === "tmdb" ? "TMDB API Read Access Token" : "API-nøgle"
  const hint = kind === "plex"
    ? "Find den i Plex Web: åbn en film, vælg ⋯ → Hent info → Vis XML. Token står efter X-Plex-Token= i adressen."
    : kind === "tmdb"
      ? <>Opret en gratis konto på <a className="underline" href="https://www.themoviedb.org/settings/api" target="_blank" rel="noreferrer">themoviedb.org</a>, gå til Indstillinger → API, og kopiér den lange &quot;API Read Access Token&quot; (ikke den korte API-nøgle).</>
      : kind === "seerr" ? "Find den i Seerr under Indstillinger → Generelt → API-nøgle." : "Find den på din konto hos indexeren, ofte under Profil eller API."
  return <Field label={label} hint={hint}><input type="password" autoComplete="new-password" value={values.token} onChange={(event) => onChange({ ...values, token: event.target.value })} className={secretField} /></Field>
}

function useRefreshSetup() {
  const queryClient = useQueryClient()
  return async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["settings"] }),
      queryClient.invalidateQueries({ queryKey: ["setup"] }),
      queryClient.invalidateQueries({ queryKey: ["indexers"] }),
    ])
  }
}

/** Connections to Plex, AltMount, TMDB and Seerr: add, edit, save a key and test. */
export function ConnectionsSection() {
  const integrations = useQuery({ queryKey: ["settings", "integrations"], queryFn: ({ signal }) => loadIntegrations(signal) })
  const setup = useQuery({ queryKey: ["setup", "status"], queryFn: ({ signal }) => loadSetupStatus(signal) })
  const [adding, setAdding] = useState<ConnectionKind | null>(null)
  if (integrations.error) return <ErrorBox error={integrations.error} retry={() => integrations.refetch()} />
  if (integrations.isPending) return <Card><ListSkeleton /></Card>
  const secretStorage = setup.data?.secret_storage_enabled ?? true
  return (
    <div className="flex flex-col gap-6">
      <p className="text-sm text-text-2">Nøgler og adgangskoder gemmes krypteret og vises aldrig igen. Brug &quot;Test forbindelse&quot; for at se, om Media Manager kan nå tjenesten.</p>
      {!secretStorage ? (
        <div role="alert" className="rounded-xl border border-warn/30 bg-warn-soft p-4 text-sm text-warn">
          Nøgler kan ikke gemmes fra hjemmesiden, fordi serveren mangler en krypteringsnøgle (SECRET_ENCRYPTION_KEY). Kør opsætningsscriptet igen, eller sæt den i serverens opsætning. Installeret fra Cosmos-markedet før november 2026? Sæt SECRET_ENCRYPTION_KEY på API-containeren til resultatet af <code>openssl rand -hex 32</code>, og gem værdien et sikkert sted.
        </div>
      ) : null}
      {connectionKinds.map((info) => {
        const items = integrations.data.filter((item) => item.kind === info.kind)
        return (
          <Card key={info.kind}>
            <CardHeader title={info.title} description={info.description} action={<Button onClick={() => setAdding(info.kind)}>Tilføj {info.title}</Button>} />
            {items.length === 0 ? <Empty title={`Ingen ${info.title}-forbindelse endnu`} /> : (
              <ul className="divide-y divide-line">
                {items.map((integration) => <ConnectionRow key={integration.id} integration={integration} secretStorage={secretStorage} seerrRunning={setup.data?.seerr_intake_enabled ?? false} />)}
              </ul>
            )}
          </Card>
        )
      })}
      {adding ? <AddConnectionDialog kind={adding} first={!integrations.data.some((item) => item.kind === adding && item.is_default && item.enabled)} secretStorage={secretStorage} onClose={() => setAdding(null)} /> : null}
    </div>
  )
}

function ConnectionRow({ integration, secretStorage, seerrRunning }: { integration: APIIntegration; secretStorage: boolean; seerrRunning: boolean }) {
  const [editing, setEditing] = useState(false)
  const [keying, setKeying] = useState(false)
  const [webhook, setWebhook] = useState(false)
  const check = useMutation({ mutationFn: () => checkIntegration(integration.id) })
  const config = integration.config as { base_url?: string; authentication?: string }
  const wantsSecret = needsSecret(integration.kind, config.authentication)
  return (
    <li className="px-5 py-3">
      <div className="flex flex-wrap items-center gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{integration.name}</span>
            {integration.is_default ? <Badge tone="info">Standard</Badge> : null}
            {!integration.enabled ? <Badge>Slået fra</Badge> : null}
            {wantsSecret ? (integration.secret_ref ? <Badge tone="good">{integration.secret_ref.startsWith("sealed:") ? "Nøgle gemt krypteret" : "Nøgle fra serverens opsætning"}</Badge> : <Badge tone="warn">Mangler nøgle</Badge>) : <Badge>Login uden adgangskode</Badge>}
            {integration.kind === "seerr" && integration.is_default && integration.enabled && !seerrRunning ? <Badge tone="warn">Starter ved næste genstart</Badge> : null}
          </div>
          <div className="mt-0.5 break-all text-xs text-text-3">{config.base_url}</div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button className="h-8 text-xs" busy={check.isPending} onClick={() => check.mutate()}>Test forbindelse</Button>
          {wantsSecret ? <Button variant="ghost" className="h-8 text-xs" disabled={!secretStorage} onClick={() => setKeying(true)}>{integration.secret_ref ? "Udskift nøgle" : "Gem nøgle"}</Button> : null}
          {integration.kind === "seerr" ? <Button variant="ghost" className="h-8 text-xs" onClick={() => setWebhook(true)}>Webhook</Button> : null}
          <Button variant="ghost" className="h-8 text-xs" onClick={() => setEditing(true)}>Rediger</Button>
        </div>
      </div>
      {check.data ? <CheckResult result={check.data} /> : null}
      {check.error ? <CheckResult result={{ ok: false, detail: errorText(check.error) }} /> : null}
      {editing ? <EditConnectionDialog integration={integration} onClose={() => setEditing(false)} /> : null}
      {keying ? <SecretDialog integration={integration} onClose={() => setKeying(false)} /> : null}
      {webhook ? <SeerrWebhookDialog integration={integration} onClose={() => setWebhook(false)} /> : null}
    </li>
  )
}

/** Saves a key encrypted, then tests the connection with it (indexers are tested by approving them). */
export function SecretDialog({ integration, onClose }: { integration: Pick<APIIntegration, "id" | "name" | "kind" | "secret_ref">; onClose: () => void }) {
  const toast = useToast()
  const refresh = useRefreshSetup()
  const [values, setValues] = useState(emptySecret)
  const mutation = useMutation({
    mutationFn: async () => {
      await saveIntegrationSecret(integration.id, secretValue(integration.kind, values))
      return integration.kind === "newznab" || integration.kind === "oldboys" ? null : checkIntegration(integration.id)
    },
    onSuccess: async () => { toast("good", "Nøglen er gemt krypteret."); setValues(emptySecret); await refresh() },
    onError: (error) => toast("bad", errorText(error)),
  })
  const value = secretValue(integration.kind, values)
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Nøgle til ${integration.name}`}
      footer={mutation.isSuccess
        ? <Button variant="primary" onClick={onClose}>Luk</Button>
        : <><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} disabled={!value} onClick={() => mutation.mutate()}>Gem krypteret</Button></>}
    >
      <div className="space-y-3">
        {mutation.isSuccess ? (
          <>
            <p>Nøglen er gemt krypteret. Den gamle nøgle bruges ikke længere.</p>
            {mutation.data ? <CheckResult result={mutation.data} /> : <p className="text-text-3">Godkend indexeren for at teste den nye nøgle.</p>}
          </>
        ) : (
          <>
            <SecretFields kind={integration.kind} values={values} onChange={setValues} />
            {integration.secret_ref ? <p className="text-xs text-text-3">Den nuværende nøgle bliver erstattet.</p> : null}
          </>
        )}
      </div>
    </Dialog>
  )
}

/** Points Seerr's webhook at Media Manager; Seerr saves it only after its test reached this API. */
function SeerrWebhookDialog({ integration, onClose }: { integration: APIIntegration; onClose: () => void }) {
  const toast = useToast()
  const status = useQuery({ queryKey: ["setup", "seerr-webhook", integration.id], queryFn: ({ signal }) => loadSeerrWebhook(integration.id, signal), retry: false })
  const [address, setAddress] = useState<string | null>(null)
  const mutation = useMutation({
    mutationFn: () => configureSeerrWebhook(integration.id, (address ?? status.data?.suggested_url ?? "").trim()),
    onSuccess: async () => { toast("good", "Seerr sender nu sine anmodninger til Media Manager."); await status.refetch() },
    onError: (error) => toast("bad", checkText(errorText(error))),
  })
  const data = mutation.data ?? status.data
  const managed = data?.webhook?.managed ?? false
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Webhook i ${integration.name}`}
      footer={data?.intake_running && !managed
        ? <><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} onClick={() => mutation.mutate()}>Sæt op i Seerr</Button></>
        : <Button variant="primary" onClick={onClose}>Luk</Button>}
    >
      <div className="space-y-3">
        {status.isPending ? <p>Spørger Seerr…</p> : null}
        {status.error ? <p role="alert" className="text-bad">{checkText(errorText(status.error))}</p> : null}
        {data && !data.intake_running ? (
          <p>Media Manager tager ikke imod anmodninger fra denne Seerr-forbindelse endnu. Genstart Media Manager-API&apos;en én gang (i Cosmos: genstart API-containeren), og åbn så dette vindue igen.</p>
        ) : null}
        {data?.detail ? <p className="text-bad">{checkText(data.detail)}</p> : null}
        {data?.intake_running && managed ? (
          <div role="status" className="flex items-start gap-2 text-good"><CheckCircle2 aria-hidden className="mt-0.5 size-4 shrink-0" /><span>Seerr sender sine anmodninger til Media Manager på <span className="break-all">{data.webhook?.webhook_url}</span>.</span></div>
        ) : null}
        {data?.intake_running && !managed ? (
          <>
            <p>Seerr skal sende nye anmodninger til Media Manager. Media Manager sætter webhooken op i Seerr for dig; Seerr sender først en test, og intet ændres i Seerr, hvis testen ikke når frem.</p>
            <Field label="Adressen, Seerr når Media Manager på" hint="Typisk Media Manager-API'ens containernavn på det fælles Docker-netværk, port 8080.">
              <input inputMode="url" value={address ?? data.suggested_url} onChange={(event) => setAddress(event.target.value)} className={fieldClass} />
            </Field>
            {mutation.error ? <p role="alert" className="text-sm text-bad">{checkText(errorText(mutation.error))}</p> : null}
          </>
        ) : null}
      </div>
    </Dialog>
  )
}

type ConnectionDraft = { name: string; baseURL: string; isDefault: boolean; enabled: boolean; authentication: "session" | "anonymous_admin"; category: string; localRoot: string }

function ConnectionFields({ kind, draft, onChange, editing }: { kind: string; draft: ConnectionDraft; onChange: (draft: ConnectionDraft) => void; editing: boolean }) {
  const info = connectionKinds.find((entry) => entry.kind === kind)
  return (
    <>
      <Field label="Navn"><input required value={draft.name} onChange={(event) => onChange({ ...draft, name: event.target.value })} className={fieldClass} /></Field>
      {kind !== "tmdb" ? (
        <Field label="Adresse" hint="Brug https://, eller http:// når tjenesten kører på dit eget netværk eller i samme Docker-netværk.">
          <input required inputMode="url" placeholder={info?.placeholder} value={draft.baseURL} onChange={(event) => onChange({ ...draft, baseURL: event.target.value })} className={fieldClass} />
        </Field>
      ) : null}
      {kind === "altmount" ? (
        <>
          <Field label="Login">
            <select value={draft.authentication} onChange={(event) => onChange({ ...draft, authentication: event.target.value as ConnectionDraft["authentication"] })} className={fieldClass}>
              <option value="session">Brugernavn og adgangskode</option>
              <option value="anonymous_admin">Uden adgangskode (AltMount har login slået fra)</option>
            </select>
          </Field>
          <Field label="Mappe med AltMount-filer" hint="Hvor Media Manager selv kan se AltMount's filer (skrivebeskyttet).">
            <input required value={draft.localRoot} onChange={(event) => onChange({ ...draft, localRoot: event.target.value })} className={fieldClass} />
          </Field>
          <Field label="Kategori i AltMount" hint="Ét ord. Media Manager lægger sine hentninger i denne kategori.">
            <input required value={draft.category} onChange={(event) => onChange({ ...draft, category: event.target.value })} className={fieldClass} />
          </Field>
        </>
      ) : null}
      {editing ? <Check checked={draft.enabled} onChange={(enabled) => onChange({ ...draft, enabled, isDefault: enabled && draft.isDefault })}>Slået til</Check> : null}
      <Check checked={draft.isDefault} disabled={!draft.enabled} onChange={(isDefault) => onChange({ ...draft, isDefault })}>Brug som standard</Check>
    </>
  )
}

function AddConnectionDialog({ kind, first, secretStorage, onClose }: { kind: ConnectionKind; first: boolean; secretStorage: boolean; onClose: () => void }) {
  const toast = useToast()
  const refresh = useRefreshSetup()
  const info = connectionKinds.find((entry) => entry.kind === kind)!
  const [draft, setDraft] = useState<ConnectionDraft>({ name: info.title, baseURL: info.placeholder, isDefault: first, enabled: true, authentication: "session", category: "media-manager", localRoot: "/mnt/altmount" })
  const [secret, setSecret] = useState(emptySecret)
  const wantsSecret = needsSecret(kind, draft.authentication)
  const mutation = useMutation({
    mutationFn: async () => {
      const baseURL = draft.baseURL.trim()
      const allowHTTP = baseURL.toLowerCase().startsWith("http://")
      const config: Record<string, unknown> = kind === "altmount"
        ? { base_url: baseURL, allow_http: allowHTTP, category: draft.category.trim(), reported_root: "/", authentication: draft.authentication, request_timeout_ms: 30000, local_read_only_root: draft.localRoot.trim() }
        : kind === "tmdb" ? {} : { base_url: baseURL, allow_http: allowHTTP, request_timeout_ms: 20000 }
      const created = await createIntegration({ kind, name: draft.name.trim(), enabled: true, is_default: draft.isDefault, config })
      if (wantsSecret) {
        try {
          await saveIntegrationSecret(created.id, secretValue(kind, secret))
        } catch (error) {
          throw new Error(`${info.title} er tilføjet, men nøglen blev ikke gemt: ${errorText(error)}. Brug "Gem nøgle" for at prøve igen.`)
        }
      }
      return checkIntegration(created.id)
    },
    onSuccess: async () => { toast("good", `${info.title} er tilføjet.`); await refresh() },
    onError: async (error) => { toast("bad", errorText(error)); await refresh() },
  })
  const ready = draft.name.trim() && (kind === "tmdb" || draft.baseURL.trim()) && (!wantsSecret || (secretStorage && secretValue(kind, secret)))
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Tilføj ${info.title}`}
      footer={mutation.isSuccess
        ? <Button variant="primary" onClick={onClose}>Luk</Button>
        : <><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} disabled={!ready} onClick={() => mutation.mutate()}>Tilføj og test</Button></>}
    >
      {mutation.isSuccess ? (
        <div className="space-y-2">
          <p>{info.title} er tilføjet.</p>
          <CheckResult result={mutation.data} />
          {!mutation.data.ok ? <p className="text-xs text-text-3">Ret forbindelsen med &quot;Rediger&quot; eller &quot;Udskift nøgle&quot;, og test igen.</p> : null}
          {kind === "seerr" ? <p className="text-xs text-text-3">Media Manager begynder at tage imod anmodninger fra Seerr, når API&apos;en er genstartet. Sæt derefter webhooken op med knappen &quot;Webhook&quot;.</p> : null}
        </div>
      ) : (
        <div className="space-y-3">
          <ConnectionFields kind={kind} draft={draft} onChange={setDraft} editing={false} />
          {wantsSecret ? <SecretFields kind={kind} values={secret} onChange={setSecret} /> : null}
          {wantsSecret && !secretStorage ? <p className="text-xs text-bad">Serveren kan ikke gemme nøgler endnu (SECRET_ENCRYPTION_KEY mangler).</p> : null}
          {mutation.error ? <p role="alert" className="text-sm text-bad">{errorText(mutation.error)}</p> : null}
        </div>
      )}
    </Dialog>
  )
}

function EditConnectionDialog({ integration, onClose }: { integration: APIIntegration; onClose: () => void }) {
  const toast = useToast()
  const refresh = useRefreshSetup()
  const config = integration.config as { base_url?: string; authentication?: string; category?: string; local_read_only_root?: string }
  const original: ConnectionDraft = {
    name: integration.name, baseURL: config.base_url ?? "", isDefault: integration.is_default, enabled: integration.enabled,
    authentication: config.authentication === "anonymous_admin" ? "anonymous_admin" : "session", category: config.category ?? "", localRoot: config.local_read_only_root ?? "",
  }
  const [draft, setDraft] = useState(original)
  const mutation = useMutation({
    mutationFn: async () => {
      const patch: APIConnectionPatch = {}
      if (draft.name.trim() !== original.name) patch.name = draft.name.trim()
      if (draft.baseURL.trim() !== original.baseURL) {
        patch.base_url = draft.baseURL.trim()
        patch.allow_http = patch.base_url.toLowerCase().startsWith("http://")
      }
      if (draft.enabled !== original.enabled) patch.enabled = draft.enabled
      if (draft.isDefault !== original.isDefault) patch.is_default = draft.isDefault
      if (integration.kind === "altmount") {
        if (draft.authentication !== original.authentication) patch.authentication = draft.authentication
        if (draft.category.trim() !== original.category) patch.category = draft.category.trim()
        if (draft.localRoot.trim() !== original.localRoot) patch.local_read_only_root = draft.localRoot.trim()
      }
      if (Object.keys(patch).length === 0) return null
      await updateIntegrationConnection(integration.id, patch)
      return draft.enabled ? checkIntegration(integration.id) : null
    },
    onSuccess: async (result) => {
      await refresh()
      if (result !== null) return
      toast("good", `${draft.name.trim()} er gemt.`)
      onClose()
    },
    onError: (error) => toast("bad", errorText(error)),
  })
  return (
    <Dialog
      open
      onClose={onClose}
      title={`Rediger ${integration.name}`}
      footer={mutation.data
        ? <Button variant="primary" onClick={onClose}>Luk</Button>
        : <><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} disabled={!draft.name.trim() || (integration.kind !== "tmdb" && !draft.baseURL.trim())} onClick={() => mutation.mutate()}>Gem</Button></>}
    >
      {mutation.data ? (
        <div className="space-y-2"><p>Ændringerne er gemt.</p><CheckResult result={mutation.data} /></div>
      ) : (
        <div className="space-y-3">
          <ConnectionFields kind={integration.kind} draft={draft} onChange={setDraft} editing />
          {integration.kind === "altmount" && draft.authentication === "session" && original.authentication !== "session" ? <p className="text-xs text-text-3">Gem bagefter AltMount-login med &quot;Gem nøgle&quot;.</p> : null}
          {mutation.error ? <p role="alert" className="text-sm text-bad">{errorText(mutation.error)}</p> : null}
        </div>
      )}
    </Dialog>
  )
}

/** Adds a Newznab indexer: saves it, stores its API key encrypted and approves it with one real request. */
export function AddIndexerDialog({ onClose }: { onClose: () => void }) {
  const toast = useToast()
  const refresh = useRefreshSetup()
  const setup = useQuery({ queryKey: ["setup", "status"], queryFn: ({ signal }) => loadSetupStatus(signal) })
  const [draft, setDraft] = useState({ name: "", baseURL: "", apiKey: "", queryLimit: "100", grabLimit: "20", interval: "3000" })
  const mutation = useMutation({
    mutationFn: async () => {
      const baseURL = draft.baseURL.trim()
      const created = await createIntegration({
        kind: "newznab", name: draft.name.trim(), enabled: false, is_default: false,
        config: { base_url: baseURL, allow_http: baseURL.toLowerCase().startsWith("http://"), query_limit: Number(draft.queryLimit), grab_limit: Number(draft.grabLimit), minimum_interval_ms: Number(draft.interval), budget_window_seconds: 86400, budget_source: "owner_stated" },
      })
      try {
        await saveIntegrationSecret(created.id, draft.apiKey.trim())
      } catch (error) {
        throw new Error(`Indexeren er gemt, men API-nøglen blev ikke gemt: ${errorText(error)}. Brug "Nøgle" på indexeren for at prøve igen.`)
      }
      try {
        await commissionIntegration(created.id)
      } catch (error) {
        throw new Error(`Indexeren er gemt, men den kunne ikke godkendes: ${errorText(error)}. Tjek adresse og API-nøgle, og tryk "Godkend" på indexeren.`)
      }
    },
    onSuccess: async () => { toast("good", `${draft.name.trim()} er godkendt og bruges nu i søgninger.`); await refresh(); onClose() },
    onError: async (error) => { toast("bad", errorText(error)); await refresh() },
  })
  const numbers = [draft.queryLimit, draft.grabLimit, draft.interval].every((value) => /^\d+$/.test(value) && Number(value) > 0)
  const secretStorage = setup.data?.secret_storage_enabled ?? true
  const ready = draft.name.trim() && draft.baseURL.trim() && draft.apiKey.trim() && numbers && secretStorage
  return (
    <Dialog open onClose={onClose} title="Tilføj indexer" footer={<><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} disabled={!ready} onClick={() => mutation.mutate()}>Tilføj og godkend</Button></>}>
      <div className="space-y-3">
        <Field label="Navn"><input value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} className={fieldClass} /></Field>
        <Field label="API-adresse" hint="Newznab-adressen, fx https://indexer.example/api">
          <input inputMode="url" placeholder="https://" value={draft.baseURL} onChange={(event) => setDraft({ ...draft, baseURL: event.target.value })} className={fieldClass} />
        </Field>
        <SecretFields kind="newznab" values={{ ...emptySecret, token: draft.apiKey }} onChange={(values) => setDraft({ ...draft, apiKey: values.token })} />
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <Field label="Søgninger pr. døgn"><input inputMode="numeric" value={draft.queryLimit} onChange={(event) => setDraft({ ...draft, queryLimit: event.target.value })} className={fieldClass} /></Field>
          <Field label="Hentninger pr. døgn"><input inputMode="numeric" value={draft.grabLimit} onChange={(event) => setDraft({ ...draft, grabLimit: event.target.value })} className={fieldClass} /></Field>
          <Field label="Pause mellem kald (ms)"><input inputMode="numeric" value={draft.interval} onChange={(event) => setDraft({ ...draft, interval: event.target.value })} className={fieldClass} /></Field>
        </div>
        <p className="text-xs text-text-3">Sæt kun grænser, din konto faktisk tillader; de kan ændres senere. Ved godkendelsen sender Media Manager én søgning, der ikke kan give resultater, for at se, at nøglen virker.</p>
        {!secretStorage ? <p className="text-xs text-bad">Serveren kan ikke gemme nøgler endnu (SECRET_ENCRYPTION_KEY mangler).</p> : null}
        {mutation.error ? <p role="alert" className="text-sm text-bad">{errorText(mutation.error)}</p> : null}
      </div>
    </Dialog>
  )
}

/** Adds a library folder by picking a Plex library; paths are suggested and editable. */
export function AddRootDialog({ onClose }: { onClose: () => void }) {
  const toast = useToast()
  const refresh = useRefreshSetup()
  const integrations = useQuery({ queryKey: ["settings", "integrations"], queryFn: ({ signal }) => loadIntegrations(signal) })
  const roots = useQuery({ queryKey: ["settings", "roots"], queryFn: ({ signal }) => loadLibraryRoots(signal) })
  const plexConnections = (integrations.data ?? []).filter((item) => item.kind === "plex" && item.enabled)
  const [plexID, setPlexID] = useState("")
  const activePlex = plexID || (plexConnections.find((item) => item.is_default) ?? plexConnections[0])?.id || ""
  const libraries = useQuery({ queryKey: ["setup", "plex-libraries", activePlex], queryFn: ({ signal }) => loadPlexLibraries(activePlex, signal), enabled: Boolean(activePlex), retry: false })
  const [kind, setKind] = useState<"movie" | "series">("movie")
  const [libraryKey, setLibraryKey] = useState("")
  const [draft, setDraft] = useState({ name: "", plexRoot: "", destination: "", source: "", edition: "standard" as "standard" | "4k", isDefault: false })
  const existing = roots.data ?? []
  const hasDefault = existing.some((root) => root.media_kind === kind && root.is_default && root.enabled)
  const plexType = kind === "movie" ? "movie" : "show"
  const choices = (libraries.data ?? []).filter((library) => library.type === plexType)
  const used = (key: string) => existing.some((root) => root.plex_instance_id === activePlex && root.plex_library_id === key)
  const pick = (key: string) => {
    setLibraryKey(key)
    const library = choices.find((entry) => entry.key === key)
    const plexRoot = library?.locations[0] ?? ""
    const fourK = /4k|2160/i.test(library?.title ?? "")
    setDraft({ name: library?.title ?? "", plexRoot, destination: suggestDestination(existing, plexRoot), source: draft.source || suggestSourceRoot(existing), edition: fourK ? "4k" : "standard", isDefault: !fourK && !hasDefault })
  }
  const mutation = useMutation({
    mutationFn: () => createLibraryRoot({
      media_kind: kind, name: draft.name.trim(), destination_root: draft.destination.trim(), plex_visible_root: draft.plexRoot.trim(), source_visible_root: draft.source.trim(),
      plex_instance_id: activePlex, plex_library_id: libraryKey, edition: draft.edition, is_default: draft.edition === "standard" && draft.isDefault, enabled: true,
    }),
    onSuccess: async () => { toast("good", `${draft.name.trim()} er tilføjet.`); await refresh(); onClose() },
    onError: (error) => toast("bad", errorText(error)),
  })
  const absolute = (value: string) => value.trim().startsWith("/")
  const ready = activePlex && libraryKey && draft.name.trim() && absolute(draft.plexRoot) && absolute(draft.destination) && absolute(draft.source)
  return (
    <Dialog open onClose={onClose} title="Tilføj biblioteksmappe" footer={<><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} disabled={!ready} onClick={() => mutation.mutate()}>Tilføj</Button></>}>
      <div className="space-y-3">
        {integrations.isSuccess && plexConnections.length === 0 ? <p className="text-bad">Tilføj og slå Plex til under Indstillinger → Forbindelser først.</p> : null}
        <fieldset className="flex gap-4">
          <legend className="mb-1">Type</legend>
          {(["movie", "series"] as const).map((value) => (
            <label key={value} className="flex items-center gap-2 text-text"><input type="radio" name="root-kind" checked={kind === value} onChange={() => { setKind(value); setLibraryKey("") }} className="size-4" />{value === "movie" ? "Film" : "Serier"}</label>
          ))}
        </fieldset>
        {plexConnections.length > 1 ? (
          <Field label="Plex-server">
            <select value={activePlex} onChange={(event) => { setPlexID(event.target.value); setLibraryKey("") }} className={fieldClass}>
              {plexConnections.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
            </select>
          </Field>
        ) : null}
        {libraries.error ? <p role="alert" className="text-sm text-bad">Plex svarede ikke: {checkText(errorText(libraries.error))}</p> : null}
        <Field label="Plex-bibliotek" hint={libraries.isFetching ? "Henter bibliotekerne fra Plex…" : "Biblioteket skal findes i Plex i forvejen."}>
          <select value={libraryKey} onChange={(event) => pick(event.target.value)} className={fieldClass} disabled={!libraries.isSuccess}>
            <option value="">Vælg bibliotek</option>
            {choices.map((library) => <option key={library.key} value={library.key} disabled={used(library.key)}>{library.title}{used(library.key) ? " (i brug)" : ""}</option>)}
          </select>
        </Field>
        {libraryKey ? (
          <>
            <Field label="Navn i Media Manager"><input value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} className={fieldClass} /></Field>
            <fieldset className="space-y-1">
              <legend className="mb-1">Udgave</legend>
              <label className="flex items-center gap-2 text-text"><input type="radio" name="root-edition" checked={draft.edition === "standard"} onChange={() => setDraft({ ...draft, edition: "standard" })} className="size-4" />Almindelig</label>
              <label className="flex items-center gap-2 text-text"><input type="radio" name="root-edition" checked={draft.edition === "4k"} onChange={() => setDraft({ ...draft, edition: "4k", isDefault: false })} className="size-4" />4K – en ekstra 4K-udgave, kun når 4K bestilles</label>
            </fieldset>
            <Field label="Mappen som Plex ser den">
              {(choices.find((entry) => entry.key === libraryKey)?.locations.length ?? 0) > 1 ? (
                <select value={draft.plexRoot} onChange={(event) => setDraft({ ...draft, plexRoot: event.target.value, destination: suggestDestination(existing, event.target.value) })} className={fieldClass}>
                  {choices.find((entry) => entry.key === libraryKey)!.locations.map((location) => <option key={location} value={location}>{location}</option>)}
                </select>
              ) : <input value={draft.plexRoot} onChange={(event) => setDraft({ ...draft, plexRoot: event.target.value })} className={fieldClass} />}
            </Field>
            <Field label="Mappen som Media Manager ser den" hint="Samme mappe set inde fra Media Manager. Her lægges genvejene til filerne. Forslaget bygger på dine andre mapper.">
              <input value={draft.destination} onChange={(event) => setDraft({ ...draft, destination: event.target.value })} className={fieldClass} />
            </Field>
            <Field label="AltMount-filerne som Plex ser dem" hint="Genvejene peger hertil, så Plex kan afspille filerne.">
              <input value={draft.source} onChange={(event) => setDraft({ ...draft, source: event.target.value })} className={fieldClass} />
            </Field>
            {draft.edition === "standard" ? <Check checked={draft.isDefault} onChange={(isDefault) => setDraft({ ...draft, isDefault })}>Brug som standard for {kind === "movie" ? "film" : "serier"}</Check> : null}
          </>
        ) : null}
      </div>
    </Dialog>
  )
}

const profileLanguages = ["da", "en", "sv", "no"]
const presetNames: Record<string, string> = {
  balanced: "Afbalanceret – højeste opløsning og god lyd, uden minimumskrav",
  maximum_quality: "Højeste kvalitet – foretrækker 4K, HDR og tabsfri lyd",
  storage_efficient: "Pladsbesparende – foretrækker 1080p/720p og små filer",
  compatibility_first: "Bedst kompatibel – foretrækker H.264 og 1080p til ældre afspillere",
}

function LanguageRow({ label, values, onChange }: { label: string; values: string[]; onChange: (values: string[]) => void }) {
  return (
    <fieldset>
      <legend className="mb-1">{label}</legend>
      <div className="flex flex-wrap gap-x-4 gap-y-1">
        {profileLanguages.map((code) => (
          <Check key={code} checked={values.includes(code)} onChange={(checked) => onChange(checked ? [...values, code] : values.filter((value) => value !== code))}>{languageName(code)}</Check>
        ))}
      </div>
    </fieldset>
  )
}

/** Creates a quality profile from a preset or a copy of an existing profile. */
export function AddProfileDialog({ onClose }: { onClose: () => void }) {
  const toast = useToast()
  const refresh = useRefreshSetup()
  const presets = useQuery({ queryKey: ["settings", "presets"], queryFn: ({ signal }) => loadQualityProfilePresets(signal) })
  const profiles = useQuery({ queryKey: ["settings", "profiles"], queryFn: ({ signal }) => loadQualityProfiles(signal) })
  const [kind, setKind] = useState<"movie" | "series">("movie")
  const [start, setStart] = useState("preset:balanced")
  const [name, setName] = useState("")
  const [requiredSubs, setRequiredSubs] = useState<string[]>([])
  const [requiredAudio, setRequiredAudio] = useState<string[]>([])
  const [preferredSubs, setPreferredSubs] = useState<string[]>([])
  const [upgrades, setUpgrades] = useState(true)
  const [isDefault, setIsDefault] = useState<boolean | null>(null)
  const sameKind = (profiles.data ?? []).filter((profile) => profile.media_kind === kind)
  const hasDefault = sameKind.some((profile) => profile.is_default && profile.enabled)
  const makeDefault = isDefault ?? !hasDefault
  const chooseStart = (value: string) => {
    setStart(value)
    const source = sameKind.find((profile) => `profile:${profile.id}` === value)
    if (!source) return
    const policy = source.policy as { required_subtitle_languages?: string[]; required_audio_languages?: string[]; preferred_subtitle_languages?: string[] }
    setRequiredSubs(policy.required_subtitle_languages ?? [])
    setRequiredAudio(policy.required_audio_languages ?? [])
    setPreferredSubs(policy.preferred_subtitle_languages ?? [])
    setUpgrades(source.upgrades_enabled)
  }
  const mutation = useMutation({
    mutationFn: () => {
      const languages = { required_subtitle_languages: requiredSubs, preferred_subtitle_languages: preferredSubs, required_audio_languages: requiredAudio }
      let policy: Record<string, unknown>
      if (start.startsWith("profile:")) {
        const source = sameKind.find((profile) => `profile:${profile.id}` === start) as APIQualityProfile
        policy = { ...source.policy, ...languages }
      } else {
        const preset = (presets.data ?? []).find((entry) => `preset:${entry.preset}` === start)
        policy = { rules: preset?.rules ?? [], ...languages }
      }
      return createQualityProfile({ media_kind: kind, name: name.trim(), policy_version: 1, policy, upgrades_enabled: upgrades, is_default: makeDefault, enabled: true })
    },
    onSuccess: async () => { toast("good", `${name.trim()} er oprettet.`); await refresh(); onClose() },
    onError: (error) => toast("bad", errorText(error)),
  })
  const ready = name.trim() && (start.startsWith("profile:") || presets.isSuccess)
  return (
    <Dialog open onClose={onClose} title="Ny kvalitetsprofil" footer={<><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} disabled={!ready} onClick={() => mutation.mutate()}>Opret profil</Button></>}>
      <div className="space-y-3">
        <fieldset className="flex gap-4">
          <legend className="mb-1">Type</legend>
          {(["movie", "series"] as const).map((value) => (
            <label key={value} className="flex items-center gap-2 text-text"><input type="radio" name="profile-kind" checked={kind === value} onChange={() => { setKind(value); setStart("preset:balanced"); setIsDefault(null) }} className="size-4" />{value === "movie" ? "Film" : "Serier"}</label>
          ))}
        </fieldset>
        <Field label="Navn"><input value={name} onChange={(event) => setName(event.target.value)} placeholder={kind === "movie" ? "Film med danske undertekster" : "Serier med danske undertekster"} className={fieldClass} /></Field>
        <Field label="Start fra" hint="Bestemmer, hvilke udgivelser der foretrækkes. Sprogkravene nedenfor er faste krav.">
          <select value={start} onChange={(event) => chooseStart(event.target.value)} className={fieldClass}>
            {(presets.data ?? []).map((preset) => <option key={preset.preset} value={`preset:${preset.preset}`}>{presetNames[preset.preset] ?? preset.name}</option>)}
            {sameKind.map((profile) => <option key={profile.id} value={`profile:${profile.id}`}>Kopi af {profile.name}</option>)}
          </select>
        </Field>
        <LanguageRow label="Kræv undertekster på" values={requiredSubs} onChange={setRequiredSubs} />
        <LanguageRow label="Kræv tale på" values={requiredAudio} onChange={setRequiredAudio} />
        <LanguageRow label="Foretræk undertekster på" values={preferredSubs} onChange={setPreferredSubs} />
        <p className="text-xs text-text-3">Et krav betyder, at en udgivelse uden sproget aldrig bliver valgt. Undertekster kontrolleres i filen, før den lægges i Plex.</p>
        <Check checked={upgrades} onChange={setUpgrades}>Opgrader, når en bedre udgivelse dukker op</Check>
        <Check checked={makeDefault} onChange={setIsDefault}>Brug som standard for {kind === "movie" ? "film" : "serier"}</Check>
      </div>
    </Dialog>
  )
}

/** Adds a webhook that receives Media Manager's events. */
export function AddNotificationDialog({ onClose }: { onClose: () => void }) {
  const toast = useToast()
  const refresh = useRefreshSetup()
  const [draft, setDraft] = useState({ name: "", endpoint: "", all: true, events: "" })
  const events = draft.all ? ["*"] : draft.events.split(/[\s,]+/).map((value) => value.trim()).filter(Boolean)
  const mutation = useMutation({
    mutationFn: () => {
      const endpoint = draft.endpoint.trim()
      return createNotificationDestination({ name: draft.name.trim(), endpoint, allow_http: endpoint.toLowerCase().startsWith("http://"), enabled: true, events })
    },
    onSuccess: async () => { toast("good", `${draft.name.trim()} er tilføjet.`); await refresh(); onClose() },
    onError: (error) => toast("bad", errorText(error)),
  })
  const ready = draft.name.trim() && draft.endpoint.trim() && events.length > 0
  return (
    <Dialog open onClose={onClose} title="Tilføj notifikation" footer={<><Button variant="ghost" onClick={onClose}>Fortryd</Button><Button variant="primary" busy={mutation.isPending} disabled={!ready} onClick={() => mutation.mutate()}>Tilføj</Button></>}>
      <div className="space-y-3">
        <Field label="Navn"><input value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} className={fieldClass} /></Field>
        <Field label="Webhook-adresse" hint="Media Manager sender en JSON-besked hertil. Adressen vises kun forkortet bagefter.">
          <input inputMode="url" placeholder="https://" value={draft.endpoint} onChange={(event) => setDraft({ ...draft, endpoint: event.target.value })} className={fieldClass} />
        </Field>
        <Check checked={draft.all} onChange={(all) => setDraft({ ...draft, all })}>Send alle hændelser</Check>
        {!draft.all ? (
          <Field label="Hændelser" hint="Adskil med komma, fx workflow.completed, workflow.attention_required">
            <input value={draft.events} onChange={(event) => setDraft({ ...draft, events: event.target.value })} className={fieldClass} />
          </Field>
        ) : null}
      </div>
    </Dialog>
  )
}

/** Approve (commission) or re-key an indexer straight from its card. */
export function IndexerSetupActions({ integration, commissioned }: { integration: Pick<APIIntegration, "id" | "name" | "kind">; commissioned: boolean }) {
  const toast = useToast()
  const refresh = useRefreshSetup()
  const [keying, setKeying] = useState(false)
  const commission = useMutation({
    mutationFn: () => commissionIntegration(integration.id),
    onSuccess: async () => { toast("good", `${integration.name} er godkendt og bruges nu i søgninger.`); await refresh() },
    onError: (error) => toast("bad", `${integration.name} kunne ikke godkendes: ${errorText(error)}`),
  })
  if (integration.kind !== "newznab") return null
  return (
    <>
      {!commissioned ? <Button className="h-8 text-xs" busy={commission.isPending} onClick={() => commission.mutate()}>Godkend</Button> : null}
      <Button variant="ghost" className="h-8 text-xs" onClick={() => setKeying(true)}>Nøgle</Button>
      {keying ? <SecretDialog integration={integration} onClose={() => setKeying(false)} /> : null}
    </>
  )
}
