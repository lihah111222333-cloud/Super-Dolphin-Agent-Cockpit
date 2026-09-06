package archtest_test

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	taskDAGWriterInventoryPath = "cmd/mcp-orch/store/taskdag/writer_inventory.json"
	taskDAGWriterPackagePath   = "cmd/mcp-orch/store/taskdag"
)

type taskDAGWriterInventory struct {
	SchemaVersion int                           `json:"schema_version"`
	Status        string                        `json:"status"`
	Owner         string                        `json:"owner"`
	Entries       []taskDAGWriterInventoryEntry `json:"entries"`
}

type taskDAGWriterInventoryEntry struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	Receiver string `json:"receiver"`
	Symbol   string `json:"symbol"`
}

type taskDAGWriterMethod struct {
	File     string
	Receiver string
	Symbol   string
}

func TestTaskDAGWriterInventoryIsExactAndCandidateOnly(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	inventory := loadTaskDAGWriterInventory(t, root)
	validateTaskDAGWriterInventoryHeader(t, inventory)
	want := expectedTaskDAGWriterMethods(t, inventory)
	got := collectTaskDAGWriterMethods(t, root)
	assertTaskDAGWriterInventoryParity(t, want, got)
}

func validateTaskDAGWriterInventoryHeader(t *testing.T, inventory taskDAGWriterInventory) {
	t.Helper()
	if inventory.SchemaVersion != 1 {
		t.Fatalf("writer inventory schema_version=%d, want 1", inventory.SchemaVersion)
	}
	if inventory.Status != "candidate_not_wired" {
		t.Fatalf("writer inventory status=%q, want candidate_not_wired", inventory.Status)
	}
	if inventory.Owner != "taskdagwriterfence.WriterSession (future; not wired)" {
		t.Fatalf("writer inventory owner=%q, want explicit future non-wired owner", inventory.Owner)
	}
}

func expectedTaskDAGWriterMethods(t *testing.T, inventory taskDAGWriterInventory) map[string]taskDAGWriterMethod {
	t.Helper()
	if len(inventory.Entries) != 42 {
		t.Fatalf("writer inventory has %d entries, want 42", len(inventory.Entries))
	}
	want := make(map[string]taskDAGWriterMethod, len(inventory.Entries))
	for index, entry := range inventory.Entries {
		validateTaskDAGWriterInventoryEntry(t, index, entry)
		key := taskDAGWriterMethodKey(entry.File, entry.Receiver, entry.Symbol)
		if _, exists := want[key]; exists {
			t.Errorf("duplicate inventory entry %s", key)
		}
		want[key] = taskDAGWriterMethod{File: entry.File, Receiver: entry.Receiver, Symbol: entry.Symbol}
	}
	return want
}

func validateTaskDAGWriterInventoryEntry(t *testing.T, index int, entry taskDAGWriterInventoryEntry) {
	t.Helper()
	wantID := fmt.Sprintf("M%02d", index+1)
	if entry.ID != wantID {
		t.Errorf("inventory entry %d has id %q, want %q", index, entry.ID, wantID)
	}
	if entry.File == "" || entry.Receiver == "" || entry.Symbol == "" {
		t.Errorf("inventory entry %s has an empty location field", entry.ID)
	}
}

func assertTaskDAGWriterInventoryParity(t *testing.T, want, got map[string]taskDAGWriterMethod) {
	t.Helper()
	missing, unknown := taskDAGWriterInventoryDrift(want, got)
	if len(missing) > 0 || len(unknown) > 0 {
		t.Fatalf("taskdag writer inventory drift: missing=%v unknown=%v", missing, unknown)
	}
}

func taskDAGWriterInventoryDrift(want, got map[string]taskDAGWriterMethod) ([]string, []string) {
	missing := methodKeysAbsent(want, got)
	unknown := methodKeysAbsent(got, want)
	sort.Strings(missing)
	sort.Strings(unknown)
	return missing, unknown
}

func methodKeysAbsent(source, reference map[string]taskDAGWriterMethod) []string {
	var absent []string
	for key := range source {
		if _, exists := reference[key]; !exists {
			absent = append(absent, key)
		}
	}
	return absent
}

func loadTaskDAGWriterInventory(t *testing.T, root string) taskDAGWriterInventory {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(taskDAGWriterInventoryPath))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read taskdag writer inventory %s: %v", path, err)
	}
	var inventory taskDAGWriterInventory
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&inventory); err != nil {
		t.Fatalf("decode taskdag writer inventory %s: %v", path, err)
	}
	return inventory
}

func collectTaskDAGWriterMethods(t *testing.T, root string) map[string]taskDAGWriterMethod {
	t.Helper()
	packageDir := filepath.Join(root, filepath.FromSlash(taskDAGWriterPackagePath))
	entries, err := os.ReadDir(packageDir)
	if err != nil {
		t.Fatalf("read taskdag package %s: %v", packageDir, err)
	}
	got := make(map[string]taskDAGWriterMethod)
	for _, entry := range entries {
		name := entry.Name()
		if !isTaskDAGWriterProductionFile(entry) {
			continue
		}
		collectTaskDAGWriterFileMethods(t, root, packageDir, name, got)
	}
	return got
}

func isTaskDAGWriterProductionFile(entry os.DirEntry) bool {
	name := entry.Name()
	return !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

func collectTaskDAGWriterFileMethods(t *testing.T, root, packageDir, name string, got map[string]taskDAGWriterMethod) {
	t.Helper()
	path := filepath.Join(packageDir, name)
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse taskdag writer file %s: %v", path, err)
	}
	relPath, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("relativize taskdag writer file %s: %v", path, err)
	}
	for _, declaration := range file.Decls {
		method, ok := taskDAGWriterMethodFromDecl(filepath.ToSlash(relPath), declaration)
		if ok {
			got[taskDAGWriterMethodKey(method.File, method.Receiver, method.Symbol)] = method
		}
	}
}

func taskDAGWriterMethodFromDecl(file string, declaration ast.Decl) (taskDAGWriterMethod, bool) {
	function, ok := declaration.(*ast.FuncDecl)
	if !ok || function.Recv == nil || function.Name == nil || !isTaskDAGWriterMutationName(function.Name.Name) {
		return taskDAGWriterMethod{}, false
	}
	receiver, ok := taskDAGWriterReceiver(function.Recv)
	if !ok {
		return taskDAGWriterMethod{}, false
	}
	return taskDAGWriterMethod{File: file, Receiver: receiver, Symbol: function.Name.Name}, true
}

func isTaskDAGWriterMutationName(name string) bool {
	// The inventory is intentionally scoped to the 42 mutation entrypoints,
	// not every private SQL helper. The two private entrypoints are included
	// explicitly; helpers such as deleteDAGRow remain implementation details
	// until the later transitive call-graph slice.
	if name == "appendTaskDagRunEvent" || name == "scheduleRootWakeup" {
		return true
	}
	if name == "" || name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	for _, prefix := range []string{
		"upsert", "batch", "patch", "delete", "update", "assign", "mark", "bump", "bind",
		"touch", "complete", "claim", "fail", "record", "append", "create", "clone", "promote",
		"terminate", "enqueue", "renew", "retry", "reclaim", "schedule", "acquire", "release",
	} {
		if strings.HasPrefix(strings.ToLower(name), prefix) {
			return true
		}
	}
	return false
}

func taskDAGWriterReceiver(field *ast.FieldList) (string, bool) {
	if field == nil || len(field.List) != 1 {
		return "", false
	}
	expression := field.List[0].Type
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
		if identifier, ok := expression.(*ast.Ident); ok {
			receiver := "*" + identifier.Name
			return receiver, isTaskDAGWriterReceiver(receiver)
		}
		return "", false
	}
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return "", false
	}
	receiver := identifier.Name
	return receiver, isTaskDAGWriterReceiver(receiver)
}

func isTaskDAGWriterReceiver(receiver string) bool {
	switch receiver {
	case "*store", "*dagCommandStore", "*dagOutputStore", "*wakeupCommandStore", "*wakeupLeaseStore":
		return true
	default:
		return false
	}
}

func taskDAGWriterMethodKey(file, receiver, symbol string) string {
	return file + ":" + receiver + "." + symbol
}
