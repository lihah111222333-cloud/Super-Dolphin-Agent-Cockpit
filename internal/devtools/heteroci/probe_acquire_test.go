package heteroci

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	acquireTestOwnerEpoch         RoutingOwnerEpoch = 17
	acquireTestTTL                                  = 30 * time.Second
	acquireTestNowUnixMilli       int64             = 1_701_000_000_000
	acquireTestMemoryBytes        int64             = 8 * 1024 * 1024 * 1024
	acquireTestImageDigest                          = "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	acquireTestWrongImageDigest                     = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	acquireTestWrongProbeIdentity                   = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
)

type acquireProbeSelfError struct{}

func (*acquireProbeSelfError) Error() string     { return "self probe error" }
func (err *acquireProbeSelfError) Unwrap() error { return err }

type acquireProbeCall struct {
	endpoint string
	request  []byte
}

type acquireProbeScript struct {
	body     []byte
	err      error
	closeErr error
	readErr  error
	reader   *acquireReadCloser
}

type acquireReadCloser struct {
	*bytes.Reader
	closeErr error
	readErr  error
	closed   bool
}

func (reader *acquireReadCloser) Read(_ context.Context, p []byte) (int, error) {
	if reader.readErr != nil {
		return 0, reader.readErr
	}
	return reader.Reader.Read(p)
}

func (reader *acquireReadCloser) Close() error {
	reader.closed = true
	return reader.closeErr
}

type acquireProbeTransport struct {
	scripts map[string]*acquireProbeScript
	calls   []acquireProbeCall
}

func (transport *acquireProbeTransport) Probe(_ context.Context, endpoint string, request []byte) (ProbeResponseBody, error) {
	transport.calls = append(transport.calls, acquireProbeCall{
		endpoint: endpoint,
		request:  append([]byte(nil), request...),
	})
	script := transport.scripts[endpoint]
	if script == nil {
		return nil, errors.New("missing probe script")
	}
	if script.body == nil {
		return nil, script.err
	}
	reader := &acquireReadCloser{Reader: bytes.NewReader(script.body), closeErr: script.closeErr, readErr: script.readErr}
	script.reader = reader
	return reader, script.err
}

func TestAcquireSnapshotNodeAndRequestOrderIsDeterministic(t *testing.T) {
	for _, nodeCount := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("nodes_%d_shuffled", nodeCount), func(t *testing.T) {
			now, manifest, responses := acquireFixture(t, nodeCount, []int{2, 0, 1})
			transport := acquireTransportForResponses(manifest, responses)
			got, err := AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
			if err != nil {
				t.Fatalf("AcquireSnapshot(%d): %v", nodeCount, err)
			}
			if err := got.ValidateAgainst(manifest, now, acquireTestTTL); err != nil {
				t.Fatalf("acquired snapshot validation: %v", err)
			}
			wantIDs := manifestNodeIDs(manifest)
			if gotIDs := snapshotNodeIDs(got); !reflect.DeepEqual(gotIDs, wantIDs) {
				t.Fatalf("snapshot node order = %v, want %v", gotIDs, wantIDs)
			}
			if len(transport.calls) != nodeCount {
				t.Fatalf("Probe calls = %d, want %d", len(transport.calls), nodeCount)
			}
			for index, nodeID := range wantIDs {
				config := manifestNodeConfig(t, manifest, nodeID)
				call := transport.calls[index]
				if call.endpoint != config.Endpoint {
					t.Errorf("call %d endpoint = %q, want %q", index, call.endpoint, config.Endpoint)
				}
				wantRequest := acquireExpectedProbeRequest(config, acquireTestOwnerEpoch)
				if string(call.request) != string(wantRequest) {
					t.Errorf("call %d request = %s, want exact %s", index, call.request, wantRequest)
				}
				assertProbeRequestShape(t, call.request, config, acquireTestOwnerEpoch)
			}
		})
	}
}

func TestAcquireSnapshotCallsEachNodeExactlyOnce(t *testing.T) {
	now, manifest, responses := acquireFixture(t, 3, []int{2, 0, 1})
	transport := acquireTransportForResponses(manifest, responses)
	if _, err := AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL); err != nil {
		t.Fatalf("AcquireSnapshot: %v", err)
	}
	counts := make(map[string]int, len(manifest.Nodes))
	for _, call := range transport.calls {
		for _, node := range manifest.Nodes {
			if node.Endpoint == call.endpoint {
				counts[node.NodeID]++
			}
		}
	}
	for _, node := range manifest.Nodes {
		if counts[node.NodeID] != 1 {
			t.Errorf("node %q Probe calls = %d, want exactly once", node.NodeID, counts[node.NodeID])
		}
	}
}

func TestAcquireSnapshotUnavailableProducesFreshOfflineNode(t *testing.T) {
	now, manifest, responses := acquireFixture(t, 2, []int{1, 0})
	offlineID := "heteroci-node-1"
	offlineConfig := manifestNodeConfig(t, manifest, offlineID)
	responses[offlineID] = ProbeResponse{}
	transport := acquireTransportForResponses(manifest, responses)
	transport.scripts[offlineConfig.Endpoint] = &acquireProbeScript{err: ErrProbeTransportUnavailable}

	got, err := AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
	if err != nil {
		t.Fatalf("unavailable probe should produce offline snapshot: %v", err)
	}
	node := snapshotNode(t, got, offlineID)
	assertFreshOfflineNode(t, node, offlineConfig)
}

func TestAcquireSnapshotWrappedUnavailableProducesOfflineNode(t *testing.T) {
	now, manifest, responses := acquireFixture(t, 1, []int{0})
	config := manifest.Nodes[0]
	transport := acquireTransportForResponses(manifest, responses)
	transport.scripts[config.Endpoint] = &acquireProbeScript{err: fmt.Errorf("wrapped unavailable: %w", ErrProbeTransportUnavailable)}
	got, err := AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
	if err != nil {
		t.Fatalf("wrapped unavailable should produce offline snapshot: %v", err)
	}
	node := snapshotNode(t, got, config.NodeID)
	if node.Status != NodeStatusOffline || node.HeartbeatUnixMilli != 0 || node.ActiveCapabilities == nil || len(node.ActiveCapabilities) != 0 {
		t.Fatalf("wrapped unavailable node = %#v, want fresh offline state", node)
	}
}

func TestAcquireSnapshotTransportFailuresAreFatalAndAtomic(t *testing.T) {
	fatalProbeCause := errors.New("endpoint-secret probe-secret fatal")
	tests := []struct {
		name   string
		make   func([]byte) *acquireProbeScript
		causes []error
	}{
		{name: "generic transport", make: func(_ []byte) *acquireProbeScript {
			return &acquireProbeScript{err: errors.New("transport sentinel")}
		}},
		{name: "context cancelled", make: func(_ []byte) *acquireProbeScript {
			return &acquireProbeScript{err: context.Canceled}
		}, causes: []error{context.Canceled}},
		{name: "joined unavailable and fatal", make: func(_ []byte) *acquireProbeScript {
			return &acquireProbeScript{err: errors.Join(ErrProbeTransportUnavailable, fatalProbeCause)}
		}, causes: []error{ErrProbeTransportUnavailable, fatalProbeCause}},
		{name: "self unwrapping", make: func(_ []byte) *acquireProbeScript {
			return &acquireProbeScript{err: &acquireProbeSelfError{}}
		}},
		{name: "deep unavailable wrapper", make: func(_ []byte) *acquireProbeScript {
			var err error = ErrProbeTransportUnavailable
			for range 65 {
				err = fmt.Errorf("wrapped probe: %w", err)
			}
			return &acquireProbeScript{err: err}
		}},
		{name: "nil body", make: func(_ []byte) *acquireProbeScript { return &acquireProbeScript{} }},
		{name: "body and error", make: func(body []byte) *acquireProbeScript {
			return &acquireProbeScript{body: body, err: errors.New("body error")}
		}},
		{name: "body and unavailable", make: func(body []byte) *acquireProbeScript {
			return &acquireProbeScript{body: body, err: fmt.Errorf("wrapped unavailable: %w", ErrProbeTransportUnavailable)}
		}},
		{name: "reader read error", make: func(body []byte) *acquireProbeScript {
			return &acquireProbeScript{body: body, readErr: errors.New("reader sentinel")}
		}},
		{name: "close error", make: func(body []byte) *acquireProbeScript {
			return &acquireProbeScript{body: body, closeErr: errors.New("close sentinel")}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now, manifest, responses := acquireFixture(t, 2, []int{1, 0})
			firstID := "heteroci-node-1"
			secondID := "heteroci-node-2"
			transport := acquireTransportForResponses(manifest, responses)
			secondConfig := manifestNodeConfig(t, manifest, secondID)
			validBody := mustProbeResponseJSON(t, responses[secondID])
			transport.scripts[secondConfig.Endpoint] = tt.make(validBody)
			got, err := AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
			if err == nil {
				t.Fatal("transport failure unexpectedly succeeded")
			}
			assertZeroSnapshot(t, got)
			assertProbeFailureError(t, err, false)
			assertProbeErrorSafe(t, err, "endpoint-secret", "probe-secret")
			for _, cause := range tt.causes {
				if !errors.Is(err, cause) {
					t.Fatalf("error = %v, want errors.Is(%v)", err, cause)
				}
			}
			if len(transport.calls) != 2 {
				t.Fatalf("Probe calls = %d, want first successful node %q and failing node %q", len(transport.calls), firstID, secondID)
			}
			if script := transport.scripts[secondConfig.Endpoint]; script.reader != nil && !script.reader.closed {
				t.Fatal("non-nil failing response body was not closed")
			}
		})
	}
}

func TestAcquireSnapshotRejectsMaximumOwnerEpochBeforeTransport(t *testing.T) {
	now, manifest, responses := acquireFixture(t, 1, []int{0})
	transport := acquireTransportForResponses(manifest, responses)
	got, err := AcquireSnapshot(context.Background(), manifest, RoutingOwnerEpoch(1<<63-1), transport, now, acquireTestTTL)
	if err == nil {
		t.Fatal("maximum owner epoch unexpectedly succeeded")
	}
	assertZeroSnapshot(t, got)
	var argument ProbeArgumentError
	if !errors.As(err, &argument) || argument.Field != "owner_epoch" {
		t.Fatalf("maximum owner epoch error = %T %v, want ProbeArgumentError(owner_epoch)", err, err)
	}
	if len(transport.calls) != 0 {
		t.Fatalf("maximum owner epoch Probe calls = %d, want zero", len(transport.calls))
	}
}

func TestAcquireSnapshotCancelledContextIsFatalAndAtomic(t *testing.T) {
	now, manifest, responses := acquireFixture(t, 2, []int{1, 0})
	transport := acquireTransportForResponses(manifest, responses)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := AcquireSnapshot(ctx, manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error = %v, want context.Canceled", err)
	}
	assertZeroSnapshot(t, got)
	if len(transport.calls) != 0 {
		t.Fatalf("cancelled context Probe calls = %d, want zero", len(transport.calls))
	}
}

func TestAcquireSnapshotRejectsOversizedRequestBeforeTransport(t *testing.T) {
	now := time.UnixMilli(acquireTestNowUnixMilli)
	manifest := snapshotTestManifest(1)
	manifest.Nodes[0].NodeID = strings.Repeat("n", acquireProbeRequestMaxBytes)
	if err := manifest.Validate(); err != nil {
		t.Fatalf("oversized request fixture manifest: %v", err)
	}
	transport := &acquireProbeTransport{scripts: map[string]*acquireProbeScript{manifest.Nodes[0].Endpoint: {}}}
	got, err := AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
	if err == nil {
		t.Fatal("oversized request unexpectedly succeeded")
	}
	assertZeroSnapshot(t, got)
	var argument ProbeArgumentError
	if !errors.As(err, &argument) || argument.Field != "request" {
		t.Fatalf("oversized request error = %T %v, want ProbeArgumentError(request)", err, err)
	}
	if len(transport.calls) != 0 {
		t.Fatalf("oversized request Probe calls = %d, want zero", len(transport.calls))
	}
}

func TestAcquireSnapshotStrictlyCopiesSuccessfulResponse(t *testing.T) {
	now, manifest, responses := acquireFixture(t, 1, []int{0})
	config := manifest.Nodes[0]
	response := responses[config.NodeID]
	response.Total = ResourceCapacity{CPUMilli: 8_000, MemoryBytes: 16 * 1024 * 1024 * 1024, MaxConcurrency: 8}
	response.Available = ResourceCapacity{CPUMilli: 3_333, MemoryBytes: 7 * 1024 * 1024 * 1024, MaxConcurrency: 3}
	response.HeartbeatUnixMilli = now.Add(-time.Second).UnixMilli()
	transport := acquireTransportForResponses(manifest, map[string]ProbeResponse{config.NodeID: response})
	got, err := AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
	if err != nil {
		t.Fatalf("AcquireSnapshot: %v", err)
	}
	node := got.Nodes[0]
	assertAcquiredRoot(t, got, now, acquireTestOwnerEpoch)
	assertAcquiredNode(t, node, response)
	if err := got.ValidateAgainst(manifest, now, acquireTestTTL); err != nil {
		t.Fatalf("strict acquired snapshot validation: %v", err)
	}
}

func assertFreshOfflineNode(t *testing.T, node NodeSnapshot, config NodeConfig) {
	t.Helper()
	want := NodeSnapshot{
		NodeID: config.NodeID, Endpoint: config.Endpoint, Platform: config.Platform, ProbeIdentity: config.ProbeIdentity,
		ActiveCapabilities: []ActiveCapabilitySnapshot{}, Status: NodeStatusOffline,
	}
	if !reflect.DeepEqual(node, want) {
		t.Fatalf("offline node = %#v, want fresh zero-capacity state %#v", node, want)
	}
}

func assertAcquiredRoot(t *testing.T, snapshot SnapshotSet, now time.Time, ownerEpoch RoutingOwnerEpoch) {
	t.Helper()
	if snapshot.SchemaVersion != SnapshotSchemaVersion || snapshot.Authority != AuthorityLocalNonAuthoritative || snapshot.OwnerEpoch != ownerEpoch || snapshot.CapacityBasis != SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations {
		t.Fatalf("snapshot root = %#v, want strict schema/authority/basis", snapshot)
	}
	if snapshot.CapturedAtUnixMilli != now.UnixMilli() {
		t.Fatalf("captured_at_unix_milli = %d, want %d", snapshot.CapturedAtUnixMilli, now.UnixMilli())
	}
}

func assertAcquiredNode(t *testing.T, node NodeSnapshot, response ProbeResponse) {
	t.Helper()
	want := NodeSnapshot{
		NodeID: response.NodeID, Endpoint: response.Endpoint, Platform: response.Platform, ProbeIdentity: response.ProbeIdentity,
		PhysicalAttestation: response.PhysicalAttestation,
		ProbeBootID:         response.ProbeBootID, ActiveCapabilities: response.ActiveCapabilities, Status: response.Status,
		HeartbeatUnixMilli: response.HeartbeatUnixMilli, Total: response.Total, Available: response.Available,
	}
	if !reflect.DeepEqual(node, want) {
		t.Fatalf("acquired node = %#v, want exact response projection %#v", node, want)
	}
}

func TestAcquireSnapshotResponseIdentityErrorsAreTyped(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProbeResponse)
	}{
		{name: "node id", mutate: func(response *ProbeResponse) { response.NodeID = "unexpected-node" }},
		{name: "endpoint", mutate: func(response *ProbeResponse) { response.Endpoint = "https://other.invalid/runner" }},
		{name: "probe identity", mutate: func(response *ProbeResponse) { response.ProbeIdentity = acquireTestWrongProbeIdentity }},
		{name: "owner epoch", mutate: func(response *ProbeResponse) { response.OwnerEpoch++ }},
		{name: "platform", mutate: func(response *ProbeResponse) { response.Platform = PlatformWindowsAMD64 }},
		{name: "capabilities empty", mutate: func(response *ProbeResponse) {
			response.ActiveCapabilities = []ActiveCapabilitySnapshot{}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now, manifest, responses := acquireFixture(t, 1, []int{0})
			response := responses[manifest.Nodes[0].NodeID]
			tt.mutate(&response)
			transport := acquireTransportForResponses(manifest, map[string]ProbeResponse{manifest.Nodes[0].NodeID: response})
			got, err := AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
			if err == nil {
				t.Fatal("identity drift unexpectedly succeeded")
			}
			assertZeroSnapshot(t, got)
			assertTypedProbeError(t, err, "identity", true)
		})
	}
}

type acquireResponseErrorCase struct {
	name         string
	wantIdentity bool
	mutate       func(*ProbeResponse)
}

func assertAcquireResponseErrors(t *testing.T, now time.Time, manifest Manifest, responses map[string]ProbeResponse, nodeID string, tests []acquireResponseErrorCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := responses[nodeID]
			response.ActiveCapabilities = cloneActiveCapabilities(response.ActiveCapabilities)
			tt.mutate(&response)
			transport := acquireTransportForResponses(manifest, map[string]ProbeResponse{nodeID: response})
			got, err := AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
			if err == nil {
				t.Fatal("invalid capability or qualification response unexpectedly succeeded")
			}
			assertZeroSnapshot(t, got)
			if len(transport.calls) != 1 {
				t.Fatalf("Probe calls = %d, want exactly 1", len(transport.calls))
			}
			assertProbeErrorSafe(t, err, manifest.Nodes[0].Endpoint, manifest.Nodes[0].ProbeIdentity)
			assertTypedProbeError(t, err, tt.name, tt.wantIdentity)
		})
	}
}

func TestAcquireSnapshotCapabilityIdentityErrors(t *testing.T) {
	now := time.UnixMilli(acquireTestNowUnixMilli)
	manifest := snapshotTestCapabilityManifest(testUbuntuCapability(), testWineCapability())
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Ubuntu/Wine fixture manifest: %v", err)
	}
	responses := acquireResponsesForManifest(t, manifest, now)
	assertAcquireResponseErrors(t, now, manifest, responses, manifest.Nodes[0].NodeID, []acquireResponseErrorCase{
		{name: "capability set", wantIdentity: true, mutate: func(response *ProbeResponse) { response.ActiveCapabilities = []ActiveCapabilitySnapshot{} }},
		{name: "capability runtime", wantIdentity: true, mutate: func(response *ProbeResponse) { response.ActiveCapabilities[1].Runtime = RuntimeKind("retired_runtime") }},
		{name: "capability image", wantIdentity: true, mutate: func(response *ProbeResponse) {
			response.ActiveCapabilities[0].ImageDigest = acquireTestWrongImageDigest
		}},
	})
}

func TestAcquireSnapshotResponseProtocolErrorsAreTyped(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(time.Time, *ProbeResponse)
	}{
		{name: "schema", mutate: func(_ time.Time, response *ProbeResponse) { response.SchemaVersion = "heteroci-probe-response/v0" }},
		{name: "capacity basis", mutate: func(_ time.Time, response *ProbeResponse) {
			response.CapacityBasis = SnapshotCapacityBasis("unknown-basis/v0")
		}},
		{name: "status", mutate: func(_ time.Time, response *ProbeResponse) { response.Status = NodeStatus("unknown") }},
		{name: "boot", mutate: func(_ time.Time, response *ProbeResponse) { response.ProbeBootID = "" }},
		{name: "capacity", mutate: func(_ time.Time, response *ProbeResponse) {
			response.Available.CPUMilli = response.Total.CPUMilli + 1
		}},
		{name: "heartbeat", mutate: func(now time.Time, response *ProbeResponse) {
			response.HeartbeatUnixMilli = now.Add(acquireTestTTL + time.Millisecond).UnixMilli()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now, manifest, responses := acquireFixture(t, 1, []int{0})
			response := responses[manifest.Nodes[0].NodeID]
			tt.mutate(now, &response)
			transport := acquireTransportForResponses(manifest, map[string]ProbeResponse{manifest.Nodes[0].NodeID: response})
			got, err := AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
			if err == nil {
				t.Fatal("invalid protocol response unexpectedly succeeded")
			}
			assertZeroSnapshot(t, got)
			assertTypedProbeError(t, err, "protocol", false)
		})
	}
}

func TestAcquireSnapshotCanPlanAndStartLocalLease(t *testing.T) {
	now, manifest, responses := acquireFixture(t, 1, []int{0})
	transport := acquireTransportForResponses(manifest, responses)
	snapshot, err := AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
	if err != nil {
		t.Fatalf("AcquireSnapshot: %v", err)
	}
	unit := ExecutionUnit{
		UnitID:              "unit-acquired-local",
		TargetPlatform:      PlatformLinuxAMD64,
		RequiredRuntime:     RuntimeUbuntuContainer,
		EstimatedDurationMS: 1_000,
		CPUMilli:            1_000,
		MemoryBytes:         1 * 1024 * 1024 * 1024,
	}
	book := NewRoutingLeaseBook()
	reserved, err := book.PlanAndReserve("run-acquired", manifest, snapshot, []ExecutionUnit{unit}, now, acquireTestTTL)
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	assertAcquiredRoot(t, snapshot, now, acquireTestOwnerEpoch)
	assertAcquiredRoot(t, reserved.SourceSnapshot, now, acquireTestOwnerEpoch)
	assertAcquiredRoot(t, reserved.EffectiveSnapshot, now, acquireTestOwnerEpoch)
	if len(reserved.Leases) != 1 || reserved.Leases[0].Executor != ExecutorKindLocalNode {
		t.Fatalf("reserved leases = %#v, want one local lease", reserved.Leases)
	}
	started, err := book.Start("run-acquired", unit.UnitID, snapshot, now.Add(time.Millisecond))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if started.State != LeaseStateStarted || started.Endpoint != manifest.Nodes[0].Endpoint || started.NodeID != manifest.Nodes[0].NodeID {
		t.Fatalf("started lease = %#v, want local endpoint/node and started state", started)
	}
}

func TestAcquireSnapshotRedactsSecretsFromErrors(t *testing.T) {
	secretEndpoint := "https://secret.example/runner?token=endpoint-secret"
	secretBody := "response-secret-6d4f"
	now, manifest, _ := acquireFixture(t, 1, []int{0})
	manifest.Nodes[0].Endpoint = secretEndpoint
	transport := &acquireProbeTransport{scripts: map[string]*acquireProbeScript{
		secretEndpoint: {err: errors.New("endpoint=" + secretEndpoint + " body=" + secretBody + " probe=probe-secret")},
	}}
	got, err := AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
	if err == nil {
		t.Fatal("secret-bearing transport failure unexpectedly succeeded")
	}
	assertZeroSnapshot(t, got)
	for _, forbidden := range []string{secretEndpoint, "endpoint-secret", secretBody, "probe-secret"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("AcquireSnapshot error leaks %q: %v", forbidden, err)
		}
	}

	transport = &acquireProbeTransport{scripts: map[string]*acquireProbeScript{
		secretEndpoint: {body: []byte(`{"schema_version":"heteroci-probe-response/v1","secret":"` + secretBody + `"}`)},
	}}
	got, err = AcquireSnapshot(context.Background(), manifest, acquireTestOwnerEpoch, transport, now, acquireTestTTL)
	if err == nil {
		t.Fatal("secret-bearing malformed response unexpectedly succeeded")
	}
	assertZeroSnapshot(t, got)
	if strings.Contains(err.Error(), secretBody) {
		t.Fatalf("malformed response error leaks body secret: %v", err)
	}
}

func acquireFixture(t *testing.T, nodeCount int, order []int) (time.Time, Manifest, map[string]ProbeResponse) {
	t.Helper()
	now := time.UnixMilli(acquireTestNowUnixMilli)
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Authority: AuthorityLocalNonAuthoritative, Nodes: make([]NodeConfig, nodeCount)}
	for index := range manifest.Nodes {
		nodeID := fmt.Sprintf("heteroci-node-%d", index+1)
		manifest.Nodes[index] = NodeConfig{NodeID: nodeID, Endpoint: "https://" + nodeID + ".invalid/runner", ProbeIdentity: snapshotTestProbeIdentity(index), Platform: PlatformLinuxAMD64, Capabilities: []CapabilityConfig{{Runtime: RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: acquireTestImageDigest}}}
	}
	if len(order) >= nodeCount {
		nodes := append([]NodeConfig(nil), manifest.Nodes...)
		manifest.Nodes = make([]NodeConfig, 0, nodeCount)
		for _, index := range order {
			if index >= 0 && index < nodeCount {
				manifest.Nodes = append(manifest.Nodes, nodes[index])
			}
		}
	}
	for index := range manifest.Nodes {
		manifest.Nodes[index].Capabilities[0].ImageDigest = acquireTestImageDigest
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("acquire fixture manifest: %v", err)
	}
	return now, manifest, acquireResponsesForManifest(t, manifest, now)
}

func acquireResponsesForManifest(t *testing.T, manifest Manifest, now time.Time) map[string]ProbeResponse {
	t.Helper()
	canonical := snapshotForManifest(manifest, now)
	responses := make(map[string]ProbeResponse, len(manifest.Nodes))
	for index, node := range canonical.Nodes {
		config := manifest.Nodes[index]
		responses[node.NodeID] = ProbeResponse{
			SchemaVersion:       ProbeResponseSchemaVersion,
			OwnerEpoch:          acquireTestOwnerEpoch,
			CapacityBasis:       SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations,
			NodeID:              node.NodeID,
			Endpoint:            node.Endpoint,
			Platform:            node.Platform,
			ProbeIdentity:       node.ProbeIdentity,
			PhysicalAttestation: node.PhysicalAttestation,
			ProbeBootID:         node.ProbeBootID,
			ActiveCapabilities:  cloneActiveCapabilities(node.ActiveCapabilities),
			Status:              node.Status,
			HeartbeatUnixMilli:  node.HeartbeatUnixMilli,
			Total:               ResourceCapacity{CPUMilli: 4_000, MemoryBytes: acquireTestMemoryBytes, MaxConcurrency: 4},
			Available:           ResourceCapacity{CPUMilli: 4_000, MemoryBytes: acquireTestMemoryBytes, MaxConcurrency: 4},
		}
		if config.NodeID != node.NodeID {
			t.Fatalf("fixture response/config node mismatch: %q/%q", node.NodeID, config.NodeID)
		}
	}
	return responses
}

func acquireTransportForResponses(manifest Manifest, responses map[string]ProbeResponse) *acquireProbeTransport {
	transport := &acquireProbeTransport{scripts: make(map[string]*acquireProbeScript, len(manifest.Nodes))}
	for _, node := range manifest.Nodes {
		response := responses[node.NodeID]
		transport.scripts[node.Endpoint] = &acquireProbeScript{body: mustProbeResponseJSONNoTest(response)}
	}
	return transport
}

func mustProbeResponseJSON(t *testing.T, response ProbeResponse) []byte {
	t.Helper()
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal probe response: %v", err)
	}
	return encoded
}

func mustProbeResponseJSONNoTest(response ProbeResponse) []byte {
	encoded, err := json.Marshal(response)
	if err != nil {
		// archguard:ignore panic_count -- 无 testing 参数的夹具编码失败必须立即暴露
		panic(err)
	}
	return encoded
}

func acquireExpectedProbeRequest(config NodeConfig, epoch RoutingOwnerEpoch) []byte {
	return fmt.Appendf(nil, `{"schema_version":"%s","node_id":"%s","probe_identity":"%s","owner_epoch":%d,"captured_at_unix_milli":%d}`, ProbeRequestSchemaVersion, config.NodeID, config.ProbeIdentity, epoch, acquireTestNowUnixMilli)
}

func assertProbeRequestShape(t *testing.T, raw []byte, config NodeConfig, epoch RoutingOwnerEpoch) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode probe request: %v", err)
	}
	if len(object) != 5 {
		t.Fatalf("probe request fields = %d, want exact 5: %s", len(object), raw)
	}
	var request ProbeRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatalf("decode ProbeRequest: %v", err)
	}
	if request != (ProbeRequest{SchemaVersion: ProbeRequestSchemaVersion, NodeID: config.NodeID, ProbeIdentity: config.ProbeIdentity, OwnerEpoch: epoch, CapturedAtUnixMilli: acquireTestNowUnixMilli}) {
		t.Fatalf("decoded request = %#v, want exact owner/node identity", request)
	}
}

func assertTypedProbeError(t *testing.T, err error, kind string, identity bool) {
	t.Helper()
	if identity {
		var value ProbeIdentityDriftError
		var pointer *ProbeIdentityDriftError
		if errors.As(err, &value) || errors.As(err, &pointer) {
			return
		}
	} else {
		var value ProbeProtocolError
		var pointer *ProbeProtocolError
		if errors.As(err, &value) || errors.As(err, &pointer) {
			return
		}
	}
	t.Fatalf("%s error type = %T, want typed probe error: %v", kind, err, err)
}

func assertProbeFailureError(t *testing.T, err error, wantMalformed bool) {
	t.Helper()
	if wantMalformed {
		if _, ok := errors.AsType[ProbeMalformedError](err); ok {
			return
		}
		t.Fatalf("malformed error type = %T, want ProbeMalformedError: %v", err, err)
	}
	if _, ok := errors.AsType[ProbeTransportError](err); ok {
		return
	}
	t.Fatalf("transport error type = %T, want ProbeTransportError: %v", err, err)
}

func assertProbeErrorSafe(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.Contains(err.Error(), value) {
			t.Fatalf("probe error leaks %q: %v", value, err)
		}
	}
}

func assertZeroSnapshot(t *testing.T, got SnapshotSet) {
	t.Helper()
	if !reflect.DeepEqual(got, SnapshotSet{}) {
		t.Fatalf("failed AcquireSnapshot returned partial snapshot: %#v", got)
	}
}

func manifestNodeIDs(manifest Manifest) []string {
	ids := make([]string, 0, len(manifest.Nodes))
	for _, node := range manifest.Nodes {
		ids = append(ids, node.NodeID)
	}
	sort.Strings(ids)
	return ids
}

func snapshotNodeIDs(snapshot SnapshotSet) []string {
	ids := make([]string, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		ids = append(ids, node.NodeID)
	}
	return ids
}

func manifestNodeConfig(t *testing.T, manifest Manifest, nodeID string) NodeConfig {
	t.Helper()
	for _, node := range manifest.Nodes {
		if node.NodeID == nodeID {
			return node
		}
	}
	t.Fatalf("manifest node %q is missing", nodeID)
	return NodeConfig{}
}

func snapshotNode(t *testing.T, snapshot SnapshotSet, nodeID string) NodeSnapshot {
	t.Helper()
	for _, node := range snapshot.Nodes {
		if node.NodeID == nodeID {
			return node
		}
	}
	t.Fatalf("snapshot node %q is missing", nodeID)
	return NodeSnapshot{}
}

func cloneActiveCapabilities(source []ActiveCapabilitySnapshot) []ActiveCapabilitySnapshot {
	if source == nil {
		return nil
	}
	clone := make([]ActiveCapabilitySnapshot, len(source))
	copy(clone, source)
	for index, capability := range source {
		if capability.Qualification == nil {
			continue
		}
		receipt := *capability.Qualification
		clone[index].Qualification = &receipt
	}
	return clone
}
