package heteroci

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type acquireCloseCountBody struct {
	reader     *bytes.Reader
	readErr    error
	closeErr   error
	closeCalls int
}

func (body *acquireCloseCountBody) Read(_ context.Context, target []byte) (int, error) {
	if body.readErr != nil {
		return 0, body.readErr
	}
	return body.reader.Read(target)
}

func (body *acquireCloseCountBody) Close() error {
	body.closeCalls++
	return body.closeErr
}

type acquireCloseCountTransport struct {
	body       ProbeResponseBody
	err        error
	probeCalls int
}

func (transport *acquireCloseCountTransport) Probe(context.Context, string, []byte) (ProbeResponseBody, error) {
	transport.probeCalls++
	return transport.body, transport.err
}

type acquireCloseSelfError struct {
	message string
}

func (err *acquireCloseSelfError) Error() string {
	return err.message
}

func (err *acquireCloseSelfError) Unwrap() error {
	return err
}

type acquireCloseResult struct {
	snapshot SnapshotSet
	err      error
}

type acquireCloseErrorInspection struct {
	isTransport bool
	isMalformed bool
	isClose     bool
}

type acquireCloseErrorKind uint8

const (
	acquireCloseSuccess acquireCloseErrorKind = iota
	acquireCloseTransport
	acquireCloseMalformed
)

type acquireCloseCase struct {
	name       string
	body       []byte
	probeErr   error
	readErr    error
	closeErr   error
	wantKind   acquireCloseErrorKind
	wantCauses []error
}

func TestAcquireSnapshotClosesEveryNonNilBodyExactlyOnce(t *testing.T) {
	now, manifest, responses := acquireFixture(t, 1, []int{0})
	const (
		endpointSecret = "https://probe-close-endpoint-secret.invalid/runner?token=endpoint-token-secret"
		bodySecret     = "probe-close-body-secret"
	)
	manifest.Nodes[0].Endpoint = endpointSecret
	if err := manifest.Validate(); err != nil {
		t.Fatalf("close-count manifest: %v", err)
	}
	response := responses[manifest.Nodes[0].NodeID]
	response.Endpoint = endpointSecret
	validBody := mustProbeResponseJSON(t, response)
	malformedBody := []byte(`{"body":"` + bodySecret + `"`)
	transportSentinel := errors.New("transport failed with " + bodySecret)
	readSentinel := errors.New("read failed with " + bodySecret)
	closeSentinel := errors.New("close failed with " + bodySecret)

	tests := []acquireCloseCase{
		{name: "success", body: validBody, wantKind: acquireCloseSuccess},
		{name: "body and transport error", body: validBody, probeErr: transportSentinel, wantKind: acquireCloseTransport, wantCauses: []error{transportSentinel}},
		{name: "malformed", body: malformedBody, wantKind: acquireCloseMalformed},
		{name: "reader error", body: validBody, readErr: readSentinel, wantKind: acquireCloseTransport, wantCauses: []error{readSentinel}},
		{name: "close error", body: validBody, closeErr: closeSentinel, wantKind: acquireCloseTransport, wantCauses: []error{closeSentinel}},
		{name: "malformed and close error", body: malformedBody, closeErr: closeSentinel, wantKind: acquireCloseTransport, wantCauses: []error{closeSentinel}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runAcquireCloseCase(t, now, manifest, test, endpointSecret, manifest.Nodes[0].ProbeIdentity, bodySecret)
		})
	}
}

func TestAcquireSnapshotSelfUnwrappingReadAndCloseReturns(t *testing.T) {
	now, manifest, responses := acquireFixture(t, 1, []int{0})
	const (
		endpointSecret = "https://probe-self-close-endpoint-secret.invalid/runner?token=endpoint-token-secret"
		bodySecret     = "probe-self-close-body-secret"
	)
	manifest.Nodes[0].Endpoint = endpointSecret
	if err := manifest.Validate(); err != nil {
		t.Fatalf("self-unwrapping close manifest: %v", err)
	}
	response := responses[manifest.Nodes[0].NodeID]
	response.Endpoint = endpointSecret
	readErr := &acquireCloseSelfError{message: "read failed with " + bodySecret}
	closeSentinel := errors.New("close failed with " + bodySecret)
	body := &acquireCloseCountBody{reader: bytes.NewReader(mustProbeResponseJSON(t, response)), readErr: readErr, closeErr: closeSentinel}
	transport := &acquireCloseCountTransport{body: body}

	result := acquireCloseWithinOneSecond(t, now, manifest, transport)
	if transport.probeCalls != 1 {
		t.Fatalf("Probe calls = %d, want exactly 1", transport.probeCalls)
	}
	if body.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want exactly 1", body.closeCalls)
	}
	if result.err == nil {
		t.Fatal("self-unwrapping read and close failure unexpectedly succeeded")
	}
	assertZeroSnapshot(t, result.snapshot)
	assertProbeErrorSafe(t, result.err, endpointSecret, manifest.Nodes[0].ProbeIdentity, bodySecret)
	assertAcquireCloseInspectionWithinOneSecond(t, result.err, closeSentinel)
}

func runAcquireCloseCase(t *testing.T, now time.Time, manifest Manifest, test acquireCloseCase, forbidden ...string) {
	t.Helper()
	body := &acquireCloseCountBody{reader: bytes.NewReader(test.body), readErr: test.readErr, closeErr: test.closeErr}
	transport := &acquireCloseCountTransport{body: body, err: test.probeErr}
	got, err := AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
	if transport.probeCalls != 1 {
		t.Fatalf("Probe calls = %d, want exactly 1", transport.probeCalls)
	}
	if body.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want exactly 1", body.closeCalls)
	}
	if test.wantKind == acquireCloseSuccess {
		assertAcquireCloseSuccess(t, got, err, manifest, now)
		return
	}
	if err == nil {
		t.Fatal("AcquireSnapshot failure case unexpectedly succeeded")
	}
	assertZeroSnapshot(t, got)
	assertProbeErrorSafe(t, err, forbidden...)
	assertAcquireCloseErrorKind(t, err, test.wantKind)
	for _, cause := range test.wantCauses {
		if !errors.Is(err, cause) {
			t.Fatalf("error = %v, want errors.Is(%v)", err, cause)
		}
	}
}

func assertAcquireCloseSuccess(t *testing.T, got SnapshotSet, err error, manifest Manifest, now time.Time) {
	t.Helper()
	if err != nil {
		t.Fatalf("AcquireSnapshot success: %v", err)
	}
	if err := got.ValidateAgainst(manifest, now, acquireTestTTL); err != nil {
		t.Fatalf("successful snapshot validation: %v", err)
	}
}

func assertAcquireCloseErrorKind(t *testing.T, err error, want acquireCloseErrorKind) {
	t.Helper()
	_, isTransport := errors.AsType[ProbeTransportError](err)
	_, isMalformed := errors.AsType[ProbeMalformedError](err)
	switch want {
	case acquireCloseTransport:
		if isTransport && !isMalformed {
			return
		}
	case acquireCloseMalformed:
		if isMalformed && !isTransport {
			return
		}
	}
	t.Fatalf("error classification transport=%t malformed=%t, want kind %d: %T %v", isTransport, isMalformed, want, err, err)
}

func acquireCloseWithinOneSecond(t *testing.T, now time.Time, manifest Manifest, transport ProbeTransport) acquireCloseResult {
	t.Helper()
	result := make(chan acquireCloseResult, 1)
	var waitGroup sync.WaitGroup
	waitGroup.Go(func() {
		snapshot, err := AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
		result <- acquireCloseResult{snapshot: snapshot, err: err}
	})
	select {
	case outcome := <-result:
		waitGroup.Wait()
		return outcome
	case <-time.After(time.Second):
		t.Fatal("AcquireSnapshot did not return within 1s for self-unwrapping read error")
		return acquireCloseResult{}
	}
}

func assertAcquireCloseInspectionWithinOneSecond(t *testing.T, err error, closeSentinel error) {
	t.Helper()
	result := make(chan acquireCloseErrorInspection, 1)
	var waitGroup sync.WaitGroup
	waitGroup.Go(func() {
		_, isTransport := errors.AsType[ProbeTransportError](err)
		_, isMalformed := errors.AsType[ProbeMalformedError](err)
		result <- acquireCloseErrorInspection{
			isTransport: isTransport,
			isMalformed: isMalformed,
			isClose:     errors.Is(err, closeSentinel),
		}
	})
	select {
	case inspection := <-result:
		waitGroup.Wait()
		if !inspection.isTransport || inspection.isMalformed {
			t.Fatalf("error classification transport=%t malformed=%t, want transport only", inspection.isTransport, inspection.isMalformed)
		}
		if !inspection.isClose {
			t.Fatal("error does not retain close sentinel")
		}
	case <-time.After(time.Second):
		t.Fatal("probe error classification did not return within 1s")
	}
}
