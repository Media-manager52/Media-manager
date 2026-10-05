package httpapi

// Named request bodies. Each is decoded with DisallowUnknownFields and is also
// the source of the request schema published in /api/v1/openapi.json.

import (
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
)

type createImportListInput struct {
	Name                  string `json:"name"`
	Provider              string `json:"provider"`
	Enabled               bool   `json:"enabled"`
	MovieRootID           string `json:"movie_root_id"`
	MovieQualityProfileID string `json:"movie_quality_profile_id"`
}

type createNotificationDestinationInput struct {
	Name      string   `json:"name"`
	Endpoint  string   `json:"endpoint"`
	AllowHTTP bool     `json:"allow_http"`
	Enabled   bool     `json:"enabled"`
	Events    []string `json:"events"`
	SecretRef string   `json:"secret_ref"`
}

type setNotificationDestinationStateInput struct {
	Enabled *bool `json:"enabled"`
}

type previewCollectionInput struct {
	MediaIDs []string `json:"media_ids"`
}

type createCollectionInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	MediaIDs    []string `json:"media_ids"`
}

type createAdoptionBatchInput struct {
	SourceRoot        string `json:"source_root"`
	RootID            string `json:"root_id"`
	QualityProfileID  string `json:"quality_profile_id"`
	MonitoringEnabled bool   `json:"monitoring_enabled"`
	SearchImmediately bool   `json:"search_immediately"`
}

type reviewAdoptionItemInput struct {
	ReviewState       string `json:"review_state"`
	MediaID           string `json:"media_id"`
	RootID            string `json:"root_id"`
	QualityProfileID  string `json:"quality_profile_id"`
	ProposedRelative  string `json:"proposed_relative"`
	Variant           string `json:"variant"`
	MonitoringEnabled bool   `json:"monitoring_enabled"`
	SearchImmediately bool   `json:"search_immediately"`
}

type executeAdoptionBatchInput struct {
	Confirmation                   string `json:"confirmation"`
	AdoptedReplacementConfirmation string `json:"adopted_replacement_confirmation"`
}

type createReleaseBlockInput struct {
	CandidateID   string `json:"candidate_id"`
	Scope         string `json:"scope"`
	Reason        string `json:"reason"`
	ExpiresAt     string `json:"expires_at"`
	RetentionDays int    `json:"retention_days"`
}

type approveProwlarrImportInput struct {
	Candidates []struct {
		ID        string `json:"id"`
		SecretRef string `json:"secret_ref"`
	} `json:"candidates"`
}

type createMediaWorkflowInput struct {
	RootID                         string `json:"root_id"`
	QualityProfileID               string `json:"quality_profile_id"`
	DownloadClientID               string `json:"download_client_id"`
	AdoptedReplacementConfirmation string `json:"adopted_replacement_confirmation"`
	SearchImmediately              *bool  `json:"search_immediately"`
	Season                         *struct {
		Number            uint16                   `json:"number"`
		Episodes          []postgres.ManualEpisode `json:"episodes"`
		Monitored         *bool                    `json:"monitored"`
		SearchImmediately *bool                    `json:"search_immediately"`
		PreferSeasonPack  *bool                    `json:"prefer_season_pack"`
	} `json:"season"`
}

type searchWorkflowInput struct {
	AdoptedReplacementConfirmation string `json:"adopted_replacement_confirmation"`
}

type relocateWorkflowLinksInput struct {
	RootID string `json:"root_id"`
}

type reverifyRetainedOutputInput struct {
	JobID string `json:"job_id"`
}

type createMediaInput struct {
	Kind              catalog.MediaKind          `json:"kind"`
	Title             string                     `json:"title"`
	Year              *uint16                    `json:"year"`
	Identities        []catalog.ExternalIdentity `json:"identities"`
	Metadata          *catalog.MediaMetadata     `json:"metadata"`
	RootID            string                     `json:"root_id"`
	QualityProfileID  string                     `json:"quality_profile_id"`
	DownloadClientID  string                     `json:"download_client_id"`
	SearchImmediately *bool                      `json:"search_immediately"`
	Seasons           []struct {
		Number            uint16                   `json:"number"`
		Episodes          []postgres.ManualEpisode `json:"episodes"`
		Monitored         *bool                    `json:"monitored"`
		SearchImmediately *bool                    `json:"search_immediately"`
		PreferSeasonPack  *bool                    `json:"prefer_season_pack"`
	} `json:"seasons"`
}

type confirmRealFilePurgeInput struct {
	Confirmation string `json:"confirmation"`
}

type restoreAdoptedFilesInput struct {
	Confirmation string `json:"confirmation"`
}

type retryAccessRejectedReleaseInput struct {
	CandidateID string `json:"candidate_id"`
	Revision    uint64 `json:"revision"`
}

type reinspectRetainedMovieInput struct {
	JobID string `json:"job_id"`
}

type sealCommissioningV2SearchPlanInput struct {
	ExpectedRevision              uint64 `json:"expected_revision"`
	MovieCategoryIDs              []int  `json:"movie_category_ids"`
	SeriesCategoryIDs             []int  `json:"series_category_ids"`
	IncludeMovieFallback          bool   `json:"include_movie_fallback"`
	IncludeCompleteSeasonFallback bool   `json:"include_complete_season_fallback"`
}

type expectedRevisionInput struct {
	ExpectedRevision uint64 `json:"expected_revision"`
}

type saveSeerrRouteInput struct {
	MediaKind        string `json:"media_kind"`
	RootFolder       string `json:"root_folder"`
	LibraryRootID    string `json:"library_root_id"`
	QualityProfileID string `json:"quality_profile_id"`
}

type setOldBoysBudgetInput struct {
	QueryLimit int `json:"query_limit"`
	GrabLimit  int `json:"grab_limit"`
}
