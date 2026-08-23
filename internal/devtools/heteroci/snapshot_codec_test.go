package heteroci

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestDecodeSnapshotSetRoundTripsUbuntuWineSnapshot(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestCapabilityManifest(testUbuntuCapability(), testWineCapability())
	mustSnapshotManifestValidate(t, manifest)
	snapshot := snapshotForManifest(manifest, now)
	snapshot.OwnerEpoch = 7
	raw := mustMarshalSnapshotSet(t, snapshot)
	decoded, err := DecodeSnapshotSet(bytes.NewReader(raw), manifest, now, snapshotTestHeartbeatTTL)
	if err != nil {
		t.Fatalf("DecodeSnapshotSet(valid snapshot): %v", err)
	}
	if !reflect.DeepEqual(decoded, snapshot) {
		t.Fatalf("decoded snapshot differs from source:\n got %#v\nwant %#v", decoded, snapshot)
	}
	if err := decoded.ValidateAgainst(manifest, now, snapshotTestHeartbeatTTL); err != nil {
		t.Fatalf("decoded snapshot ValidateAgainst: %v", err)
	}
}

func TestDecodeSnapshotSetRejectsStrictJSONShape(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestManifest(1)
	mustSnapshotManifestValidate(t, manifest)
	valid := mustMarshalSnapshotSet(t, snapshotForManifest(manifest, now))
	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "unknown root field", raw: snapshotCodecUpdateJSON(t, valid, func(root map[string]json.RawMessage) { root["unknown_root"] = json.RawMessage(`true`) })},
		{name: "unknown node field", raw: snapshotCodecUpdateNode(t, valid, 0, func(node map[string]json.RawMessage) { node["unknown_node"] = json.RawMessage(`true`) })},
		{name: "null root", raw: []byte("null")},
		{name: "null active capability", raw: snapshotCodecUpdateActive(t, valid, 0, 0, func(active map[string]json.RawMessage) { active["runtime"] = json.RawMessage(`null`) })},
		{name: "null nodes", raw: snapshotCodecUpdateJSON(t, valid, func(root map[string]json.RawMessage) { root["nodes"] = json.RawMessage(`null`) })},
		{name: "duplicate root key", raw: snapshotCodecDuplicateRootField(valid, "authority", `"LOCAL_NON_AUTHORITATIVE"`)},
		{name: "trailing second value", raw: append(append([]byte(nil), valid...), []byte(` {}`)...)},
		{name: "missing schema", raw: snapshotCodecUpdateJSON(t, valid, func(root map[string]json.RawMessage) { delete(root, "schema_version") })},
		{name: "missing nodes", raw: snapshotCodecUpdateJSON(t, valid, func(root map[string]json.RawMessage) { delete(root, "nodes") })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeSnapshotSet(bytes.NewReader(tt.raw), manifest, now, snapshotTestHeartbeatTTL); err == nil {
				t.Fatalf("DecodeSnapshotSet unexpectedly accepted %s", tt.name)
			}
		})
	}
}

func TestDecodeSnapshotSetRejectsMissingCanonicalFields(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestManifest(1)
	mustSnapshotManifestValidate(t, manifest)
	snapshot := snapshotForManifest(manifest, now)
	valid := mustMarshalSnapshotSet(t, snapshot)
	rootFields := []string{"schema_version", "authority", "owner_epoch", "capacity_basis", "captured_at_unix_milli", "nodes"}
	for _, field := range rootFields {
		t.Run("root_missing_"+field, func(t *testing.T) {
			raw := snapshotCodecUpdateJSON(t, valid, func(root map[string]json.RawMessage) { delete(root, field) })
			assertSnapshotDecodeRejects(t, raw, manifest, now)
		})
	}
	nodeFields := []string{"node_id", "endpoint", "platform", "probe_identity", "probe_boot_id", "active_capabilities", "status", "heartbeat_unix_milli", "total", "available"}
	for _, field := range nodeFields {
		t.Run("node_missing_"+field, func(t *testing.T) {
			raw := snapshotCodecUpdateNode(t, valid, 0, func(node map[string]json.RawMessage) { delete(node, field) })
			assertSnapshotDecodeRejects(t, raw, manifest, now)
		})
	}
	for _, field := range []string{"runtime", "image_digest"} {
		t.Run("active_missing_"+field, func(t *testing.T) {
			raw := snapshotCodecUpdateActive(t, valid, 0, 0, func(active map[string]json.RawMessage) { delete(active, field) })
			assertSnapshotDecodeRejects(t, raw, manifest, now)
		})
	}
}

func TestDecodeSnapshotSetRejectsStaleFutureAndMismatchedManifest(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestManifest(1)
	mustSnapshotManifestValidate(t, manifest)
	valid := snapshotForManifest(manifest, now)
	for _, test := range []struct {
		name   string
		mutate func(*SnapshotSet)
		now    time.Time
	}{
		{name: "stale capture", mutate: func(snapshot *SnapshotSet) {
			snapshot.CapturedAtUnixMilli -= snapshotTestHeartbeatTTL.Milliseconds() + 1
		}, now: now},
		{name: "future capture", mutate: func(snapshot *SnapshotSet) { snapshot.CapturedAtUnixMilli = now.Add(time.Second).UnixMilli() }, now: now},
		{name: "future heartbeat", mutate: func(snapshot *SnapshotSet) { snapshot.Nodes[0].HeartbeatUnixMilli = now.Add(time.Second).UnixMilli() }, now: now},
		{name: "negative capacity", mutate: func(snapshot *SnapshotSet) { snapshot.Nodes[0].Available.CPUMilli = -1 }, now: now},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneSnapshotSet(valid)
			test.mutate(&candidate)
			assertSnapshotDecodeRejects(t, mustMarshalSnapshotSet(t, candidate), manifest, test.now)
		})
	}
	mismatched := snapshotTestManifest(2)
	if _, err := DecodeSnapshotSet(bytes.NewReader(mustMarshalSnapshotSet(t, valid)), mismatched, now, snapshotTestHeartbeatTTL); err == nil {
		t.Fatal("DecodeSnapshotSet unexpectedly accepted mismatched manifest")
	}
}

func TestDecodeSnapshotSetOfflineRoundTrip(t *testing.T) {
	now := snapshotTestNow()
	manifest := snapshotTestManifest(1)
	snapshot := snapshotForManifest(manifest, now)
	snapshot = snapshotTestOffline(snapshot)
	raw := mustMarshalSnapshotSet(t, snapshot)
	decoded, err := DecodeSnapshotSet(bytes.NewReader(raw), manifest, now, snapshotTestHeartbeatTTL)
	if err != nil {
		t.Fatalf("DecodeSnapshotSet(offline): %v", err)
	}
	if !reflect.DeepEqual(decoded, snapshot) {
		t.Fatalf("offline decoded snapshot differs: got %#v want %#v", decoded, snapshot)
	}
}

func mustMarshalSnapshotSet(t *testing.T, snapshot SnapshotSet) []byte {
	t.Helper()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	return raw
}

func assertSnapshotDecodeRejects(t *testing.T, raw []byte, manifest Manifest, now time.Time) {
	t.Helper()
	if decoded, err := DecodeSnapshotSet(bytes.NewReader(raw), manifest, now, snapshotTestHeartbeatTTL); err == nil {
		t.Fatalf("DecodeSnapshotSet unexpectedly accepted invalid snapshot: %#v", decoded)
	}
}

func snapshotCodecUpdateJSON(t *testing.T, raw []byte, mutate func(map[string]json.RawMessage)) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode snapshot JSON: %v", err)
	}
	mutate(object)
	updated, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("encode snapshot JSON: %v", err)
	}
	return updated
}

func snapshotCodecUpdateNode(t *testing.T, raw []byte, nodeIndex int, mutate func(map[string]json.RawMessage)) []byte {
	t.Helper()
	return snapshotCodecUpdateJSON(t, raw, func(root map[string]json.RawMessage) {
		var nodes []json.RawMessage
		if err := json.Unmarshal(root["nodes"], &nodes); err != nil || nodeIndex < 0 || nodeIndex >= len(nodes) {
			t.Fatalf("decode snapshot nodes: %v", err)
		}
		var node map[string]json.RawMessage
		if err := json.Unmarshal(nodes[nodeIndex], &node); err != nil {
			t.Fatalf("decode snapshot node: %v", err)
		}
		mutate(node)
		updated, err := json.Marshal(node)
		if err != nil {
			t.Fatalf("encode snapshot node: %v", err)
		}
		nodes[nodeIndex] = updated
		root["nodes"], err = json.Marshal(nodes)
		if err != nil {
			t.Fatalf("encode snapshot nodes: %v", err)
		}
	})
}

func snapshotCodecUpdateActive(t *testing.T, raw []byte, nodeIndex, activeIndex int, mutate func(map[string]json.RawMessage)) []byte {
	t.Helper()
	return snapshotCodecUpdateNode(t, raw, nodeIndex, func(node map[string]json.RawMessage) {
		var active []json.RawMessage
		if err := json.Unmarshal(node["active_capabilities"], &active); err != nil || activeIndex < 0 || activeIndex >= len(active) {
			t.Fatalf("decode active capabilities: %v", err)
		}
		var capability map[string]json.RawMessage
		if err := json.Unmarshal(active[activeIndex], &capability); err != nil {
			t.Fatalf("decode active capability: %v", err)
		}
		mutate(capability)
		updated, err := json.Marshal(capability)
		if err != nil {
			t.Fatalf("encode active capability: %v", err)
		}
		active[activeIndex] = updated
		node["active_capabilities"], err = json.Marshal(active)
		if err != nil {
			t.Fatalf("encode active capabilities: %v", err)
		}
	})
}

func snapshotCodecDuplicateRootField(raw []byte, name, value string) []byte {
	needle := []byte(`"` + name + `":`)
	index := bytes.Index(raw, needle)
	if index < 0 {
		return raw
	}
	start := index + len(needle)
	end := start
	for end < len(raw) && raw[end] != ',' && raw[end] != '}' {
		end++
	}
	duplicate := make([]byte, 0, len(raw)+len(needle)+len(value)+1)
	duplicate = append(duplicate, raw[:end]...)
	duplicate = append(duplicate, ',')
	duplicate = append(duplicate, needle...)
	duplicate = append(duplicate, value...)
	duplicate = append(duplicate, raw[end:]...)
	return duplicate
}
