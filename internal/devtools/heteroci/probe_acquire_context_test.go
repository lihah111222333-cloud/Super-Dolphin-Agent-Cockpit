package heteroci

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const acquireContextCancelTimeout = time.Second

type acquireContextCancelBody struct {
	started    chan<- context.Context
	startOnce  sync.Once
	closeCount atomic.Int32
	closeErr   error
}

func (body *acquireContextCancelBody) Read(ctx context.Context, _ []byte) (int, error) {
	body.startOnce.Do(func() { body.started <- ctx })
	<-ctx.Done()
	return 0, ctx.Err()
}

func (body *acquireContextCancelBody) Close() error {
	body.closeCount.Add(1)
	return body.closeErr
}

type acquireContextCancelTransport struct {
	body       ProbeResponseBody
	probeCount atomic.Int32
}

func (transport *acquireContextCancelTransport) Probe(context.Context, string, []byte) (ProbeResponseBody, error) {
	transport.probeCount.Add(1)
	return transport.body, nil
}

type acquireContextNilBodyTransport struct {
	started    chan<- context.Context
	probeCount atomic.Int32
	closeCount atomic.Int32
	body       ProbeResponseBody
}

func (transport *acquireContextNilBodyTransport) Probe(ctx context.Context, _ string, _ []byte) (ProbeResponseBody, error) {
	transport.probeCount.Add(1)
	transport.started <- ctx
	<-ctx.Done()
	return nil, nil
}

type acquireContextCancelOutcome struct {
	snapshot SnapshotSet
	err      error
}

func TestAcquireSnapshotCancelsBlockedProbeRead(t *testing.T) {
	now, manifest, _ := acquireFixture(t, 1, []int{0})
	manifest.Nodes[0].Endpoint = "https://probe-endpoint-secret.invalid/runner"
	secretProbeIdentity := manifest.Nodes[0].ProbeIdentity
	secretCloseCause := errors.New("probe-body-secret")
	started := make(chan context.Context, 1)
	body := &acquireContextCancelBody{started: started, closeErr: secretCloseCause}
	transport := &acquireContextCancelTransport{body: body}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan acquireContextCancelOutcome, 1)
	var waitGroup sync.WaitGroup
	waitGroup.Go(func() {
		snapshot, err := AcquireSnapshot(ctx, manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
		done <- acquireContextCancelOutcome{snapshot: snapshot, err: err}
	})

	waitForAcquireContextStart(t, started, ctx)
	cancel()
	outcome := waitForAcquireContextOutcome(t, done)
	waitGroup.Wait()
	assertAcquireContextCancellation(t, outcome, manifest.Nodes[0].Endpoint, secretProbeIdentity, secretCloseCause, body, transport)
}

func TestAcquireSnapshotTransportCancellationReturnsNilBody(t *testing.T) {
	now, manifest, _ := acquireFixture(t, 1, []int{0})
	manifest.Nodes[0].Endpoint = "https://nil-body-endpoint-secret.invalid/runner"
	secretProbeIdentity := manifest.Nodes[0].ProbeIdentity
	started := make(chan context.Context, 1)
	transport := &acquireContextNilBodyTransport{started: started}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan acquireContextCancelOutcome, 1)
	var waitGroup sync.WaitGroup
	waitGroup.Go(func() {
		snapshot, err := AcquireSnapshot(ctx, manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
		done <- acquireContextCancelOutcome{snapshot: snapshot, err: err}
	})

	waitForAcquireContextStart(t, started, ctx)
	cancel()
	outcome := waitForAcquireContextOutcome(t, done)
	waitGroup.Wait()
	assertAcquireNilBodyCancellation(t, outcome, manifest.Nodes[0].Endpoint, secretProbeIdentity, transport)
}

func waitForAcquireContextStart(t *testing.T, started <-chan context.Context, want context.Context) {
	t.Helper()
	timer := time.NewTimer(acquireContextCancelTimeout)
	defer timer.Stop()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("ProbeResponseBody.Read context = %p, want AcquireSnapshot context %p", got, want)
		}
	case <-timer.C:
		t.Fatal("ProbeResponseBody.Read did not start before timeout")
	}
}

func waitForAcquireContextOutcome(t *testing.T, done <-chan acquireContextCancelOutcome) acquireContextCancelOutcome {
	t.Helper()
	timer := time.NewTimer(acquireContextCancelTimeout)
	defer timer.Stop()
	select {
	case outcome := <-done:
		return outcome
	case <-timer.C:
		t.Fatal("AcquireSnapshot did not return after probe read cancellation")
		return acquireContextCancelOutcome{}
	}
}

func assertAcquireContextCancellation(t *testing.T, outcome acquireContextCancelOutcome, endpoint, probeIdentity string, closeCause error, body *acquireContextCancelBody, transport *acquireContextCancelTransport) {
	t.Helper()
	if outcome.err == nil || !errors.Is(outcome.err, context.Canceled) {
		t.Fatalf("AcquireSnapshot cancellation error = %v, want context.Canceled", outcome.err)
	}
	assertZeroSnapshot(t, outcome.snapshot)
	if _, ok := errors.AsType[ProbeTransportError](outcome.err); !ok {
		t.Fatalf("AcquireSnapshot cancellation error = %T %v, want ProbeTransportError", outcome.err, outcome.err)
	}
	if got := body.closeCount.Load(); got != 1 {
		t.Fatalf("ProbeResponseBody.Close calls = %d, want exactly 1", got)
	}
	if got := transport.probeCount.Load(); got != 1 {
		t.Fatalf("Probe calls = %d, want exactly 1", got)
	}
	for _, secret := range []string{endpoint, probeIdentity, closeCause.Error()} {
		if strings.Contains(outcome.err.Error(), secret) {
			t.Fatalf("AcquireSnapshot cancellation error leaks %q: %v", secret, outcome.err)
		}
	}
}

func assertAcquireNilBodyCancellation(t *testing.T, outcome acquireContextCancelOutcome, endpoint, probeIdentity string, transport *acquireContextNilBodyTransport) {
	t.Helper()
	if outcome.err == nil || !errors.Is(outcome.err, context.Canceled) {
		t.Fatalf("nil-body cancellation error = %v, want context.Canceled", outcome.err)
	}
	assertZeroSnapshot(t, outcome.snapshot)
	if _, ok := errors.AsType[ProbeTransportError](outcome.err); !ok {
		t.Fatalf("nil-body cancellation error = %T %v, want ProbeTransportError", outcome.err, outcome.err)
	}
	if got := transport.probeCount.Load(); got != 1 {
		t.Fatalf("nil-body Probe calls = %d, want exactly 1", got)
	}
	if transport.body != nil {
		t.Fatalf("nil-body transport retained body = %T, want nil", transport.body)
	}
	if got := transport.closeCount.Load(); got != 0 {
		t.Fatalf("nil-body Close calls = %d, want zero because body is not callable", got)
	}
	for _, secret := range []string{endpoint, probeIdentity} {
		if strings.Contains(outcome.err.Error(), secret) {
			t.Fatalf("nil-body cancellation error leaks %q: %v", secret, outcome.err)
		}
	}
}

var _ ProbeResponseBody = (*acquireContextCancelBody)(nil)
var _ ProbeTransport = (*acquireContextCancelTransport)(nil)
