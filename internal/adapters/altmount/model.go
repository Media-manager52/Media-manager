package altmount

import (
	"context"
	"io"
	"time"
)

type QueueID int64
type QueuePriority int

const (
	PriorityHigh   QueuePriority = 1
	PriorityNormal QueuePriority = 2
	PriorityLow    QueuePriority = 3
)

type QueueStatus string

const (
	StatusPending    QueueStatus = "pending"
	StatusProcessing QueueStatus = "processing"
	StatusCompleted  QueueStatus = "completed"
	StatusFailed     QueueStatus = "failed"
	StatusPaused     QueueStatus = "paused"
	StatusFallback   QueueStatus = "fallback"
)

type User struct {
	ID       string `json:"id"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	IsAdmin  bool   `json:"is_admin"`
}

type Session struct {
	User            User      `json:"user"`
	AuthenticatedAt time.Time `json:"authenticated_at"`
}

type QueueItem struct {
	ID             QueueID       `json:"id"`
	NZBPath        string        `json:"nzb_path"`
	NZBDisplayName string        `json:"nzb_display_name"`
	TargetPath     string        `json:"target_path"`
	Category       *string       `json:"category"`
	Priority       QueuePriority `json:"priority"`
	Status         QueueStatus   `json:"status"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	StartedAt      *time.Time    `json:"started_at"`
	CompletedAt    *time.Time    `json:"completed_at"`
	RetryCount     int           `json:"retry_count"`
	MaxRetries     int           `json:"max_retries"`
	ErrorMessage   *string       `json:"error_message"`
	BatchID        *string       `json:"batch_id"`
	Metadata       *string       `json:"metadata"`
	FileSize       *int64        `json:"file_size"`
	Indexer        *string       `json:"indexer,omitempty"`
	Percentage     *int          `json:"percentage,omitempty"`
	Stage          string        `json:"stage,omitempty"`
	StoragePath    *string       `json:"storage_path,omitempty"`
}

type PageMeta struct {
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	Count  int `json:"count"`
}
type QueuePage struct {
	Items []QueueItem `json:"items"`
	Meta  PageMeta    `json:"meta"`
}

type QueueQuery struct {
	Status    *QueueStatus
	Search    string
	Since     *time.Time
	SortBy    string
	SortOrder string
	Limit     int
	Offset    int
}

type UploadRequest struct {
	Filename     string
	Open         func() (io.ReadCloser, error)
	Size         int64
	Category     string
	Priority     QueuePriority
	RelativePath string
}

type PollPolicy struct {
	Interval time.Duration
	Timeout  time.Duration
}
type RemoveResult struct {
	Cancelled bool       `json:"cancelled"`
	Deleted   bool       `json:"deleted"`
	Final     *QueueItem `json:"final,omitempty"`
}

type Phase string

const (
	PhaseUnknown        Phase = "unknown"
	PhaseQueued         Phase = "queued"
	PhasePaused         Phase = "paused"
	PhaseProcessing     Phase = "processing"
	PhaseStreamingReady Phase = "streaming_ready"
	PhaseImportComplete Phase = "import_complete"
	PhaseFailed         Phase = "failed"
	PhaseCancelled      Phase = "cancelled"
	PhaseNeedsAttention Phase = "needs_attention"
	PhaseRemoved        Phase = "removed"
)

type OutputObservation struct {
	Mapped            bool
	Exists            bool
	Readable          bool
	InventoryComplete bool
}
type ReconcileInput struct {
	Queue           *QueueItem
	QueueNotFound   bool
	Output          OutputObservation
	CancelRequested bool
	DeleteConfirmed bool
}

type Client interface {
	Login(context.Context) (Session, error)
	Refresh(context.Context) error
	Logout(context.Context) error
	UploadNZB(context.Context, UploadRequest) (QueueItem, error)
	Queue(context.Context, QueueID) (QueueItem, error)
	ListQueue(context.Context, QueueQuery) (QueuePage, error)
	RequestCancel(context.Context, QueueID) error
	DeleteQueueRecord(context.Context, QueueID) error
	CancelAndDelete(context.Context, QueueID, PollPolicy) (RemoveResult, error)
}
