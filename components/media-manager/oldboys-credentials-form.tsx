"use client"

import * as React from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { loadOldBoysCredentialStatus, saveOldBoysCredentials, type APIOldBoysCredentialStatus } from "@/lib/media-manager-api"

export function OldBoysCredentialsForm() {
  const [status, setStatus] = React.useState<APIOldBoysCredentialStatus | null>(null)
  const [apiToken, setAPIToken] = React.useState("")
  const [passKey, setPassKey] = React.useState("")
  const [rssKey, setRSSKey] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState("")

  React.useEffect(() => {
    const controller = new AbortController()
    void loadOldBoysCredentialStatus(controller.signal).then(setStatus).catch(() => {
      if (!controller.signal.aborted) setError("Could not load OldBoys credential status. Reload this page to try again.")
    })
    return () => controller.abort()
  }, [])

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true); setError("")
    try {
      const next = await saveOldBoysCredentials({ api_token: apiToken, pass_key: passKey, rss_key: rssKey })
      setStatus(next)
      setAPIToken(""); setPassKey(""); setRSSKey("")
    } catch {
      // Never render a request body or upstream error that could echo secrets.
      setError("Could not save credentials. Enter all three keys as tokens without spaces or URLs, then try again.")
    } finally { setBusy(false) }
  }

  return <section className="rounded-xl border border-line bg-surface p-5 xl:col-span-2" aria-labelledby="oldboys-credentials-title">
    <h2 id="oldboys-credentials-title" className="font-semibold text-fg">OldBoys credentials</h2>
    <p className="mt-1 text-sm text-fg-subtle">Enter the three values from your OldBoys account. They are stored encrypted on this server and used only by Media Manager.</p>
    <p className="mt-3 text-sm" role="status">{status?.configured ? "Credentials saved. OldBoys is awaiting validation for Danish TV and films." : status?.available ? "No OldBoys credentials saved yet." : status ? "Credential storage needs server setup before you can save." : "Loading credential status…"}</p>
    {error ? <p role="alert" className="mt-3 text-sm text-red-500">{error}</p> : null}
    <form onSubmit={submit} className="mt-4 grid gap-4 sm:grid-cols-3" autoComplete="off">
      <label className="grid gap-2 text-sm" htmlFor="oldboys-api-token">API Token<Input id="oldboys-api-token" type="password" autoComplete="off" spellCheck={false} maxLength={4096} value={apiToken} onChange={(event) => setAPIToken(event.target.value)} disabled={busy || !status?.available} required /></label>
      <label className="grid gap-2 text-sm" htmlFor="oldboys-pass-key">Pass Key (PID)<Input id="oldboys-pass-key" type="password" autoComplete="off" spellCheck={false} maxLength={4096} value={passKey} onChange={(event) => setPassKey(event.target.value)} disabled={busy || !status?.available} required /></label>
      <label className="grid gap-2 text-sm" htmlFor="oldboys-rss-key">RSS Key (RID)<Input id="oldboys-rss-key" type="password" autoComplete="off" spellCheck={false} maxLength={4096} value={rssKey} onChange={(event) => setRSSKey(event.target.value)} disabled={busy || !status?.available} required /></label>
      <div className="sm:col-span-3"><Button type="submit" disabled={busy || !status?.available || !apiToken || !passKey || !rssKey}>{busy ? "Saving…" : status?.configured ? "Replace OldBoys credentials" : "Save OldBoys credentials"}</Button></div>
    </form>
  </section>
}
