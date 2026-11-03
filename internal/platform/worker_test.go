package platform

import (
	"bytes"
	"context"
	"errors"
	"log"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/postgres"
)

type codedTestError struct {
	code string
}

func (err codedTestError) Error() string    { return "details must remain private" }
func (err codedTestError) SafeCode() string { return err.code }

func TestSafeErrorUsesOnlyValidatedCodes(t *testing.T) {
	if got := safeError(codedTestError{code: "newznab_invalid_nzb"}); got != "newznab_invalid_nzb" {
		t.Fatalf("safe error=%q", got)
	}
	unsafe := safeError(codedTestError{code: "secret=value"})
	if strings.Contains(unsafe, "secret") || unsafe == "secret=value" {
		t.Fatalf("unsafe error code escaped validation: %q", unsafe)
	}
	if got := safeError(errors.New("private details")); got != "*errors.errorString" {
		t.Fatalf("fallback error=%q", got)
	}
}

type deferredTestError struct{ at time.Time }

func (err deferredTestError) Error() string      { return "deferred" }
func (err deferredTestError) RetryAt() time.Time { return err.at }
func (err deferredTestError) SafeCode() string   { return "indexer_pacing_deferred" }

func TestNextRetryAtHonorsADeferredPacingDeadline(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	want := now.Add(10 * time.Minute)
	if got := nextRetryAt(deferredTestError{at: want}, 1, now); got != want {
		t.Fatalf("retry=%v want=%v", got, want)
	}
	if got := nextRetryAt(errors.New("ordinary"), 1, now); got != now.Add(2*time.Second) {
		t.Fatalf("ordinary retry=%v", got)
	}
}

func TestRunHandlerAbandonsAHandlerThatNeverReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blocked := make(chan struct{})
	defer close(blocked)
	// Like a read blocked inside a FUSE mount: ignores its context.
	stuck := func(context.Context, postgres.OutboxEvent) error { <-blocked; return nil }
	started := time.Now()
	err := runHandler(ctx, cancel, stuck, postgres.OutboxEvent{Type: "altmount.reconcile_requested"}, 20*time.Millisecond, 20*time.Millisecond)
	var stuckErr handlerStuckError
	if !errors.As(err, &stuckErr) || safeError(err) != "handler_stuck" || time.Since(started) > 5*time.Second {
		t.Fatalf("err=%v, want an abandoned handler", err)
	}
	if retry := nextRetryAt(err, 1, time.Now()); retry.Before(time.Now().Add(stuckHandlerRetry - time.Minute)) {
		t.Fatalf("a stuck handler must not be retried immediately: %v", retry)
	}
}

func TestRunHandlerCancelsASlowHandlerThatHonorsItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	slow := func(handlerCtx context.Context, _ postgres.OutboxEvent) error {
		<-handlerCtx.Done()
		return handlerCtx.Err()
	}
	if err := runHandler(ctx, cancel, slow, postgres.OutboxEvent{}, 20*time.Millisecond, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want the cancellation", err)
	}
}

func TestRunHandlerTurnsAPanicIntoARetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	panicking := func(context.Context, postgres.OutboxEvent) error { panic("nil map") }
	if err := runHandler(ctx, cancel, panicking, postgres.OutboxEvent{}, time.Second, time.Second); safeError(err) != "handler_panicked" {
		t.Fatalf("err=%v, want handler_panicked", err)
	}
	ok := func(context.Context, postgres.OutboxEvent) error { return nil }
	if err := runHandler(ctx, cancel, ok, postgres.OutboxEvent{}, time.Second, time.Second); err != nil {
		t.Fatalf("err=%v", err)
	}
}

// captureDefaultLog routes slog's default logger into a buffer for the
// duration of the test.
func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous, previousWriter, previousFlags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() {
		// SetDefault redirects the log package to the handler and does not
		// undo that when the original default logger is restored.
		slog.SetDefault(previous)
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})
	return &output
}

func TestRunHandlerPanicLogOmitsThePanicValue(t *testing.T) {
	const sentinel = "apikey=SENTINEL-7f3c9d"
	cases := map[string]any{
		"string":      sentinel,
		"error":       errors.New("provider rejected " + sentinel),
		"coded error": codedTestError{code: "newznab_invalid_nzb"},
		"struct":      struct{ Token string }{Token: sentinel},
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			output := captureDefaultLog(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			panicking := func(context.Context, postgres.OutboxEvent) error { panic(value) }
			err := runHandler(ctx, cancel, panicking, postgres.OutboxEvent{Type: "test.panic_requested"}, time.Second, time.Second)
			if safeError(err) != "handler_panicked" {
				t.Fatalf("err=%v, want handler_panicked so the event is retried", err)
			}
			logged := output.String()
			if strings.Contains(logged, "SENTINEL") || strings.Contains(logged, "details must remain private") {
				t.Fatalf("panic value reached the log: %s", logged)
			}
			for _, want := range []string{"outbox handler panicked", "event_type=test.panic_requested", "panic_type=", "stack="} {
				if !strings.Contains(logged, want) {
					t.Fatalf("log is missing %q: %s", want, logged)
				}
			}
		})
	}
}

func TestPanicLogAttrsKeepsSafeCodeAndBoundsTheStack(t *testing.T) {
	attrs := panicLogAttrs(codedTestError{code: "newznab_invalid_nzb"}, bytes.Repeat([]byte("x"), maxPanicStackBytes+10))
	values := map[string]any{}
	for index := 0; index+1 < len(attrs); index += 2 {
		values[attrs[index].(string)] = attrs[index+1]
	}
	if values["panic_type"] != "platform.codedTestError" || values["panic_code"] != "newznab_invalid_nzb" {
		t.Fatalf("attrs=%v", values)
	}
	if stack, _ := values["stack"].(string); len(stack) != maxPanicStackBytes || values["stack_truncated"] != true {
		t.Fatalf("stack length=%d truncated=%v", len(stack), values["stack_truncated"])
	}
	unsafe := panicLogAttrs(codedTestError{code: "secret=value"}, nil)
	for _, attr := range unsafe {
		if text, ok := attr.(string); ok && strings.Contains(text, "secret") {
			t.Fatalf("unvalidated code reached the log: %v", unsafe)
		}
	}
}
