import { useMutation, useQuery } from "@tanstack/react-query"
import { useState } from "react"

import { ApiError, connectPlexLogin, pollPlexLogin, startPlexLogin, type APIPlexLoginServer, type APIPlexLoginStarted } from "@/api/client"
import { Badge, Button, Dialog } from "@/components/ui"

import { CheckResult, errorText } from "./setup-forms"

// The browser opens app.plex.tv in a window it is allowed to open: it must be
// opened in the click itself, before the login is created.
function openBlankWindow(): Window | null {
  try {
    return window.open("about:blank", "plex-login", "popup,width=600,height=720")
  } catch {
    return null
  }
}

const plexErrorText: Record<string, string> = {
  plex_login_failed: "Plex svarede ikke. Prøv igen om lidt.",
  plex_login_expired: "Login hos Plex er udløbet eller allerede brugt. Tryk \"Log ind med Plex\" igen.",
  plex_login_waiting: "Du har ikke godkendt Media Manager hos Plex endnu.",
  plex_server_unknown: "Vælg en af de servere og adresser, Media Manager har fundet.",
  plex_login_unavailable: "Login med Plex er ikke tilgængeligt på denne server. Tilføj Plex manuelt.",
  secret_storage_unavailable: "Serveren kan ikke gemme nøgler endnu (SECRET_ENCRYPTION_KEY mangler).",
  secret_save_failed: "Plex-forbindelsen er gemt, men nøglen kunne ikke gemmes. Tryk \"Log ind med Plex\" igen.",
  invalid_integration: "Plex-forbindelsen kunne ikke gemmes. Prøv igen, eller tilføj Plex manuelt.",
  settings_update_failed: "Plex-forbindelsen kunne ikke opdateres. Prøv igen.",
  database_error: "Media Manager kunne ikke læse sine forbindelser. Prøv igen om lidt.",
}

/** Danish text for a Plex login error; anything unknown is shown as the server wrote it. */
export function plexLoginError(error: unknown): string {
  if (error instanceof ApiError && error.code && plexErrorText[error.code]) return plexErrorText[error.code]
  return errorText(error)
}

function loginGone(error: unknown): boolean {
  return error instanceof ApiError && (error.code === "plex_login_expired" || error.code === "plex_login_unavailable")
}

export function addressLabel(address: { uri: string; local: boolean; relay: boolean }): string {
  if (address.uri === "http://plex:32400") return "Plex-containeren på samme netværk"
  if (address.relay) return "Via Plex' relæ (langsom, kun nødløsning)"
  return address.local ? "Lokalt netværk" : "Via internettet"
}

/**
 * "Log ind med Plex": the owner approves Media Manager on plex.tv, and
 * Media Manager finds the server and an address it can reach by itself. The
 * Plex token stays on the server.
 */
export function PlexLoginButton({ onConnected, variant = "primary" }: { onConnected: () => Promise<void> | void; variant?: "primary" | "ghost" }) {
  const [login, setLogin] = useState<APIPlexLoginStarted | null>(null)
  const [popupBlocked, setPopupBlocked] = useState(false)
  const start = useMutation({
    mutationFn: async () => {
      const popup = openBlankWindow()
      try {
        const started = await startPlexLogin()
        if (popup && !popup.closed) popup.location.href = started.auth_url
        setPopupBlocked(!popup)
        return started
      } catch (error) {
        popup?.close()
        throw error
      }
    },
    onSuccess: (started) => setLogin(started),
  })
  return (
    <>
      <Button variant={variant} busy={start.isPending} onClick={() => start.mutate()}>Log ind med Plex</Button>
      {start.error ? <span role="alert" className="text-sm text-bad">{plexLoginError(start.error)}</span> : null}
      {login ? <PlexLoginDialog login={login} popupBlocked={popupBlocked} onConnected={onConnected} onClose={() => { setLogin(null); start.reset() }} /> : null}
    </>
  )
}

function PlexLoginDialog({ login, popupBlocked, onConnected, onClose }: { login: APIPlexLoginStarted; popupBlocked: boolean; onConnected: () => Promise<void> | void; onClose: () => void }) {
  const [chosen, setChosen] = useState<{ server: string; uri: string } | null>(null)
  const status = useQuery({
    queryKey: ["plex-login", login.id],
    queryFn: ({ signal }) => pollPlexLogin(login.id, signal),
    // Keep asking through a passing plex.tv hiccup; stop once approved or
    // when the login is gone.
    refetchInterval: (query) => (query.state.data?.state === "approved" || loginGone(query.state.error) ? false : 2000),
    retry: false,
    gcTime: 0,
  })
  const servers = status.data?.servers ?? []
  const firstReachable = servers.find((server) => server.recommended)
  const selection = chosen ?? (firstReachable ? { server: firstReachable.id, uri: firstReachable.recommended ?? "" } : null)
  const connect = useMutation({
    mutationFn: () => connectPlexLogin(login.id, { server_id: selection!.server, uri: selection!.uri }),
    onSuccess: async () => { await onConnected() },
  })
  const approved = status.data?.state === "approved"
  const footer = connect.isSuccess
    ? <Button variant="primary" onClick={onClose}>Luk</Button>
    : (
      <>
        <Button variant="ghost" onClick={onClose}>Fortryd</Button>
        {approved ? <Button variant="primary" busy={connect.isPending} disabled={!selection} onClick={() => connect.mutate()}>Forbind</Button> : null}
      </>
    )
  return (
    <Dialog open onClose={onClose} title="Log ind med Plex" footer={footer}>
      {connect.isSuccess ? (
        <div className="space-y-2">
          <p>{connect.data.created ? "Plex er tilføjet." : "Plex-forbindelsen er opdateret."}</p>
          <CheckResult result={connect.data.check} />
        </div>
      ) : loginGone(status.error) ? (
        <p role="alert" className="text-sm text-bad">{plexLoginError(status.error)}</p>
      ) : !approved ? (
        <div className="space-y-3 text-sm">
          <p>Log ind på Plex i det nye vindue, og giv Media Manager adgang (knappen hedder &quot;Allow&quot; eller &quot;Tillad&quot;). Media Manager finder derefter selv din server.</p>
          {popupBlocked ? <p>Browseren blokerede vinduet. <a className="text-accent underline" href={login.auth_url} target="_blank" rel="noreferrer">Åbn Plex-login</a></p> : <p className="text-text-3">Kan du ikke se vinduet? <a className="text-accent underline" href={login.auth_url} target="_blank" rel="noreferrer">Åbn det igen</a></p>}
          <p className="text-text-3" aria-live="polite">{status.error ? "Plex svarede ikke lige nu. Prøver igen …" : "Venter på Plex …"}</p>
        </div>
      ) : servers.length === 0 ? (
        <p className="text-sm">Din Plex-konto har ingen Plex Media Server. Installér Plex Media Server først, eller tilføj forbindelsen manuelt.</p>
      ) : (
        <div className="space-y-3 text-sm">
          <p>Vælg din server. Media Manager har selv testet, hvilke adresser den kan nå.</p>
          {connect.error ? <p role="alert" className="text-bad">{plexLoginError(connect.error)}</p> : null}
          <ul className="space-y-3">
            {servers.map((server) => <ServerChoice key={server.id} server={server} selection={selection} onSelect={setChosen} />)}
          </ul>
          {!firstReachable ? <p className="text-bad">Media Manager kan ikke nå nogen af serverne. Tjek at Plex kører, og at Media Manager og Plex er på samme netværk.</p> : null}
        </div>
      )}
    </Dialog>
  )
}

function ServerChoice({ server, selection, onSelect }: { server: APIPlexLoginServer; selection: { server: string; uri: string } | null; onSelect: (value: { server: string; uri: string }) => void }) {
  const reachable = (server.addresses ?? []).filter((address) => address.reachable)
  return (
    <li className="rounded-lg border border-line p-3">
      <div className="flex flex-wrap items-center gap-2 font-medium">
        {server.name}
        {server.owned ? null : <Badge tone="warn">Delt med dig</Badge>}
        {reachable.length === 0 ? <Badge tone="bad">Kan ikke nås</Badge> : null}
      </div>
      {!server.owned ? <p className="mt-1 text-xs text-text-3">Denne server ejes af en anden. Media Manager skal bruge din egen server for at kunne lægge film og serier ind.</p> : null}
      {reachable.length > 0 ? (
        <fieldset className="mt-2 space-y-1">
          <legend className="sr-only">Adresse for {server.name}</legend>
          {reachable.map((address) => (
            <label key={address.uri} className="flex items-start gap-2">
              <input type="radio" name="plex-address" className="mt-1" checked={selection?.server === server.id && selection.uri === address.uri} onChange={() => onSelect({ server: server.id, uri: address.uri })} />
              <span>
                {addressLabel(address)}
                {address.uri === server.recommended ? <span className="ml-1 text-good">(anbefalet)</span> : null}
                <span className="block break-all text-xs text-text-3">{address.uri}</span>
              </span>
            </label>
          ))}
        </fieldset>
      ) : null}
    </li>
  )
}
