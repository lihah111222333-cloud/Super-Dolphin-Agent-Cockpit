package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUIMemoryHealthSimilarityDegradedJSONRoundTripUsesStructTag(t *testing.T) {
	field, ok := reflect.TypeFor[UIMemoryHealth]().FieldByName("SimilarityDegraded")
	if !ok {
		t.Fatal("UIMemoryHealth.SimilarityDegraded field is missing")
	}
	jsonField, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if jsonField != "similarityDegraded" {
		t.Fatalf("SimilarityDegraded json field = %q, want similarityDegraded", jsonField)
	}
	raw, err := json.Marshal(UIMemoryHealth{SimilarityDegraded: true})
	if err != nil {
		t.Fatalf("Marshal(UIMemoryHealth) error = %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("Unmarshal(wire) error = %v", err)
	}
	if wire[jsonField] != true {
		t.Fatalf("wire[%q] = %#v, want true", jsonField, wire[jsonField])
	}
	var roundTrip UIMemoryHealth
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("Unmarshal(UIMemoryHealth) error = %v", err)
	}
	if !roundTrip.SimilarityDegraded {
		t.Fatal("roundTrip.SimilarityDegraded = false, want true")
	}
}

func writeUIMemoryScanEntry(t *testing.T, root string, index int, body string) {
	t.Helper()
	path := filepath.Join(root, string(MemoryTypeProject), fmt.Sprintf("scan-entry-%03d.md", index))
	writeTestTopicFile(t, path, testMemoryEntry(fmt.Sprintf("Scan Entry %03d", index), "scan budget fixture", MemoryTypeProject, body))
}

func assertUIMemoryScanReason(t *testing.T, snapshot UIMemorySnapshot, want string) {
	t.Helper()
	scan := uiMemoryOverviewScanMap(t, snapshot)
	if got, _ := scan["reason"].(string); got != want {
		t.Fatalf("overview.scan.reason = %q, want %q (scan=%#v)", got, want, scan)
	}
	if want == "memory_scan_truncated" && scan["truncated"] != true {
		t.Fatalf("overview.scan = %#v, want truncated=true", scan)
	}
	if want == "memory_scan_canceled" && scan["canceled"] != true {
		t.Fatalf("overview.scan = %#v, want canceled=true", scan)
	}
}

func uiMemoryOverviewScanMap(t *testing.T, snapshot UIMemorySnapshot) map[string]any {
	t.Helper()
	raw, err := json.Marshal(snapshot.Overview)
	if err != nil {
		t.Fatalf("Marshal(overview) error = %v", err)
	}
	var overview map[string]any
	if err := json.Unmarshal(raw, &overview); err != nil {
		t.Fatalf("Unmarshal(overview) error = %v", err)
	}
	scan, ok := overview["scan"].(map[string]any)
	if !ok {
		t.Fatalf("overview.scan missing from wire snapshot: %#v", overview)
	}
	return scan
}

func uniqueTokenRun(prefix string, count int) string {
	parts := make([]string, 0, count)
	for i := range count {
		parts = append(parts, fmt.Sprintf("%s%03d", prefix, i))
	}
	return strings.Join(parts, " ")
}

func TestReadUIMemoryEntryByPathRejectsEntrypointIndex(t *testing.T) {
	root := t.TempDir()
	writeTestTopicFile(t, filepath.Join(root, string(MemoryTypeProject), "actual.md"), testMemoryEntry("Actual Entry", "actual", MemoryTypeProject, "Actual body.\nWhy: valid topic files should still load.\nHow to apply: read the topic path."))
	if _, err := UpdateMemoryIndex(root); err != nil {
		t.Fatalf("UpdateMemoryIndex() error = %v", err)
	}

	if _, _, err := readUIMemoryEntryByPath(root, "private", memoryIndexFileName); !errors.Is(err, ErrInvalidMemoryReadPath) {
		t.Fatalf("readUIMemoryEntryByPath(MEMORY.md) error = %v, want %v", err, ErrInvalidMemoryReadPath)
	}
}

func TestDeleteUIMemoryEntryRejectsEntrypointIndex(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
	}
	entryPath := filepath.Join(privateRoot, string(MemoryTypeProject), "actual.md")
	writeTestTopicFile(t, entryPath, testMemoryEntry("Actual Entry", "actual", MemoryTypeProject, "Actual body.\nWhy: valid topic files should survive rejected index deletion.\nHow to apply: reject MEMORY.md deletes from UI."))
	if _, err := UpdateMemoryIndex(privateRoot); err != nil {
		t.Fatalf("UpdateMemoryIndex() error = %v", err)
	}

	err := deleteUIMemoryEntry(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryDeleteParams{
		CWD:    projectRoot,
		Target: "private",
		Path:   memoryIndexFileName,
	})
	if !errors.Is(err, errDurableMemoryDeleteFailed) {
		t.Fatalf("deleteUIMemoryEntry(MEMORY.md) error = %v, want %v", err, errDurableMemoryDeleteFailed)
	}
	if _, err := os.Stat(memoryIndexPath(privateRoot)); err != nil {
		t.Fatalf("MEMORY.md was removed or inaccessible after rejected delete: %v", err)
	}
	if _, err := readMemoryEntryFile(entryPath); err != nil {
		t.Fatalf("topic entry was removed after rejected delete: %v", err)
	}
}
