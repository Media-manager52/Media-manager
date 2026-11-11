import { createBrowserRouter, Navigate, useRouteError } from "react-router"

import { AppShell } from "@/components/shell"
import { ErrorBox } from "@/components/ui"
import { AddPage } from "@/pages/add"
import { AttentionPage } from "@/pages/attention"
import { ErrorLogPage } from "@/pages/error-log"
import { CalendarPage } from "@/pages/calendar"
import { LibraryPage } from "@/pages/library"
import { ListsPage } from "@/pages/lists"
import { MissingPage } from "@/pages/missing"
import { NotFoundPage } from "@/pages/not-found"
import { OverviewPage } from "@/pages/overview"
import { RequestsPage } from "@/pages/requests"
import { SettingsPage } from "@/pages/settings"
import { SetupPage } from "@/pages/setup"
import { SourcesPage } from "@/pages/sources"
import { SystemPage } from "@/pages/system"
import { TitlePage } from "@/pages/title"
import { WorkPage } from "@/pages/work"
import { WorkflowPage } from "@/pages/workflow"

function RouteError() {
  const error = useRouteError()
  return <AppShell><ErrorBox error={error} retry={() => window.location.reload()} /></AppShell>
}

/** Old English paths keep working, so bookmarks and links in notes still land. */
const legacy: Array<[string, string]> = [
  ["/media", "/bibliotek?type=film"],
  ["/media/series", "/bibliotek?type=serier"],
  ["/requests", "/anmodninger"],
  ["/activity/queue", "/arbejde"],
  ["/calendar", "/kalender"],
  ["/discover", "/tilfoej"],
  ["/sources/indexers", "/kilder"],
  ["/settings/profiles", "/indstillinger/profiler"],
  ["/settings/storage", "/indstillinger/biblioteker"],
  ["/settings/connections", "/indstillinger/forbindelser"],
  ["/setup", "/kom-i-gang"],
  ["/system/health", "/system"],
  ["/system/backups", "/system/backup"],
  ["/system/problems", "/handling"],
  ["/v2", "/"],
  ["/v2/library", "/bibliotek"],
  ["/v2/requests", "/anmodninger"],
  ["/v2/attention", "/handling"],
]

export const router = createBrowserRouter([
  {
    element: <AppShell />,
    errorElement: <RouteError />,
    children: [
      { index: true, element: <OverviewPage /> },
      { path: "handling", element: <AttentionPage /> },
      { path: "fejl", element: <ErrorLogPage /> },
      { path: "bibliotek", element: <LibraryPage /> },
      { path: "bibliotek/mangler", element: <MissingPage /> },
      { path: "bibliotek/:id", element: <TitlePage /> },
      { path: "media/movies/:id", element: <TitlePage /> },
      { path: "media/series/:id", element: <TitlePage /> },
      { path: "tilfoej", element: <AddPage /> },
      { path: "anmodninger", element: <RequestsPage /> },
      { path: "arbejde", element: <WorkPage /> },
      { path: "arbejde/:id", element: <WorkflowPage /> },
      { path: "kalender", element: <CalendarPage /> },
      { path: "kilder", element: <SourcesPage /> },
      { path: "lister", element: <ListsPage /> },
      { path: "kom-i-gang", element: <SetupPage /> },
      { path: "indstillinger", element: <Navigate to="/indstillinger/profiler" replace /> },
      { path: "indstillinger/:section", element: <SettingsPage /> },
      { path: "system", element: <SystemPage /> },
      { path: "system/:section", element: <SystemPage /> },
      ...legacy.map(([from, to]) => ({ path: from, element: <Navigate to={to} replace /> })),
      { path: "*", element: <NotFoundPage /> },
    ],
  },
])
