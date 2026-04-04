package shared

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRetryWithPolicyCallsOnRetry(t *testing.T) {
	t.Parallel()

	type retryNote struct {
		attempt int
		err     string
		delay   time.Duration
	}

	var notes []retryNote
	attempts := 0
	err := RetryWithPolicy(context.Background(), Policy{
		MaxAttempts: 3,
		BaseDelay:   time.Millisecond,
		OnRetry: func(attempt int, err error, delay time.Duration) {
			notes = append(notes, retryNote{
				attempt: attempt,
				err:     err.Error(),
				delay:   delay,
			})
		},
	}, func() error {
		attempts++
		if attempts < 3 {
			return fmt.Errorf("attempt %d failed", attempts)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RetryWithPolicy() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if len(notes) != 2 {
		t.Fatalf("callback count = %d, want 2", len(notes))
	}
	if notes[0] != (retryNote{attempt: 1, err: "attempt 1 failed", delay: time.Millisecond}) {
		t.Fatalf("first callback = %+v", notes[0])
	}
	if notes[1] != (retryNote{attempt: 2, err: "attempt 2 failed", delay: 2 * time.Millisecond}) {
		t.Fatalf("second callback = %+v", notes[1])
	}
}

func TestRetryDelayHonorsMaxDelay(t *testing.T) {
	t.Parallel()

	policy := normalizeRetryPolicy(Policy{
		BaseDelay: 10 * time.Millisecond,
		MaxDelay:  15 * time.Millisecond,
	})

	if got := retryDelay(policy, 0, 0.5); got != 10*time.Millisecond {
		t.Fatalf("attempt 0 delay = %v, want %v", got, 10*time.Millisecond)
	}
	if got := retryDelay(policy, 1, 0.5); got != 15*time.Millisecond {
		t.Fatalf("attempt 1 delay = %v, want %v", got, 15*time.Millisecond)
	}
	if got := retryDelay(policy, 4, 0.5); got != 15*time.Millisecond {
		t.Fatalf("attempt 4 delay = %v, want %v", got, 15*time.Millisecond)
	}
}

func TestRetryDelayAppliesJitter(t *testing.T) {
	t.Parallel()

	policy := normalizeRetryPolicy(Policy{
		BaseDelay: 100 * time.Millisecond,
		Jitter:    0.25,
	})

	if got := retryDelay(policy, 0, 0); got != 75*time.Millisecond {
		t.Fatalf("rnd=0 delay = %v, want %v", got, 75*time.Millisecond)
	}
	if got := retryDelay(policy, 0, 0.5); got != 100*time.Millisecond {
		t.Fatalf("rnd=0.5 delay = %v, want %v", got, 100*time.Millisecond)
	}
	if got := retryDelay(policy, 0, 1); got != 125*time.Millisecond {
		t.Fatalf("rnd=1 delay = %v, want %v", got, 125*time.Millisecond)
	}
}

func TestRetryWithPolicyStopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	attempts := 0
	err := RetryWithPolicy(ctx, Policy{
		MaxAttempts: 3,
		BaseDelay:   time.Second,
		OnRetry: func(_ int, _ error, _ time.Duration) {
			cancel()
		},
	}, func() error {
		attempts++
		return errors.New("boom")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RetryWithPolicy() error = %v, want %v", err, context.Canceled)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestRetryWrapperUsesBaseDelay(t *testing.T) {
	t.Parallel()

	attempts := 0
	err := Retry(context.Background(), 2, time.Millisecond, func() error {
		attempts++
		if attempts == 1 {
			return errors.New("boom")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}
