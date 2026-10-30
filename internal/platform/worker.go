package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/postgres"
)

type Handler func(context.Context, postgres.OutboxEvent) error

type Worker struct {
	Store        *postgres.Store
	Owner        string
	Handlers     map[string]Handler
	PollInterval time.Duration
	Lease        time.Duration
	// Limits caps how long one event type's handler may run (default
	// DefaultHandlerLimit). AbandonGrace is how long a cancelled handler gets
	// to return before it is abandoned (default HandlerAbandonGrace).
	Limits          map[string]time.Duration
	AbandonGrace    time.Duration
	preferDiscovery bool
}

// The worker handles one event at a time, so a handler that never returns
// stops every search, publication, repair and Seerr update behind it. On
// 2026-10-17 a read blocked inside the AltMount FUSE mount, which no context
// can interrupt, stalled the worker for two hours. A handler that exceeds its
// limit is cancelled; one that still does not return within the grace period
// is abandoned: its event is retried later and the worker moves on.
const (
	DefaultHandlerLimit = 30 * time.Minute
	HandlerAbandonGrace = 30 * time.Second
	stuckHandlerRetry   = 10 * time.Minute
)

type handlerStuckError struct {
	limit time.Duration
	until time.Time
}

func (err handlerStuckError) Error() string {
	return fmt.Sprintf("handler did not return within %s and was abandoned", err.limit)
}
func (err handlerStuckError) SafeCode() string   { return "handler_stuck" }
func (err handlerStuckError) RetryAt() time.Time { return err.until }

type handlerPanicError struct{}

func (handlerPanicError) Error() string    { return "handler panicked" }
func (handlerPanicError) SafeCode() string { return "handler_panicked" }

// runHandler runs one handler under its time limit. A panic becomes an error
// (the event is retried) instead of stopping the process.
func runHandler(ctx context.Context, cancel context.CancelFunc, handler Handler, event postgres.OutboxEvent, limit, grace time.Duration) error {
	done := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("outbox handler panicked", "event_type", event.Type, "event_id", event.ID, "panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
				done <- handlerPanicError{}
			}
		}()
		done <- handler(ctx, event)
	}()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
	case <-ctx.Done():
	}
	cancel()
	abandon := time.NewTimer(grace)
	defer abandon.Stop()
	select {
	case err := <-done:
		if err == nil {
			err = ctx.Err()
		}
		return err
	case <-abandon.C:
		slog.Error("outbox handler did not return; abandoned so the worker continues", "event_type", event.Type, "event_id", event.ID, "limit", limit.String())
		return handlerStuckError{limit: limit, until: time.Now().Add(stuckHandlerRetry)}
	}
}

// RunBatch processes at most one currently claimable outbox event. It is the
// testable boundary for the same claim/handler/acknowledgement path used by
// Run; callers must not use it as a replacement worker loop.
func (worker *Worker) RunBatch(ctx context.Context) error {
	return worker.runBatch(ctx)
}

func (worker *Worker) Run(ctx context.Context) error {
	if worker.Store == nil || worker.Owner == "" {
		return errors.New("outbox worker is not configured")
	}
	if worker.PollInterval <= 0 {
		worker.PollInterval = time.Second
	}
	if worker.Lease <= 0 {
		worker.Lease = time.Minute
	}
	ticker := time.NewTicker(worker.PollInterval)
	defer ticker.Stop()
	for {
		if err := worker.runBatch(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.ErrorContext(ctx, "outbox batch failed", "error", err)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (worker *Worker) runBatch(ctx context.Context) error {
	events, err := worker.Store.ClaimOutbox(ctx, worker.Owner, 1, worker.Lease, worker.preferDiscovery)
	if err != nil {
		return err
	}
	for _, event := range events {
		// Rotate on claim rather than completion: pacing and budget deferrals
		// must not monopolize the next indexer turn.
		switch event.Type {
		case "workflow.search_requested":
			worker.preferDiscovery = true
		case "workflow.discovery_rescan_requested":
			worker.preferDiscovery = false
		}
		handler := worker.Handlers[event.Type]
		if handler == nil {
			_ = worker.Store.FailOutbox(ctx, event.ID, worker.Owner, "no handler registered", time.Now().Add(30*time.Minute))
			continue
		}
		handlerCtx, cancelHandler := context.WithCancel(ctx)
		heartbeatDone := make(chan error, 1)
		stopHeartbeat := make(chan struct{})
		go func() {
			interval := worker.Lease / 3
			if interval > 30*time.Second {
				interval = 30 * time.Second
			}
			if interval < time.Second {
				interval = time.Second
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if renewErr := worker.Store.RenewOutboxLease(handlerCtx, event.ID, worker.Owner, worker.Lease); renewErr != nil {
						cancelHandler()
						heartbeatDone <- renewErr
						return
					}
				case <-stopHeartbeat:
					heartbeatDone <- nil
					return
				case <-handlerCtx.Done():
					heartbeatDone <- nil
					return
				}
			}
		}()
		limit := worker.Limits[event.Type]
		if limit <= 0 {
			limit = DefaultHandlerLimit
		}
		grace := worker.AbandonGrace
		if grace <= 0 {
			grace = HandlerAbandonGrace
		}
		handlerErr := runHandler(handlerCtx, cancelHandler, handler, event, limit, grace)
		close(stopHeartbeat)
		heartbeatErr := <-heartbeatDone
		cancelHandler()
		if handlerErr == nil {
			handlerErr = heartbeatErr
		}
		if handlerErr != nil {
			retryAt := nextRetryAt(handlerErr, event.Attempts, time.Now())
			var deferred interface{ RetryAt() time.Time }
			if errors.As(handlerErr, &deferred) {
				preserve := false
				var preserveDeferral interface{ PreserveDeferralAttempts() bool }
				if errors.As(handlerErr, &preserveDeferral) {
					preserve = preserveDeferral.PreserveDeferralAttempts()
				}
				var deferErr error
				if preserve {
					deferErr = worker.Store.DeferOutboxPreservingAttempts(ctx, event.ID, worker.Owner, safeError(handlerErr), retryAt)
				} else {
					deferErr = worker.Store.DeferOutbox(ctx, event.ID, worker.Owner, safeError(handlerErr), retryAt)
				}
				if deferErr != nil {
					return deferErr
				}
				continue
			}
			if failErr := worker.Store.FailOutbox(ctx, event.ID, worker.Owner, safeError(handlerErr), retryAt); failErr != nil {
				return failErr
			}
			continue
		}
		if err := worker.Store.CompleteOutbox(ctx, event.ID, worker.Owner); err != nil {
			return err
		}
	}
	return nil
}

func nextRetryAt(handlerErr error, attempts int, now time.Time) time.Time {
	backoff := time.Duration(1<<min(attempts, 8)) * time.Second
	retryAt := now.Add(backoff)
	var deferred interface{ RetryAt() time.Time }
	if errors.As(handlerErr, &deferred) && deferred.RetryAt().After(retryAt) {
		retryAt = deferred.RetryAt()
	}
	return retryAt
}

var safeErrorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,99}$`)

func safeError(err error) string {
	var coded interface{ SafeCode() string }
	if errors.As(err, &coded) {
		code := strings.TrimSpace(coded.SafeCode())
		if safeErrorCodePattern.MatchString(code) {
			return code
		}
	}
	return fmt.Sprintf("%T", err)
}
func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
