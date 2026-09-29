package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/languages"
	"github.com/media-manager/media-manager/internal/servarr"
)

// A Seerr request carries the service root folder and profile the requester
// chose. Media Manager only knows its own library roots, so an owner-created
// route connects the two. When a request carries a folder without an enabled
// route, intake pauses instead of publishing into the wrong library.
var (
	ErrSeerrRouteUnmapped          = errors.New("Seerr request root folder has no Media Manager library route")
	ErrSeerrRouteTargetUnavailable = errors.New("the library root or quality profile mapped to this Seerr folder is unavailable")
)

type SeerrLibraryRoute struct {
	ID               uuid.UUID         `json:"id"`
	SeerrInstanceID  uuid.UUID         `json:"seerr_instance_id"`
	MediaKind        catalog.MediaKind `json:"media_kind"`
	RootFolder       string            `json:"root_folder"`
	LibraryRootID    uuid.UUID         `json:"library_root_id"`
	QualityProfileID *uuid.UUID        `json:"quality_profile_id,omitempty"`
	Enabled          bool              `json:"enabled"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// SeerrObservedFolder reports a root folder seen on a persisted Seerr request
// together with its current route, so the owner UI can prompt for the folders
// that still need mapping.
type SeerrObservedFolder struct {
	MediaKind        catalog.MediaKind `json:"media_kind"`
	RootFolder       string            `json:"root_folder"`
	RequestCount     int64             `json:"request_count"`
	LatestAt         time.Time         `json:"latest_at"`
	RouteID          *uuid.UUID        `json:"route_id,omitempty"`
	LibraryRootID    *uuid.UUID        `json:"library_root_id,omitempty"`
	QualityProfileID *uuid.UUID        `json:"quality_profile_id,omitempty"`
	Routed           bool              `json:"routed"`
}

func (store *Store) ListSeerrLibraryRoutes(ctx context.Context, instanceID uuid.UUID) ([]SeerrLibraryRoute, error) {
	rows, err := store.Pool.Query(ctx, `SELECT id::text,seerr_instance_id::text,media_kind,root_folder,library_root_id::text,COALESCE(quality_profile_id::text,''),enabled,created_at,updated_at FROM seerr_library_routes WHERE seerr_instance_id=$1 ORDER BY media_kind,root_folder`, instanceID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var routes []SeerrLibraryRoute
	for rows.Next() {
		var route SeerrLibraryRoute
		var idText, instanceText, rootText, profileText string
		if err := rows.Scan(&idText, &instanceText, &route.MediaKind, &route.RootFolder, &rootText, &profileText, &route.Enabled, &route.CreatedAt, &route.UpdatedAt); err != nil {
			return nil, err
		}
		parsed, err := uuid.Parse(idText)
		if err != nil {
			return nil, err
		}
		route.ID = parsed
		if route.SeerrInstanceID, err = uuid.Parse(instanceText); err != nil {
			return nil, err
		}
		if route.LibraryRootID, err = uuid.Parse(rootText); err != nil {
			return nil, err
		}
		if profileText != "" {
			profile, err := uuid.Parse(profileText)
			if err != nil {
				return nil, err
			}
			route.QualityProfileID = &profile
		}
		routes = append(routes, route)
	}
	return routes, rows.Err()
}

func (store *Store) ListSeerrObservedFolders(ctx context.Context, instanceID uuid.UUID) ([]SeerrObservedFolder, error) {
	rows, err := store.Pool.Query(ctx, `SELECT snapshot.media_kind,snapshot.seerr_root_folder,count(*),max(snapshot.reconciled_at),route.id::text,route.library_root_id::text,COALESCE(route.quality_profile_id::text,''),route.enabled
		FROM seerr_request_snapshots snapshot
		LEFT JOIN seerr_library_routes route ON route.seerr_instance_id=snapshot.seerr_instance_id AND route.media_kind=snapshot.media_kind AND route.root_folder=snapshot.seerr_root_folder
		WHERE snapshot.seerr_instance_id=$1 AND snapshot.seerr_root_folder IS NOT NULL AND snapshot.seerr_root_folder <> ''
		GROUP BY snapshot.media_kind,snapshot.seerr_root_folder,route.id,route.library_root_id,route.quality_profile_id,route.enabled
		ORDER BY snapshot.media_kind,snapshot.seerr_root_folder`, instanceID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var folders []SeerrObservedFolder
	for rows.Next() {
		var folder SeerrObservedFolder
		var idText, rootText, profileText *string
		var routed *bool
		if err := rows.Scan(&folder.MediaKind, &folder.RootFolder, &folder.RequestCount, &folder.LatestAt, &idText, &rootText, &profileText, &routed); err != nil {
			return nil, err
		}
		if routed != nil {
			folder.Routed = *routed
		}
		if idText != nil {
			parsed, err := uuid.Parse(*idText)
			if err != nil {
				return nil, err
			}
			folder.RouteID = &parsed
		}
		if rootText != nil {
			parsed, err := uuid.Parse(*rootText)
			if err != nil {
				return nil, err
			}
			folder.LibraryRootID = &parsed
		}
		if profileText != nil && *profileText != "" {
			parsed, err := uuid.Parse(*profileText)
			if err != nil {
				return nil, err
			}
			folder.QualityProfileID = &parsed
		}
		folders = append(folders, folder)
	}
	return folders, rows.Err()
}

// UpsertSeerrLibraryRoute creates or updates the mapping for one Seerr root
// folder. The unique key is (instance, media kind, root folder), so saving the
// same folder again replaces its target and re-enables the route.
func (store *Store) UpsertSeerrLibraryRoute(ctx context.Context, route SeerrLibraryRoute, actor string) (SeerrLibraryRoute, error) {
	if route.MediaKind != catalog.KindMovie && route.MediaKind != catalog.KindSeries {
		return SeerrLibraryRoute{}, errors.New("Seerr route requires a movie or series media kind")
	}
	route.RootFolder = strings.TrimSpace(route.RootFolder)
	if route.RootFolder == "" || len(route.RootFolder) > 512 || strings.ContainsAny(route.RootFolder, "\x00\n\r") {
		return SeerrLibraryRoute{}, errors.New("Seerr root folder is invalid")
	}
	if route.LibraryRootID == (uuid.UUID{}) {
		return SeerrLibraryRoute{}, errors.New("Seerr route requires a library root")
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return SeerrLibraryRoute{}, err
	}
	defer transaction.Rollback(ctx)
	var kind string
	if err := transaction.QueryRow(ctx, `SELECT kind FROM integration_instances WHERE id=$1`, route.SeerrInstanceID.String()).Scan(&kind); errors.Is(err, pgx.ErrNoRows) {
		return SeerrLibraryRoute{}, ErrNotFound
	} else if err != nil {
		return SeerrLibraryRoute{}, err
	}
	if kind != "seerr" {
		return SeerrLibraryRoute{}, errors.New("integration is not a Seerr instance")
	}
	var rootKind catalog.MediaKind
	if err := transaction.QueryRow(ctx, `SELECT media_kind FROM library_roots WHERE id=$1`, route.LibraryRootID.String()).Scan(&rootKind); errors.Is(err, pgx.ErrNoRows) {
		return SeerrLibraryRoute{}, ErrNotFound
	} else if err != nil {
		return SeerrLibraryRoute{}, err
	}
	if rootKind != route.MediaKind {
		return SeerrLibraryRoute{}, errors.New("library root media kind does not match the route")
	}
	var profileValue any
	if route.QualityProfileID != nil {
		var profileKind catalog.MediaKind
		if err := transaction.QueryRow(ctx, `SELECT media_kind FROM quality_profiles WHERE id=$1`, route.QualityProfileID.String()).Scan(&profileKind); errors.Is(err, pgx.ErrNoRows) {
			return SeerrLibraryRoute{}, ErrNotFound
		} else if err != nil {
			return SeerrLibraryRoute{}, err
		}
		if profileKind != route.MediaKind {
			return SeerrLibraryRoute{}, errors.New("quality profile media kind does not match the route")
		}
		profileValue = route.QualityProfileID.String()
	}
	now := time.Now().UTC()
	if route.ID == (uuid.UUID{}) {
		route.ID = uuid.NewV7()
	}
	var idText string
	if err := transaction.QueryRow(ctx, `INSERT INTO seerr_library_routes(id,seerr_instance_id,media_kind,root_folder,library_root_id,quality_profile_id,enabled,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,true,$7,$7)
		ON CONFLICT (seerr_instance_id,media_kind,root_folder) DO UPDATE SET library_root_id=EXCLUDED.library_root_id,quality_profile_id=EXCLUDED.quality_profile_id,enabled=true,updated_at=EXCLUDED.updated_at
		RETURNING id::text,created_at`, route.ID.String(), route.SeerrInstanceID.String(), route.MediaKind, route.RootFolder, route.LibraryRootID.String(), profileValue, now).Scan(&idText, &route.CreatedAt); err != nil {
		return SeerrLibraryRoute{}, err
	}
	parsed, err := uuid.Parse(idText)
	if err != nil {
		return SeerrLibraryRoute{}, err
	}
	route.ID = parsed
	route.Enabled = true
	route.UpdatedAt = now
	detail, _ := json.Marshal(map[string]string{"media_kind": string(route.MediaKind), "root_folder": route.RootFolder, "library_root_id": route.LibraryRootID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,$2,'seerr.route_saved','seerr_library_route',$3,$4,$5)`, uuid.NewV7().String(), actor, route.ID.String(), string(detail), now); err != nil {
		return SeerrLibraryRoute{}, err
	}
	return route, transaction.Commit(ctx)
}

func (store *Store) DeleteSeerrLibraryRoute(ctx context.Context, instanceID, routeID uuid.UUID, actor string) error {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(ctx)
	command, err := transaction.Exec(ctx, `DELETE FROM seerr_library_routes WHERE id=$1 AND seerr_instance_id=$2`, routeID.String(), instanceID.String())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	detail, _ := json.Marshal(map[string]string{"route_id": routeID.String()})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,$2,'seerr.route_removed','seerr_library_route',$3,$4,$5)`, uuid.NewV7().String(), actor, routeID.String(), string(detail), time.Now().UTC()); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

// resolveSeerrLibraryRoute returns the library root and optional quality
// profile mapped to the Seerr root folder carried by the request. An empty
// profile means the caller keeps the existing original-language routing.
func resolveSeerrLibraryRoute(ctx context.Context, transaction pgx.Tx, intake SeerrIntake) (string, string, error) {
	if servarr.IsAutoRootPath(intake.RootFolder) {
		return resolveSeerrAutoRoute(ctx, transaction, intake)
	}
	var routeRoot, routeProfile string
	err := transaction.QueryRow(ctx, `SELECT library_root_id::text,COALESCE(quality_profile_id::text,'') FROM seerr_library_routes WHERE seerr_instance_id=$1 AND media_kind=$2 AND root_folder=$3 AND enabled`, intake.InstanceID, intake.Kind, intake.RootFolder).Scan(&routeRoot, &routeProfile)
	if errors.Is(err, pgx.ErrNoRows) {
		// Media Manager can itself be the Radarr/Sonarr server Seerr talks to
		// (see internal/servarr). In that mode Seerr sends the library root's
		// destination path as rootFolderPath, so match it directly.
		err = transaction.QueryRow(ctx, `SELECT root.id::text FROM library_roots root JOIN integration_instances plex ON plex.id=root.plex_instance_id AND plex.kind='plex' AND plex.enabled WHERE root.media_kind=$1 AND root.destination_root=$2 AND root.enabled AND root.plex_library_id IS NOT NULL ORDER BY root.id LIMIT 1`, intake.Kind, intake.RootFolder).Scan(&routeRoot)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrSeerrRouteUnmapped
		}
		if err != nil {
			return "", "", err
		}
		routeProfile = ""
	} else if err != nil {
		return "", "", err
	}
	var rootReady bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM library_roots root JOIN integration_instances plex ON plex.id=root.plex_instance_id AND plex.kind='plex' AND plex.enabled WHERE root.id=$1 AND root.media_kind=$2 AND root.enabled AND root.plex_library_id IS NOT NULL)`, routeRoot, intake.Kind).Scan(&rootReady); err != nil {
		return "", "", err
	}
	if !rootReady {
		return "", "", ErrSeerrRouteTargetUnavailable
	}
	if routeProfile != "" {
		var profileReady bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM quality_profiles WHERE id=$1 AND media_kind=$2 AND enabled)`, routeProfile, intake.Kind).Scan(&profileReady); err != nil {
			return "", "", err
		}
		if !profileReady {
			return "", "", ErrSeerrRouteTargetUnavailable
		}
	}
	return routeRoot, routeProfile, nil
}

const (
	autoRouteKids        = "kids"
	autoRouteDocumentary = "documentary"
	autoRouteHoliday     = "holiday"
	autoRouteDanish      = "danish"
	autoRouteDefault     = "default"
)

// classifySeerrAutoRoute maps metadata to the Auto routing category. The
// order is deliberate: kids content wins over holiday and original language.
func classifySeerrAutoRoute(metadata *catalog.MediaMetadata) string {
	if metadata == nil {
		return autoRouteDefault
	}
	for _, genre := range metadata.Genres {
		switch strings.ToLower(strings.TrimSpace(genre)) {
		case "family", "kids":
			return autoRouteKids
		}
	}
	for _, genre := range metadata.Genres {
		if strings.EqualFold(strings.TrimSpace(genre), "documentary") {
			return autoRouteDocumentary
		}
	}
	for _, keyword := range metadata.Keywords {
		normalized := strings.ToLower(strings.TrimSpace(keyword))
		if strings.Contains(normalized, "christmas") || normalized == "holiday" {
			return autoRouteHoliday
		}
	}
	if language, reliable := languages.Canonical(metadata.OriginalLanguage); reliable && language == "da" {
		return autoRouteDanish
	}
	return autoRouteDefault
}

// resolveSeerrAutoRoute maps the single Auto root to a themed library root
// and, for kids content, to the profile that does not require Danish subtitles.
func resolveSeerrAutoRoute(ctx context.Context, transaction pgx.Tx, intake SeerrIntake) (string, string, error) {
	category := classifySeerrAutoRoute(intake.Metadata)
	rootText, err := selectAutoRouteRoot(ctx, transaction, intake.Kind, autoRouteRootName(intake.Kind, category))
	if err != nil {
		return "", "", err
	}
	if category != autoRouteKids {
		return rootText, "", nil
	}
	profileText, err := selectNoRequiredSubtitleProfile(ctx, transaction, intake.Kind)
	if err != nil {
		return "", "", err
	}
	return rootText, profileText, nil
}

// autoRouteRootName returns the themed library root name for a category. An
// empty name means the kind's default root.
func autoRouteRootName(kind catalog.MediaKind, category string) string {
	if kind == catalog.KindSeries {
		switch category {
		case autoRouteKids:
			return "Børn Serier"
		case autoRouteDocumentary:
			return "Dokumentar Serie"
		case autoRouteHoliday:
			return "Julekalender"
		case autoRouteDanish:
			return "Dansk Tv-Serier"
		}
		return ""
	}
	switch category {
	case autoRouteKids:
		return "Børn Film"
	case autoRouteDocumentary:
		return "Dokumentar"
	case autoRouteHoliday:
		return "Julefilm"
	case autoRouteDanish:
		return "Danske Film"
	}
	return ""
}

// selectAutoRouteRoot resolves a themed root by name and falls back to the
// kind's default root when the themed root is missing or unavailable.
func selectAutoRouteRoot(ctx context.Context, transaction pgx.Tx, kind catalog.MediaKind, name string) (string, error) {
	var rootText string
	if strings.TrimSpace(name) != "" {
		err := transaction.QueryRow(ctx, `SELECT root.id::text FROM library_roots root JOIN integration_instances plex ON plex.id=root.plex_instance_id AND plex.kind='plex' AND plex.enabled WHERE root.media_kind=$1 AND root.name=$2 AND root.enabled AND root.plex_library_id IS NOT NULL ORDER BY root.id LIMIT 1`, kind, name).Scan(&rootText)
		if err == nil {
			return rootText, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	err := transaction.QueryRow(ctx, `SELECT root.id::text FROM library_roots root JOIN integration_instances plex ON plex.id=root.plex_instance_id AND plex.kind='plex' AND plex.enabled WHERE root.media_kind=$1 AND root.is_default AND root.enabled AND root.plex_library_id IS NOT NULL ORDER BY root.id LIMIT 1`, kind).Scan(&rootText)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrSeerrRouteTargetUnavailable
	}
	return rootText, err
}

// selectNoRequiredSubtitleProfile returns the enabled profile for a kind that
// does not require any subtitle language, preferring one routed for Danish
// originals. It returns an empty string when no such profile exists.
func selectNoRequiredSubtitleProfile(ctx context.Context, transaction pgx.Tx, kind catalog.MediaKind) (string, error) {
	rows, err := transaction.Query(ctx, `SELECT id::text,policy FROM quality_profiles WHERE media_kind=$1 AND enabled ORDER BY is_default DESC,name,id`, kind)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	fallback := ""
	for rows.Next() {
		var idText string
		var policy json.RawMessage
		if err := rows.Scan(&idText, &policy); err != nil {
			return "", err
		}
		var routing struct {
			Required  []string `json:"required_subtitle_languages"`
			Languages []string `json:"original_language_routing"`
		}
		if err := json.Unmarshal(policy, &routing); err != nil {
			return "", errors.New("quality profile policy is invalid")
		}
		if len(routing.Required) != 0 {
			continue
		}
		for _, value := range routing.Languages {
			if canonical, reliable := languages.Canonical(value); reliable && canonical == "da" {
				return idText, nil
			}
		}
		if fallback == "" {
			fallback = idText
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return fallback, nil
}

// selectSeerrProfileBySyntheticID resolves the quality-profile id Seerr got
// from Media Manager's servarr API back to the Media Manager profile. Old
// Seerr server rows keep small *arr profile ids, which never match; intake then
// falls back to original-language routing.
func selectSeerrProfileBySyntheticID(ctx context.Context, transaction pgx.Tx, kind catalog.MediaKind, profileID int) (string, error) {
	if profileID <= 0 {
		return "", nil
	}
	rows, err := transaction.Query(ctx, `SELECT id::text FROM quality_profiles WHERE media_kind=$1 AND enabled ORDER BY is_default DESC,name,id`, kind)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var idText string
		if err := rows.Scan(&idText); err != nil {
			return "", err
		}
		parsed, err := uuid.Parse(idText)
		if err != nil {
			return "", err
		}
		if servarr.SyntheticID("profile", parsed.String()) == profileID {
			return idText, nil
		}
	}
	return "", rows.Err()
}
