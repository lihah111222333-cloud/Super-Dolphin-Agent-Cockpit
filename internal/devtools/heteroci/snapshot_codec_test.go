package heteroci

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDecodeSnapshotSetRoundTripsJSONSnapshotAndValidates(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestDarlingManifest()
	mustSnapshotManifestValidate(t, manifest)
	snapshot := snapshotForManifest(manifest, now)
	snapshot.OwnerEpoch = RoutingOwnerEpoch(7)
	raw := mustMarshalSnapshotSet(t, snapshot)

	decoded, err := DecodeSnapshotSet(bytes.NewReader(raw), manifest, now, snapshotTestHeartbeatTTL)
	if err != nil {
		t.Fatalf("DecodeSnapshotSet(valid snapshot): %v", err)
	}
	if !reflect.DeepEqual(decoded, snapshot) {
		t.Fatalf("decoded snapshot differs from source:\n got %#v\nwant %#v", decoded, snapshot)
	}
	if decoded.CapacityBasis != snapshot.CapacityBasis {
		t.Fatalf("decoded snapshot capacity_basis = %q, want %q", decoded.CapacityBasis, snapshot.CapacityBasis)
	}
	if decoded.OwnerEpoch != snapshot.OwnerEpoch {
		t.Fatalf("decoded snapshot owner_epoch = %d, want %d", decoded.OwnerEpoch, snapshot.OwnerEpoch)
	}
	snapshotCodecAssertCanonicalRoot(t, raw, snapshot)
	snapshotCodecAssertRootFieldOrder(t, raw)
	if err := decoded.ValidateAgainst(manifest, now, snapshotTestHeartbeatTTL); err != nil {
		t.Fatalf("decoded snapshot ValidateAgainst: %v", err)
	}
}

func TestDecodeSnapshotSetAcceptsMaximumValidOwnerEpoch(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestManifest(1)
	mustSnapshotManifestValidate(t, manifest)
	snapshot := snapshotForManifest(manifest, now)
	snapshot.OwnerEpoch = RoutingOwnerEpoch(math.MaxInt64 - 1)
	decoded, err := DecodeSnapshotSet(bytes.NewReader(mustMarshalSnapshotSet(t, snapshot)), manifest, now, snapshotTestHeartbeatTTL)
	if err != nil {
		t.Fatalf("DecodeSnapshotSet(owner_epoch=MaxInt64-1): %v", err)
	}
	if decoded.OwnerEpoch != snapshot.OwnerEpoch {
		t.Fatalf("decoded owner_epoch = %d, want %d", decoded.OwnerEpoch, snapshot.OwnerEpoch)
	}
}

func TestDecodeSnapshotSetRejectsNilReaderAndOversizedPayload(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestManifest(1)
	mustSnapshotManifestValidate(t, manifest)

	if _, err := DecodeSnapshotSet(nil, manifest, now, snapshotTestHeartbeatTTL); err == nil {
		t.Fatal("DecodeSnapshotSet(nil reader) unexpectedly succeeded")
	}

	valid := mustMarshalSnapshotSet(t, snapshotForManifest(manifest, now))
	if len(valid) >= 1<<20 {
		t.Fatalf("snapshot fixture unexpectedly exceeds the 1 MiB limit: %d bytes", len(valid))
	}
	oversized := append(append([]byte(nil), valid...), bytes.Repeat([]byte(" "), (1<<20+1)-len(valid))...)
	if _, err := DecodeSnapshotSet(bytes.NewReader(oversized), manifest, now, snapshotTestHeartbeatTTL); err == nil {
		t.Fatal("DecodeSnapshotSet(>1 MiB payload) unexpectedly succeeded")
	}
}

func TestDecodeSnapshotSetRejectsStrictJSONShape(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestDarlingManifest()
	mustSnapshotManifestValidate(t, manifest)
	valid := mustMarshalSnapshotSet(t, snapshotForManifest(manifest, now))
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "unknown root field", raw: snapshotCodecSetRootField(t, valid, "unknown_root", json.RawMessage(`true`))},
		{name: "unknown nested field", raw: snapshotCodecSetNodeField(t, valid, 0, "unknown_nested", json.RawMessage(`true`))},
		{name: "unknown qualification field", raw: snapshotCodecSetReceiptField(t, valid, 0, 1, "unknown_receipt", json.RawMessage(`true`))},
		{name: "null root", raw: []byte("null")},
		{name: "null qualification", raw: snapshotCodecSetActiveField(t, valid, 0, 1, "qualification", json.RawMessage(`null`))},
		{name: "null active capability", raw: snapshotCodecSetActiveValue(t, valid, 0, 0, json.RawMessage(`null`))},
		{name: "null active capabilities", raw: snapshotCodecSetNodeField(t, valid, 0, "active_capabilities", json.RawMessage(`null`))},
		{name: "null nodes", raw: snapshotCodecSetRootField(t, valid, "nodes", json.RawMessage(`null`))},
		{name: "duplicate root key", raw: snapshotCodecDuplicateRootField(valid, "authority", `"LOCAL_NON_AUTHORITATIVE"`)},
		{name: "duplicate owner epoch key", raw: snapshotCodecDuplicateRootField(valid, "owner_epoch", `7`)},
		{name: "duplicate nested key", raw: snapshotCodecDuplicateNodeField(valid, 0, "node_id", `"heteroci-node-1"`)},
		{name: "trailing second value", raw: append(append([]byte(nil), valid...), []byte(` {}`)...)},
		{name: "malformed JSON", raw: []byte(`{"schema_version":`)},
		{name: "missing schema", raw: snapshotCodecRemoveRootField(t, valid, "schema_version")},
		{name: "missing authority", raw: snapshotCodecRemoveRootField(t, valid, "authority")},
		{name: "missing captured timestamp", raw: snapshotCodecRemoveRootField(t, valid, "captured_at_unix_milli")},
		{name: "missing capacity basis", raw: snapshotCodecRemoveRootField(t, valid, "capacity_basis")},
		{name: "missing nodes", raw: snapshotCodecRemoveRootField(t, valid, "nodes")},
		{name: "missing owner epoch", raw: snapshotCodecRemoveRootField(t, valid, "owner_epoch")},
		{name: "old schema", raw: snapshotCodecSetRootField(t, valid, "schema_version", json.RawMessage(`"heteroci-snapshot/v0"`))},
		{name: "old snapshot schema v1", raw: snapshotCodecSetRootField(t, valid, "schema_version", json.RawMessage(`"heteroci-snapshot/v1"`))},
		{name: "old snapshot schema v2", raw: snapshotCodecSetRootField(t, valid, "schema_version", json.RawMessage(`"heteroci-snapshot/v2"`))},
		{name: "unknown owner epoch", raw: snapshotCodecSetRootField(t, valid, "owner_epoch", json.RawMessage(`9223372036854775808`))},
		{name: "zero owner epoch", raw: snapshotCodecSetRootField(t, valid, "owner_epoch", json.RawMessage(`0`))},
		{name: "negative owner epoch", raw: snapshotCodecSetRootField(t, valid, "owner_epoch", json.RawMessage(`-1`))},
		{name: "maximum owner epoch", raw: snapshotCodecSetRootField(t, valid, "owner_epoch", json.RawMessage(`9223372036854775807`))},
		{name: "null owner epoch", raw: snapshotCodecSetRootField(t, valid, "owner_epoch", json.RawMessage(`null`))},
		{name: "null capacity basis", raw: snapshotCodecSetRootField(t, valid, "capacity_basis", json.RawMessage(`null`))},
		{name: "unknown capacity basis", raw: snapshotCodecSetRootField(t, valid, "capacity_basis", json.RawMessage(`"unknown-capacity-basis"`))},
		{name: "remote authority", raw: snapshotCodecSetRootField(t, valid, "authority", json.RawMessage(`"REMOTE_AUTHORITATIVE"`))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshotCodecAssertDecodeRejects(t, tt.raw, manifest, now, tt.name)
		})
	}
}

func TestDecodeSnapshotSetOfflineRoundTripsExplicitEmptyCapabilities(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestManifest(1)
	mustSnapshotManifestValidate(t, manifest)
	snapshot := snapshotForManifest(manifest, now)
	node := &snapshot.Nodes[0]
	node.Status = NodeStatusOffline
	node.HeartbeatUnixMilli = 0
	node.ProbeBootID = ""
	node.ActiveCapabilities = []ActiveCapabilitySnapshot{}
	node.Total = ResourceCapacity{}
	node.Available = ResourceCapacity{}
	if node.ActiveCapabilities == nil {
		t.Fatal("offline fixture must use a non-nil empty active_capabilities slice")
	}
	if err := snapshot.ValidateAgainst(manifest, now, snapshotTestHeartbeatTTL); err != nil {
		t.Fatalf("offline snapshot with explicit empty capabilities should validate: %v", err)
	}

	raw := mustMarshalSnapshotSet(t, snapshot)
	snapshotCodecAssertOfflineJSONRoundTrip(t, raw, snapshot, manifest, now)

	nilCapabilities := cloneSnapshotSet(snapshot)
	nilCapabilities.Nodes[0].ActiveCapabilities = nil
	if err := nilCapabilities.ValidateAgainst(manifest, now, snapshotTestHeartbeatTTL); err == nil {
		t.Fatal("offline snapshot with nil active_capabilities unexpectedly validated")
	}

	for _, tt := range []struct {
		name string
		raw  []byte
	}{
		{name: "live heartbeat zero", raw: snapshotCodecSetNodeField(t, raw, 0, "status", json.RawMessage(`"live"`))},
		{name: "active capabilities null", raw: snapshotCodecSetNodeField(t, raw, 0, "active_capabilities", json.RawMessage(`null`))},
		{name: "active capabilities missing", raw: snapshotCodecRemoveNodeField(t, raw, 0, "active_capabilities")},
		{name: "probe boot id null", raw: snapshotCodecSetNodeField(t, raw, 0, "probe_boot_id", json.RawMessage(`null`))},
		{name: "probe boot id missing", raw: snapshotCodecRemoveNodeField(t, raw, 0, "probe_boot_id")},
		{name: "available null", raw: snapshotCodecSetNodeField(t, raw, 0, "available", json.RawMessage(`null`))},
		{name: "available missing", raw: snapshotCodecRemoveNodeField(t, raw, 0, "available")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeSnapshotSet(bytes.NewReader(tt.raw), manifest, now, snapshotTestHeartbeatTTL); err == nil {
				t.Fatalf("DecodeSnapshotSet unexpectedly accepted %s", tt.name)
			}
		})
	}
}

func snapshotCodecAssertOfflineJSONRoundTrip(t *testing.T, raw []byte, snapshot SnapshotSet, manifest Manifest, now time.Time) {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("decode offline snapshot root fixture: %v", err)
	}
	var nodes []map[string]json.RawMessage
	if err := json.Unmarshal(root["nodes"], &nodes); err != nil {
		t.Fatalf("decode offline snapshot node fixture: %v", err)
	}
	if len(nodes) != 1 || !bytes.Equal(bytes.TrimSpace(nodes[0]["active_capabilities"]), []byte("[]")) {
		t.Fatalf("offline JSON must contain active_capabilities:[], got %s", raw)
	}
	decoded, err := DecodeSnapshotSet(bytes.NewReader(raw), manifest, now, snapshotTestHeartbeatTTL)
	if err != nil {
		t.Fatalf("DecodeSnapshotSet(offline explicit empty capabilities): %v", err)
	}
	if decoded.Nodes[0].ActiveCapabilities == nil {
		t.Fatal("decoded offline active_capabilities must remain a non-nil empty slice")
	}
	if !reflect.DeepEqual(decoded, snapshot) {
		t.Fatalf("decoded offline snapshot differs from source:\n got %#v\nwant %#v", decoded, snapshot)
	}
	if err := decoded.ValidateAgainst(manifest, now, snapshotTestHeartbeatTTL); err != nil {
		t.Fatalf("decoded offline snapshot ValidateAgainst: %v", err)
	}
}

func TestDecodeSnapshotSetRejectsMissingRequiredNodeFields(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestManifest(1)
	mustSnapshotManifestValidate(t, manifest)
	live := mustMarshalSnapshotSet(t, snapshotForManifest(manifest, now))
	offlineSnapshot := snapshotForManifest(manifest, now)
	offlineNode := &offlineSnapshot.Nodes[0]
	offlineNode.Status = NodeStatusOffline
	offlineNode.ProbeBootID = ""
	offlineNode.ActiveCapabilities = []ActiveCapabilitySnapshot{}
	offlineNode.Available = ResourceCapacity{}
	offline := mustMarshalSnapshotSet(t, offlineSnapshot)

	fields := []string{
		"node_id", "endpoint", "platform", "probe_identity", "probe_boot_id",
		"active_capabilities", "status", "heartbeat_unix_milli", "total", "available",
	}
	for _, fixture := range []struct {
		name string
		raw  []byte
	}{
		{name: "live", raw: live},
		{name: "offline", raw: offline},
	} {
		for _, field := range fields {
			t.Run(fixture.name+"/missing_"+field, func(t *testing.T) {
				raw := snapshotCodecRemoveNodeField(t, fixture.raw, 0, field)
				if _, err := DecodeSnapshotSet(bytes.NewReader(raw), manifest, now, snapshotTestHeartbeatTTL); err == nil {
					t.Fatalf("DecodeSnapshotSet unexpectedly accepted missing node field %q", field)
				}
			})
		}
	}
}
func TestDecodeSnapshotSetRejectsMissingResourceCapacityFields(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestManifest(1)
	mustSnapshotManifestValidate(t, manifest)
	live := mustMarshalSnapshotSet(t, snapshotForManifest(manifest, now))
	offlineSnapshot := snapshotForManifest(manifest, now)
	offlineNode := &offlineSnapshot.Nodes[0]
	offlineNode.Status = NodeStatusOffline
	offlineNode.ProbeBootID = ""
	offlineNode.ActiveCapabilities = []ActiveCapabilitySnapshot{}
	offlineNode.Available = ResourceCapacity{}
	offline := mustMarshalSnapshotSet(t, offlineSnapshot)
	for _, fixture := range []struct {
		name string
		raw  []byte
	}{
		{name: "live", raw: live},
		{name: "offline", raw: offline},
	} {
		for _, resource := range []string{"total", "available"} {
			for _, field := range []string{"cpu_milli", "memory_bytes", "max_concurrency"} {
				t.Run(fixture.name+"/missing_"+resource+"/"+field, func(t *testing.T) {
					raw := snapshotCodecRemoveResourceField(t, fixture.raw, 0, resource, field)
					if _, err := DecodeSnapshotSet(bytes.NewReader(raw), manifest, now, snapshotTestHeartbeatTTL); err == nil {
						t.Fatalf("DecodeSnapshotSet unexpectedly accepted missing %s.%s", resource, field)
					}
				})
			}
		}
	}
}
func TestDecodeSnapshotSetRejectsMissingActiveCapabilityFields(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestDarlingManifest()
	mustSnapshotManifestValidate(t, manifest)
	snapshot := snapshotForManifest(manifest, now)
	valid := mustMarshalSnapshotSet(t, snapshot)
	missing := snapshot
	missing.Nodes[0].ActiveCapabilities = missing.Nodes[0].ActiveCapabilities[:1]
	missingRaw := mustMarshalSnapshotSet(t, missing)
	if _, err := DecodeSnapshotSet(bytes.NewReader(missingRaw), manifest, now, snapshotTestHeartbeatTTL); err == nil {
		t.Fatal("DecodeSnapshotSet unexpectedly accepted a live snapshot missing one configured active capability")
	}
	for _, field := range []string{"runtime", "image_digest"} {
		t.Run("missing_"+field, func(t *testing.T) {
			raw := snapshotCodecRemoveActiveField(t, valid, 0, 0, field)
			if _, err := DecodeSnapshotSet(bytes.NewReader(raw), manifest, now, snapshotTestHeartbeatTTL); err == nil {
				t.Fatalf("DecodeSnapshotSet unexpectedly accepted missing active capability field %q", field)
			}
		})
	}
}
func TestDecodeSnapshotSetRejectsMissingDarlingAndDockerQualification(t *testing.T) {
	now := snapshotTestNow()
	for _, fixture := range []struct {
		name     string
		manifest Manifest
		runtime  RuntimeKind
	}{
		{name: "darling", manifest: snapshotTestDarlingManifest(), runtime: RuntimeDarlingCompatRuntime},
		{name: "docker_osx", manifest: snapshotTestDockerManifest(), runtime: RuntimeDockerOSXExperimental},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			manifest := fixture.manifest
			mustSnapshotManifestValidate(t, manifest)
			snapshot := snapshotForManifest(manifest, now)
			activeIndex := snapshotTestCapabilityIndex(snapshot, fixture.runtime)
			raw := snapshotCodecRemoveActiveField(t, mustMarshalSnapshotSet(t, snapshot), 0, activeIndex, "qualification")
			if _, err := DecodeSnapshotSet(bytes.NewReader(raw), manifest, now, snapshotTestHeartbeatTTL); err == nil {
				t.Fatalf("DecodeSnapshotSet unexpectedly accepted missing %s qualification", fixture.name)
			}
		})
	}
}
func TestDecodeSnapshotSetRejectsMissingQualificationReceiptFields(t *testing.T) {
	now := snapshotTestNow()
	receiptFields := []string{
		"schema_version", "node_id", "runtime", "probe_identity", "probe_boot_id",
		"image_digest", "source_revision", "qualification_suite", "immutable_identity",
		"completed_at_unix_milli", "digest",
	}
	for _, fixture := range []struct {
		name     string
		manifest Manifest
		runtime  RuntimeKind
	}{
		{name: "darling", manifest: snapshotTestDarlingManifest(), runtime: RuntimeDarlingCompatRuntime},
		{name: "docker_osx", manifest: snapshotTestDockerManifest(), runtime: RuntimeDockerOSXExperimental},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			manifest := fixture.manifest
			mustSnapshotManifestValidate(t, manifest)
			snapshot := snapshotForManifest(manifest, now)
			activeIndex := snapshotTestCapabilityIndex(snapshot, fixture.runtime)
			valid := mustMarshalSnapshotSet(t, snapshot)
			if _, err := DecodeSnapshotSet(bytes.NewReader(valid), manifest, now, snapshotTestHeartbeatTTL); err != nil {
				t.Fatalf("DecodeSnapshotSet(valid %s receipt): %v", fixture.name, err)
			}
			for _, field := range receiptFields {
				t.Run("missing_"+field, func(t *testing.T) {
					raw := snapshotCodecRemoveReceiptField(t, valid, 0, activeIndex, field)
					if _, err := DecodeSnapshotSet(bytes.NewReader(raw), manifest, now, snapshotTestHeartbeatTTL); err == nil {
						t.Fatalf("DecodeSnapshotSet unexpectedly accepted missing %s receipt field %q", fixture.name, field)
					}
				})
			}
		})
	}
}
func TestDecodeSnapshotSetRejectsMissingCanonicalJSONFields(t *testing.T) {
	now := snapshotTestNow()
	fixtures := []struct {
		name     string
		manifest Manifest
	}{
		{name: "live", manifest: snapshotTestManifest(1)},
		{name: "darling", manifest: snapshotTestDarlingManifest()},
		{name: "docker_osx", manifest: snapshotTestDockerManifest()},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			mustSnapshotManifestValidate(t, fixture.manifest)
			snapshot := snapshotForManifest(fixture.manifest, now)
			valid := mustMarshalSnapshotSet(t, snapshot)
			snapshotCodecAssertRootFieldsReject(t, valid, fixture.manifest, now)
			for nodeIndex := range snapshot.Nodes {
				snapshotCodecAssertNodeFieldsReject(t, valid, fixture.manifest, snapshot, nodeIndex, now)
			}
		})
	}
}
func TestDecodeSnapshotSetRejectsStaleFutureAndMismatchedManifest(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestManifest(1)
	mustSnapshotManifestValidate(t, manifest)
	valid := mustMarshalSnapshotSet(t, snapshotForManifest(manifest, now))
	mismatchedManifest := snapshotTestManifest(2)
	mismatchedManifest.Nodes[0].NodeID = "mismatched-node"

	tests := []struct {
		name     string
		manifest Manifest
		now      func() time.Time
	}{
		{name: "stale capture", manifest: manifest, now: func() time.Time { return now.Add(snapshotTestHeartbeatTTL + time.Millisecond) }},
		{name: "future capture", manifest: manifest, now: func() time.Time { return now.Add(-time.Millisecond) }},
		{name: "mismatched manifest", manifest: mismatchedManifest, now: func() time.Time { return now }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeSnapshotSet(bytes.NewReader(valid), tt.manifest, tt.now(), snapshotTestHeartbeatTTL); err == nil {
				t.Fatalf("DecodeSnapshotSet unexpectedly accepted %s", tt.name)
			}
		})
	}
	decoded, err := DecodeSnapshotSet(bytes.NewReader(valid), manifest, now, snapshotTestHeartbeatTTL)
	if err != nil {
		t.Fatalf("DecodeSnapshotSet(valid snapshot): %v", err)
	}
	if err := decoded.ValidateAgainst(mismatchedManifest, now, snapshotTestHeartbeatTTL); err == nil {
		t.Fatal("ValidateAgainst unexpectedly accepted a mismatched manifest")
	}
	for _, tt := range []struct {
		name string
		now  time.Time
	}{
		{name: "stale capture", now: now.Add(snapshotTestHeartbeatTTL + time.Millisecond)},
		{name: "future capture", now: now.Add(-time.Millisecond)},
	} {
		t.Run("ValidateAgainst_"+tt.name, func(t *testing.T) {
			if err := decoded.ValidateAgainst(manifest, tt.now, snapshotTestHeartbeatTTL); err == nil {
				t.Fatalf("ValidateAgainst unexpectedly accepted %s", tt.name)
			}
		})
	}
}

type snapshotCodecJSONField struct {
	goName    string
	name      string
	omitEmpty bool
}

func snapshotCodecCanonicalJSONFields(typ reflect.Type) []snapshotCodecJSONField {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		// archguard:ignore panic_count -- 非结构体类型是测试辅助函数的不可恢复输入错误。
		panic(fmt.Sprintf("snapshot codec JSON field type must be a struct, got %s", typ))
	}
	fields := make([]snapshotCodecJSONField, 0)
	for field := range typ.Fields() {
		if field.PkgPath != "" {
			continue
		}
		name := field.Name
		omitEmpty := false
		if tag, present := field.Tag.Lookup("json"); present {
			parts := strings.Split(tag, ",")
			if parts[0] == "-" {
				continue
			}
			if parts[0] != "" {
				name = parts[0]
			}
			for _, option := range parts[1:] {
				if option == "omitempty" {
					omitEmpty = true
				}
			}
		}
		fields = append(fields, snapshotCodecJSONField{goName: field.Name, name: name, omitEmpty: omitEmpty})
	}
	return fields
}
func snapshotCodecActualJSONFields(t *testing.T, object map[string]json.RawMessage, typ reflect.Type) []snapshotCodecJSONField {
	t.Helper()
	actual := make([]snapshotCodecJSONField, 0)
	for _, field := range snapshotCodecCanonicalJSONFields(typ) {
		if _, present := object[field.name]; present {
			actual = append(actual, field)
			continue
		}
		if !field.omitEmpty {
			t.Fatalf("valid snapshot JSON is missing non-omitempty field %q for %s", field.name, typ)
		}
	}
	return actual
}
func snapshotCodecJSONFieldName(t *testing.T, typ reflect.Type, goName string) string {
	t.Helper()
	for _, field := range snapshotCodecCanonicalJSONFields(typ) {
		if field.goName == goName {
			return field.name
		}
	}
	t.Fatalf("JSON tag for %s.%s is missing", typ, goName)
	return ""
}
func snapshotCodecAssertRootFieldOrder(t *testing.T, raw []byte) {
	t.Helper()
	want := []string{"schema_version", "authority", "owner_epoch", "capacity_basis", "captured_at_unix_milli", "nodes"}
	fields := snapshotCodecCanonicalJSONFields(reflect.TypeFor[SnapshotSet]())
	if len(fields) != len(want) {
		t.Fatalf("SnapshotSet root field count = %d, want %d", len(fields), len(want))
	}
	for index, field := range fields {
		if field.name != want[index] {
			t.Fatalf("SnapshotSet reflection field %d = %q, want %q", index, field.name, want[index])
		}
	}
	if got := snapshotCodecJSONFieldOrder(t, raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("SnapshotSet JSON root field order = %v, want %v", got, want)
	}
}
func snapshotCodecAssertCanonicalRoot(t *testing.T, raw []byte, snapshot SnapshotSet) {
	t.Helper()
	root := snapshotCodecRootObject(t, raw)
	basis, ok := root["capacity_basis"]
	if !ok {
		t.Fatalf("canonical snapshot JSON is missing capacity_basis: %s", raw)
	}
	var encodedBasis string
	if err := json.Unmarshal(basis, &encodedBasis); err != nil {
		t.Fatalf("decode canonical capacity_basis: %v", err)
	}
	ownerEpochName := snapshotCodecJSONFieldName(t, reflect.TypeFor[SnapshotSet](), "OwnerEpoch")
	if encodedBasis != string(snapshot.CapacityBasis) || ownerEpochName != "owner_epoch" || snapshot.SchemaVersion != "heteroci-snapshot/v3" {
		t.Fatalf("canonical snapshot root basis/schema/tag = %q/%q/%q, want %q/heteroci-snapshot/v3/owner_epoch", encodedBasis, snapshot.SchemaVersion, ownerEpochName, snapshot.CapacityBasis)
	}
}
func snapshotCodecJSONFieldOrder(t *testing.T, raw []byte) []string {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		t.Fatalf("snapshot root is not an object: %v", err)
	}
	order := make([]string, 0, 6)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			t.Fatalf("decode snapshot root field: %v", err)
		}
		name, ok := key.(string)
		if !ok {
			t.Fatalf("snapshot root field key is %T, want string", key)
		}
		order = append(order, name)
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			t.Fatalf("decode snapshot root field %q value: %v", name, err)
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		t.Fatalf("snapshot root closing token = %v, err=%v", closing, err)
	}
	return order
}
func mustMarshalSnapshotSet(t *testing.T, snapshot SnapshotSet) []byte {
	t.Helper()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal snapshot: %v", err)
	}
	return raw
}
func snapshotCodecDecodeJSONObject(t *testing.T, raw json.RawMessage, context string) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode %s object: %v", context, err)
	}
	if object == nil {
		t.Fatalf("decode %s object: null object", context)
	}
	return object
}
func snapshotCodecDecodeJSONArray(t *testing.T, raw json.RawMessage, context string) []json.RawMessage {
	t.Helper()
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatalf("decode %s array: %v", context, err)
	}
	if values == nil {
		t.Fatalf("decode %s array: null array", context)
	}
	return values
}
func snapshotCodecRootObject(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	return snapshotCodecDecodeJSONObject(t, raw, "snapshot root")
}
func snapshotCodecNodeObject(t *testing.T, raw []byte, nodeIndex int) map[string]json.RawMessage {
	t.Helper()
	root := snapshotCodecRootObject(t, raw)
	nodes := snapshotCodecDecodeJSONArray(t, root["nodes"], "snapshot nodes")
	if nodeIndex < 0 || nodeIndex >= len(nodes) {
		t.Fatalf("snapshot node index %d out of range", nodeIndex)
	}
	return snapshotCodecDecodeJSONObject(t, nodes[nodeIndex], fmt.Sprintf("snapshot node %d", nodeIndex))
}
func snapshotCodecResourceObject(t *testing.T, raw []byte, nodeIndex int, resourceName string) map[string]json.RawMessage {
	t.Helper()
	node := snapshotCodecNodeObject(t, raw, nodeIndex)
	return snapshotCodecDecodeJSONObject(t, node[resourceName], "snapshot "+resourceName)
}
func snapshotCodecActiveObject(t *testing.T, raw []byte, nodeIndex, activeIndex int) map[string]json.RawMessage {
	t.Helper()
	node := snapshotCodecNodeObject(t, raw, nodeIndex)
	active := snapshotCodecDecodeJSONArray(t, node["active_capabilities"], "snapshot active capabilities")
	if activeIndex < 0 || activeIndex >= len(active) {
		t.Fatalf("snapshot active capability index %d out of range", activeIndex)
	}
	return snapshotCodecDecodeJSONObject(t, active[activeIndex], "snapshot active capability")
}
func snapshotCodecReceiptObject(t *testing.T, raw []byte, nodeIndex, activeIndex int) map[string]json.RawMessage {
	t.Helper()
	active := snapshotCodecActiveObject(t, raw, nodeIndex, activeIndex)
	qualificationName := snapshotCodecJSONFieldName(t, reflect.TypeFor[ActiveCapabilitySnapshot](), "Qualification")
	return snapshotCodecDecodeJSONObject(t, active[qualificationName], "snapshot qualification")
}
func snapshotCodecAssertDecodeRejects(t *testing.T, raw []byte, manifest Manifest, now time.Time, context string) {
	t.Helper()
	got, err := DecodeSnapshotSet(bytes.NewReader(raw), manifest, now, snapshotTestHeartbeatTTL)
	if err == nil {
		t.Fatalf("DecodeSnapshotSet unexpectedly accepted %s", context)
	}
	if !reflect.DeepEqual(got, SnapshotSet{}) {
		t.Fatalf("DecodeSnapshotSet(%s) returned non-zero snapshot on error: %#v", context, got)
	}
}
func snapshotCodecAssertFieldsReject(t *testing.T, fields []snapshotCodecJSONField, scope string, remove func(string) []byte, manifest Manifest, now time.Time) {
	t.Helper()
	for _, field := range fields {
		t.Run(fmt.Sprintf("%s/missing_%s", scope, field.name), func(t *testing.T) {
			snapshotCodecAssertDecodeRejects(t, remove(field.name), manifest, now, fmt.Sprintf("%s field %q", scope, field.name))
		})
	}
}
func snapshotCodecAssertRootFieldsReject(t *testing.T, raw []byte, manifest Manifest, now time.Time) {
	t.Helper()
	root := snapshotCodecRootObject(t, raw)
	capacityBasisName := snapshotCodecJSONFieldName(t, reflect.TypeFor[SnapshotSet](), "CapacityBasis")
	if capacityBasisName != "capacity_basis" {
		t.Fatalf("SnapshotSet.CapacityBasis JSON tag = %q, want %q", capacityBasisName, "capacity_basis")
	}
	if _, present := root[capacityBasisName]; !present {
		t.Fatalf("valid snapshot JSON is missing dynamically enumerated root field %q", capacityBasisName)
	}
	fields := snapshotCodecActualJSONFields(t, root, reflect.TypeFor[SnapshotSet]())
	snapshotCodecAssertFieldsReject(t, fields, "root", func(name string) []byte {
		return snapshotCodecRemoveRootField(t, raw, name)
	}, manifest, now)
}
func snapshotCodecAssertNodeFieldsReject(t *testing.T, raw []byte, manifest Manifest, snapshot SnapshotSet, nodeIndex int, now time.Time) {
	t.Helper()
	node := snapshotCodecNodeObject(t, raw, nodeIndex)
	nodeFields := snapshotCodecActualJSONFields(t, node, reflect.TypeFor[NodeSnapshot]())
	snapshotCodecAssertFieldsReject(t, nodeFields, fmt.Sprintf("node_%d", nodeIndex), func(name string) []byte {
		return snapshotCodecRemoveNodeField(t, raw, nodeIndex, name)
	}, manifest, now)
	for _, resourceName := range []string{"total", "available"} {
		resource := snapshotCodecResourceObject(t, raw, nodeIndex, resourceName)
		resourceFields := snapshotCodecActualJSONFields(t, resource, reflect.TypeFor[ResourceCapacity]())
		snapshotCodecAssertFieldsReject(t, resourceFields, fmt.Sprintf("node_%d/%s", nodeIndex, resourceName), func(name string) []byte {
			return snapshotCodecRemoveResourceField(t, raw, nodeIndex, resourceName, name)
		}, manifest, now)
	}
	active := snapshotCodecDecodeJSONArray(t, node["active_capabilities"], "snapshot active capabilities")
	for activeIndex := range active {
		snapshotCodecAssertActiveFieldsReject(t, raw, manifest, snapshot, nodeIndex, activeIndex, now)
	}
}
func snapshotCodecAssertActiveFieldsReject(t *testing.T, raw []byte, manifest Manifest, snapshot SnapshotSet, nodeIndex, activeIndex int, now time.Time) {
	t.Helper()
	activeObject := snapshotCodecActiveObject(t, raw, nodeIndex, activeIndex)
	activeFields := snapshotCodecActualJSONFields(t, activeObject, reflect.TypeFor[ActiveCapabilitySnapshot]())
	qualificationName := snapshotCodecJSONFieldName(t, reflect.TypeFor[ActiveCapabilitySnapshot](), "Qualification")
	runtime := snapshot.Nodes[nodeIndex].ActiveCapabilities[activeIndex].Runtime
	if !snapshotCodecRuntimeRequiresQualification(runtime) {
		activeFields = snapshotCodecFilterField(activeFields, qualificationName)
	}
	snapshotCodecAssertFieldsReject(t, activeFields, fmt.Sprintf("node_%d/active_%d", nodeIndex, activeIndex), func(name string) []byte {
		return snapshotCodecRemoveActiveField(t, raw, nodeIndex, activeIndex, name)
	}, manifest, now)
	if snapshotCodecRuntimeRequiresQualification(runtime) {
		receiptObject := snapshotCodecReceiptObject(t, raw, nodeIndex, activeIndex)
		receiptFields := snapshotCodecActualJSONFields(t, receiptObject, reflect.TypeFor[CapabilityQualificationReceipt]())
		snapshotCodecAssertFieldsReject(t, receiptFields, fmt.Sprintf("node_%d/active_%d/qualification", nodeIndex, activeIndex), func(name string) []byte {
			return snapshotCodecRemoveReceiptField(t, raw, nodeIndex, activeIndex, name)
		}, manifest, now)
	}
}
func snapshotCodecFilterField(fields []snapshotCodecJSONField, name string) []snapshotCodecJSONField {
	filtered := make([]snapshotCodecJSONField, 0, len(fields))
	for _, field := range fields {
		if field.name != name {
			filtered = append(filtered, field)
		}
	}
	return filtered
}
func snapshotCodecRuntimeRequiresQualification(runtime RuntimeKind) bool {
	return runtime == RuntimeDarlingCompatRuntime || runtime == RuntimeDockerOSXExperimental
}
func snapshotCodecSetRootField(t *testing.T, raw []byte, name string, value json.RawMessage) []byte {
	t.Helper()
	return snapshotCodecUpdateJSON(t, raw, func(root map[string]json.RawMessage) { root[name] = value })
}
func snapshotCodecRemoveRootField(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	return snapshotCodecUpdateJSON(t, raw, func(root map[string]json.RawMessage) { delete(root, name) })
}
func snapshotCodecSetNodeField(t *testing.T, raw []byte, nodeIndex int, name string, value json.RawMessage) []byte {
	t.Helper()
	return snapshotCodecUpdateNode(t, raw, nodeIndex, func(node map[string]json.RawMessage) { node[name] = value })
}
func snapshotCodecRemoveNodeField(t *testing.T, raw []byte, nodeIndex int, name string) []byte {
	t.Helper()
	return snapshotCodecUpdateNode(t, raw, nodeIndex, func(node map[string]json.RawMessage) { delete(node, name) })
}
func snapshotCodecRemoveResourceField(t *testing.T, raw []byte, nodeIndex int, resourceName, fieldName string) []byte {
	t.Helper()
	return snapshotCodecUpdateNode(t, raw, nodeIndex, func(node map[string]json.RawMessage) {
		node[resourceName] = snapshotCodecUpdateJSON(t, node[resourceName], func(resource map[string]json.RawMessage) { delete(resource, fieldName) })
	})
}
func snapshotCodecSetActiveField(t *testing.T, raw []byte, nodeIndex, activeIndex int, name string, value json.RawMessage) []byte {
	t.Helper()
	return snapshotCodecUpdateActive(t, raw, nodeIndex, activeIndex, func(active map[string]json.RawMessage) { active[name] = value })
}
func snapshotCodecRemoveActiveField(t *testing.T, raw []byte, nodeIndex, activeIndex int, name string) []byte {
	t.Helper()
	return snapshotCodecUpdateActive(t, raw, nodeIndex, activeIndex, func(active map[string]json.RawMessage) { delete(active, name) })
}
func snapshotCodecSetActiveValue(t *testing.T, raw []byte, nodeIndex, activeIndex int, value json.RawMessage) []byte {
	t.Helper()
	return snapshotCodecUpdateNode(t, raw, nodeIndex, func(node map[string]json.RawMessage) {
		active := snapshotCodecDecodeJSONArray(t, node["active_capabilities"], "snapshot active capability fixture")
		if activeIndex < 0 || activeIndex >= len(active) {
			t.Fatalf("snapshot fixture active capability index %d out of range", activeIndex)
		}
		active[activeIndex] = value
		updated, err := json.Marshal(active)
		if err != nil {
			t.Fatalf("encode snapshot active capability fixture: %v", err)
		}
		node["active_capabilities"] = updated
	})
}
func snapshotCodecSetReceiptField(t *testing.T, raw []byte, nodeIndex, activeIndex int, name string, value json.RawMessage) []byte {
	t.Helper()
	return snapshotCodecUpdateReceipt(t, raw, nodeIndex, activeIndex, func(receipt map[string]json.RawMessage) { receipt[name] = value })
}
func snapshotCodecRemoveReceiptField(t *testing.T, raw []byte, nodeIndex, activeIndex int, name string) []byte {
	t.Helper()
	return snapshotCodecUpdateReceipt(t, raw, nodeIndex, activeIndex, func(receipt map[string]json.RawMessage) { delete(receipt, name) })
}
func snapshotCodecUpdateJSON(t *testing.T, raw []byte, mutate func(map[string]json.RawMessage)) []byte {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("decode snapshot root fixture: %v", err)
	}
	mutate(root)
	updated, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("encode snapshot root fixture: %v", err)
	}
	return updated
}
func snapshotCodecUpdateNode(t *testing.T, raw []byte, nodeIndex int, mutate func(map[string]json.RawMessage)) []byte {
	t.Helper()
	return snapshotCodecUpdateJSON(t, raw, func(root map[string]json.RawMessage) {
		nodes := snapshotCodecDecodeJSONArray(t, root["nodes"], "snapshot nodes fixture")
		if nodeIndex < 0 || nodeIndex >= len(nodes) {
			t.Fatalf("snapshot fixture node index %d out of range", nodeIndex)
		}
		nodes[nodeIndex] = snapshotCodecUpdateJSON(t, nodes[nodeIndex], mutate)
		updated, err := json.Marshal(nodes)
		if err != nil {
			t.Fatalf("encode snapshot nodes fixture: %v", err)
		}
		root["nodes"] = updated
	})
}
func snapshotCodecUpdateActive(t *testing.T, raw []byte, nodeIndex, activeIndex int, mutate func(map[string]json.RawMessage)) []byte {
	t.Helper()
	return snapshotCodecUpdateNode(t, raw, nodeIndex, func(node map[string]json.RawMessage) {
		active := snapshotCodecDecodeJSONArray(t, node["active_capabilities"], "snapshot active capabilities fixture")
		if activeIndex < 0 || activeIndex >= len(active) {
			t.Fatalf("snapshot fixture active capability index %d out of range", activeIndex)
		}
		active[activeIndex] = snapshotCodecUpdateJSON(t, active[activeIndex], mutate)
		updatedActive, err := json.Marshal(active)
		if err != nil {
			t.Fatalf("encode snapshot active capability fixture: %v", err)
		}
		node["active_capabilities"] = updatedActive
	})
}
func snapshotCodecUpdateReceipt(t *testing.T, raw []byte, nodeIndex, activeIndex int, mutate func(map[string]json.RawMessage)) []byte {
	t.Helper()
	return snapshotCodecUpdateActive(t, raw, nodeIndex, activeIndex, func(active map[string]json.RawMessage) {
		active["qualification"] = snapshotCodecUpdateJSON(t, active["qualification"], mutate)
	})
}
func snapshotCodecDuplicateRootField(raw []byte, name, value string) []byte {
	prefix := []byte("{\"" + name + "\":" + value + ",")
	return append(prefix, raw[1:]...)
}
func snapshotCodecDuplicateNodeField(raw []byte, nodeIndex int, name, value string) []byte {
	marker := []byte("\"nodes\":[{")
	if nodeIndex != 0 {
		return raw
	}
	index := bytes.Index(raw, marker)
	if index < 0 {
		return raw
	}
	insertAt := index + len(marker)
	duplicate := []byte("\"" + name + "\":" + value + ",")
	result := make([]byte, 0, len(raw)+len(duplicate))
	result = append(result, raw[:insertAt]...)
	result = append(result, duplicate...)
	result = append(result, raw[insertAt:]...)
	return result
}
