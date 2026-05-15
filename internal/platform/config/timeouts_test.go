package config

import (
	"context"
	"testing"
	"time"
)

const deadlineTolerance = 100 * time.Millisecond

func TestWithTimeout(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		parent          func(t *testing.T) (context.Context, context.CancelFunc)
		timeout         time.Duration
		wantDeadline    bool
		wantCanceledCtx bool
	}{
		{
			name: "nil_ctx_uses_background",
			parent: func(t *testing.T) (context.Context, context.CancelFunc) {
				t.Helper()
				return nil, func() {}
			},
			timeout:         50 * time.Millisecond,
			wantDeadline:    true,
			wantCanceledCtx: true,
		},
		{
			name: "positive_timeout_sets_deadline",
			parent: func(t *testing.T) (context.Context, context.CancelFunc) {
				t.Helper()
				return context.Background(), func() {}
			},
			timeout:         75 * time.Millisecond,
			wantDeadline:    true,
			wantCanceledCtx: true,
		},
		{
			name: "non_positive_timeout_keeps_deadline_free_context",
			parent: func(t *testing.T) (context.Context, context.CancelFunc) {
				t.Helper()
				return context.Background(), func() {}
			},
			timeout:         0,
			wantDeadline:    false,
			wantCanceledCtx: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			parent, cleanupParent := tc.parent(t)
			defer cleanupParent()

			start := time.Now()
			ctx, cancel := WithTimeout(parent, tc.timeout)
			defer cancel()

			deadline, ok := ctx.Deadline()
			if ok != tc.wantDeadline {
				t.Fatalf("Deadline() ok = %t, want %t", ok, tc.wantDeadline)
			}
			if tc.wantDeadline {
				assertDeadlineNear(t, deadline, start, tc.timeout)
			}

			cancel()
			if tc.wantCanceledCtx {
				if err := ctx.Err(); err != context.Canceled {
					t.Fatalf("ctx.Err() after cancel = %v, want %v", err, context.Canceled)
				}
				return
			}
			if err := ctx.Err(); err != nil {
				t.Fatalf("ctx.Err() after cancel = %v, want nil", err)
			}
		})
	}
}

type withTimeoutIfNoneCase struct {
	name              string
	parent            func(t *testing.T) (context.Context, context.CancelFunc)
	timeout           time.Duration
	wantDeadline      bool
	wantPreserve      bool
	wantCanceledCtx   bool
	expectedRemaining time.Duration
}

func TestWithTimeoutIfNone(t *testing.T) {
	t.Parallel()

	cases := []withTimeoutIfNoneCase{
		{
			name: "no_parent_deadline_adds_timeout",
			parent: func(t *testing.T) (context.Context, context.CancelFunc) {
				t.Helper()
				return context.Background(), func() {}
			},
			timeout:         80 * time.Millisecond,
			wantDeadline:    true,
			wantCanceledCtx: true,
		},
		{
			name: "existing_deadline_is_preserved",
			parent: func(t *testing.T) (context.Context, context.CancelFunc) {
				t.Helper()
				return context.WithTimeout(context.Background(), 250*time.Millisecond)
			},
			timeout:           80 * time.Millisecond,
			wantDeadline:      true,
			wantPreserve:      true,
			wantCanceledCtx:   false,
			expectedRemaining: 250 * time.Millisecond,
		},
		{
			name: "non_positive_timeout_without_parent_deadline_keeps_deadline_free_context",
			parent: func(t *testing.T) (context.Context, context.CancelFunc) {
				t.Helper()
				return context.Background(), func() {}
			},
			timeout:         0,
			wantDeadline:    false,
			wantCanceledCtx: false,
		},
		{
			name: "nil_ctx_does_not_panic",
			parent: func(t *testing.T) (context.Context, context.CancelFunc) {
				t.Helper()
				return nil, func() {}
			},
			timeout:         60 * time.Millisecond,
			wantDeadline:    true,
			wantCanceledCtx: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runWithTimeoutIfNoneCase(t, tc)
		})
	}
}

func runWithTimeoutIfNoneCase(t *testing.T, tc withTimeoutIfNoneCase) {
	t.Helper()

	parent, cleanupParent := tc.parent(t)
	defer cleanupParent()

	start := time.Now()
	var parentDeadline time.Time
	if parent != nil {
		parentDeadline, _ = parent.Deadline()
	}

	ctx, cancel := WithTimeoutIfNone(parent, tc.timeout)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if ok != tc.wantDeadline {
		t.Fatalf("Deadline() ok = %t, want %t", ok, tc.wantDeadline)
	}
	if tc.wantDeadline {
		assertTimeoutIfNoneDeadline(t, tc, deadline, parentDeadline, start)
	}

	cancel()
	if tc.wantCanceledCtx {
		if err := ctx.Err(); err != context.Canceled {
			t.Fatalf("ctx.Err() after cancel = %v, want %v", err, context.Canceled)
		}
		return
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("ctx.Err() after cancel = %v, want nil", err)
	}
}

func assertTimeoutIfNoneDeadline(t *testing.T, tc withTimeoutIfNoneCase, deadline, parentDeadline, start time.Time) {
	t.Helper()
	if tc.wantPreserve {
		if !deadline.Equal(parentDeadline) {
			t.Fatalf("Deadline() = %s, want preserved %s", deadline, parentDeadline)
		}
		return
	}
	assertDeadlineNear(t, deadline, start, tc.timeout)
}

func assertDeadlineNear(t *testing.T, deadline time.Time, start time.Time, timeout time.Duration) {
	t.Helper()

	wantMin := start.Add(timeout - deadlineTolerance)
	wantMax := start.Add(timeout + deadlineTolerance)
	if deadline.Before(wantMin) || deadline.After(wantMax) {
		t.Fatalf("deadline = %s, want within [%s, %s]", deadline, wantMin, wantMax)
	}
}
