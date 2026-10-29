"use client"

import * as React from "react"
import Link from "next/link"

import { loadBackups, loadLibraryRoots, loadProblems, loadQualityProfiles, loadSources, loadSystemInfo, type APIQualityProfile, type APISourceStatus } from "@/lib/media-manager-api"
import { languageName, relativeTime, type AppTone } from "@/lib/app-presentation"
import { useLiveData } from "../live-operational-screens"
import { AppShell } from "./shell"
import { LoadState, PageTitle } from "./parts"

async function loadSettings(signal: AbortSignal) {
  const [sources, roots, profiles, backups, problems, info] = await Promise.all([
    loadSources(signal), loadLibraryRoots(signal), loadQualityProfiles(signal), loadBackups(signal), loadProblems(signal), loadSystemInfo(signal),
  ])
  return { sources, roots, profiles, backups, problems, info }
}

type Connection = { name: string; state: string; tone: AppTone; detail: string; href: string }

function connection(name: string, items: APISourceStatus[], detail: string, href: string): Connection {
  const enabled = items.filter((item) => item.enabled)
  const failing = enabled.filter((item) => item.consecutive_failure > 0)
  if (!items.length) return { name, state: "Not set up", tone: "warn", detail, href }
  if (!enabled.length) return { name, state: items.some((item) => item.configuration_state === "awaiting_commission") ? "Waiting to be tested" : "Turned off", tone: "warn", detail, href }
  if (failing.length) return { name, state: "Needs a look", tone: "warn", detail: `${failing[0].name}: ${failing[0].last_outcome ?? "recent requests failed"}`, href }
  return { name, state: enabled.length > 1 ? `${enabled.length} connected` : "Connected", tone: "ok", detail, href }
}

function policyList(policy: Record<string, unknown>, key: string): string[] {
  const value = policy[key]
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : []
}

function profileSummary(profile: APIQualityProfile) {
  const required = policyList(profile.policy, "required_subtitle_languages").map(languageName)
  const preferred = policyList(profile.policy, "preferred_subtitle_languages").map(languageName)
  const parts = [
    required.length ? `${required.join(" and ")} subtitles required` : "No subtitles required",
    preferred.length ? `${preferred.join(" and ")} subtitles preferred` : "",
    profile.upgrades_enabled ? "Upgrades to better copies" : "Keeps the first good copy",
  ]
  return parts.filter(Boolean).join(". ") + "."
}

function SettingsContent() {
  const { data, error } = useLiveData(loadSettings)
  if (!data) return <div className="mm-stack-tight"><PageTitle title="Settings" /><LoadState error={error} loading={!error} label="Loading your settings…" /></div>
  const sources = data.sources
  const indexers = sources.filter((item) => item.kind === "newznab" || item.kind === "oldboys")
  const tmdb = data.info.metadata_providers?.tmdb ?? ""
  const connections: Connection[] = [
    connection("Plex", sources.filter((item) => item.kind === "plex"), "Your movie and TV libraries. Plex confirms every new file before it counts as done.", "/settings/connections"),
    connection("AltMount", sources.filter((item) => item.kind === "altmount"), "Downloads from Usenet. Media Manager only ever reads from it.", "/sources/download-clients"),
    { ...connection("Indexers", indexers, "Where Media Manager searches. Searches are spaced out so no indexer is overloaded.", "/sources/indexers"), ...(indexers.some((item) => item.enabled) ? { state: `${indexers.filter((item) => item.enabled && item.consecutive_failure === 0).length} of ${indexers.filter((item) => item.enabled).length} working` } : {}) },
    connection("Seerr", sources.filter((item) => item.kind === "seerr"), "Requests from your household arrive here, and Add uses it to search for titles.", "/settings/connections"),
    { name: "TMDB", state: tmdb === "direct_configured" ? "Connected" : tmdb === "seerr_fallback" ? "Through Seerr" : "Not set up", tone: tmdb === "direct_configured" || tmdb === "seerr_fallback" ? "ok" : "warn", detail: "Posters, descriptions and episode lists.", href: "/settings/metadata" },
  ]
  const profiles = data.profiles.filter((profile) => profile.enabled)
  const roots = data.roots.filter((root) => root.enabled)
  const lastBackup = data.backups.records.find((record) => record.state === "verified")
  const problems = data.problems.problems

  return <div className="mm-stack">
    <PageTitle title="Settings" lede="Set it up once. Media Manager handles the rest." />
    {problems.length ? <div className="mm-notice mm-notice-error"><span>{problems.length === 1 ? "1 problem needs a look" : `${problems.length} problems need a look`}: {problems[0].title}</span><Link className="mm-btn mm-btn-small" href="/system/problems">See problems</Link></div> : null}
    <section className="mm-section" aria-labelledby="mm-connections"><h2 id="mm-connections" className="mm-h2">Connections</h2><div className="mm-cards">{connections.map((item) => <div key={item.name} className="mm-setting">
      <div className="mm-setting-head"><span>{item.name}</span><span className="mm-status" data-tone={item.tone}>{item.state}</span></div>
      <p>{item.detail}</p>
      <div><Link className="mm-btn mm-btn-small" href={item.href} aria-label={`Change ${item.name}`}>Change</Link></div>
    </div>)}</div></section>
    <section className="mm-section" aria-labelledby="mm-quality"><h2 id="mm-quality" className="mm-h2">What to download</h2><div className="mm-cards">
      {profiles.length ? profiles.map((profile) => <div key={profile.id} className="mm-setting">
        <div className="mm-setting-head"><span>{profile.name}</span>{profile.is_default ? <span className="mm-tag">Default for {profile.media_kind === "series" ? "TV" : "movies"}</span> : null}</div>
        <p>{profileSummary(profile)}</p>
      </div>) : <div className="mm-setting"><div className="mm-setting-head">No quality setting yet</div><p>Media Manager needs one for movies and one for TV before it can add titles.</p></div>}
    </div><div><Link className="mm-btn mm-btn-small" href="/settings/profiles">Change quality settings</Link></div></section>
    <section className="mm-section" aria-labelledby="mm-safety"><h2 id="mm-safety" className="mm-h2">Library and safety</h2><div className="mm-cards">
      <div className="mm-setting"><div className="mm-setting-head">Folders</div><p>{roots.length ? roots.map((root) => `${root.media_kind === "series" ? "TV shows" : "Movies"} go to ${root.destination_root}`).join(". ") + ". Your downloads are never moved or changed." : "No library folder yet. Add one for movies and one for TV."}</p><div><Link className="mm-btn mm-btn-small" href="/settings/storage" aria-label="Change folders">Change</Link></div></div>
      <div className="mm-setting"><div className="mm-setting-head"><span>Backups</span><span className="mm-status" data-tone={data.backups.configured ? "ok" : "warn"}>{data.backups.configured ? "On" : "Off"}</span></div><p>{!data.backups.configured ? "Backups are off. Add a backup key to turn them on." : lastBackup ? `Encrypted. Last good backup ${relativeTime(lastBackup.completed_at ?? lastBackup.created_at)}${data.backups.offsite.configured ? ", with a copy stored off-site" : ""}.` : "Encrypted. No backup has finished yet."}</p><div><Link className="mm-btn mm-btn-small" href="/system/backups" aria-label="Open backups">Open</Link></div></div>
      <div className="mm-setting"><div className="mm-setting-head">Expert console</div><p>Every detail is still there: release evidence, indexer pacing, workflows, history and logs.</p><div><Link className="mm-btn mm-btn-small" href="/console">Open expert console</Link></div></div>
    </div></section>
    <p className="mm-small mm-muted">Media Manager {data.info.version}</p>
  </div>
}

export function AppSettings() {
  return <AppShell section="settings"><SettingsContent /></AppShell>
}
