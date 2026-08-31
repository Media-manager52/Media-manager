package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
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
	events, err := worker.Store.ClaimOutbox(ctx, worker.Owner, 1, worker.Lease)
	if err != nil {
		return err
	}
	for _, event := range events {
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
		handlerErr := handler(handlerCtx, event)
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
				if deferErr := worker.Store.DeferOutbox(ctx, event.ID, worker.Owner, safeError(handlerErr), retryAt); deferErr != nil {
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
