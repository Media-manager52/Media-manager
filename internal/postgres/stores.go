package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/postgres/backupstore"
	"github.com/media-manager/media-manager/internal/postgres/commissioningstore"
	"github.com/media-manager/media-manager/internal/postgres/errorlogstore"
	"github.com/media-manager/media-manager/internal/postgres/importliststore"
	"github.com/media-manager/media-manager/internal/postgres/notificationstore"
	"github.com/media-manager/media-manager/internal/postgres/servarrstore"
)

// Store is the one storage entry point callers use. Subjects that stand on
// their own live in their own package under internal/postgres and are embedded
// here, so their methods stay on Store and callers do not change. The aliases
// below keep the subject types available as postgres.X for the same reason.
type Store struct {
	Pool              *pgxpool.Pool
	acquisitionLimits AcquisitionLimits
	// relocateCheckpoint lets tests interrupt a move at every boundary.
	relocateCheckpoint func() error

	backupstore.Backups
	notificationstore.Notifications
	importliststore.ImportLists
	servarrstore.ServarrViews
	errorlogstore.ErrorLogs
	commissioningstore.Commissioning
}

func newStore(pool *pgxpool.Pool) *Store {
	return &Store{
		Pool:          pool,
		Backups:       backupstore.Backups{Pool: pool},
		Notifications: notificationstore.Notifications{Pool: pool},
		ImportLists:   importliststore.ImportLists{Pool: pool},
		ServarrViews:  servarrstore.ServarrViews{Pool: pool},
		ErrorLogs:     errorlogstore.ErrorLogs{Pool: pool},
		Commissioning: commissioningstore.Commissioning{Pool: pool},
	}
}

type BackupRecord = backupstore.BackupRecord

var ErrBackupInProgress = backupstore.ErrBackupInProgress

type NotificationDelivery = notificationstore.NotificationDelivery
type NotificationDeliveryContext = notificationstore.NotificationDeliveryContext
type NotificationDestination = notificationstore.NotificationDestination

var RedactNotificationEndpoint = notificationstore.RedactNotificationEndpoint

type ImportList = importliststore.ImportList
type ImportListItem = importliststore.ImportListItem
type ImportListSnapshot = importliststore.ImportListSnapshot

var ErrDuplicateSnapshot = importliststore.ErrDuplicateSnapshot

type ServarrMovie = servarrstore.ServarrMovie
type ServarrSeries = servarrstore.ServarrSeries
type ServarrSeriesSeason = servarrstore.ServarrSeriesSeason

const ErrorLogWindow = errorlogstore.ErrorLogWindow

type ErrorLog = errorlogstore.ErrorLog
type ErrorLogEntry = errorlogstore.ErrorLogEntry
type ErrorLogFilter = errorlogstore.ErrorLogFilter

const commissionedScopedIndexerSQL = commissioningstore.CommissionedScopedIndexerSQL

const MaxOldBoysGrabLimit = commissioningstore.MaxOldBoysGrabLimit
const MaxOldBoysQueryLimit = commissioningstore.MaxOldBoysQueryLimit

type CommissioningReservation = commissioningstore.CommissioningReservation
type IndexerCapabilityProfile = commissioningstore.IndexerCapabilityProfile
type IndexerCommissioningRun = commissioningstore.IndexerCommissioningRun
type IndexerCommissioningStep = commissioningstore.IndexerCommissioningStep
type IndexerCommissioningStepTransition = commissioningstore.IndexerCommissioningStepTransition
type IndexerOperationStatus = commissioningstore.IndexerOperationStatus
type IndexerSettingsPatch = commissioningstore.IndexerSettingsPatch

var ErrCommissioningGateUnresolved = commissioningstore.ErrCommissioningGateUnresolved
var ErrCommissioningReservationStale = commissioningstore.ErrCommissioningReservationStale
var ErrIndexerCategoryReviewRequired = commissioningstore.ErrIndexerCategoryReviewRequired
var ErrIndexerCommissioningConfigChanged = commissioningstore.ErrIndexerCommissioningConfigChanged
var ErrIndexerCommissioningDraftUnavailable = commissioningstore.ErrIndexerCommissioningDraftUnavailable
var ErrIndexerEndpointConfigurationChanged = commissioningstore.ErrIndexerEndpointConfigurationChanged
var ErrIndexerProviderContractMismatch = commissioningstore.ErrIndexerProviderContractMismatch
var ErrInvalidIndexerCapabilityProfile = commissioningstore.ErrInvalidIndexerCapabilityProfile
var ErrInvalidIndexerCommissioning = commissioningstore.ErrInvalidIndexerCommissioning
var ErrInvalidIndexerOperationStatus = commissioningstore.ErrInvalidIndexerOperationStatus
var ErrInvalidOldBoysBudget = commissioningstore.ErrInvalidOldBoysBudget
var ErrLiveExecutorCancelled = commissioningstore.ErrLiveExecutorCancelled
var OldBoysMediaAllowed = commissioningstore.OldBoysMediaAllowed
var ReservationLockKeyForTest = commissioningstore.ReservationLockKeyForTest
