package heteroci

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	snapshotTestNowUnixMilli       int64 = 1_700_000_000_000
	snapshotTestMemoryBytes        int64 = 8 * 1024 * 1024 * 1024
	snapshotTestHeartbeatTTL             = 30 * time.Second
	snapshotTestImageDigest              = "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	snapshotTestWrongImageDigest         = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	snapshotTestWrongProbeIdentity       = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
)

func TestSnapshotSetValidateAgainstDynamicNodeCounts(t *testing.T) {
	for _, nodeCount := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("nodes_%d", nodeCount), func(t *testing.T) {
			now, manifest, snapshot := snapshotTestFixture(t, nodeCount)
			if err := snapshot.ValidateAgainst(manifest, now, snapshotTestHeartbeatTTL); err != nil {
				t.Fatalf("ValidateAgainst(%d nodes): %v", nodeCount, err)
			}
		})
	}
}
func TestSnapshotSetValidateAgainstRejectsSchemaAndAuthorityDrift(t *testing.T) {
	now, manifest, valid := snapshotTestFixture(t, 1)
	tests := []struct {
		name   string
		mutate func(*SnapshotSet)
	}{
		{name: "schema empty", mutate: func(snapshot *SnapshotSet) { snapshot.SchemaVersion = "" }},
		{name: "schema old v1", mutate: func(snapshot *SnapshotSet) { snapshot.SchemaVersion = "heteroci-snapshot/v1" }},
		{name: "schema old v2", mutate: func(snapshot *SnapshotSet) { snapshot.SchemaVersion = "heteroci-snapshot/v2" }},
		{name: "schema old", mutate: func(snapshot *SnapshotSet) { snapshot.SchemaVersion = "heteroci-snapshot/v0" }},
		{name: "schema unknown", mutate: func(snapshot *SnapshotSet) { snapshot.SchemaVersion = "heteroci-snapshot/v99" }},
		{name: "authority empty", mutate: func(snapshot *SnapshotSet) { snapshot.Authority = "" }},
		{name: "authority remote", mutate: func(snapshot *SnapshotSet) { snapshot.Authority = "REMOTE" }},
		{name: "capacity basis empty", mutate: func(snapshot *SnapshotSet) { snapshot.CapacityBasis = "" }},
		{name: "capacity basis old", mutate: func(snapshot *SnapshotSet) {
			snapshot.CapacityBasis = SnapshotCapacityBasis("external-available-excludes-routing-book-reservations/v0")
		}},
		{name: "capacity basis unknown", mutate: func(snapshot *SnapshotSet) {
			snapshot.CapacityBasis = SnapshotCapacityBasis("unknown-capacity-basis/v99")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneSnapshotSet(valid)
			tt.mutate(&candidate)
			assertSnapshotRejects(t, manifest, candidate, now, snapshotTestHeartbeatTTL, tt.name)
		})
	}
}
func TestSnapshotSetSchemaAndCapacityBasisAreFrozen(t *testing.T) {
	if SnapshotSchemaVersion != "heteroci-snapshot/v5" {
		t.Fatalf("SnapshotSchemaVersion = %q, want heteroci-snapshot/v5", SnapshotSchemaVersion)
	}
	if got := SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations; got != SnapshotCapacityBasis("external-available-excludes-routing-book-reservations/v1") {
		t.Fatalf("SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations = %q, want external-available-excludes-routing-book-reservations/v1", got)
	}

	typ := reflect.TypeFor[SnapshotSet]()
	want := []struct {
		name string
		typ  reflect.Type
		json string
	}{
		{name: "SchemaVersion", typ: reflect.TypeFor[string](), json: "schema_version"},
		{name: "Authority", typ: reflect.TypeFor[string](), json: "authority"},
		{name: "OwnerEpoch", typ: reflect.TypeFor[RoutingOwnerEpoch](), json: "owner_epoch"},
		{name: "CapacityBasis", typ: reflect.TypeFor[SnapshotCapacityBasis](), json: "capacity_basis"},
		{name: "CapturedAtUnixMilli", typ: reflect.TypeFor[int64](), json: "captured_at_unix_milli"},
		{name: "Nodes", typ: reflect.TypeFor[[]NodeSnapshot](), json: "nodes"},
	}
	if typ.NumField() != len(want) {
		t.Fatalf("SnapshotSet field count = %d, want %d", typ.NumField(), len(want))
	}
	for index, expected := range want {
		field := typ.Field(index)
		if field.Name != expected.name || field.Type != expected.typ {
			t.Fatalf("SnapshotSet field %d = %s %s, want %s %s", index, field.Name, field.Type, expected.name, expected.typ)
		}
		if got := field.Tag.Get("json"); got != expected.json || strings.Contains(got, ",omitempty") {
			t.Fatalf("SnapshotSet field %s json tag = %q, want %q without omitempty", field.Name, got, expected.json)
		}
	}
}
func TestSnapshotSetValidateAgainstOwnerEpochMatrix(t *testing.T) {
	now, manifest, valid := snapshotTestFixture(t, 1)
	for _, tt := range []struct {
		name      string
		epoch     RoutingOwnerEpoch
		wantError bool
	}{
		{name: "zero", epoch: 0, wantError: true},
		{name: "negative", epoch: -1, wantError: true},
		{name: "maximum", epoch: RoutingOwnerEpoch(1<<63 - 1), wantError: true},
		{name: "one", epoch: 1},
		{name: "maximum minus one", epoch: RoutingOwnerEpoch(1<<63 - 2)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneSnapshotSet(valid)
			candidate.OwnerEpoch = tt.epoch
			if tt.wantError {
				assertSnapshotRejects(t, manifest, candidate, now, snapshotTestHeartbeatTTL, tt.name)
				return
			}
			if err := candidate.ValidateAgainst(manifest, now, snapshotTestHeartbeatTTL); err != nil {
				t.Fatalf("owner epoch %d should be valid: %v", tt.epoch, err)
			}
		})
	}
}
func TestSnapshotSetMarshalUsesCanonicalSnakeCaseAndOmitsUbuntuQualification(t *testing.T) {
	_, _, snapshot := snapshotTestFixture(t, 1)
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal valid snapshot: %v", err)
	}
	snapshotTestAssertRootMarshal(t, data, snapshot)
	root := snapshotTestDecodeObject(t, data, "snapshot root")
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(root["nodes"], &nodes); err != nil || len(nodes) != 1 {
		t.Fatalf("marshaled snapshot nodes = %d, err=%v, want 1: %s", len(nodes), err, data)
	}
	snapshotTestAssertNodeMarshal(t, nodes[0], data)
	if bytes.Contains(data, []byte(`"qualification":null`)) {
		t.Fatalf("marshaled Ubuntu capability contains qualification:null: %s", data)
	}
}

func snapshotTestAssertRootMarshal(t *testing.T, data []byte, snapshot SnapshotSet) {
	t.Helper()
	wantKeys := []string{"schema_version", "authority", "owner_epoch", "capacity_basis", "captured_at_unix_milli", "nodes"}
	if got := snapshotTestJSONKeys(t, data); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("marshaled snapshot root keys = %v, want exact order %v", got, wantKeys)
	}
	root := snapshotTestDecodeObject(t, data, "snapshot root")
	var ownerEpoch RoutingOwnerEpoch
	if err := json.Unmarshal(root["owner_epoch"], &ownerEpoch); err != nil || ownerEpoch != snapshot.OwnerEpoch {
		t.Fatalf("marshaled owner_epoch = %d, err=%v, want %d", ownerEpoch, err, snapshot.OwnerEpoch)
	}
	var capacityBasis SnapshotCapacityBasis
	if err := json.Unmarshal(root["capacity_basis"], &capacityBasis); err != nil || capacityBasis != snapshot.CapacityBasis {
		t.Fatalf("marshaled capacity_basis = %q, err=%v, want %q", capacityBasis, err, snapshot.CapacityBasis)
	}
	var roundTripped SnapshotSet
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unmarshal marshaled snapshot: %v", err)
	}
	if !reflect.DeepEqual(roundTripped, snapshot) {
		t.Fatalf("snapshot JSON roundtrip differs:\n got %#v\nwant %#v", roundTripped, snapshot)
	}
}

func snapshotTestDecodeObject(t *testing.T, raw []byte, context string) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		t.Fatalf("decode %s: err=%v, null=%t", context, err, object == nil)
	}
	return object
}

func snapshotTestRequireKeys(t *testing.T, object map[string]json.RawMessage, data []byte, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			t.Fatalf("marshaled object missing key %q: %s", key, data)
		}
	}
}

func snapshotTestAssertNodeMarshal(t *testing.T, node map[string]json.RawMessage, data []byte) {
	t.Helper()
	snapshotTestRequireKeys(t, node, data, "node_id", "endpoint", "platform", "probe_identity", "probe_boot_id", "active_capabilities", "status", "heartbeat_unix_milli", "total", "available")
	for _, capacityKey := range []string{"total", "available"} {
		capacity := snapshotTestDecodeObject(t, node[capacityKey], "marshaled "+capacityKey+" capacity")
		snapshotTestRequireKeys(t, capacity, data, "cpu_milli", "memory_bytes", "max_concurrency")
	}
	var capabilities []map[string]json.RawMessage
	if err := json.Unmarshal(node["active_capabilities"], &capabilities); err != nil || len(capabilities) != 1 {
		t.Fatalf("marshaled active capabilities = %d, err=%v, want 1", len(capabilities), err)
	}
	snapshotTestRequireKeys(t, capabilities[0], data, "runtime", "image_digest")
	if _, ok := capabilities[0]["qualification"]; ok {
		t.Fatalf("marshaled Ubuntu capability unexpectedly contains qualification: %s", data)
	}
}

func TestSnapshotSetValidateAgainstRequiresExactlyOneSnapshotPerConfiguredNode(t *testing.T) {
	now, manifest, valid := snapshotTestFixture(t, 2)

	tests := []struct {
		name   string
		mutate func(*SnapshotSet)
	}{
		{
			name: "missing configured node",
			mutate: func(snapshot *SnapshotSet) {
				snapshot.Nodes = snapshot.Nodes[:1]
			},
		},
		{
			name: "extra unconfigured node",
			mutate: func(snapshot *SnapshotSet) {
				extra := snapshot.Nodes[0]
				extra.NodeID = "heteroci-extra"
				snapshot.Nodes = append(snapshot.Nodes, extra)
			},
		},
		{
			name: "duplicate node snapshot",
			mutate: func(snapshot *SnapshotSet) {
				snapshot.Nodes[1] = snapshot.Nodes[0]
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneSnapshotSet(valid)
			tt.mutate(&candidate)
			assertSnapshotRejects(t, manifest, candidate, now, snapshotTestHeartbeatTTL, tt.name)
		})
	}
}

func TestSnapshotSetValidateAgainstRejectsNodeIdentityMismatch(t *testing.T) {
	now, manifest, valid := snapshotTestFixture(t, 1)

	for _, tt := range []struct {
		name   string
		mutate func(*NodeSnapshot)
	}{
		{name: "platform mismatch", mutate: func(node *NodeSnapshot) { node.Platform = PlatformWindowsAMD64 }},
		{name: "endpoint mismatch", mutate: func(node *NodeSnapshot) { node.Endpoint = "https://other.invalid/runner" }},
		{name: "probe identity mismatch", mutate: func(node *NodeSnapshot) { node.ProbeIdentity = snapshotTestWrongProbeIdentity }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneSnapshotSet(valid)
			tt.mutate(&candidate.Nodes[0])
			assertSnapshotRejects(t, manifest, candidate, now, snapshotTestHeartbeatTTL, tt.name)
		})
	}
}

func TestSnapshotSetValidateAgainstRejectsInvalidProbeBootIDs(t *testing.T) {
	now, manifest, valid := snapshotTestFixture(t, 1)

	for _, tt := range []struct {
		name   string
		mutate func(*NodeSnapshot)
	}{
		{name: "live boot id empty", mutate: func(node *NodeSnapshot) { node.ProbeBootID = "" }},
		{name: "live boot id whitespace", mutate: func(node *NodeSnapshot) { node.ProbeBootID = " boot-id " }},
		{name: "live boot id control", mutate: func(node *NodeSnapshot) { node.ProbeBootID = "boot\nid" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneSnapshotSet(valid)
			tt.mutate(&candidate.Nodes[0])
			assertSnapshotRejects(t, manifest, candidate, now, snapshotTestHeartbeatTTL, tt.name)
		})
	}

	offline := cloneSnapshotSet(valid)
	offline.Nodes[0].Status = NodeStatusOffline
	offline.Nodes[0].ProbeBootID = ""
	offline.Nodes[0].ActiveCapabilities = []ActiveCapabilitySnapshot{}
	offline.Nodes[0].Total, offline.Nodes[0].Available = ResourceCapacity{}, ResourceCapacity{}
	if err := offline.ValidateAgainst(manifest, now, snapshotTestHeartbeatTTL); err != nil {
		t.Fatalf("offline snapshot with empty boot id should be valid: %v", err)
	}
	offline.Nodes[0].ProbeBootID = "boot-offline"
	assertSnapshotRejects(t, manifest, offline, now, snapshotTestHeartbeatTTL, "offline boot id nonempty")
}

func TestSnapshotSetValidateAgainstRejectsUbuntuDigestMismatchAndReceipt(t *testing.T) {
	now, manifest, valid := snapshotTestFixture(t, 1)

	tests := []struct {
		name   string
		mutate func(*ActiveCapabilitySnapshot)
	}{
		{name: "ubuntu image digest mismatch", mutate: func(active *ActiveCapabilitySnapshot) { active.ImageDigest = snapshotTestWrongImageDigest }},
		{name: "ubuntu qualification receipt is non-nil", mutate: func(active *ActiveCapabilitySnapshot) { active.Qualification = &CapabilityQualificationReceipt{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneSnapshotSet(valid)
			tt.mutate(&candidate.Nodes[0].ActiveCapabilities[0])
			assertSnapshotRejects(t, manifest, candidate, now, snapshotTestHeartbeatTTL, tt.name)
		})
	}
}

func TestSnapshotSetValidateAgainstRequiresExactActiveCapabilitiesWithoutDuplicates(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestCapabilityManifest(
		CapabilityConfig{Runtime: RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: snapshotTestImageDigest},
		CapabilityConfig{Runtime: RuntimeUbuntuWineContainer, Image: "ubuntu-wine:24.04", ImageDigest: snapshotTestImageDigest, SourceRevision: "https://github.com/wine-mirror/wine@refs/tags/wine-9.0", QualificationSuite: "wine-windows-amd64/v1"},
	)
	mustSnapshotManifestValidate(t, manifest)
	valid := snapshotForManifest(manifest, now)
	if err := valid.ValidateAgainst(manifest, now, snapshotTestHeartbeatTTL); err != nil {
		t.Fatalf("exact configured capability declaration should be valid: %v", err)
	}
	if len(valid.Nodes[0].ActiveCapabilities) != 2 {
		t.Fatalf("exact capability fixture length = %d, want 2", len(valid.Nodes[0].ActiveCapabilities))
	}

	permuted := cloneSnapshotSet(valid)
	permuted.Nodes[0].ActiveCapabilities[0], permuted.Nodes[0].ActiveCapabilities[1] = permuted.Nodes[0].ActiveCapabilities[1], permuted.Nodes[0].ActiveCapabilities[0]
	if err := permuted.ValidateAgainst(manifest, now, snapshotTestHeartbeatTTL); err != nil {
		t.Fatalf("permuted exact capability declaration should be valid: %v", err)
	}

	missingOne := cloneSnapshotSet(valid)
	missingOne.Nodes[0].ActiveCapabilities = missingOne.Nodes[0].ActiveCapabilities[:1]
	assertSnapshotRejects(t, manifest, missingOne, now, snapshotTestHeartbeatTTL, "missing configured capability")

	subset := cloneSnapshotSet(valid)
	subset.Nodes[0].ActiveCapabilities = []ActiveCapabilitySnapshot{subset.Nodes[0].ActiveCapabilities[1]}
	assertSnapshotRejects(t, manifest, subset, now, snapshotTestHeartbeatTTL, "configured capability subset")

	empty := cloneSnapshotSet(valid)
	empty.Nodes[0].ActiveCapabilities = []ActiveCapabilitySnapshot{}
	assertSnapshotRejects(t, manifest, empty, now, snapshotTestHeartbeatTTL, "empty active capability declaration")

	unknown := cloneSnapshotSet(valid)
	unknown.Nodes[0].ActiveCapabilities[1] = ActiveCapabilitySnapshot{Runtime: RuntimeKind("not-configured")}
	assertSnapshotRejects(t, manifest, unknown, now, snapshotTestHeartbeatTTL, "active capability outside configured subset")

	duplicate := cloneSnapshotSet(valid)
	duplicate.Nodes[0].ActiveCapabilities[1] = ActiveCapabilitySnapshot{
		Runtime:     RuntimeUbuntuContainer,
		ImageDigest: manifest.Nodes[0].Capabilities[0].ImageDigest,
	}
	assertSnapshotRejects(t, manifest, duplicate, now, snapshotTestHeartbeatTTL, "duplicate active capability")

	nilCapabilities := cloneSnapshotSet(valid)
	nilCapabilities.Nodes[0].ActiveCapabilities = nil
	assertSnapshotRejects(t, manifest, nilCapabilities, now, snapshotTestHeartbeatTTL, "live node without active capability")
}

func TestSnapshotSetValidateAgainstTemporalBounds(t *testing.T) {
	now, manifest, valid := snapshotTestFixture(t, 1)
	for _, tt := range []struct {
		name      string
		mutate    func(*SnapshotSet)
		ttl       time.Duration
		wantError bool
	}{
		{name: "heartbeat at TTL", mutate: func(s *SnapshotSet) { s.Nodes[0].HeartbeatUnixMilli = now.Add(-snapshotTestHeartbeatTTL).UnixMilli() }},
		{name: "future heartbeat", mutate: func(s *SnapshotSet) { s.Nodes[0].HeartbeatUnixMilli = now.Add(time.Millisecond).UnixMilli() }, wantError: true},
		{name: "stale heartbeat", mutate: func(s *SnapshotSet) {
			s.Nodes[0].HeartbeatUnixMilli = now.Add(-(snapshotTestHeartbeatTTL + time.Millisecond)).UnixMilli()
		}, wantError: true},
		{name: "capture at TTL", mutate: func(s *SnapshotSet) {
			s.CapturedAtUnixMilli = now.Add(-snapshotTestHeartbeatTTL).UnixMilli()
			s.Nodes[0].HeartbeatUnixMilli = s.CapturedAtUnixMilli
		}},
		{name: "zero capture", mutate: func(s *SnapshotSet) { s.CapturedAtUnixMilli = 0 }, wantError: true},
		{name: "future capture", mutate: func(s *SnapshotSet) { s.CapturedAtUnixMilli = now.Add(time.Millisecond).UnixMilli() }, wantError: true},
		{name: "stale capture", mutate: func(s *SnapshotSet) {
			s.CapturedAtUnixMilli = now.Add(-(snapshotTestHeartbeatTTL + time.Millisecond)).UnixMilli()
			s.Nodes[0].HeartbeatUnixMilli = s.CapturedAtUnixMilli
		}, wantError: true},
		{name: "zero heartbeat", mutate: func(s *SnapshotSet) { s.Nodes[0].HeartbeatUnixMilli = 0 }, wantError: true},
		{name: "heartbeat after capture", mutate: func(s *SnapshotSet) {
			s.CapturedAtUnixMilli = now.Add(-time.Millisecond).UnixMilli()
			s.Nodes[0].HeartbeatUnixMilli = now.UnixMilli()
		}, wantError: true},
		{name: "zero TTL", ttl: 0, wantError: true},
		{name: "negative TTL", ttl: -time.Millisecond, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneSnapshotSet(valid)
			if tt.mutate != nil {
				tt.mutate(&candidate)
			}
			ttl := tt.ttl
			if tt.name != "zero TTL" && tt.name != "negative TTL" {
				ttl = snapshotTestHeartbeatTTL
			}
			if tt.wantError {
				assertSnapshotRejects(t, manifest, candidate, now, ttl, tt.name)
				return
			}
			if err := candidate.ValidateAgainst(manifest, now, ttl); err != nil {
				t.Fatalf("%s: unexpected validation error: %v", tt.name, err)
			}
		})
	}
}

func TestSnapshotSetValidateAgainstOfflineHeartbeatSemantics(t *testing.T) {
	now, manifest, valid := snapshotTestFixture(t, 1)
	base := snapshotTestOffline(valid)
	for _, tt := range []struct {
		name      string
		heartbeat int64
		wantError bool
	}{
		{name: "zero heartbeat", heartbeat: 0},
		{name: "historical heartbeat", heartbeat: now.Add(-(snapshotTestHeartbeatTTL + time.Second)).UnixMilli()},
		{name: "future heartbeat", heartbeat: now.Add(time.Millisecond).UnixMilli(), wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneSnapshotSet(base)
			candidate.Nodes[0].HeartbeatUnixMilli = tt.heartbeat
			if tt.wantError {
				assertSnapshotRejects(t, manifest, candidate, now, snapshotTestHeartbeatTTL, tt.name)
				return
			}
			if err := candidate.ValidateAgainst(manifest, now, snapshotTestHeartbeatTTL); err != nil {
				t.Fatalf("offline heartbeat %d rejected: %v", tt.heartbeat, err)
			}
		})
	}
}

func TestSnapshotSetValidateAgainstRejectsInvalidLiveCapacity(t *testing.T) {
	now, manifest, base := snapshotTestFixture(t, 1)

	tests := []struct {
		name   string
		mutate func(*NodeSnapshot)
	}{
		{name: "zero total cpu", mutate: func(node *NodeSnapshot) { node.Total.CPUMilli, node.Available.CPUMilli = 0, 0 }},
		{name: "zero total memory", mutate: func(node *NodeSnapshot) { node.Total.MemoryBytes, node.Available.MemoryBytes = 0, 0 }},
		{name: "zero total concurrency", mutate: func(node *NodeSnapshot) { node.Total.MaxConcurrency, node.Available.MaxConcurrency = 0, 0 }},
		{name: "negative total cpu", mutate: func(node *NodeSnapshot) { node.Total.CPUMilli = -1 }},
		{name: "negative total memory", mutate: func(node *NodeSnapshot) { node.Total.MemoryBytes = -1 }},
		{name: "negative total concurrency", mutate: func(node *NodeSnapshot) { node.Total.MaxConcurrency = -1 }},
		{name: "negative available cpu", mutate: func(node *NodeSnapshot) { node.Available.CPUMilli = -1 }},
		{name: "negative available memory", mutate: func(node *NodeSnapshot) { node.Available.MemoryBytes = -1 }},
		{name: "negative available concurrency", mutate: func(node *NodeSnapshot) { node.Available.MaxConcurrency = -1 }},
		{name: "available cpu exceeds total", mutate: func(node *NodeSnapshot) { node.Available.CPUMilli = node.Total.CPUMilli + 1 }},
		{name: "available memory exceeds total", mutate: func(node *NodeSnapshot) { node.Available.MemoryBytes = node.Total.MemoryBytes + 1 }},
		{name: "available concurrency exceeds total", mutate: func(node *NodeSnapshot) { node.Available.MaxConcurrency = node.Total.MaxConcurrency + 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneSnapshotSet(base)
			tt.mutate(&candidate.Nodes[0])
			assertSnapshotRejects(t, manifest, candidate, now, snapshotTestHeartbeatTTL, tt.name)
		})
	}
}

func TestSnapshotSetValidateAgainstRequiresOfflineZeroCapacityAndNoActiveCapabilities(t *testing.T) {
	now, manifest, valid := snapshotTestFixture(t, 1)
	saturated := cloneSnapshotSet(valid)
	saturated.Nodes[0].Available = ResourceCapacity{}
	if err := saturated.ValidateAgainst(manifest, now, snapshotTestHeartbeatTTL); err != nil {
		t.Fatalf("live node with zero available capacity should be valid when saturated: %v", err)
	}
	valid = snapshotTestOffline(valid)
	if err := valid.ValidateAgainst(manifest, now, snapshotTestHeartbeatTTL); err != nil {
		t.Fatalf("offline node with zero available capacity and no active capabilities should be valid: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*NodeSnapshot)
	}{
		{name: "offline nil capabilities", mutate: func(node *NodeSnapshot) { node.ActiveCapabilities = nil }},
		{name: "offline total cpu", mutate: func(node *NodeSnapshot) { node.Total.CPUMilli = 1 }},
		{name: "offline total memory", mutate: func(node *NodeSnapshot) { node.Total.MemoryBytes = 1 }},
		{name: "offline total concurrency", mutate: func(node *NodeSnapshot) { node.Total.MaxConcurrency = 1 }},
		{name: "offline available cpu", mutate: func(node *NodeSnapshot) { node.Available.CPUMilli = 1 }},
		{name: "offline available memory", mutate: func(node *NodeSnapshot) { node.Available.MemoryBytes = 1 }},
		{name: "offline available concurrency", mutate: func(node *NodeSnapshot) { node.Available.MaxConcurrency = 1 }},
		{name: "offline active capability", mutate: func(node *NodeSnapshot) {
			node.ActiveCapabilities = []ActiveCapabilitySnapshot{{Runtime: RuntimeUbuntuContainer, ImageDigest: snapshotTestImageDigest}}
		}},
		{name: "unknown status", mutate: func(node *NodeSnapshot) { node.Status = NodeStatus("unknown") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneSnapshotSet(valid)
			tt.mutate(&candidate.Nodes[0])
			assertSnapshotRejects(t, manifest, candidate, now, snapshotTestHeartbeatTTL, tt.name)
		})
	}
}

func snapshotTestFixture(t *testing.T, nodeCount int) (time.Time, Manifest, SnapshotSet) {
	t.Helper()
	now := snapshotTestNow()
	manifest := snapshotTestManifest(nodeCount)
	mustSnapshotManifestValidate(t, manifest)
	return now, manifest, snapshotForManifest(manifest, now)
}

func snapshotTestNow() time.Time {
	return time.UnixMilli(snapshotTestNowUnixMilli)
}

func snapshotTestProbeIdentity(index int) string {
	return fmt.Sprintf("sha256:%064x", index+1)
}

func snapshotTestManifest(nodeCount int) Manifest {
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Authority: AuthorityLocalNonAuthoritative, Nodes: make([]NodeConfig, nodeCount)}
	for index := range manifest.Nodes {
		nodeID := fmt.Sprintf("heteroci-node-%d", index+1)
		manifest.Nodes[index] = NodeConfig{NodeID: nodeID, Endpoint: "https://" + nodeID + ".invalid/runner", ProbeIdentity: snapshotTestProbeIdentity(index), Platform: PlatformLinuxAMD64, Capabilities: []CapabilityConfig{{Runtime: RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: snapshotTestImageDigest}}}
	}
	return manifest
}

func snapshotTestCapabilityManifest(capabilities ...CapabilityConfig) Manifest {
	manifest := snapshotTestManifest(1)
	manifest.Nodes[0].Capabilities = capabilities
	return manifest
}

func snapshotForManifest(manifest Manifest, now time.Time) SnapshotSet {
	snapshot := SnapshotSet{
		SchemaVersion:       SnapshotSchemaVersion,
		Authority:           AuthorityLocalNonAuthoritative,
		OwnerEpoch:          RoutingOwnerEpoch(1),
		CapacityBasis:       SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations,
		CapturedAtUnixMilli: now.UnixMilli(),
		Nodes:               make([]NodeSnapshot, len(manifest.Nodes)),
	}
	for index, config := range manifest.Nodes {
		probeBootID := "boot-" + config.NodeID
		active := make([]ActiveCapabilitySnapshot, len(config.Capabilities))
		for capabilityIndex, capability := range config.Capabilities {
			active[capabilityIndex] = ActiveCapabilitySnapshot{
				Runtime:     capability.Runtime,
				ImageDigest: capability.ImageDigest,
			}
		}
		snapshot.Nodes[index] = NodeSnapshot{
			NodeID:              config.NodeID,
			Endpoint:            config.Endpoint,
			Platform:            Platform(config.Platform),
			ProbeIdentity:       config.ProbeIdentity,
			PhysicalAttestation: testPhysicalHostAttestation(),
			ProbeBootID:         probeBootID,
			ActiveCapabilities:  active,
			Status:              NodeStatusLive,
			HeartbeatUnixMilli:  now.UnixMilli(),
			Total:               ResourceCapacity{CPUMilli: 4_000, MemoryBytes: snapshotTestMemoryBytes, MaxConcurrency: 4},
			Available:           ResourceCapacity{CPUMilli: 4_000, MemoryBytes: snapshotTestMemoryBytes, MaxConcurrency: 4},
		}
	}
	return snapshot
}

func snapshotTestJSONKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		t.Fatalf("snapshot JSON root is not an object: %v", err)
	}
	keys := []string{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			t.Fatalf("snapshot JSON root key is invalid: %v", err)
		}
		key, ok := token.(string)
		if !ok {
			t.Fatalf("snapshot JSON root key is not a string: %v", token)
		}
		keys = append(keys, key)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			t.Fatalf("snapshot JSON root value is invalid: %v", err)
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		t.Fatalf("snapshot JSON root is not closed: %v", err)
	}
	return keys
}

func cloneSnapshotSet(snapshot SnapshotSet) SnapshotSet {
	clone := snapshot
	clone.Nodes = append([]NodeSnapshot(nil), snapshot.Nodes...)
	for index := range clone.Nodes {
		active := snapshot.Nodes[index].ActiveCapabilities
		if active == nil {
			continue
		}
		clone.Nodes[index].ActiveCapabilities = append(make([]ActiveCapabilitySnapshot, 0, len(active)), active...)
		for capabilityIndex := range active {
			if receipt := active[capabilityIndex].Qualification; receipt != nil {
				receiptCopy := *receipt
				clone.Nodes[index].ActiveCapabilities[capabilityIndex].Qualification = &receiptCopy
			}
		}
	}
	return clone
}

func snapshotTestOffline(snapshot SnapshotSet) SnapshotSet {
	offline := cloneSnapshotSet(snapshot)
	node := &offline.Nodes[0]
	node.Status, node.ProbeBootID = NodeStatusOffline, ""
	node.ActiveCapabilities, node.Total, node.Available = []ActiveCapabilitySnapshot{}, ResourceCapacity{}, ResourceCapacity{}
	return offline
}

func mustSnapshotManifestValidate(t *testing.T, manifest Manifest) {
	t.Helper()
	if err := manifest.Validate(); err != nil {
		t.Fatalf("snapshot test manifest is invalid: %v", err)
	}
}

func assertSnapshotRejects(t *testing.T, manifest Manifest, snapshot SnapshotSet, now time.Time, heartbeatTTL time.Duration, context string) {
	t.Helper()
	before := cloneSnapshotSet(snapshot)
	err := snapshot.ValidateAgainst(manifest, now, heartbeatTTL)
	if err == nil || !reflect.DeepEqual(snapshot, before) {
		t.Fatalf("%s: rejection error=%v, mutated=%t", context, err, !reflect.DeepEqual(snapshot, before))
	}
	if strings.Contains(err.Error(), manifest.Nodes[0].Endpoint) || strings.Contains(err.Error(), manifest.Nodes[0].ProbeIdentity) {
		t.Fatalf("%s: validation error leaked sensitive node identity: %v", context, err)
	}
}
