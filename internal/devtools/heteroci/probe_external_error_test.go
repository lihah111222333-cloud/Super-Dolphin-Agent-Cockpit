package heteroci

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type externalProbeBody struct {
	reader     *bytes.Reader
	readErr    error
	closeErr   error
	closeCalls int
}

func (body *externalProbeBody) Read(_ context.Context, target []byte) (int, error) {
	if body.readErr != nil {
		return 0, body.readErr
	}
	return body.reader.Read(target)
}

func (body *externalProbeBody) Close() error {
	body.closeCalls++
	return body.closeErr
}

type externalProbeTransport struct {
	body       ProbeResponseBody
	err        error
	probeCalls int
}

func (transport *externalProbeTransport) Probe(context.Context, string, []byte) (ProbeResponseBody, error) {
	transport.probeCalls++
	return transport.body, transport.err
}

type externalProbeContext struct {
	context.Context
	errs  []error
	calls atomic.Int32
}

func (probeContext *externalProbeContext) Err() error {
	index := int(probeContext.calls.Add(1)) - 1
	if index < len(probeContext.errs) {
		return probeContext.errs[index]
	}
	return probeContext.Context.Err()
}

type externalProbeSelfError struct{ text string }

func (err *externalProbeSelfError) Error() string { return err.text }
func (err *externalProbeSelfError) Unwrap() error { return err }

type externalProbeStatefulError struct {
	text  string
	next  error
	calls atomic.Int32
}

func (err *externalProbeStatefulError) Error() string { return err.text }

func (err *externalProbeStatefulError) Unwrap() error {
	if err.calls.Add(1) == 1 {
		return err.next
	}
	return err
}

type externalProbePanicError struct{ text string }

func (err *externalProbePanicError) Error() string { return err.text }

func (err *externalProbePanicError) Unwrap() error {
	// archguard:ignore panic_count -- 测试外部错误树必须覆盖 Unwrap panic
	panic(err.text)
}

type externalProbeWrapError struct {
	text string
	next error
}

func (err *externalProbeWrapError) Error() string { return err.text }
func (err *externalProbeWrapError) Unwrap() error { return err.next }

type externalProbePayloadError struct {
	Payload any
	text    string
}

func (err externalProbePayloadError) Error() string { return err.text }

type externalProbeErrContext struct {
	sequence []error
	calls    atomic.Int32
}

func (ctx *externalProbeErrContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (ctx *externalProbeErrContext) Done() <-chan struct{}       { return nil }

func (ctx *externalProbeErrContext) Err() error {
	index := int(ctx.calls.Add(1)) - 1
	if index < len(ctx.sequence) {
		return ctx.sequence[index]
	}
	if len(ctx.sequence) == 0 {
		return nil
	}
	return ctx.sequence[len(ctx.sequence)-1]
}

func (*externalProbeErrContext) Value(any) any { return nil }

type externalProbeErrorCase struct {
	name      string
	makeError func(string) (*externalProbeStatefulError, []error, error)
}

type externalProbeCloseErrorCase struct {
	name      string
	makeError func(string) externalProbeErrorBuild
}

type externalProbeErrorBuild struct {
	err      error
	stateful *externalProbeStatefulError
	wantIs   []error
}

func TestAcquireSnapshotExternalProbeErrorTrustBoundary(t *testing.T) {
	const (
		endpointSecret = "https://external-probe-endpoint-secret.invalid/runner?token=endpoint-secret"
	)
	cases := []externalProbeErrorCase{
		{
			name: "ordinary",
			makeError: func(secret string) (*externalProbeStatefulError, []error, error) {
				sentinel := errors.New("ordinary-probe-sentinel-" + secret)
				return nil, []error{sentinel}, sentinel
			},
		},
		{
			name: "self-cycle",
			makeError: func(secret string) (*externalProbeStatefulError, []error, error) {
				return nil, nil, &externalProbeSelfError{text: secret}
			},
		},
		{
			name: "stateful",
			makeError: func(secret string) (*externalProbeStatefulError, []error, error) {
				sentinel := errors.New("stateful-probe-sentinel-" + secret)
				stateful := &externalProbeStatefulError{text: secret, next: sentinel}
				return stateful, []error{sentinel}, stateful
			},
		},
		{
			name: "panic",
			makeError: func(secret string) (*externalProbeStatefulError, []error, error) {
				return nil, nil, &externalProbePanicError{text: secret}
			},
		},
		{
			name: "deep",
			makeError: func(secret string) (*externalProbeStatefulError, []error, error) {
				sentinel := errors.New("deep-probe-sentinel-" + secret)
				return nil, nil, externalProbeDeepError(secret, sentinel)
			},
		},
		{
			name: "uncomparable-payload",
			makeError: func(secret string) (*externalProbeStatefulError, []error, error) {
				return nil, nil, externalProbePayloadError{Payload: []int{1, 2, 3}, text: secret}
			},
		},
		{
			name: "multi-branch-unavailable",
			makeError: func(secret string) (*externalProbeStatefulError, []error, error) {
				sentinel := errors.New("fatal-probe-branch-" + secret)
				return nil, []error{ErrProbeTransportUnavailable, sentinel}, errors.Join(ErrProbeTransportUnavailable, sentinel)
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			now, manifest, _ := acquireFixture(t, 1, []int{0})
			manifest.Nodes[0].Endpoint = endpointSecret
			probeSecret := manifest.Nodes[0].ProbeIdentity
			if err := manifest.Validate(); err != nil {
				t.Fatalf("external Probe manifest: %v", err)
			}
			stateful, wantIs, probeErr := test.makeError(test.name + "-redaction-secret")
			transport := &externalProbeTransport{err: probeErr}
			got, err := externalProbeAcquire(t, context.Background(), now, manifest, transport)
			if err == nil {
				t.Fatal("nil-body external Probe failure unexpectedly succeeded")
			}
			assertZeroSnapshot(t, got)
			assertExternalProbeTransportOnly(t, err)
			assertExternalProbeSafe(t, err, endpointSecret, probeSecret, test.name+"-redaction-secret")
			assertExternalProbeBounded(t, err, wantIs...)
			if transport.probeCalls != 1 {
				t.Fatalf("Probe calls = %d, want exactly 1", transport.probeCalls)
			}
			if stateful != nil && stateful.calls.Load() != 1 {
				t.Fatalf("stateful Probe Unwrap calls = %d, want exactly 1", stateful.calls.Load())
			}
		})
	}
}

func TestAcquireSnapshotExternalProbeUnavailableSingleChainRemainsOffline(t *testing.T) {
	now, manifest, _ := acquireFixture(t, 1, []int{0})
	const secret = "single-chain-unavailable-secret"
	wrapped := &externalProbeWrapError{text: secret, next: ErrProbeTransportUnavailable}
	transport := &externalProbeTransport{err: wrapped}
	got, err := externalProbeAcquire(t, context.Background(), now, manifest, transport)
	if err != nil {
		t.Fatalf("single-chain unavailable error = %v, want offline success", err)
	}
	assertFreshOfflineNode(t, snapshotNode(t, got, manifest.Nodes[0].NodeID), manifest.Nodes[0])
	if transport.probeCalls != 1 {
		t.Fatalf("Probe calls = %d, want exactly 1", transport.probeCalls)
	}
}

func TestAcquireSnapshotExternalProbeBodyAndFatalProbeErrorCloseBoundary(t *testing.T) {
	const (
		endpointSecret = "https://external-body-endpoint-secret.invalid/runner"
	)
	now, manifest, responses := acquireFixture(t, 1, []int{0})
	manifest.Nodes[0].Endpoint = endpointSecret
	probeSecret := manifest.Nodes[0].ProbeIdentity
	if err := manifest.Validate(); err != nil {
		t.Fatalf("body and Probe error manifest: %v", err)
	}
	response := responses[manifest.Nodes[0].NodeID]
	response.Endpoint = endpointSecret
	probeSentinel := errors.New("ordinary-probe-failure-secret")
	closeSentinel := errors.New("ordinary-close-failure-secret")
	body := &externalProbeBody{reader: bytes.NewReader(mustProbeResponseJSON(t, response)), closeErr: closeSentinel}
	transport := &externalProbeTransport{body: body, err: probeSentinel}
	got, err := externalProbeAcquire(t, context.Background(), now, manifest, transport)
	if err == nil {
		t.Fatal("non-nil body plus fatal Probe error unexpectedly succeeded")
	}
	assertZeroSnapshot(t, got)
	assertExternalProbeTransportOnly(t, err)
	assertExternalProbeSafe(t, err, endpointSecret, probeSecret, probeSentinel.Error(), closeSentinel.Error())
	assertExternalProbeBounded(t, err, probeSentinel, closeSentinel)
	if transport.probeCalls != 1 {
		t.Fatalf("Probe calls = %d, want exactly 1", transport.probeCalls)
	}
	if body.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want exactly 1", body.closeCalls)
	}
}

func TestAcquireSnapshotExternalContextErrTrustBoundary(t *testing.T) {
	const endpointSecret = "https://external-context-endpoint-secret.invalid/runner"
	probeSentinel := errors.New("external-context-probe-sentinel-secret")
	closeSentinel := errors.New("external-context-close-sentinel-secret")
	stateful := &externalProbeStatefulError{
		text: "external-context-stateful-secret",
		next: context.Canceled,
	}
	payload := externalProbePayloadError{Payload: []int{7, 8, 9}, text: "external-context-payload-secret"}
	cases := []struct {
		name           string
		sequence       []error
		wantIs         []error
		wantProbeCalls int
		wantCloseCalls int
		wantErrCalls   int
		stateful       *externalProbeStatefulError
	}{
		{name: "pre-probe uncomparable", sequence: []error{payload}, wantErrCalls: 1},
		{
			name:           "post-probe stateful context canceled",
			sequence:       []error{nil, stateful},
			wantIs:         []error{probeSentinel, closeSentinel, context.Canceled},
			wantProbeCalls: 1,
			wantCloseCalls: 1,
			wantErrCalls:   2,
			stateful:       stateful,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			now, manifest, responses := acquireFixture(t, 1, []int{0})
			manifest.Nodes[0].Endpoint = endpointSecret
			probeIdentity := manifest.Nodes[0].ProbeIdentity
			if err := manifest.Validate(); err != nil {
				t.Fatalf("external context manifest: %v", err)
			}
			response := responses[manifest.Nodes[0].NodeID]
			response.Endpoint = endpointSecret
			body := &externalProbeBody{
				reader:   bytes.NewReader(mustProbeResponseJSON(t, response)),
				closeErr: closeSentinel,
			}
			transportErr := error(probeSentinel)
			if test.wantProbeCalls == 0 {
				transportErr = nil
			}
			transport := &externalProbeTransport{body: body, err: transportErr}
			ctx := &externalProbeErrContext{sequence: test.sequence}
			got, err := externalProbeAcquire(t, ctx, now, manifest, transport)
			if err == nil {
				t.Fatal("custom context.Err failure unexpectedly succeeded")
			}
			assertZeroSnapshot(t, got)
			assertExternalProbeTransportOnly(t, err)
			assertExternalProbeSafe(t, err, endpointSecret, probeIdentity, "external-context-")
			assertExternalProbeBounded(t, err, test.wantIs...)
			if transport.probeCalls != test.wantProbeCalls {
				t.Fatalf("Probe calls = %d, want %d", transport.probeCalls, test.wantProbeCalls)
			}
			if body.closeCalls != test.wantCloseCalls {
				t.Fatalf("Close calls = %d, want %d", body.closeCalls, test.wantCloseCalls)
			}
			if got := int(ctx.calls.Load()); got != test.wantErrCalls {
				t.Fatalf("context.Err calls = %d, want %d", got, test.wantErrCalls)
			}
			if test.stateful != nil && test.stateful.calls.Load() != 1 {
				t.Fatalf("stateful context Unwrap calls = %d, want exactly 1", test.stateful.calls.Load())
			}
		})
	}
}

func TestAcquireSnapshotExternalContextErrorBeforeProbeIsSanitized(t *testing.T) {
	now, manifest, _ := acquireFixture(t, 1, []int{0})
	const contextSecret = "external-context-uncomparable-secret"
	probeContext := &externalProbeContext{
		Context: context.Background(),
		errs:    []error{externalProbePayloadError{Payload: []int{7, 8, 9}, text: contextSecret}},
	}
	transport := &externalProbeTransport{}
	got, err := externalProbeAcquire(t, probeContext, now, manifest, transport)
	if err == nil {
		t.Fatal("external context error unexpectedly succeeded")
	}
	assertZeroSnapshot(t, got)
	assertExternalProbeTransportOnly(t, err)
	assertExternalProbeSafe(t, err, contextSecret, manifest.Nodes[0].Endpoint, manifest.Nodes[0].ProbeIdentity)
	assertExternalProbeBounded(t, err)
	if probeContext.calls.Load() != 1 || transport.probeCalls != 0 {
		t.Fatalf("context Err calls=%d Probe calls=%d, want 1 and 0", probeContext.calls.Load(), transport.probeCalls)
	}
}

func TestAcquireSnapshotExternalContextErrorAfterProbeIsFrozen(t *testing.T) {
	now, manifest, responses := acquireFixture(t, 1, []int{0})
	const contextSecret = "external-context-stateful-secret"
	stateful := &externalProbeStatefulError{text: contextSecret, next: context.Canceled}
	probeContext := &externalProbeContext{Context: context.Background(), errs: []error{nil, stateful}}
	closeSentinel := errors.New("external-context-close-secret")
	response := responses[manifest.Nodes[0].NodeID]
	body := &externalProbeBody{reader: bytes.NewReader(mustProbeResponseJSON(t, response)), closeErr: closeSentinel}
	transport := &externalProbeTransport{body: body}
	got, err := externalProbeAcquire(t, probeContext, now, manifest, transport)
	if err == nil {
		t.Fatal("post-Probe external context error unexpectedly succeeded")
	}
	assertZeroSnapshot(t, got)
	assertExternalProbeTransportOnly(t, err)
	assertExternalProbeSafe(t, err, contextSecret, closeSentinel.Error(), manifest.Nodes[0].Endpoint, manifest.Nodes[0].ProbeIdentity)
	assertExternalProbeBounded(t, err, context.Canceled, closeSentinel)
	if probeContext.calls.Load() != 2 || stateful.calls.Load() != 1 {
		t.Fatalf("context Err calls=%d Unwrap calls=%d, want 2 and 1", probeContext.calls.Load(), stateful.calls.Load())
	}
	if transport.probeCalls != 1 || body.closeCalls != 1 {
		t.Fatalf("Probe calls=%d Close calls=%d, want exactly 1 each", transport.probeCalls, body.closeCalls)
	}
}

func TestAcquireSnapshotExternalCloseErrorTrustBoundary(t *testing.T) {
	for _, closeCase := range externalProbeCloseErrorCases() {
		for _, mode := range []string{"success", "malformed", "read", "context"} {
			t.Run(closeCase.name+"/"+mode, func(t *testing.T) {
				runExternalProbeCloseCase(t, closeCase, mode)
			})
		}
	}
}

func externalProbeCloseErrorCases() []externalProbeCloseErrorCase {
	return []externalProbeCloseErrorCase{
		{
			name: "ordinary",
			makeError: func(secret string) externalProbeErrorBuild {
				sentinel := errors.New("ordinary-close-sentinel-" + secret)
				return externalProbeErrorBuild{err: sentinel, wantIs: []error{sentinel}}
			},
		},
		{
			name: "self-cycle",
			makeError: func(secret string) externalProbeErrorBuild {
				return externalProbeErrorBuild{err: &externalProbeSelfError{text: secret}}
			},
		},
		{
			name: "stateful",
			makeError: func(secret string) externalProbeErrorBuild {
				sentinel := errors.New("stateful-close-sentinel-" + secret)
				stateful := &externalProbeStatefulError{text: secret, next: sentinel}
				return externalProbeErrorBuild{err: stateful, stateful: stateful, wantIs: []error{sentinel}}
			},
		},
		{
			name: "panic",
			makeError: func(secret string) externalProbeErrorBuild {
				return externalProbeErrorBuild{err: &externalProbePanicError{text: secret}}
			},
		},
		{
			name: "deep",
			makeError: func(secret string) externalProbeErrorBuild {
				return externalProbeErrorBuild{err: externalProbeDeepError(secret, errors.New("deep-close-sentinel-"+secret))}
			},
		},
		{
			name: "uncomparable-payload",
			makeError: func(secret string) externalProbeErrorBuild {
				return externalProbeErrorBuild{err: externalProbePayloadError{Payload: []int{4, 5, 6}, text: secret}}
			},
		},
	}
}

func runExternalProbeCloseCase(t *testing.T, closeCase externalProbeCloseErrorCase, mode string) {
	t.Helper()
	now, manifest, responses := acquireFixture(t, 1, []int{0})
	config := manifest.Nodes[0]
	response := responses[config.NodeID]
	built := closeCase.makeError(closeCase.name + "-" + mode + "-secret")
	body, readErr := externalProbeBodyForMode(t, response, built.err, closeCase.name, mode)
	transport := &externalProbeTransport{body: body}
	got, err := externalProbeAcquire(t, context.Background(), now, manifest, transport)
	if err == nil {
		t.Fatal("external Close failure unexpectedly succeeded")
	}
	assertZeroSnapshot(t, got)
	assertExternalProbeTransportOnly(t, err)
	assertExternalProbeSafe(t, err, config.Endpoint, config.ProbeIdentity, closeCase.name+"-"+mode+"-secret")
	wantIs := append([]error(nil), built.wantIs...)
	if readErr != nil {
		wantIs = append(wantIs, readErr)
	}
	assertExternalProbeBounded(t, err, wantIs...)
	if transport.probeCalls != 1 {
		t.Fatalf("Probe calls = %d, want exactly 1", transport.probeCalls)
	}
	if body.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want exactly 1", body.closeCalls)
	}
	if built.stateful != nil && built.stateful.calls.Load() != 1 {
		t.Fatalf("stateful Close Unwrap calls = %d, want exactly 1", built.stateful.calls.Load())
	}
}

func externalProbeBodyForMode(t *testing.T, response ProbeResponse, closeErr error, caseName, mode string) (*externalProbeBody, error) {
	t.Helper()
	var bodyData []byte
	var readErr error
	switch mode {
	case "success":
		bodyData = mustProbeResponseJSON(t, response)
	case "malformed":
		bodyData = []byte(`{"secret":"` + caseName + `-malformed-secret"`)
	case "read":
		bodyData = mustProbeResponseJSON(t, response)
		readErr = errors.New("ordinary-read-sentinel-" + caseName + "-" + mode)
	case "context":
		bodyData = mustProbeResponseJSON(t, response)
		readErr = context.Canceled
	}
	return &externalProbeBody{reader: bytes.NewReader(bodyData), readErr: readErr, closeErr: closeErr}, readErr
}

func externalProbeDeepError(secret string, terminal error) error {
	var err error = terminal
	for range maxProbeExternalErrorNodes + 1 {
		err = &externalProbeWrapError{text: secret, next: err}
	}
	return err
}

func externalProbeAcquire(t *testing.T, ctx context.Context, now time.Time, manifest Manifest, transport ProbeTransport) (SnapshotSet, error) {
	t.Helper()
	result := make(chan externalProbeAcquireResult, 1)
	var waitGroup sync.WaitGroup
	waitGroup.Go(func() {
		snapshot, err := AcquireSnapshot(ctx, manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
		result <- externalProbeAcquireResult{snapshot: snapshot, err: err}
	})
	select {
	case outcome := <-result:
		waitGroup.Wait()
		return outcome.snapshot, outcome.err
	case <-time.After(time.Second):
		t.Fatal("AcquireSnapshot external error handling exceeded 1s")
		return SnapshotSet{}, errors.New("unreachable external probe timeout")
	}
}

type externalProbeAcquireResult struct {
	snapshot SnapshotSet
	err      error
}

func assertExternalProbeTransportOnly(t *testing.T, err error) {
	t.Helper()
	if _, ok := errors.AsType[ProbeTransportError](err); !ok {
		t.Fatalf("error = %T %v, want ProbeTransportError", err, err)
	}
	if _, ok := errors.AsType[ProbeMalformedError](err); ok {
		t.Fatalf("error = %T %v, must not retain ProbeMalformedError", err, err)
	}
}

func assertExternalProbeBounded(t *testing.T, err error, sentinels ...error) {
	t.Helper()
	done := make(chan bool, 1)
	var waitGroup sync.WaitGroup
	waitGroup.Go(func() {
		for _, sentinel := range sentinels {
			if !errors.Is(err, sentinel) {
				done <- false
				return
			}
		}
		if _, ok := errors.AsType[ProbeTransportError](err); !ok {
			done <- false
			return
		}
		if _, ok := errors.AsType[ProbeMalformedError](err); ok {
			done <- false
			return
		}
		done <- true
	})
	select {
	case ok := <-done:
		waitGroup.Wait()
		if !ok {
			t.Fatal("bounded errors.Is/As lost expected classification or sentinel")
		}
	case <-time.After(time.Second):
		t.Fatal("errors.Is/As on sanitized external probe error exceeded 1s")
	}
}

func assertExternalProbeSafe(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.Contains(err.Error(), value) {
			t.Fatalf("external probe error leaks %q: %v", value, err)
		}
	}
}

var _ ProbeResponseBody = (*externalProbeBody)(nil)
var _ ProbeTransport = (*externalProbeTransport)(nil)
