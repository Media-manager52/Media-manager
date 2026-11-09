package httpapi

import (
	"net/http"

	"github.com/media-manager/media-manager/internal/acquisition"

	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"github.com/media-manager/media-manager/internal/oldboyscredentials"
	"github.com/media-manager/media-manager/internal/plexlogin"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/quicksetup"
	"github.com/media-manager/media-manager/internal/releases"
)

// apiEffect tells an API client what calling an operation does, so an agent
// can decide whether it is safe to call without asking the owner first.
type apiEffect string

const (
	// effectRead never changes state and never contacts a provider.
	effectRead apiEffect = "read"
	// effectWrite changes Media Manager state or queues local work.
	effectWrite apiEffect = "write"
	// effectProvider sends, or queues, requests to an external provider
	// (indexers, Seerr, Plex, metadata sources). It can spend provider budget.
	effectProvider apiEffect = "provider"
	// effectDestructive removes, replaces or purges data and may be
	// irreversible. These operations always require explicit owner intent.
	effectDestructive apiEffect = "destructive"
)

type apiAuth string

const (
	authNone  apiAuth = "none"
	authOwner apiAuth = "owner"
	// authOwnerBrowser additionally requires the trusted owner Origin, so it
	// can only be driven from the Media Manager settings page.
	authOwnerBrowser apiAuth = "owner_browser"
	authSeerrWebhook apiAuth = "seerr_webhook"
	// authOwnerOrPlayer also accepts the player token (PLAYER_TOKEN), which
	// the Nuvio/Stremio addon uses instead of the owner token. Only the few
	// routes a player needs carry it: browsing the library, looking up and
	// requesting a title, and choosing a release to stream.
	authOwnerOrPlayer apiAuth = "owner_or_player"
)

type apiParam struct {
	Name        string
	Type        string // integer, string, boolean
	Format      string // optional OpenAPI format, e.g. date-time
	Description string
	Required    bool
}

// rawBody marks an operation whose request body is not JSON.
type rawBody struct{ ContentType, Description string }

type apiOperation struct {
	Method, Path string
	Handler      http.HandlerFunc
	Auth         apiAuth
	Effect       apiEffect
	Tag          string
	Summary      string
	Description  string
	Query        []apiParam
	// Body is a zero value of the JSON request type, or a rawBody.
	Body     any
	Required []string // required top-level body fields
	// OptionalBody means the body may be omitted entirely.
	OptionalBody bool
	// Response is a zero value of the type returned in "data", when declared.
	Response any
}

var (
	limitParam  = apiParam{Name: "limit", Type: "integer", Description: "Maximum number of rows to return."}
	offsetParam = apiParam{Name: "offset", Type: "integer", Description: "Number of rows to skip, for paging."}
)

func (server *Server) apiOperations() []apiOperation {
	owner, player, s := authOwner, authOwnerOrPlayer, server
	return []apiOperation{
		{Method: "POST", Path: "/api/v1/streaming/search", Handler: s.streamingSearch, Auth: player, Effect: effectProvider, Tag: "streaming", Summary: "Browse exact releases without acquiring or publishing", Body: acquisition.BrowseInput{}, Required: []string{"kind", "title", "year"}, Response: acquisition.BrowseResult{}},
		{Method: "POST", Path: "/api/v1/streaming/releases/{id}/nzb", Handler: s.streamingNZB, Auth: player, Effect: effectProvider, Tag: "streaming", Summary: "Retrieve the viewer-selected NZB using the shared provider budget", Description: "Binary NZB response. Choice expires after six hours or a server restart. Never chooses a replacement."},
		{Method: "POST", Path: "/api/v1/streaming/releases/{id}/prepare", Handler: s.streamingPrepare, Auth: player, Effect: effectProvider, Tag: "streaming", Response: acquisition.PreparedStream{}, Summary: "Have AltMount prepare the viewer-selected release for playback", Description: "Retrieves the NZB with the shared provider budget and submits it to AltMount's stream endpoint. Returns only the prepared file's path on the read-only AltMount mount (data.path, starting with /). 410 when the choice expired, 422 when the release's articles are missing, 503 when the NZB could not be retrieved, 504 when AltMount did not finish in time."},
		{Method: "GET", Path: "/health/live", Handler: s.live, Auth: authNone, Effect: effectRead, Tag: "system", Summary: "Process liveness"},
		{Method: "GET", Path: "/health/ready", Handler: s.ready, Auth: authNone, Effect: effectRead, Tag: "system", Summary: "Readiness: database reachable and schema version current"},
		{Method: "GET", Path: "/api/v1", Handler: s.apiIndex, Auth: owner, Effect: effectRead, Tag: "system", Summary: "API index for agents: conventions, effects, auth and where the OpenAPI document is"},
		{Method: "GET", Path: "/api/v1/openapi.json", Handler: s.openAPI, Auth: owner, Effect: effectRead, Tag: "system", Summary: "OpenAPI 3.1 description of every route, generated from the live route table"},

		{Method: "GET", Path: "/api/v1/dashboard", Handler: s.dashboard, Auth: owner, Effect: effectRead, Tag: "overview", Summary: "Counts, recent workflows and recent activity"},
		{Method: "GET", Path: "/api/v1/activity", Handler: s.listActivity, Auth: owner, Effect: effectRead, Tag: "overview", Summary: "Merged audit and workflow-transition timeline", Query: []apiParam{limitParam}},
		{Method: "GET", Path: "/api/v1/calendar", Handler: s.listCalendar, Auth: owner, Effect: effectRead, Tag: "overview", Summary: "Upcoming and recent episode air dates, one row per episode", Query: []apiParam{{Name: "from", Type: "string", Format: "date", Description: "First day (YYYY-MM-DD)."}, {Name: "to", Type: "string", Format: "date", Description: "Last day (YYYY-MM-DD)."}, limitParam}},
		{Method: "GET", Path: "/api/v1/wanted", Handler: s.listWanted, Auth: owner, Effect: effectRead, Tag: "overview", Summary: "Monitored media that is still missing", Query: []apiParam{limitParam, offsetParam}},

		{Method: "GET", Path: "/api/v1/media", Handler: s.listMedia, Auth: player, Effect: effectRead, Tag: "media", Summary: "List catalogue media", Query: []apiParam{limitParam, offsetParam}},
		{Method: "GET", Path: "/api/v1/media/status", Handler: s.listMediaStatus, Auth: owner, Effect: effectRead, Tag: "media", Summary: "Library status of every active title: in Plex, missing scopes and owner attention", Description: "missing counts wanted scopes (the film, a season or an episode) that no library file covers yet; a season fully present as episode files, or an episode inside a published season pack, is not missing. Upgrades of a present file are not missing.", Response: []postgres.MediaStatus{}},
		{Method: "POST", Path: "/api/v1/media", Handler: s.createMedia, Auth: player, Effect: effectProvider, Tag: "media", Summary: "Add a movie or series to the catalogue and create its workflows", Description: "With search_immediately (default true) the new workflows search the indexers right away.", Body: createMediaInput{}, Required: []string{"kind", "title"}},
		{Method: "GET", Path: "/api/v1/media/{id}", Handler: s.getMedia, Auth: owner, Effect: effectRead, Tag: "media", Summary: "One media item"},
		{Method: "GET", Path: "/api/v1/media/{id}/detail", Handler: s.getMediaDetail, Auth: player, Effect: effectRead, Tag: "media", Summary: "Media title page: seasons, episodes, workflows, files and Plex state"},
		{Method: "POST", Path: "/api/v1/media/{id}/metadata/refresh", Handler: s.refreshMediaMetadata, Auth: owner, Effect: effectProvider, Tag: "media", Summary: "Re-read catalogue metadata (TMDB/TVDB) for one media item"},
		{Method: "POST", Path: "/api/v1/media/{id}/workflows", Handler: s.createMediaWorkflow, Auth: player, Effect: effectProvider, Tag: "media", Summary: "Create a workflow (for example a season) for existing media", Body: createMediaWorkflowInput{}},
		{Method: "POST", Path: "/api/v1/media/{id}/archive", Handler: s.archiveMedia, Auth: owner, Effect: effectWrite, Tag: "media", Summary: "Retire unwanted media from the active catalogue", Description: "Cancels non-terminal workflows and soft-deletes the media row. Never deletes files, AltMount sources or history; refused while files are published or work is in flight."},
		{Method: "POST", Path: "/api/v1/media/{id}/unarchive", Handler: s.unarchiveMedia, Auth: owner, Effect: effectWrite, Tag: "media", Summary: "Return archived media to the active catalogue", Description: "Clears the archive mark. Workflows the archive cancelled stay cancelled; refused while the media is in the recycle bin."},
		{Method: "DELETE", Path: "/api/v1/media/{id}", Handler: s.recycleMedia, Auth: owner, Effect: effectDestructive, Tag: "media", Summary: "Move media and its published links to the recycle bin", Description: "Links stay in the recycle bin for 30 days and can be restored; AltMount content is never touched. Refused with media_has_open_work while a workflow is open, unless cancel_open_work=true, which first cancels waiting, searching and monitoring work (refused with workflow_in_flight while anything is downloading or publishing).", Query: []apiParam{{Name: "cancel_open_work", Type: "boolean", Description: "Cancel open workflows of the media first."}}},
		{Method: "POST", Path: "/api/v1/media/{id}/relocate", Handler: s.relocateMedia, Auth: owner, Effect: effectWrite, Tag: "media", Summary: "Move a whole title to another library root of the same kind and edition", Description: "Moves the published links of every workflow of the title (every season of a series) and points open workflows at the new root, then queues a Plex refresh of the old and new folder. Refused with workflow_in_flight while anything is downloading or publishing, so nothing moves halfway. Link targets and AltMount content are never touched.", Body: relocateMediaInput{}, Required: []string{"root_id"}, Response: postgres.MediaRelocation{}},

		{Method: "GET", Path: "/api/v1/workflows", Handler: s.listWorkflows, Auth: owner, Effect: effectRead, Tag: "workflows", Summary: "List workflows with their available actions", Query: []apiParam{{Name: "active", Type: "boolean", Description: "Only non-terminal workflows (default true). Pass false for all."}, {Name: "state", Type: "string", Description: "Only workflows in this durable state (for example needs_attention, blocked, waiting or plex_confirmed)."}, {Name: "attention", Type: "boolean", Description: "Only workflows that need an owner decision, using the same predicate as the Dashboard count."}, limitParam, offsetParam}, Response: []workflowView{}},
		{Method: "GET", Path: "/api/v1/workflows/{id}", Handler: s.getWorkflow, Auth: owner, Effect: effectRead, Tag: "workflows", Summary: "One workflow: state, revision, rounds, retained output, attention and available_actions", Description: "available_actions lists every owner action with its route, effect, whether it applies now (or why not) and a ready-to-send body where one is needed.", Response: workflowDetail{}},
		{Method: "GET", Path: "/api/v1/workflows/{id}/searches", Handler: s.listWorkflowSearches, Auth: owner, Effect: effectRead, Tag: "workflows", Summary: "Recent search runs of one workflow with each indexer's outcome", Description: "partial runs had a deferred or failed indexer and are retried; only complete runs advance search phases.", Query: []apiParam{limitParam}, Response: []postgres.SearchRunView{}},
		{Method: "GET", Path: "/api/v1/workflows/{id}/candidates", Handler: s.listWorkflowCandidates, Auth: owner, Effect: effectRead, Tag: "workflows", Summary: "Release candidates of one workflow with their latest decision, reasons, attempts and exclusions", Query: []apiParam{{Name: "verdict", Type: "string", Description: "Only candidates whose latest decision is eligible, held or rejected."}, limitParam}, Response: []postgres.WorkflowCandidateView{}},
		{Method: "GET", Path: "/api/v1/workflows/{id}/candidates/dry-run", Handler: s.dryRunWorkflowCandidates, Auth: owner, Effect: effectRead, Tag: "workflows", Summary: "Read-only policy replay of one workflow's stored release candidates", Description: "Re-evaluates stored observations with the same evaluator as the worker. It never searches, downloads, submits to AltMount, publishes, or changes exclusions.", Response: acquisition.DryRunResult{}},
		{Method: "POST", Path: "/api/v1/workflows/{id}/search", Handler: s.searchWorkflow, Auth: owner, Effect: effectProvider, Tag: "workflows", Summary: "Queue one paced indexer search now", Description: "Allowed in monitored, waiting, held and needs_attention. Refused while the acquisition round is exhausted (open the next round instead) or an AltMount job is active.", Body: searchWorkflowInput{}, OptionalBody: true},
		{Method: "POST", Path: "/api/v1/workflows/{id}/discover", Handler: s.discoverWorkflow, Auth: owner, Effect: effectProvider, Tag: "workflows", Summary: "Queue an evidence-only discovery rescan (finds new releases without grabbing)"},
		{Method: "POST", Path: "/api/v1/workflows/{id}/acquisition-rounds", Handler: s.openAcquisitionRound, Auth: owner, Effect: effectProvider, Tag: "workflows", Summary: "Open the next acquisition round after discovery found a new eligible release"},
		{Method: "POST", Path: "/api/v1/workflows/{id}/acquisition-rounds/from-known", Handler: s.openKnownAcquisitionRound, Auth: owner, Effect: effectProvider, Tag: "workflows", Summary: "Open the next round from the best already-known, never-attempted eligible release", Description: "Prefers explicit Danish subtitle evidence, then a Danish/Nordic title marker, then score. Still honours the retained failed-output limit."},
		{Method: "POST", Path: "/api/v1/workflows/{id}/retry-access-rejected-release", Handler: s.retryAccessRejectedRelease, Auth: owner, Effect: effectProvider, Tag: "workflows", Summary: "Retry one release that the indexer rejected with HTTP 401/403 before submission", Body: retryAccessRejectedReleaseInput{}, Required: []string{"candidate_id", "revision"}},
		{Method: "POST", Path: "/api/v1/workflows/{id}/revive-rejected-archives", Handler: s.reviveRejectedArchives, Auth: owner, Effect: effectWrite, Tag: "workflows", Summary: "Revive releases rejected as unsupported archives before AltMount ever saw them"},
		{Method: "POST", Path: "/api/v1/workflows/{id}/relocate-links", Handler: s.relocateWorkflowLinks, Auth: owner, Effect: effectWrite, Tag: "workflows", Summary: "Move a workflow's published links to another library root of the same kind", Description: "Link targets and AltMount content are never touched. Plex is not refreshed by this call.", Body: relocateWorkflowLinksInput{}, Required: []string{"root_id"}},
		{Method: "POST", Path: "/api/v1/workflows/{id}/recover-altmount-submission", Handler: s.recoverAltMountSubmission, Auth: owner, Effect: effectWrite, Tag: "workflows", Summary: "Recover a submission whose AltMount transport outcome is unknown"},
		{Method: "POST", Path: "/api/v1/workflows/{id}/reconcile-altmount", Handler: s.reconcileAltMount, Auth: owner, Effect: effectWrite, Tag: "workflows", Summary: "Re-read the workflow's AltMount job and continue import/inspection (no new NZB)"},
		{Method: "POST", Path: "/api/v1/workflows/{id}/reverify-retained-output", Handler: s.reverifyRetainedOutput, Auth: owner, Effect: effectWrite, Tag: "workflows", Summary: "Re-run subtitle verification on one retained output", Body: reverifyRetainedOutputInput{}, Required: []string{"job_id"}},
		{Method: "POST", Path: "/api/v1/workflows/{id}/reinspect-retained-movie", Handler: s.reinspectRetainedMovie, Auth: owner, Effect: effectWrite, Tag: "workflows", Summary: "Re-inspect the latest ambiguous retained movie output once (no new NZB)", Body: reinspectRetainedMovieInput{}, Required: []string{"job_id"}},
		{Method: "POST", Path: "/api/v1/workflows/{id}/reconfirm-plex", Handler: s.reconfirmPlex, Auth: owner, Effect: effectProvider, Tag: "workflows", Summary: "Retry exact-path Plex confirmation after a timeout"},
		{Method: "POST", Path: "/api/v1/workflows/{id}/cancel", Handler: s.cancelWorkflow, Auth: owner, Effect: effectWrite, Tag: "workflows", Summary: "Cancel a workflow", Description: "Jobs, retained output and exclusions stay visible. Refused while an AltMount job is active."},
		{Method: "POST", Path: "/api/v1/plex/reconfirm-timed-out", Handler: s.reconfirmTimedOutPlex, Auth: owner, Effect: effectProvider, Tag: "workflows", Summary: "Re-queue exact-path Plex confirmation for every timed-out publication"},

		{Method: "GET", Path: "/api/v1/requests", Handler: s.listRequests, Auth: owner, Effect: effectRead, Tag: "requests", Summary: "Seerr requests with their Media Manager lifecycle", Query: []apiParam{limitParam, offsetParam}},
		{Method: "GET", Path: "/api/v1/requests/{id}", Handler: s.getRequest, Auth: owner, Effect: effectRead, Tag: "requests", Summary: "One Seerr request with linked workflows"},
		{Method: "POST", Path: "/api/v1/requests/{id}/approve", Handler: s.decideRequest(seerr.DecisionApprove), Auth: owner, Effect: effectProvider, Tag: "requests", Summary: "Approve a pending Seerr request (sent to Seerr)"},
		{Method: "POST", Path: "/api/v1/requests/{id}/decline", Handler: s.decideRequest(seerr.DecisionDecline), Auth: owner, Effect: effectProvider, Tag: "requests", Summary: "Decline a pending Seerr request (sent to Seerr)"},
		{Method: "POST", Path: "/api/v1/integrations/seerr/webhook", Handler: s.seerrWebhook, Auth: authSeerrWebhook, Effect: effectWrite, Tag: "requests", Summary: "Seerr webhook receiver (called by Seerr, not by agents)"},

		{Method: "GET", Path: "/api/v1/intelligence/releases", Handler: s.listReleaseCandidates, Auth: owner, Effect: effectRead, Tag: "releases", Summary: "Recently observed release candidates with their decisions, evidence time and last-seen time", Query: []apiParam{limitParam}, Response: []postgres.ReleaseCandidateSummary{}},
		{Method: "GET", Path: "/api/v1/acquisition-verifications", Handler: s.listSubtitleVerifications, Auth: owner, Effect: effectRead, Tag: "releases", Summary: "Subtitle/output verification results, newest first", Query: []apiParam{limitParam, {Name: "workflow_id", Type: "string", Format: "uuid", Description: "Only results for this workflow."}}},
		{Method: "GET", Path: "/api/v1/blocklist", Handler: s.listReleaseBlocks, Auth: owner, Effect: effectRead, Tag: "releases", Summary: "Blocked releases", Query: []apiParam{limitParam}},
		{Method: "POST", Path: "/api/v1/blocklist", Handler: s.createReleaseBlock, Auth: owner, Effect: effectWrite, Tag: "releases", Summary: "Block a release candidate", Body: createReleaseBlockInput{}, Required: []string{"candidate_id"}},
		{Method: "DELETE", Path: "/api/v1/blocklist/{id}", Handler: s.deleteReleaseBlock, Auth: owner, Effect: effectWrite, Tag: "releases", Summary: "Remove a release block"},

		{Method: "GET", Path: "/api/v1/files", Handler: s.listManagedFiles, Auth: owner, Effect: effectRead, Tag: "library", Summary: "Managed files (published link generations and adopted files)", Query: []apiParam{limitParam, offsetParam}, Response: []postgres.ManagedFile{}},
		{Method: "GET", Path: "/api/v1/files/{id}", Handler: s.getManagedFile, Auth: player, Effect: effectRead, Tag: "library", Summary: "One managed file with provenance and evidence", Response: postgres.ManagedFile{}},
		{Method: "GET", Path: "/api/v1/collections", Handler: s.listCollections, Auth: owner, Effect: effectRead, Tag: "library", Summary: "Collections", Query: []apiParam{limitParam}},
		{Method: "POST", Path: "/api/v1/collections", Handler: s.createCollection, Auth: owner, Effect: effectWrite, Tag: "library", Summary: "Create a collection", Body: createCollectionInput{}, Required: []string{"name", "media_ids"}},
		{Method: "POST", Path: "/api/v1/collections/preview", Handler: s.previewCollection, Auth: owner, Effect: effectRead, Tag: "library", Summary: "Preview a collection without saving it", Body: previewCollectionInput{}, Required: []string{"media_ids"}},
		{Method: "DELETE", Path: "/api/v1/collections/{id}", Handler: s.deleteCollection, Auth: owner, Effect: effectWrite, Tag: "library", Summary: "Delete a collection (media is not affected)"},
		{Method: "GET", Path: "/api/v1/recycle", Handler: s.listRecycle, Auth: owner, Effect: effectRead, Tag: "library", Summary: "Recycle bin entries"},
		{Method: "POST", Path: "/api/v1/recycle/{id}/restore", Handler: s.restoreRecycle, Auth: owner, Effect: effectWrite, Tag: "library", Summary: "Restore a recycled item's links"},
		{Method: "POST", Path: "/api/v1/recycle/{id}/restore-adopted", Handler: s.restoreAdoptedFiles, Auth: owner, Effect: effectDestructive, Tag: "library", Summary: "Restore a retained adopted file after Plex verification", Body: restoreAdoptedFilesInput{}, Required: []string{"confirmation"}, Description: "confirmation must be exactly \"" + postgres.AdoptedRestoreConfirmationPhrase + "\"."},
		{Method: "POST", Path: "/api/v1/recycle/{id}/confirm-purge", Handler: s.confirmRealFilePurge, Auth: owner, Effect: effectDestructive, Tag: "library", Summary: "Permanently purge a retained adopted file", Body: confirmRealFilePurgeInput{}, Required: []string{"confirmation"}, Description: "Irreversible. confirmation must be exactly \"" + postgres.RealFilePurgeConfirmation + "\"."},

		{Method: "GET", Path: "/api/v1/adoption", Handler: s.listAdoptionBatches, Auth: owner, Effect: effectRead, Tag: "adoption", Summary: "Adoption batches (importing existing files)", Query: []apiParam{limitParam}},
		{Method: "POST", Path: "/api/v1/adoption", Handler: s.createAdoptionBatch, Auth: owner, Effect: effectWrite, Tag: "adoption", Summary: "Scan a source root and propose an adoption batch", Body: createAdoptionBatchInput{}, Required: []string{"source_root", "root_id", "quality_profile_id"}},
		{Method: "GET", Path: "/api/v1/adoption/{id}", Handler: s.getAdoptionBatch, Auth: owner, Effect: effectRead, Tag: "adoption", Summary: "One adoption batch with its items"},
		{Method: "PATCH", Path: "/api/v1/adoption/{id}/items/{itemID}", Handler: s.reviewAdoptionItem, Auth: owner, Effect: effectWrite, Tag: "adoption", Summary: "Review one adoption item", Body: reviewAdoptionItemInput{}, Required: []string{"review_state"}},
		{Method: "POST", Path: "/api/v1/adoption/{id}/approve", Handler: s.approveAdoptionBatch, Auth: owner, Effect: effectWrite, Tag: "adoption", Summary: "Approve a reviewed adoption batch"},
		{Method: "POST", Path: "/api/v1/adoption/{id}/execute", Handler: s.executeAdoptionBatch, Auth: owner, Effect: effectDestructive, Tag: "adoption", Summary: "Execute an approved adoption batch", Body: executeAdoptionBatchInput{}, Required: []string{"confirmation"}, Description: "confirmation must be exactly \"EXECUTE REVIEWED ADOPTION\"; replacing an adopted file also needs adopted_replacement_confirmation \"" + postgres.AdoptedReplacementConfirmation + "\"."},

		{Method: "GET", Path: "/api/v1/discovery/search", Handler: s.discoverySearch, Auth: player, Effect: effectProvider, Tag: "discovery", Summary: "Search metadata providers for titles to add", Query: []apiParam{{Name: "query", Type: "string", Description: "Title to search for.", Required: true}, {Name: "page", Type: "integer", Description: "Result page (1-based)."}}},
		{Method: "GET", Path: "/api/v1/discovery/{kind}/{id}", Handler: s.discoveryMetadata, Auth: player, Effect: effectProvider, Tag: "discovery", Summary: "Provider metadata for one movie or series", Query: []apiParam{{Name: "season", Type: "integer", Description: "Include this season's episodes."}}},
		{Method: "GET", Path: "/api/v1/sources", Handler: s.listSources, Auth: owner, Effect: effectRead, Tag: "discovery", Summary: "Request sources (Seerr, import lists) and their status"},
		{Method: "GET", Path: "/api/v1/import-lists", Handler: s.listImportLists, Auth: owner, Effect: effectRead, Tag: "discovery", Summary: "Import lists"},
		{Method: "POST", Path: "/api/v1/import-lists", Handler: s.createImportList, Auth: owner, Effect: effectWrite, Tag: "discovery", Summary: "Create an import list", Body: createImportListInput{}, Required: []string{"name", "provider"}},
		{Method: "GET", Path: "/api/v1/import-lists/{id}", Handler: s.getImportList, Auth: owner, Effect: effectRead, Tag: "discovery", Summary: "One import list"},
		{Method: "POST", Path: "/api/v1/import-lists/{id}/snapshots", Handler: s.createImportListSnapshot, Auth: owner, Effect: effectProvider, Tag: "discovery", Summary: "Fetch a new snapshot of an import list"},

		{Method: "GET", Path: "/api/v1/system/health", Handler: s.systemHealth, Auth: owner, Effect: effectRead, Tag: "system", Summary: "Integration health, outbox and failure counters"},
		{Method: "GET", Path: "/api/v1/problems", Handler: s.listProblems, Auth: owner, Effect: effectRead, Tag: "system", Summary: "Grouped owner-actionable problems derived from persisted workflows, integrations, backups, notifications and repairs", Response: postgres.ProblemFeed{}},
		{Method: "GET", Path: "/api/v1/errors", Handler: s.listErrorLog, Auth: owner, Effect: effectRead, Tag: "system", Summary: "Everything that went wrong, newest first, with credentials removed"},
		{Method: "GET", Path: "/api/v1/problems/support", Handler: s.supportBundle, Auth: owner, Effect: effectRead, Tag: "system", Summary: "Redacted plain-text support bundle with problems, schema, components and recent backup/queue state"},
		{Method: "GET", Path: "/api/v1/system/queue", Handler: s.systemQueue, Auth: owner, Effect: effectRead, Tag: "system", Summary: "Background queue: due, scheduled, leased and retrying events per type, plus recent throughput", Response: postgres.QueueSnapshot{}},
		{Method: "GET", Path: "/api/v1/indexers", Handler: s.listIndexerUsage, Auth: owner, Effect: effectRead, Tag: "system", Summary: "Every indexer's effective budget, rolling-window usage, remaining requests, pacing and 24h search outcomes", Response: []postgres.IndexerUsage{}},
		{Method: "GET", Path: "/api/v1/system/tasks", Handler: s.listTasks, Auth: owner, Effect: effectRead, Tag: "system", Summary: "Background tasks"},
		{Method: "GET", Path: "/api/v1/system/info", Handler: s.systemInfo, Auth: owner, Effect: effectRead, Tag: "system", Summary: "Version, commit and schema"},
		{Method: "GET", Path: "/api/v1/backups", Handler: s.listBackups, Auth: owner, Effect: effectRead, Tag: "system", Summary: "Encrypted in-app backups"},
		{Method: "POST", Path: "/api/v1/backups", Handler: s.createBackup, Auth: owner, Effect: effectWrite, Tag: "system", Summary: "Queue an encrypted backup with isolated restore verification"},
		{Method: "GET", Path: "/api/v1/backups/{id}/download", Handler: s.downloadBackup, Auth: owner, Effect: effectRead, Tag: "system", Summary: "Download a verified backup file (binary)", Description: "Falls back to the verified off-site copy when the local file is unavailable; both paths verify the recorded size and SHA-256."},
		{Method: "POST", Path: "/api/v1/backups/{id}/offsite", Handler: s.retryBackupOffsite, Auth: owner, Effect: effectWrite, Tag: "system", Summary: "Queue a retry of the off-site copy for a verified backup"},
		{Method: "GET", Path: "/api/v1/notifications/deliveries", Handler: s.listNotificationDeliveries, Auth: owner, Effect: effectRead, Tag: "system", Summary: "Notification delivery log", Query: []apiParam{limitParam}},

		{Method: "GET", Path: "/api/v1/settings/integrations", Handler: s.listIntegrations, Auth: owner, Effect: effectRead, Tag: "settings", Summary: "Integrations (indexers, AltMount, Plex, Seerr) and their configuration"},
		{Method: "POST", Path: "/api/v1/settings/integrations", Handler: s.createIntegration, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Create a disabled integration", Body: postgres.Integration{}, Required: []string{"kind", "name"}},
		{Method: "PATCH", Path: "/api/v1/settings/integrations/{id}", Handler: s.updateIndexerSettings, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Change an indexer's name, daily limits, minimum interval, or disable it", Description: "Indexers only (newznab, oldboys). Limits become budget_source owner_stated; minimum_interval_ms is at least 3000 (OldBoys 5000). enabled=true needs a commissioned Newznab indexer; OldBoys is re-enabled only by its validation. Audited as integration.settings_changed.", Body: postgres.IndexerSettingsPatch{}, Response: postgres.Integration{}},
		{Method: "PUT", Path: "/api/v1/settings/integrations/{id}/secret", Handler: s.saveIntegrationSecret, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Save a connection's secret (Plex token, API key) encrypted; it is never returned", Body: saveSecretInput{}, Required: []string{"value"}, Description: "Stores the value AES-256-GCM encrypted and sets the integration's secret_ref to sealed:integration:<id>. AltMount session login takes {\"username\":\"...\",\"password\":\"...\"} as the value. 503 secret_storage_unavailable when SECRET_ENCRYPTION_KEY (or the OldBoys key) is not configured. OldBoys keeps its own credentials route."},
		{Method: "PATCH", Path: "/api/v1/settings/integrations/{id}/connection", Handler: s.updateIntegrationConnection, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Edit a Plex, AltMount or Seerr connection", Body: postgres.IntegrationConnectionPatch{}, Description: "Changes name, base_url, allow_http, enabled, is_default and, for AltMount, category, local_read_only_root and authentication. Indexers are changed with PATCH /api/v1/settings/integrations/{id}."},
		{Method: "POST", Path: "/api/v1/settings/integrations/{id}/check", Handler: s.checkIntegration, Auth: owner, Effect: effectProvider, Tag: "settings", Summary: "Check that a Plex, AltMount, Seerr or TMDB connection works", Response: connectionCheck{}, Description: "Plex: lists its libraries. AltMount: logs in and checks local_read_only_root is visible. Seerr: checks the API key. TMDB: checks the read access token and makes title search use it at once. Changes nothing else; ok is false with a detail when it does not work."},
		{Method: "GET", Path: "/api/v1/settings/integrations/{id}/plex-libraries", Handler: s.listPlexLibraries, Auth: owner, Effect: effectProvider, Tag: "settings", Summary: "The Plex libraries of a Plex connection, for creating a library root", Response: []acquisition.PlexLibrary{}},
		{Method: "GET", Path: "/api/v1/settings/integrations/{id}/seerr-webhook", Handler: s.getSeerrWebhook, Auth: owner, Effect: effectProvider, Tag: "settings", Summary: "Whether Seerr's webhook sends requests to this Media Manager", Response: seerrWebhookStatus{}, Description: "Reads Seerr's webhook settings with the connection's API key. managed is true only when Seerr sends Media Manager's events with this API's webhook secret. The authorization header is never returned."},
		{Method: "POST", Path: "/api/v1/settings/integrations/{id}/seerr-webhook", Handler: s.configureSeerrWebhook, Auth: owner, Effect: effectProvider, Tag: "settings", Summary: "Point Seerr's webhook at Media Manager", Body: configureSeerrWebhookInput{}, Required: []string{"webhook_url"}, Response: seerrWebhookStatus{}, Description: "Seerr first sends a test notification with the new settings to webhook_url; they are saved in Seerr only when this API answered it, and read back before success. 409 intake_not_running until the API runs Seerr intake for this connection. Other Seerr notification types and custom headers are kept."},
		{Method: "POST", Path: "/api/v1/setup/plex/login", Handler: s.startPlexLogin, Auth: owner, Effect: effectProvider, Tag: "settings", Summary: "Start \"Log ind med Plex\": a Plex PIN login for the owner to approve", Response: plexlogin.Started{}, Description: "Open auth_url in a browser and approve Media Manager on Plex. The login is kept in memory for at most 20 minutes; the Plex token is never returned. 503 secret_storage_unavailable when the token could not be saved encrypted."},
		{Method: "GET", Path: "/api/v1/setup/plex/login/{id}", Handler: s.pollPlexLogin, Auth: owner, Effect: effectProvider, Tag: "settings", Summary: "Whether the Plex login was approved, and the account's servers", Response: plexlogin.Status{}, Description: "state is waiting until the owner approves the login on Plex, then approved with the account's Plex Media Servers. Every address is checked from the API once: reachable is true only when the server with that identity answers there. 404 plex_login_expired when the login is unknown or expired."},
		{Method: "POST", Path: "/api/v1/setup/plex/login/{id}/connect", Handler: s.connectPlexLogin, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Save the chosen Plex server as a Plex connection", Body: plexLoginConnectInput{}, Required: []string{"server_id"}, Response: plexLoginConnected{}, Description: "Uses uri when given (it must be reachable), otherwise the recommended address. Creates an enabled Plex connection (the default when none is) or updates integration_id, or the connection that already has this address; saves the token sealed and runs the connection check. The login is used up."},
		{Method: "POST", Path: "/api/v1/setup/recommended", Handler: s.recommendedSetup, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "\"Brug anbefalet opsætning\": create the missing default library roots and quality profiles", Body: recommendedSetupInput{}, Response: quicksetup.Report{}, Description: "For a media kind without a default library root, uses the first ordinary (non-4K) Plex library of that kind. The folder Media Manager writes is proven, not guessed: Media Manager creates an empty marker folder (mm-opsaetning-tjek-…) in each folder it may write, asks Plex to list the library's folder, keeps the one whose marker Plex shows and removes every marker. Where Plex sees AltMount is proven by comparing the folders both list. For a kind without a default quality profile, creates \"Dansk tekst eller tale\" (Balanced preset, Danish subtitles or Danish speech required). Existing settings are never changed. With apply false (also creates and removes the markers) it only reports; every step that cannot be done says why."},
		{Method: "GET", Path: "/api/v1/setup/status", Handler: s.setupStatus, Auth: owner, Effect: effectRead, Tag: "settings", Summary: "What is still missing before Media Manager can acquire titles", Response: setupStatus{}},
		{Method: "POST", Path: "/api/v1/settings/integrations/{id}/commission", Handler: s.commissionIntegration, Auth: owner, Effect: effectProvider, Tag: "settings", Summary: "Commission and enable a Newznab indexer (bounded live checks)"},
		{Method: "GET", Path: "/api/v1/settings/integrations/{id}/seerr-routes", Handler: s.listSeerrRoutes, Auth: owner, Effect: effectRead, Tag: "settings", Summary: "Seerr root-folder routing for this Seerr integration"},
		{Method: "POST", Path: "/api/v1/settings/integrations/{id}/seerr-routes", Handler: s.saveSeerrRoute, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Map a Seerr root folder to a library root and quality profile", Body: saveSeerrRouteInput{}, Required: []string{"media_kind", "root_folder", "library_root_id", "quality_profile_id"}},
		{Method: "DELETE", Path: "/api/v1/settings/integrations/{id}/seerr-routes/{route}", Handler: s.deleteSeerrRoute, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Delete a Seerr route"},
		{Method: "GET", Path: "/api/v1/settings/oldboys/credentials", Handler: s.oldBoysCredentialsStatus, Auth: owner, Effect: effectRead, Tag: "settings", Summary: "OldBoys credential status (never returns the credentials)"},
		{Method: "PUT", Path: "/api/v1/settings/oldboys/credentials", Handler: s.saveOldBoysCredentials, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Replace the OldBoys API token, PID and RID (disables the provider until recommissioned)", Body: oldboyscredentials.Credentials{}, Required: []string{"api_token", "pass_key", "rss_key"}},
		{Method: "PUT", Path: "/api/v1/settings/oldboys/budget", Handler: s.setOldBoysBudget, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Set OldBoys daily search and download limits", Body: setOldBoysBudgetInput{}, Required: []string{"query_limit"}, Description: "grab_limit is optional and keeps the current value."},
		{Method: "POST", Path: "/api/v1/settings/oldboys/commission", Handler: s.commissionOldBoys, Auth: owner, Effect: effectProvider, Tag: "settings", Summary: "Validate OldBoys with five bounded live checks and enable it"},
		{Method: "GET", Path: "/api/v1/repairs", Handler: s.listAltMountRepairs, Auth: owner, Effect: effectRead, Tag: "library", Summary: "AltMount repairs: published files whose Usenet articles disappeared and their replacement workflows", Description: "Media Manager checks AltMount's corrupted-file list every 30 minutes and acquires a replacement for each broken movie or episode it published. state is open (replacement being acquired), repaired (replacement confirmed in Plex, broken file retired) or needs_attention (cannot be repaired automatically; detail says why)."},
		{Method: "POST", Path: "/api/v1/repairs", Handler: s.requestAltMountReplacement, Auth: owner, Effect: effectProvider, Tag: "library", Summary: "Replace a published file that plays badly", Body: requestReplacementInput{}, Required: []string{"altmount_file_path"}, Response: replacementResult{}, Description: "For a file AltMount does not report but that plays badly (for example slow seeking because its articles come from a backup provider). altmount_file_path is the file's path as AltMount reports it, e.g. media-manager/<release>/<file>.mkv. The website also supplies file_id to bind the request to that exact current publication; a mismatched or stale ID is 404. Opens a repair (reason owner_requested) that acquires a replacement under the publication's profile, excluding the current release title; the old file keeps playing until Plex confirms the replacement. outcome is opened, attached (an open workflow already acquires that title), already_open or needs_attention (detail in GET /api/v1/repairs). 404 when the path backs no current publication, 409 while the publishing workflow is still in flight."},
		{Method: "GET", Path: "/api/v1/settings/library-roots", Handler: s.listLibraryRoots, Auth: owner, Effect: effectRead, Tag: "settings", Summary: "Library roots"},
		{Method: "POST", Path: "/api/v1/settings/library-roots", Handler: s.createLibraryRoot, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Create a library root", Body: postgres.LibraryRoot{}},
		{Method: "PATCH", Path: "/api/v1/settings/library-roots/{id}", Handler: s.updateLibraryRoot, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Make a library root the default for its media kind, or enable/disable it", Description: "Making a root the default clears the default of the other roots of the same kind; requests and manual adds without an explicit root land there. Disabling is refused while the root is the default, has open workflows, holds current published links, or an enabled Seerr route points to it (relocate-links first). Audited as library.root_updated.", Body: postgres.LibraryRootPatch{}, Response: postgres.LibraryRoot{}},
		{Method: "GET", Path: "/api/v1/settings/quality-profiles", Handler: s.listQualityProfiles, Auth: owner, Effect: effectRead, Tag: "settings", Summary: "Quality profiles"},
		{Method: "POST", Path: "/api/v1/settings/quality-profiles", Handler: s.createQualityProfile, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Create a quality profile", Body: postgres.QualityProfile{}},
		{Method: "PATCH", Path: "/api/v1/settings/quality-profiles/{id}", Handler: s.updateQualityProfile, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Update a quality profile (applies to new rounds only)", Description: "Optimistic: policy_version is the version the editor read and is required. A stale version returns 409 quality_profile_changed and writes nothing, so an edit cannot overwrite a newer policy or its required rules; reload the profile and repeat the edit. A successful edit bumps policy_version by one. Existing acquisition rounds keep their snapshotted policy until the owner calls apply-policy.", Body: postgres.QualityProfile{}, Required: []string{"policy_version"}},
		{Method: "GET", Path: "/api/v1/settings/quality-profile-presets", Handler: s.listQualityProfilePresets, Auth: owner, Effect: effectRead, Tag: "settings", Summary: "Quality profile presets", Description: "Opt-in soft-scoring presets. They never replace required rules, scope, identity gates or required languages.", Response: []releases.PresetDefinition{}},
		{Method: "POST", Path: "/api/v1/settings/quality-profiles/{id}/preset", Handler: s.applyQualityProfilePreset, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Apply a soft quality preset to a profile", Description: "Replaces only preferred rules for the preset's managed facts. Required rules, language requirements and unrelated preferences are preserved. Existing acquisition rounds keep their snapshotted policy until the owner calls apply-policy.", Body: applyQualityProfilePresetInput{}, Required: []string{"preset", "policy_version"}},
		{Method: "POST", Path: "/api/v1/settings/quality-profiles/{id}/apply-policy", Handler: s.applyQualityProfilePolicy, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Apply the current profile policy to eligible existing workflows", Description: "Workflows with active AltMount work, a started publication or recovery-only attention are skipped and reported."},
		{Method: "GET", Path: "/api/v1/settings/notifications", Handler: s.listNotificationDestinations, Auth: owner, Effect: effectRead, Tag: "settings", Summary: "Notification destinations"},
		{Method: "POST", Path: "/api/v1/settings/notifications", Handler: s.createNotificationDestination, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Create a notification destination", Body: createNotificationDestinationInput{}, Required: []string{"name", "endpoint", "events"}},
		{Method: "PATCH", Path: "/api/v1/settings/notifications/{id}", Handler: s.setNotificationDestinationState, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Enable or disable a notification destination", Body: setNotificationDestinationStateInput{}, Required: []string{"enabled"}},

		{Method: "POST", Path: "/api/v1/imports/prowlarr", Handler: s.createProwlarrImport, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Upload a Prowlarr backup to propose indexer imports", Body: rawBody{ContentType: "application/octet-stream", Description: "Prowlarr backup archive, at most 128 MiB."}},
		{Method: "GET", Path: "/api/v1/imports/prowlarr/{id}", Handler: s.getProwlarrImport, Auth: owner, Effect: effectRead, Tag: "settings", Summary: "One Prowlarr import proposal"},
		{Method: "POST", Path: "/api/v1/imports/prowlarr/{id}/approve", Handler: s.approveProwlarrImport, Auth: owner, Effect: effectWrite, Tag: "settings", Summary: "Approve selected Prowlarr indexer candidates as disabled integrations", Body: approveProwlarrImportInput{}, Required: []string{"candidates"}},

		{Method: "POST", Path: "/api/v1/settings/integrations/{id}/commissioning-v2/runs", Handler: s.createCommissioningV2Run, Auth: authOwnerBrowser, Effect: effectWrite, Tag: "commissioning", Summary: "Start a commissioning v2 run from fixtures", Body: commissioningv2.FixtureDocument{}},
		{Method: "GET", Path: "/api/v1/settings/integrations/{id}/commissioning-v2/runs/{run}", Handler: s.getCommissioningV2Run, Auth: owner, Effect: effectRead, Tag: "commissioning", Summary: "One commissioning v2 run"},
		{Method: "POST", Path: "/api/v1/settings/integrations/{id}/commissioning-v2/runs/{run}/authorize-caps", Handler: s.authorizeCommissioningV2CAPS, Auth: authOwnerBrowser, Effect: effectProvider, Tag: "commissioning", Summary: "Authorize the CAPS probe for a run", Body: expectedRevisionInput{}, Required: []string{"expected_revision"}},
		{Method: "POST", Path: "/api/v1/settings/integrations/{id}/commissioning-v2/runs/{run}/categories", Handler: s.sealCommissioningV2SearchPlan, Auth: authOwnerBrowser, Effect: effectWrite, Tag: "commissioning", Summary: "Seal the run's category search plan", Body: sealCommissioningV2SearchPlanInput{}, Required: []string{"expected_revision"}},
		{Method: "POST", Path: "/api/v1/settings/integrations/{id}/commissioning-v2/runs/{run}/authorize-searches", Handler: s.authorizeCommissioningV2Searches, Auth: authOwnerBrowser, Effect: effectProvider, Tag: "commissioning", Summary: "Authorize the run's live searches", Body: expectedRevisionInput{}, Required: []string{"expected_revision"}},
		{Method: "POST", Path: "/api/v1/settings/integrations/{id}/commissioning-v2/runs/{run}/steps/{step}/execute", Handler: s.executeCommissioningV2CAPS, Auth: authOwnerBrowser, Effect: effectProvider, Tag: "commissioning", Summary: "Execute one authorized live step"},
		{Method: "POST", Path: "/api/v1/settings/integrations/{id}/commissioning-v2/runs/{run}/draft", Handler: s.finalizeCommissioningV2Draft, Auth: authOwnerBrowser, Effect: effectWrite, Tag: "commissioning", Summary: "Finalize the capability profile draft", Body: expectedRevisionInput{}, Required: []string{"expected_revision"}},
		{Method: "GET", Path: "/api/v1/settings/integrations/{id}/commissioning-v2/runs/{run}/draft", Handler: s.getCommissioningV2Draft, Auth: owner, Effect: effectRead, Tag: "commissioning", Summary: "The run's capability profile draft"},
		{Method: "POST", Path: "/api/v1/settings/integrations/{id}/commissioning-v2/runs/{run}/activate", Handler: s.activateCommissioningV2Profile, Auth: authOwnerBrowser, Effect: effectWrite, Tag: "commissioning", Summary: "Activate the finalized capability profile", Body: expectedRevisionInput{}, Required: []string{"expected_revision"}},
	}
}

func (server *Server) routes() {
	for _, operation := range server.apiOperations() {
		server.mux.HandleFunc(operation.Method+" "+operation.Path, server.authorize(operation.Auth, operation.Handler))
	}
	server.registerServarrRoutes()
}

func (server *Server) authorize(auth apiAuth, handler http.HandlerFunc) http.HandlerFunc {
	switch auth {
	case authOwner:
		return server.requireOwner(handler)
	case authOwnerBrowser:
		return server.requireOwnerMutation(handler)
	case authOwnerOrPlayer:
		return server.requireOwnerOrPlayer(handler)
	case authNone, authSeerrWebhook:
		// The webhook checks its own shared secret inside the handler.
		return handler
	}
	panic("unknown API auth mode " + string(auth))
}
