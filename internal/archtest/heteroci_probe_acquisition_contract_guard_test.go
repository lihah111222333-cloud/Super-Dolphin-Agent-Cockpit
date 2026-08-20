package archtest

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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// TestHeterociProbeWireContractsAreExact freezes the probe request/response
// field order, Go types, JSON tags, and schema identities at the wire boundary.
func TestHeterociProbeWireContractsAreExact(t *testing.T) {
	if got, want := heteroci.ProbeRequestSchemaVersion, "heteroci-probe-request/v1"; got != want {
		t.Fatalf("ProbeRequestSchemaVersion = %q, want %q", got, want)
	}
	if got, want := heteroci.ProbeResponseSchemaVersion, "heteroci-probe-response/v1"; got != want {
		t.Fatalf("ProbeResponseSchemaVersion = %q, want %q", got, want)
	}
	if typ := reflect.TypeFor[heteroci.RoutingOwnerEpoch](); typ.Kind() != reflect.Int64 || typ.Bits() != 64 {
		t.Fatalf("RoutingOwnerEpoch = %s/%d, want int64/64", typ.Kind(), typ.Bits())
	}
	assertHeterociProbeStructLayout(t, reflect.TypeFor[heteroci.ProbeRequest](), []heterociProbeFieldSpec{
		{name: "SchemaVersion", typ: reflect.TypeFor[string](), tag: `json:"schema_version"`},
		{name: "NodeID", typ: reflect.TypeFor[string](), tag: `json:"node_id"`},
		{name: "ProbeIdentity", typ: reflect.TypeFor[string](), tag: `json:"probe_identity"`},
		{name: "OwnerEpoch", typ: reflect.TypeFor[heteroci.RoutingOwnerEpoch](), tag: `json:"owner_epoch"`},
	})
	assertHeterociProbeStructLayout(t, reflect.TypeFor[heteroci.ProbeResponse](), []heterociProbeFieldSpec{
		{name: "SchemaVersion", typ: reflect.TypeFor[string](), tag: `json:"schema_version"`},
		{name: "OwnerEpoch", typ: reflect.TypeFor[heteroci.RoutingOwnerEpoch](), tag: `json:"owner_epoch"`},
		{name: "CapacityBasis", typ: reflect.TypeFor[heteroci.SnapshotCapacityBasis](), tag: `json:"capacity_basis"`},
		{name: "NodeID", typ: reflect.TypeFor[string](), tag: `json:"node_id"`},
		{name: "Endpoint", typ: reflect.TypeFor[string](), tag: `json:"endpoint"`},
		{name: "Platform", typ: reflect.TypeFor[heteroci.Platform](), tag: `json:"platform"`},
		{name: "ProbeIdentity", typ: reflect.TypeFor[string](), tag: `json:"probe_identity"`},
		{name: "ProbeBootID", typ: reflect.TypeFor[string](), tag: `json:"probe_boot_id"`},
		{name: "ActiveCapabilities", typ: reflect.TypeFor[[]heteroci.ActiveCapabilitySnapshot](), tag: `json:"active_capabilities"`},
		{name: "Status", typ: reflect.TypeFor[heteroci.NodeStatus](), tag: `json:"status"`},
		{name: "HeartbeatUnixMilli", typ: reflect.TypeFor[int64](), tag: `json:"heartbeat_unix_milli"`},
		{name: "Total", typ: reflect.TypeFor[heteroci.ResourceCapacity](), tag: `json:"total"`},
		{name: "Available", typ: reflect.TypeFor[heteroci.ResourceCapacity](), tag: `json:"available"`},
	})
}

type heterociProbeFieldSpec struct {
	name string
	typ  reflect.Type
	tag  string
}

func assertHeterociProbeStructLayout(t *testing.T, got reflect.Type, want []heterociProbeFieldSpec) {
	t.Helper()
	if got.Kind() != reflect.Struct || got.NumField() != len(want) {
		t.Fatalf("%s layout = %s/%d fields, want struct/%d", got.Name(), got.Kind(), got.NumField(), len(want))
	}
	for index, expected := range want {
		field := got.Field(index)
		if field.Name != expected.name || field.Type != expected.typ || string(field.Tag) != expected.tag {
			t.Fatalf("%s field %d = %s %s %q, want %s %s %q", got.Name(), index, field.Name, field.Type, field.Tag, expected.name, expected.typ, expected.tag)
		}
	}
}

// TestHeterociProbeTransportRequestSignature freezes the one request-bytes
// transport method; callers cannot replace it with a typed endpoint protocol.
func TestHeterociProbeTransportRequestSignature(t *testing.T) {
	typ := reflect.TypeFor[heteroci.ProbeTransport]()
	if typ.NumMethod() != 1 {
		t.Fatalf("ProbeTransport method count = %d, want 1", typ.NumMethod())
	}
	method, ok := typ.MethodByName("Probe")
	if !ok {
		t.Fatal("ProbeTransport.Probe method is missing")
	}
	want := reflect.TypeOf(func(context.Context, string, []byte) (heteroci.ProbeResponseBody, error) { return nil, nil })
	if method.Type != want {
		t.Fatalf("ProbeTransport.Probe type = %s, want %s", method.Type, want)
	}
}

// TestHeterociProbeAcquisitionBehaviorMatrix proves fail-fast and the two
// intended non-fatal states without assuming a fixed node count or endpoint map.
func TestHeterociProbeAcquisitionBehaviorMatrix(t *testing.T) {
	now := time.UnixMilli(1_725_000_000_000)
	ttl := 2 * time.Minute
	t.Run("unavailable becomes offline", func(t *testing.T) {
		assertHeterociProbeUnavailable(t, now, ttl)
	})
	t.Run("fatal error returns zero snapshot", func(t *testing.T) {
		assertHeterociProbeFatal(t, now, ttl)
	})
	t.Run("shuffled dynamic N is deterministic", func(t *testing.T) {
		assertHeterociProbeDeterministic(t, now, ttl)
	})
	t.Run("gross available capacity is unchanged", func(t *testing.T) {
		assertHeterociProbeGrossAvailable(t, now, ttl)
	})
}
func assertHeterociProbeUnavailable(t *testing.T, now time.Time, ttl time.Duration) {
	t.Helper()
	manifest := newHeterociProbeManifest(t, []string{"node-b", "node-a"})
	transport := newHeterociProbeTransport(manifest, 7, now)
	transport.unavailable[manifest.Nodes[1].Endpoint] = true
	got, err := heteroci.AcquireSnapshot(context.Background(), manifest, 7, transport, now, ttl)
	if err != nil {
		t.Fatalf("AcquireSnapshot unavailable = %v", err)
	}
	node := heterociProbeNodeByID(t, got, "node-a")
	if node.Status != heteroci.NodeStatusOffline || node.ProbeBootID != "" || len(node.ActiveCapabilities) != 0 || node.Available != (heteroci.ResourceCapacity{}) {
		t.Fatalf("unavailable node snapshot = %#v, want explicit offline zero-capacity state", node)
	}
}
func assertHeterociProbeFatal(t *testing.T, now time.Time, ttl time.Duration) {
	t.Helper()
	manifest := newHeterociProbeManifest(t, []string{"node-fatal"})
	transport := newHeterociProbeTransport(manifest, 8, now)
	transport.failures[manifest.Nodes[0].Endpoint] = errors.New("fatal probe failure")
	got, err := heteroci.AcquireSnapshot(context.Background(), manifest, 8, transport, now, ttl)
	if err == nil || !reflect.DeepEqual(got, heteroci.SnapshotSet{}) {
		t.Fatalf("fatal AcquireSnapshot = %#v, %v; want zero snapshot and error", got, err)
	}
}
func assertHeterociProbeDeterministic(t *testing.T, now time.Time, ttl time.Duration) {
	t.Helper()
	for _, ids := range [][]string{{"node-c", "node-a", "node-b"}, {"node-e", "node-b", "node-d", "node-a"}} {
		manifest := newHeterociProbeManifest(t, ids)
		firstTransport := newHeterociProbeTransport(manifest, 9, now)
		first, err := heteroci.AcquireSnapshot(context.Background(), manifest, 9, firstTransport, now, ttl)
		if err != nil {
			t.Fatalf("first AcquireSnapshot(%d nodes) = %v", len(ids), err)
		}
		secondTransport := newHeterociProbeTransport(manifest, 9, now)
		second, err := heteroci.AcquireSnapshot(context.Background(), manifest, 9, secondTransport, now, ttl)
		if err != nil || !reflect.DeepEqual(first, second) {
			t.Fatalf("repeat AcquireSnapshot(%d nodes) = %#v, %v; want deterministic %#v", len(ids), second, err, first)
		}
		assertHeterociProbeSortedCalls(t, firstTransport, manifest)
		assertHeterociProbeSortedCalls(t, secondTransport, manifest)
	}
}
func assertHeterociProbeGrossAvailable(t *testing.T, now time.Time, ttl time.Duration) {
	t.Helper()
	manifest := newHeterociProbeManifest(t, []string{"node-capacity"})
	available := heteroci.ResourceCapacity{CPUMilli: 1_234, MemoryBytes: 987_654, MaxConcurrency: 3}
	transport := newHeterociProbeTransportWithAvailable(manifest, 10, now, available)
	got, err := heteroci.AcquireSnapshot(context.Background(), manifest, 10, transport, now, ttl)
	if err != nil {
		t.Fatalf("AcquireSnapshot capacity = %v", err)
	}
	if node := heterociProbeNodeByID(t, got, "node-capacity"); node.Available != available {
		t.Fatalf("AcquireSnapshot available = %#v, want gross %#v", node.Available, available)
	}
}

type heterociProbeCall struct {
	endpoint string
	request  []byte
}
type heterociProbeTransportStub struct {
	responses   map[string][]byte
	failures    map[string]error
	unavailable map[string]bool
	calls       []heterociProbeCall
}

func (stub *heterociProbeTransportStub) Probe(_ context.Context, endpoint string, request []byte) (heteroci.ProbeResponseBody, error) {
	stub.calls = append(stub.calls, heterociProbeCall{endpoint: endpoint, request: append([]byte(nil), request...)})
	if err := stub.failures[endpoint]; err != nil {
		return nil, err
	}
	if stub.unavailable[endpoint] {
		return nil, heteroci.ErrProbeTransportUnavailable
	}
	payload, ok := stub.responses[endpoint]
	if !ok {
		return nil, fmt.Errorf("missing probe response for %s", endpoint)
	}
	return heterociProbeResponseBody{reader: bytes.NewReader(payload)}, nil
}

type heterociProbeResponseBody struct{ reader *bytes.Reader }

func (body heterociProbeResponseBody) Read(ctx context.Context, dst []byte) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
		return body.reader.Read(dst)
	}
}
func (heterociProbeResponseBody) Close() error { return nil }
func newHeterociProbeTransport(manifest heteroci.Manifest, owner heteroci.RoutingOwnerEpoch, now time.Time) *heterociProbeTransportStub {
	return newHeterociProbeTransportWithAvailable(manifest, owner, now, heteroci.ResourceCapacity{CPUMilli: 4_000, MemoryBytes: 4 * 1024 * 1024 * 1024, MaxConcurrency: 4})
}
func newHeterociProbeTransportWithAvailable(manifest heteroci.Manifest, owner heteroci.RoutingOwnerEpoch, now time.Time, available heteroci.ResourceCapacity) *heterociProbeTransportStub {
	stub := &heterociProbeTransportStub{responses: make(map[string][]byte), failures: make(map[string]error), unavailable: make(map[string]bool)}
	for _, config := range manifest.Nodes {
		response := heteroci.ProbeResponse{
			SchemaVersion: heteroci.ProbeResponseSchemaVersion, OwnerEpoch: owner,
			CapacityBasis: heteroci.SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations,
			NodeID:        config.NodeID, Endpoint: config.Endpoint, Platform: config.Platform,
			ProbeIdentity: config.ProbeIdentity, ProbeBootID: "boot-" + config.NodeID,
			ActiveCapabilities: []heteroci.ActiveCapabilitySnapshot{{Runtime: heteroci.RuntimeUbuntuContainer, ImageDigest: config.Capabilities[0].ImageDigest}},
			Status:             heteroci.NodeStatusLive, HeartbeatUnixMilli: now.UnixMilli(),
			Total:     heteroci.ResourceCapacity{CPUMilli: maxInt64(available.CPUMilli, 4_000), MemoryBytes: maxInt64(available.MemoryBytes, 4*1024*1024*1024), MaxConcurrency: maxInt(available.MaxConcurrency, 4)},
			Available: available,
		}
		payload, err := json.Marshal(response)
		if err != nil {
			// archguard:ignore panic_count -- 测试夹具序列化失败必须立即暴露
			panic(fmt.Sprintf("marshal probe response fixture: %v", err))
		}
		stub.responses[config.Endpoint] = payload
	}
	return stub
}
func newHeterociProbeManifest(t *testing.T, ids []string) heteroci.Manifest {
	t.Helper()
	manifest := heteroci.Manifest{SchemaVersion: heteroci.ManifestSchemaVersion, Authority: heteroci.AuthorityLocalNonAuthoritative, Nodes: make([]heteroci.NodeConfig, len(ids))}
	for index, id := range ids {
		manifest.Nodes[index] = heteroci.NodeConfig{NodeID: id, Endpoint: "https://" + id + ".invalid/probe", ProbeIdentity: "sha256:" + strings.Repeat(fmt.Sprintf("%x", index+1), 64)[:64], Platform: heteroci.PlatformLinuxAMD64, Capabilities: []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: "sha256:" + strings.Repeat("a", 64)}}}
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("probe manifest.Validate() = %v", err)
	}
	return manifest
}
func assertHeterociProbeSortedCalls(t *testing.T, transport *heterociProbeTransportStub, manifest heteroci.Manifest) {
	t.Helper()
	configs := append([]heteroci.NodeConfig(nil), manifest.Nodes...)
	sort.Slice(configs, func(left, right int) bool { return configs[left].NodeID < configs[right].NodeID })
	if len(transport.calls) != len(configs) {
		t.Fatalf("probe call count = %d, want %d", len(transport.calls), len(configs))
	}
	for index, config := range configs {
		if transport.calls[index].endpoint != config.Endpoint {
			t.Fatalf("probe call %d endpoint = %q, want %q", index, transport.calls[index].endpoint, config.Endpoint)
		}
		want, err := json.Marshal(heteroci.ProbeRequest{SchemaVersion: heteroci.ProbeRequestSchemaVersion, NodeID: config.NodeID, ProbeIdentity: config.ProbeIdentity, OwnerEpoch: 9})
		if err != nil || !bytes.Equal(transport.calls[index].request, want) {
			t.Fatalf("probe call %d request = %s, want %s", index, transport.calls[index].request, want)
		}
	}
}
func heterociProbeNodeByID(t *testing.T, set heteroci.SnapshotSet, nodeID string) heteroci.NodeSnapshot {
	t.Helper()
	for _, node := range set.Nodes {
		if node.NodeID == nodeID {
			return node
		}
	}
	t.Fatalf("snapshot node %q is missing", nodeID)
	return heteroci.NodeSnapshot{}
}
func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
