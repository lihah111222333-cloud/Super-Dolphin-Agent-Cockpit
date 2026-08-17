package remoteci

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

func TestMcpLSPCatalogGuardFingerprintTracksCanonicalInputs(t *testing.T) {
	entries := []remoteGitTreeEntry{
		{mode: "100644", kind: "blob", objectID: "make-v1", path: "Makefile"},
		{mode: "100755", kind: "blob", objectID: "script-v1", path: "scripts/check_mcp_lsp_workload_catalog.sh"},
		{mode: "100755", kind: "blob", objectID: "runner-script-v1", path: "scripts/run_mcp_lsp_workload.sh"},
		{mode: "100644", kind: "blob", objectID: "runner-main-v1", path: "scripts/mcp_lsp_workload_runner/main.go"},
		{mode: "100644", kind: "blob", objectID: "runner-other-v1", path: "scripts/mcp_lsp_workload_runner/platform_other.go"},
		{mode: "100644", kind: "blob", objectID: "runner-windows-v1", path: "scripts/mcp_lsp_workload_runner/platform_windows.go"},
		{mode: "100644", kind: "blob", objectID: "catalog-v1", path: mcpLSPQuickWorkloadCatalogPath},
		{mode: "100644", kind: "blob", objectID: "quick-test-v1", path: "cmd/mcp-lsp/quick/quick_test.go"},
		{mode: "100644", kind: "blob", objectID: "quick-source-v1", path: "cmd/mcp-lsp/quick/quick.go"},
		{mode: "100644", kind: "blob", objectID: "native-test-v1", path: "cmd/mcp-lsp/native/native_test.go"},
		{mode: "100644", kind: "blob", objectID: "test-cli-v1", path: "cmd/super-dolphin-gate/test_cli.go"},
		{mode: "100644", kind: "blob", objectID: "remote-token-v1", path: "cmd/super-dolphin-gate/remote_agent_token.go"},
		{mode: "100644", kind: "blob", objectID: "remote-options-v1", path: "cmd/super-dolphin-gate/remote_run_options.go"},
		{mode: "100644", kind: "blob", objectID: "owned-v1", path: "scripts/ai_maintenance/owned_gate_execution.go"},
		{mode: "100644", kind: "blob", objectID: "executor-mapping-v1", path: "internal/devtools/gate/executor_mapping.go"},
		{mode: "100644", kind: "blob", objectID: "executor-v1", path: "internal/devtools/gate/executor.go"},
		{mode: "100644", kind: "blob", objectID: "executor-tree-v1", path: "internal/devtools/gate/executor_catalog_tree.go"},
		{mode: "100644", kind: "blob", objectID: "inventory-v1", path: "internal/devtools/remoteci/go_test_inventory.go"},
		{mode: "100644", kind: "blob", objectID: "fingerprint-v1", path: "internal/devtools/remoteci/workload_fingerprint_digests.go"},
		{mode: "100644", kind: "blob", objectID: "producer-v1", path: ".github/workflows/ci.yml"},
		{mode: "100644", kind: "blob", objectID: "go-mod-v1", path: "go.mod"},
		{mode: "100644", kind: "blob", objectID: "go-sum-v1", path: "go.sum"},
		{mode: "100644", kind: "blob", objectID: "workflow-v1", path: ".github/workflows/unrelated.yml"},
	}
	entries = d1AppendCatalogConsumerEntries(entries)
	snapshot := &remoteGitTreeSnapshot{
		entries: entries,
		byPath:  d1FingerprintEntriesByPath(entries),
		gitBlobContents: map[string][]byte{
			"catalog-v1":      d1FingerprintCatalogRaw(t, "./cmd/mcp-lsp/quick"),
			"quick-test-v1":   []byte("package quick\n\nimport \"testing\"\nfunc TestQuick(t *testing.T) {}\n"),
			"quick-source-v1": []byte("package quick\n\nfunc ProductionQuick() {}\n"),
			"native-test-v1":  []byte("package native\n\nimport \"testing\"\nfunc TestNative(t *testing.T) {}\n"),
		},
	}
	id := string(gate.GateIDBackendTestWithGuard) + "::go-guard::" +
		base64.RawURLEncoding.EncodeToString([]byte(gate.GoGuardTargetMcpLSPWorkloadCatalog))
	workload := gate.Workload{ID: id}
	baseline, err := snapshot.workloadInputDigest(context.Background(), workload)
	if err != nil {
		t.Fatal(err)
	}
	d1AssertFingerprintMutation(t, snapshot, entries, workload, baseline, true, ".github/workflows/unrelated.yml", "workflow-v2")
	d1AssertFingerprintMutation(t, snapshot, entries, workload, baseline, false, "cmd/mcp-lsp/quick/quick_test.go", "quick-test-v2")
	d1AssertFingerprintMutation(t, snapshot, entries, workload, baseline, false, "cmd/mcp-lsp/quick/quick.go", "quick-source-v2")
	d1AssertFingerprintMutation(t, snapshot, entries, workload, baseline, false, "cmd/mcp-lsp/native/native_test.go", "native-test-v2")
	d1AssertFingerprintMutation(t, snapshot, entries, workload, baseline, false, "scripts/ai_maintenance/owned_gate_execution.go", "owned-v2")
	d1AssertFingerprintMutation(t, snapshot, entries, workload, baseline, false, "cmd/super-dolphin-gate/remote_agent_token.go", "remote-token-v2")
	d1AssertFingerprintMutation(t, snapshot, entries, workload, baseline, false, "cmd/super-dolphin-gate/remote_run_options.go", "remote-options-v2")
	d1AssertFingerprintMutation(t, snapshot, entries, workload, baseline, false, "internal/devtools/gate/executor_mapping.go", "executor-mapping-v2")
	d1AssertFingerprintMutation(t, snapshot, entries, workload, baseline, false, "internal/devtools/gate/executor.go", "executor-v2")
	d1AssertFingerprintMutation(t, snapshot, entries, workload, baseline, false, "internal/devtools/gate/executor_catalog_tree.go", "executor-tree-v2")
	d1AssertFingerprintMutation(t, snapshot, entries, workload, baseline, false, "internal/devtools/mcplspguardcore/guard.go", "guard-core-v2")
	d1AssertFingerprintMutation(t, snapshot, entries, workload, baseline, false, ".github/workflows/ci.yml", "producer-v2")
	d1AssertFingerprintMutation(t, snapshot, entries, workload, baseline, false, "go.mod", "go-mod-v2")
	d1AssertFingerprintMutation(t, snapshot, entries, workload, baseline, false, "go.sum", "go-sum-v2")
	d1AssertFingerprintDeletion(t, snapshot, entries, workload, "scripts/ai_maintenance/owned_gate_execution.go")
	d1AssertFingerprintDeletion(t, snapshot, entries, workload, "internal/devtools/gate/executor_catalog_tree.go")

	snapshot.gitBlobContents["catalog-bad"] = d1FingerprintCatalogRaw(t, "./cmd/mcp-lsp/internal/hiddenexec/.")
	snapshot.entries = append([]remoteGitTreeEntry(nil), entries...)
	d1SetFingerprintObjectID(t, snapshot.entries, mcpLSPQuickWorkloadCatalogPath, "catalog-bad")
	snapshot.byPath = d1FingerprintEntriesByPath(snapshot.entries)
	if _, err := snapshot.workloadInputDigest(context.Background(), workload); err == nil {
		t.Fatal("fingerprint accepted non-canonical catalog package path")
	}
}

func d1AppendCatalogConsumerEntries(entries []remoteGitTreeEntry) []remoteGitTreeEntry {
	registered := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		registered[entry.path] = struct{}{}
	}
	for _, path := range catalog.WorkloadConsumerPaths() {
		if _, ok := registered[path]; ok {
			continue
		}
		entries = append(entries, remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: "consumer-v1-" + strings.ReplaceAll(path, "/", "-"), path: path})
	}
	return entries
}

func d1AssertFingerprintMutation(t *testing.T, snapshot *remoteGitTreeSnapshot, entries []remoteGitTreeEntry, workload gate.Workload, baseline string, wantSame bool, path, objectID string) {
	t.Helper()
	snapshot.entries = append([]remoteGitTreeEntry(nil), entries...)
	for _, entry := range entries {
		if entry.path == path {
			if raw, ok := snapshot.gitBlobContents[entry.objectID]; ok {
				snapshot.gitBlobContents[objectID] = append([]byte(nil), raw...)
			}
			break
		}
	}
	d1SetFingerprintObjectID(t, snapshot.entries, path, objectID)
	snapshot.byPath = d1FingerprintEntriesByPath(snapshot.entries)
	got, err := snapshot.workloadInputDigest(context.Background(), workload)
	if err != nil {
		t.Fatal(err)
	}
	if (got == baseline) != wantSame {
		t.Fatalf("fingerprint equality = %t, want %t", got == baseline, wantSame)
	}
}

func d1FingerprintCatalogRaw(t *testing.T, packagePath string) []byte {
	t.Helper()
	document := catalog.Catalog{
		Schema:        catalog.Schema,
		CatalogDigest: "sha256:" + strings.Repeat("0", 64),
		Workloads: []catalog.Workload{
			{ID: "mcp-lsp-idle-quick", ImplementationStatus: "implemented", ProducerWorkflowPath: ".github/workflows/ci.yml", Command: []string{"go", "test", packagePath, "-run", "^TestQuick$"}},
			{ID: "mcp-lsp-native-process-tree", ImplementationStatus: "implemented", ProducerWorkflowPath: ".github/workflows/ci.yml", Command: []string{"go", "test", "./cmd/mcp-lsp/native", "-run", "^TestNative$"}},
		},
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	document.CatalogDigest, err = catalog.CanonicalDigest(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func d1AssertFingerprintDeletion(t *testing.T, snapshot *remoteGitTreeSnapshot, entries []remoteGitTreeEntry, workload gate.Workload, path string) {
	t.Helper()
	without := make([]remoteGitTreeEntry, 0, len(entries)-1)
	for _, entry := range entries {
		if entry.path != path {
			without = append(without, entry)
		}
	}
	snapshot.entries = without
	snapshot.byPath = d1FingerprintEntriesByPath(without)
	if _, err := snapshot.workloadInputDigest(context.Background(), workload); err == nil {
		t.Fatalf("fingerprint accepted deleted required input %q", path)
	}
}

func d1SetFingerprintObjectID(t *testing.T, entries []remoteGitTreeEntry, path, objectID string) {
	t.Helper()
	for index := range entries {
		if entries[index].path == path {
			entries[index].objectID = objectID
			return
		}
	}
	t.Fatalf("fingerprint path %q not found", path)
}

func d1FingerprintEntriesByPath(entries []remoteGitTreeEntry) map[string]remoteGitTreeEntry {
	indexed := make(map[string]remoteGitTreeEntry, len(entries))
	for _, entry := range entries {
		indexed[entry.path] = entry
	}
	return indexed
}
