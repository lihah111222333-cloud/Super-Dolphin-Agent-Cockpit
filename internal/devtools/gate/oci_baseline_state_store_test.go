package gate_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestRemoteBaselineStateStoreCASRoundTripsOCIOnlyState(t *testing.T) {
	state := validOCIOnlyState()
	if err := state.Validate(); err != nil {
		t.Fatalf("OCI state validation error = %v", err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	store, err := gate.NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwapRemoteBaselineState(0, gate.RemoteBaselineStateRecord{Generation: state.Generation, StateJSON: data, StateSHA256: fmt.Sprintf("sha256:%x", sum)}); err != nil {
		t.Fatalf("CompareAndSwapRemoteBaselineState() error = %v", err)
	}
	record, err := store.LoadRemoteBaselineState()
	if err != nil {
		t.Fatal(err)
	}
	var loaded remoteci.BaselineState
	if err := json.Unmarshal(record.StateJSON, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.OCIProjectCache == nil || loaded.Validate() != nil {
		t.Fatalf("loaded OCI-only baseline state = %#v", loaded)
	}
}

func TestRemoteBaselineStateStoreCASRejectsStaleGeneration(t *testing.T) {
	state := validOCIOnlyState()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	store, err := gate.NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwapRemoteBaselineState(0, gate.RemoteBaselineStateRecord{Generation: state.Generation, StateJSON: data, StateSHA256: fmt.Sprintf("sha256:%x", sum)}); err != nil {
		t.Fatal(err)
	}
	state.Generation = 2
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256(data)
	if _, err := store.CompareAndSwapRemoteBaselineState(0, gate.RemoteBaselineStateRecord{Generation: state.Generation, StateJSON: data, StateSHA256: fmt.Sprintf("sha256:%x", sum)}); err == nil {
		t.Fatal("CompareAndSwapRemoteBaselineState() accepted stale generation")
	}
}

func validOCIOnlyState() remoteci.BaselineState {
	digest := func(value string) string { return "sha256:" + strings.Repeat(value, 64) }
	tree := strings.Repeat("a", 40)
	toolchain := digest("b")
	image := "registry.example/runtime@" + digest("c")
	created := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	return remoteci.BaselineState{
		SchemaVersion: remoteci.BaselineStateSchemaVersion, Generation: 1,
		MainCommit: strings.Repeat("d", 40), MainTree: tree, Platform: "linux/amd64",
		PolicyDigest: digest("e"), ToolchainDigest: toolchain, RuntimeImage: image,
		GateBinarySHA256: digest("f"), RuntimeSeedSHA256: digest("1"), BaselineManifestDigest: digest("2"),
		CreatedAt: created, AcceptedAt: created,
		OCIProjectCache: &remoteci.BaselineOCIProjectCache{Image: image, ContentManifestSHA256: digest("3"), MainTree: tree, ToolchainDigest: toolchain, Platform: "linux/amd64", CachePath: remoteci.OCIProjectGoBuildCachePath},
	}
}
