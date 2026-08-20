package heteroci

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSnapshotProbeExternalErrorClassifiesUnavailableChains(t *testing.T) {
	cases := []struct {
		name            string
		build           func() ([]error, error)
		onlyUnavailable bool
	}{
		{
			name: "direct",
			build: func() ([]error, error) {
				return []error{ErrProbeTransportUnavailable}, ErrProbeTransportUnavailable
			},
			onlyUnavailable: true,
		},
		{
			name: "wrapped",
			build: func() ([]error, error) {
				wrapped := fmt.Errorf("wrapped probe unavailable: %w", ErrProbeTransportUnavailable)
				return []error{wrapped, ErrProbeTransportUnavailable}, wrapped
			},
			onlyUnavailable: true,
		},
		{
			name: "join single",
			build: func() ([]error, error) {
				joined := errors.Join(ErrProbeTransportUnavailable)
				return []error{joined, ErrProbeTransportUnavailable}, joined
			},
			onlyUnavailable: true,
		},
		{
			name: "join unavailable and fatal",
			build: func() ([]error, error) {
				fatal := errors.New("probe fatal secret")
				joined := errors.Join(ErrProbeTransportUnavailable, fatal)
				return []error{joined, ErrProbeTransportUnavailable, fatal}, joined
			},
			onlyUnavailable: false,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tokens, cause := test.build()
			snapshot := probeSnapshotRunBounded(t, test.name+" snapshot", func() probeExternalErrorSnapshot {
				return snapshotProbeExternalError(cause)
			})
			if snapshot.onlyUnavailable != test.onlyUnavailable {
				t.Fatalf("onlyUnavailable = %v, want %v", snapshot.onlyUnavailable, test.onlyUnavailable)
			}
			probeSnapshotAssertOpaqueCause(t, snapshot, []string{"probe fatal secret"})
			probeSnapshotAssertTokens(t, snapshot.cause, tokens, true)
		})
	}
}

func TestSnapshotProbeExternalErrorPreservesOrdinaryTokens(t *testing.T) {
	cases := []struct {
		name  string
		build func() ([]error, error)
	}{
		{
			name: "direct",
			build: func() ([]error, error) {
				token := errors.New("ordinary direct secret")
				return []error{token}, token
			},
		},
		{
			name: "wrapped",
			build: func() ([]error, error) {
				token := errors.New("ordinary wrapped secret")
				wrapped := fmt.Errorf("ordinary wrapper: %w", token)
				return []error{wrapped, token}, wrapped
			},
		},
		{
			name: "join",
			build: func() ([]error, error) {
				left := errors.New("ordinary join left secret")
				right := errors.New("ordinary join right secret")
				joined := errors.Join(left, right)
				return []error{joined, left, right}, joined
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tokens, cause := test.build()
			snapshot := probeSnapshotRunBounded(t, test.name+" snapshot", func() probeExternalErrorSnapshot {
				return snapshotProbeExternalError(cause)
			})
			if snapshot.onlyUnavailable {
				t.Fatal("onlyUnavailable = true for ordinary error")
			}
			probeSnapshotAssertOpaqueCause(t, snapshot, []string{
				"ordinary direct secret",
				"ordinary wrapped secret",
				"ordinary join left secret",
				"ordinary join right secret",
			})
			probeSnapshotAssertTokens(t, snapshot.cause, tokens, true)
		})
	}
}

func TestSnapshotProbeExternalErrorDropsUnsafeTrees(t *testing.T) {
	for _, test := range probeSnapshotUnsafeCases() {
		t.Run(test.name, func(t *testing.T) {
			tokens, cause := test.build()
			snapshot := probeSnapshotRunBounded(t, test.name+" snapshot", func() probeExternalErrorSnapshot {
				return snapshotProbeExternalError(cause)
			})
			if snapshot.onlyUnavailable {
				t.Fatal("onlyUnavailable = true for unsafe error tree")
			}
			probeSnapshotAssertOpaqueCause(t, snapshot, []string{
				"self cycle secret",
				"two node left secret",
				"two node right secret",
				"deep tail secret",
				"fanout tail secret",
				"panic unwrap secret",
				"unhashable interface secret",
			})
			probeSnapshotAssertNoMatches(t, snapshot)
			probeSnapshotAssertTokens(t, snapshot.cause, tokens, false)
		})
	}
}

func probeSnapshotUnsafeCases() []struct {
	name  string
	build func() ([]error, error)
} {
	return []struct {
		name  string
		build func() ([]error, error)
	}{
		{
			name: "self cycle",
			build: func() ([]error, error) {
				cycle := &probeSnapshotSelfCycleError{secret: "self cycle secret"}
				return []error{cycle}, cycle
			},
		},
		{
			name: "two node cycle",
			build: func() ([]error, error) {
				left := &probeSnapshotCycleError{secret: "two node left secret"}
				right := &probeSnapshotCycleError{secret: "two node right secret"}
				left.next = right
				right.next = left
				return []error{left, right}, left
			},
		},
		{
			name: "65 deep",
			build: func() ([]error, error) {
				tail := errors.New("deep tail secret")
				root := probeSnapshotDeepError(tail)
				return []error{root, tail}, root
			},
		},
		{
			name: "65 node fanout",
			build: func() ([]error, error) {
				children := make([]error, 65)
				children[0] = ErrProbeTransportUnavailable
				for index := 1; index < len(children)-1; index++ {
					children[index] = fmt.Errorf("fanout branch %d secret", index)
				}
				children[len(children)-1] = errors.New("fanout tail secret")
				joined := errors.Join(children...)
				return []error{joined, children[0], children[len(children)-1]}, joined
			},
		},
		{
			name: "panic unwrap",
			build: func() ([]error, error) {
				panicError := &probeSnapshotPanicUnwrapError{secret: "panic unwrap secret"}
				return []error{panicError}, panicError
			},
		},
		{
			name: "typed nil",
			build: func() ([]error, error) {
				var typedNil *probeSnapshotTypedNilError
				var cause error = typedNil
				return []error{cause}, cause
			},
		},
		{
			name: "unhashable interface field",
			build: func() ([]error, error) {
				tricky := probeSnapshotTrickyError{Payload: []int{1, 2, 3}, secret: "unhashable interface secret"}
				var cause error = tricky
				return []error{cause}, cause
			},
		},
	}
}

func TestSnapshotProbeExternalErrorFreezesStatefulUnwrap(t *testing.T) {
	sentinel := errors.New("stateful sentinel secret")
	stateful := &probeSnapshotStatefulError{sentinel: sentinel}
	snapshot := probeSnapshotRunBounded(t, "stateful snapshot", func() probeExternalErrorSnapshot {
		return snapshotProbeExternalError(stateful)
	})
	if snapshot.onlyUnavailable {
		t.Fatal("onlyUnavailable = true for ordinary stateful sentinel")
	}
	probeSnapshotAssertOpaqueCause(t, snapshot, []string{"stateful sentinel secret", "stateful raw secret"})
	if got := stateful.unwrapCalls.Load(); got != 1 {
		t.Fatalf("raw Unwrap calls after snapshot = %d, want 1", got)
	}
	if !probeSnapshotRunBounded(t, "stateful errors.Is sentinel", func() bool {
		return errors.Is(snapshot.cause, sentinel)
	}) {
		t.Fatal("errors.Is(opaque, sentinel) = false, want true")
	}
	if !probeSnapshotRunBounded(t, "stateful errors.Is raw", func() bool {
		return errors.Is(snapshot.cause, stateful)
	}) {
		t.Fatal("errors.Is(opaque, raw) = false, want true")
	}
	var rawTarget *probeSnapshotStatefulError
	if probeSnapshotRunBounded(t, "stateful errors.As raw", func() bool {
		return errors.As(snapshot.cause, &rawTarget)
	}) {
		t.Fatal("errors.As(opaque, raw) = true, want false")
	}
	if got := stateful.unwrapCalls.Load(); got != 1 {
		t.Fatalf("raw Unwrap calls after opaque queries = %d, want 1", got)
	}
}

func TestSnapshotProbeOpaqueCauseDoesNotInvokeRawIsOrAs(t *testing.T) {
	cases := []struct {
		name      string
		panicInIs bool
		panicInAs bool
	}{
		{name: "panic hooks", panicInIs: true, panicInAs: true},
		{name: "counting hooks"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			raw := &probeSnapshotHookError{
				panicInIs: test.panicInIs,
				panicInAs: test.panicInAs,
				secret:    "raw hook secret",
			}
			snapshot := probeSnapshotRunBounded(t, test.name+" snapshot", func() probeExternalErrorSnapshot {
				return snapshotProbeExternalError(raw)
			})
			probeSnapshotAssertOpaqueCause(t, snapshot, []string{"raw hook secret"})
			if !probeSnapshotRunBounded(t, test.name+" errors.Is raw", func() bool {
				return errors.Is(snapshot.cause, raw)
			}) {
				t.Fatal("errors.Is(opaque, raw) = false, want true")
			}
			other := errors.New("other hook target")
			if probeSnapshotRunBounded(t, test.name+" errors.Is other", func() bool {
				return errors.Is(snapshot.cause, other)
			}) {
				t.Fatal("errors.Is(opaque, other) = true, want false")
			}
			var asTarget *probeSnapshotAsTarget
			if probeSnapshotRunBounded(t, test.name+" errors.As other", func() bool {
				return errors.As(snapshot.cause, &asTarget)
			}) {
				t.Fatal("errors.As(opaque, other) = true, want false")
			}
			if got := raw.isCalls.Load(); got != 0 {
				t.Fatalf("raw Is calls = %d, want 0", got)
			}
			if got := raw.asCalls.Load(); got != 0 {
				t.Fatalf("raw As calls = %d, want 0", got)
			}
		})
	}
}

func probeSnapshotAssertOpaqueCause(t *testing.T, snapshot probeExternalErrorSnapshot, secrets []string) {
	t.Helper()
	if _, ok := snapshot.cause.(*probeOpaqueCause); !ok {
		t.Fatalf("cause type = %T, want *probeOpaqueCause", snapshot.cause)
	}
	if got := snapshot.cause.Error(); got != "probe transport failed" {
		t.Fatalf("opaque Error() = %q, want fixed transport message", got)
	}
	for _, secret := range secrets {
		if strings.Contains(snapshot.cause.Error(), secret) {
			t.Fatalf("opaque Error() leaks secret %q: %q", secret, snapshot.cause.Error())
		}
	}
}

func probeSnapshotAssertNoMatches(t *testing.T, snapshot probeExternalErrorSnapshot) {
	t.Helper()
	cause, ok := snapshot.cause.(*probeOpaqueCause)
	if !ok {
		t.Fatalf("cause type = %T, want *probeOpaqueCause", snapshot.cause)
	}
	if len(cause.matches) != 0 {
		t.Fatalf("opaque matches = %d, want 0 for unsafe tree", len(cause.matches))
	}
}

func probeSnapshotAssertTokens(t *testing.T, cause error, tokens []error, want bool) {
	t.Helper()
	for index, token := range tokens {
		got := probeSnapshotRunBounded(t, fmt.Sprintf("errors.Is token %d", index), func() bool {
			return errors.Is(cause, token)
		})
		if got != want {
			t.Fatalf("errors.Is(token %d) = %v, want %v", index, got, want)
		}
	}
}

func probeSnapshotRunBounded[T any](t *testing.T, name string, fn func() T) T {
	t.Helper()
	type result struct {
		value      T
		panicValue any
	}
	done := make(chan result, 1)
	var waitGroup sync.WaitGroup
	waitGroup.Go(func() {
		var got result
		defer func() {
			got.panicValue = recover()
			done <- got
		}()
		got.value = fn()
	})
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case got := <-done:
		waitGroup.Wait()
		if got.panicValue != nil {
			t.Fatalf("%s panicked: %v", name, got.panicValue)
		}
		return got.value
	case <-timer.C:
		t.Fatalf("%s did not return within 1s", name)
		var zero T
		return zero
	}
}

type probeSnapshotSelfCycleError struct {
	secret string
}

func (err *probeSnapshotSelfCycleError) Error() string {
	return "self cycle: " + err.secret
}

func (err *probeSnapshotSelfCycleError) Unwrap() error {
	return err
}

type probeSnapshotCycleError struct {
	next   error
	secret string
}

func (err *probeSnapshotCycleError) Error() string {
	return "cycle: " + err.secret
}

func (err *probeSnapshotCycleError) Unwrap() error {
	return err.next
}

func probeSnapshotDeepError(tail error) error {
	err := tail
	for range 65 {
		err = fmt.Errorf("deep wrapper: %w", err)
	}
	return err
}

type probeSnapshotPanicUnwrapError struct {
	secret string
}

func (err *probeSnapshotPanicUnwrapError) Error() string {
	return "panic unwrap: " + err.secret
}

func (err *probeSnapshotPanicUnwrapError) Unwrap() error {
	// archguard:ignore panic_count -- 测试错误快照必须覆盖 Unwrap panic
	panic("panic unwrap secret: " + err.secret)
}

type probeSnapshotTypedNilError struct{}

func (*probeSnapshotTypedNilError) Error() string {
	// archguard:ignore panic_count -- 测试 typed nil 必须证明 Error 不会被调用
	panic("typed nil Error must not be called")
}

func (*probeSnapshotTypedNilError) Unwrap() error {
	// archguard:ignore panic_count -- 测试 typed nil 必须证明 Unwrap 不会被调用
	panic("typed nil Unwrap must not be called")
}

type probeSnapshotTrickyError struct {
	Payload any
	secret  string
}

func (err probeSnapshotTrickyError) Error() string {
	return "unhashable interface: " + err.secret
}

type probeSnapshotStatefulError struct {
	sentinel    error
	unwrapCalls atomic.Int32
}

func (err *probeSnapshotStatefulError) Error() string {
	return "stateful raw secret"
}

func (err *probeSnapshotStatefulError) Unwrap() error {
	if err.unwrapCalls.Add(1) == 1 {
		return err.sentinel
	}
	return err
}

type probeSnapshotHookError struct {
	panicInIs bool
	panicInAs bool
	secret    string
	isCalls   atomic.Int32
	asCalls   atomic.Int32
}

func (err *probeSnapshotHookError) Error() string {
	return "raw hook: " + err.secret
}

func (err *probeSnapshotHookError) Is(error) bool {
	err.isCalls.Add(1)
	if err.panicInIs {
		// archguard:ignore panic_count -- 测试 opaque cause 必须阻止原始 Is hook
		panic("raw Is must not be called")
	}
	return false
}

func (err *probeSnapshotHookError) As(any) bool {
	err.asCalls.Add(1)
	if err.panicInAs {
		// archguard:ignore panic_count -- 测试 opaque cause 必须阻止原始 As hook
		panic("raw As must not be called")
	}
	return false
}

type probeSnapshotAsTarget struct{}

func (probeSnapshotAsTarget) Error() string {
	return "probe snapshot As target"
}
