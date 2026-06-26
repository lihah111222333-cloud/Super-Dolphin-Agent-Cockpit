package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestMergeUIMemoryEntriesUsesTeamGuardWhenKeptEntryIsTeam(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	teamRoot, err := configuredTeamMemRoot(cfg, contract.BuildCtx{CWD: projectRoot})
	if err != nil {
		t.Fatalf("configuredTeamMemRoot() error = %v", err)
	}

	keptPath := filepath.Join(teamRoot, string(MemoryTypeProject), "team-kept.md")
	absorbedPath := filepath.Join(privateRoot, string(MemoryTypeProject), "private-absorbed.md")
	secret := "sk-proj-abcdefghijklmnopqrstuvwxyz1234567890"
	commonBody := "Shared team merge guard content common phrase common phrase common phrase.\nWhy: merge validation should pass before guarded team write.\nHow to apply: reject merged content containing secrets."
	keptBody := commonBody + "\nTeam kept clean marker."
	absorbedBody := commonBody + "\nPrivate absorbed marker includes token " + secret + "."

	writeTestTopicFile(t, keptPath, testMemoryEntry("Team Guard Kept", "team kept", MemoryTypeProject, keptBody))
	writeTestTopicFile(t, absorbedPath, testMemoryEntry("Private Guard Absorbed", "private absorbed", MemoryTypeProject, absorbedBody))
	if _, err := UpdateMemoryIndex(teamRoot); err != nil {
		t.Fatalf("UpdateMemoryIndex(teamRoot) error = %v", err)
	}
	if _, err := UpdateMemoryIndex(privateRoot); err != nil {
		t.Fatalf("UpdateMemoryIndex(privateRoot) error = %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{}))
	_, err = mergeUIMemoryEntries(context.Background(), memoryHandlerDeps{
		Service: newServiceWithConsolidator(cfg, nil, nil, nil),
		Logger:  logger,
	}, uiMemoryEntryMergeParams{
		CWD:     projectRoot,
		TargetA: "team",
		PathA:   memoryEntryDisplayPath(teamRoot, keptPath),
		TargetB: "private",
		PathB:   memoryEntryDisplayPath(privateRoot, absorbedPath),
	})
	if !errors.Is(err, errDurableMemorySaveFailed) {
		t.Fatalf("mergeUIMemoryEntries() error = %v, want %v", err, errDurableMemorySaveFailed)
	}
	if !strings.Contains(logBuf.String(), ErrTeamMemSecretDetected.Error()) {
		t.Fatalf("merge log = %q, want TeamMemoryGuard rejection %q", logBuf.String(), ErrTeamMemSecretDetected.Error())
	}
	kept, readErr := readMemoryEntryFile(keptPath)
	if readErr != nil {
		t.Fatalf("read kept after rejected merge error = %v", readErr)
	}
	if kept.Content != keptBody {
		t.Fatalf("team kept content changed after rejected merge:\n%s", kept.Content)
	}
	if strings.Contains(kept.Content, secret) {
		t.Fatalf("team kept content contains secret after rejected merge:\n%s", kept.Content)
	}
	if _, err := os.Stat(absorbedPath); err != nil {
		t.Fatalf("absorbed private entry was deleted after rejected team write: %v", err)
	}
}

func TestRollbackMergedEntryUsesTeamGuardWhenTargetIsTeam(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	teamRoot, err := configuredTeamMemRoot(cfg, contract.BuildCtx{CWD: projectRoot})
	if err != nil {
		t.Fatalf("configuredTeamMemRoot() error = %v", err)
	}
	keptPath := filepath.Join(teamRoot, string(MemoryTypeProject), "team-kept.md")
	cleanBody := "Clean rollback placeholder.\nWhy: the existing team file should remain unchanged.\nHow to apply: reject unsafe rollback writes."
	writeTestTopicFile(t, keptPath, testMemoryEntry("Team Rollback Kept", "team kept", MemoryTypeProject, cleanBody))

	unsafeRollback := testMemoryEntry("Team Rollback Kept", "team kept", MemoryTypeProject,
		"Unsafe rollback body.\nWhy: rollback must still use TeamMemoryGuard.\nHow to apply: reject token sk-proj-abcdefghijklmnopqrstuvwxyz1234567890.")
	err = rollbackMergedEntry(newServiceWithConsolidator(cfg, nil, nil, nil), teamRoot, "team", memoryEntryDisplayPath(teamRoot, keptPath), unsafeRollback)

	if !errors.Is(err, ErrTeamMemSecretDetected) {
		t.Fatalf("rollbackMergedEntry(team) error = %v, want %v", err, ErrTeamMemSecretDetected)
	}
	kept, readErr := readMemoryEntryFile(keptPath)
	if readErr != nil {
		t.Fatalf("read kept after rejected rollback error = %v", readErr)
	}
	if kept.Content != cleanBody {
		t.Fatalf("team kept content changed after rejected rollback:\n%s", kept.Content)
	}
}

func TestMergeUIMemoryEntriesRollsBackKeptEntryWhenDeleteAbsorbedFails(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
	teamRoot, err := configuredTeamMemRoot(cfg, contract.BuildCtx{CWD: projectRoot})
	if err != nil {
		t.Fatalf("configuredTeamMemRoot() error = %v", err)
	}
	keptPath := filepath.Join(privateRoot, string(MemoryTypeProject), "kept.md")
	absorbedPath := filepath.Join(teamRoot, string(MemoryTypeProject), "absorbed.md")
	commonBody := "Shared rollback content common phrase common phrase common phrase.\nWhy: merge validation should pass before the delete step.\nHow to apply: rollback the kept write on merge failure."
	keptBody := commonBody + "\nKept original marker."
	absorbedBody := commonBody + "\nAbsorbed unique marker."

	writeTestTopicFile(t, keptPath, testMemoryEntry("Rollback Kept", "kept", MemoryTypeProject, keptBody))
	writeTestTopicFile(t, absorbedPath, testMemoryEntry("Rollback Absorbed", "absorbed", MemoryTypeProject, absorbedBody))
	if _, err := UpdateMemoryIndex(privateRoot); err != nil {
		t.Fatalf("UpdateMemoryIndex(privateRoot) error = %v", err)
	}
	if _, err := UpdateMemoryIndex(teamRoot); err != nil {
		t.Fatalf("UpdateMemoryIndex(teamRoot) error = %v", err)
	}
	fixture := mergeRollbackDeleteFailureFixture{
		cfg:                  cfg,
		projectRoot:          projectRoot,
		privateRoot:          privateRoot,
		teamRoot:             teamRoot,
		keptPath:             keptPath,
		absorbedPath:         absorbedPath,
		keptBody:             keptBody,
		absorbedBody:         absorbedBody,
		originalPrivateIndex: readIndexEntries(t, privateRoot),
		originalTeamIndex:    readIndexEntries(t, teamRoot),
	}
	makeAbsorbedEntryDeleteFail(t, absorbedPath)

	_, err = mergeUIMemoryEntries(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryMergeParams{
		CWD:     projectRoot,
		TargetA: "private",
		PathA:   memoryEntryDisplayPath(privateRoot, keptPath),
		TargetB: "team",
		PathB:   memoryEntryDisplayPath(teamRoot, absorbedPath),
	})
	if err == nil {
		t.Fatal("mergeUIMemoryEntries() error = nil, want delete failure")
	}
	assertRollbackFailureEntriesPreserved(t, fixture)
	assertRollbackFailureIndexesPreserved(t, fixture)
}

type mergeRollbackDeleteFailureFixture struct {
	cfg                  *Config
	projectRoot          string
	privateRoot          string
	teamRoot             string
	keptPath             string
	absorbedPath         string
	keptBody             string
	absorbedBody         string
	originalPrivateIndex []MemoryIndexEntry
	originalTeamIndex    []MemoryIndexEntry
}

func assertRollbackFailureEntriesPreserved(t *testing.T, fixture mergeRollbackDeleteFailureFixture) {
	t.Helper()
	kept, readErr := readMemoryEntryFile(fixture.keptPath)
	if readErr != nil {
		t.Fatalf("read kept after failed merge error = %v", readErr)
	}
	if kept.Content != fixture.keptBody {
		t.Fatalf("kept content changed after failed merge:\n%s", kept.Content)
	}
	if strings.Contains(kept.Content, "Absorbed unique marker") {
		t.Fatalf("kept content contains absorbed marker after failed merge:\n%s", kept.Content)
	}
	absorbed, readErr := readMemoryEntryFile(fixture.absorbedPath)
	if readErr != nil {
		t.Fatalf("read absorbed after failed merge error = %v", readErr)
	}
	if absorbed.Content != fixture.absorbedBody {
		t.Fatalf("absorbed content changed after failed merge:\n%s", absorbed.Content)
	}
}

func assertRollbackFailureIndexesPreserved(t *testing.T, fixture mergeRollbackDeleteFailureFixture) {
	t.Helper()
	privateIndex := readIndexEntries(t, fixture.privateRoot)
	if !reflect.DeepEqual(privateIndex, fixture.originalPrivateIndex) {
		t.Fatalf("private index after failed merge = %#v, want original %#v", privateIndex, fixture.originalPrivateIndex)
	}
	teamIndex := readIndexEntries(t, fixture.teamRoot)
	if !reflect.DeepEqual(teamIndex, fixture.originalTeamIndex) {
		t.Fatalf("team index after failed merge = %#v, want original %#v", teamIndex, fixture.originalTeamIndex)
	}
}

func TestMergeUIMemoryEntriesUpdatesAndDeletesRequestedPathsWhenDuplicateNamesExist(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
	}
	paths := writeDuplicateMergeEntries(t, privateRoot)
	setDuplicateMergeTimes(t, paths)

	merged, err := mergeUIMemoryEntries(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryMergeParams{
		CWD:     projectRoot,
		TargetA: "private",
		PathA:   memoryEntryDisplayPath(privateRoot, paths.keptOld),
		TargetB: "private",
		PathB:   memoryEntryDisplayPath(privateRoot, paths.absorbedOld),
	})
	if err != nil {
		t.Fatalf("mergeUIMemoryEntries() error = %v", err)
	}
	assertDuplicateMergeResult(t, privateRoot, paths, merged)
}

type duplicateMergePaths struct {
	keptOld     string
	keptNew     string
	absorbedOld string
	absorbedNew string
}

func writeDuplicateMergeEntries(t *testing.T, privateRoot string) duplicateMergePaths {
	t.Helper()
	paths := duplicateMergePaths{
		keptOld:     filepath.Join(privateRoot, string(MemoryTypeProject), "kept-old.md"),
		keptNew:     filepath.Join(privateRoot, string(MemoryTypeProject), "kept-new.md"),
		absorbedOld: filepath.Join(privateRoot, string(MemoryTypeProject), "absorbed-old.md"),
		absorbedNew: filepath.Join(privateRoot, string(MemoryTypeProject), "absorbed-new.md"),
	}
	writeTestTopicFile(t, paths.keptOld, testMemoryEntry("Kept Duplicate", "old kept", MemoryTypeProject, "Shared merge content for requested kept path.\nWhy: requested kept path must be updated.\nHow to apply: update the older kept file."))
	writeTestTopicFile(t, paths.keptNew, testMemoryEntry("Kept Duplicate", "new kept", MemoryTypeProject, "Shared merge content for non-requested kept path.\nWhy: newer duplicate should not be updated.\nHow to apply: keep this file untouched."))
	writeTestTopicFile(t, paths.absorbedOld, testMemoryEntry("Absorbed Duplicate", "old absorbed", MemoryTypeProject, "Shared merge content for requested absorbed path.\nWhy: requested absorbed path must be removed.\nHow to apply: delete the older absorbed file."))
	writeTestTopicFile(t, paths.absorbedNew, testMemoryEntry("Absorbed Duplicate", "new absorbed", MemoryTypeProject, "Shared merge content for non-requested absorbed path.\nWhy: newer absorbed duplicate should remain.\nHow to apply: keep this file untouched."))
	return paths
}

func setDuplicateMergeTimes(t *testing.T, paths duplicateMergePaths) {
	t.Helper()
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	for _, path := range []string{paths.keptOld, paths.absorbedOld} {
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatalf("Chtimes(%q) error = %v", path, err)
		}
	}
	for _, path := range []string{paths.keptNew, paths.absorbedNew} {
		if err := os.Chtimes(path, newTime, newTime); err != nil {
			t.Fatalf("Chtimes(%q) error = %v", path, err)
		}
	}
}

func assertDuplicateMergeResult(t *testing.T, privateRoot string, paths duplicateMergePaths, merged UIMemoryEntryDetail) {
	t.Helper()
	if merged.Path != memoryEntryDisplayPath(privateRoot, paths.keptOld) {
		t.Fatalf("merged.Path = %q, want requested kept path", merged.Path)
	}
	keptOld, err := readMemoryEntryFile(paths.keptOld)
	if err != nil {
		t.Fatalf("read requested kept path error = %v", err)
	}
	if !strings.Contains(keptOld.Content, "requested absorbed path") {
		t.Fatalf("requested kept path was not merged:\n%s", keptOld.Content)
	}
	keptNew, err := readMemoryEntryFile(paths.keptNew)
	if err != nil {
		t.Fatalf("read newer kept path error = %v", err)
	}
	if strings.Contains(keptNew.Content, "requested absorbed path") {
		t.Fatalf("newer kept duplicate was modified:\n%s", keptNew.Content)
	}
	if _, err := os.Stat(paths.absorbedOld); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("requested absorbed path still exists or stat error = %v", err)
	}
	if _, err := os.Stat(paths.absorbedNew); err != nil {
		t.Fatalf("newer absorbed duplicate was deleted or inaccessible: %v", err)
	}
}

func TestMergeUIMemoryEntriesRejectsDifferentTypesAndKeepsBoth(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
	}
	store, err := newDiskStore(privateRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(privateRoot, nil) error = %v", err)
	}
	feedback, err := store.CreateStructured(MemoryWriteRequest{
		Name:        "Verification preference",
		Description: "How the user wants verification handled",
		Type:        MemoryTypeFeedback,
		Body:        "Run guarded verification before success claims.\nWhy: verification avoids false completion reports.\nHow to apply: run the relevant guard before final summaries.",
	})
	if err != nil {
		t.Fatalf("CreateStructured(feedback) error = %v", err)
	}
	project, err := store.CreateStructured(MemoryWriteRequest{
		Name:        "Verification project fact",
		Description: "Project verification workflow context",
		Type:        MemoryTypeProject,
		Body:        "Run guarded verification before success claims.\nWhy: verification avoids false completion reports.\nHow to apply: run the relevant guard before final summaries.",
	})
	if err != nil {
		t.Fatalf("CreateStructured(project) error = %v", err)
	}

	_, err = mergeUIMemoryEntries(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryMergeParams{
		CWD:     projectRoot,
		TargetA: "private",
		PathA:   memoryEntryDisplayPath(privateRoot, feedback.FilePath),
		TargetB: "private",
		PathB:   memoryEntryDisplayPath(privateRoot, project.FilePath),
	})
	if err == nil {
		t.Fatal("mergeUIMemoryEntries() error = nil, want type mismatch rejection")
	}
	if _, _, err := readUIMemoryEntryByName(privateRoot, "Verification preference"); err != nil {
		t.Fatalf("feedback entry missing after rejected merge: %v", err)
	}
	if _, _, err := readUIMemoryEntryByName(privateRoot, "Verification project fact"); err != nil {
		t.Fatalf("project entry missing after rejected merge: %v", err)
	}
}

func TestMergeUIMemoryEntriesRejectsDissimilarEntriesAndKeepsBoth(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
	}
	store, err := newDiskStore(privateRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(privateRoot, nil) error = %v", err)
	}
	first, err := store.CreateStructured(MemoryWriteRequest{
		Name:        "Reply language",
		Description: "Preferred response language",
		Type:        MemoryTypeFeedback,
		Body:        "Answer the user in Chinese by default.\nWhy: the user prefers Chinese collaboration.\nHow to apply: use Chinese for user-facing summaries.",
	})
	if err != nil {
		t.Fatalf("CreateStructured(first) error = %v", err)
	}
	second, err := store.CreateStructured(MemoryWriteRequest{
		Name:        "Database engine",
		Description: "Project database choice",
		Type:        MemoryTypeFeedback,
		Body:        "Use PostgreSQL migrations for schema changes.\nWhy: the backend relies on PostgreSQL-specific migrations.\nHow to apply: do not write MySQL-only migration syntax.",
	})
	if err != nil {
		t.Fatalf("CreateStructured(second) error = %v", err)
	}

	_, err = mergeUIMemoryEntries(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryMergeParams{
		CWD:     projectRoot,
		TargetA: "private",
		PathA:   memoryEntryDisplayPath(privateRoot, first.FilePath),
		TargetB: "private",
		PathB:   memoryEntryDisplayPath(privateRoot, second.FilePath),
	})
	if err == nil {
		t.Fatal("mergeUIMemoryEntries() error = nil, want dissimilar rejection")
	}
	if _, _, err := readUIMemoryEntryByName(privateRoot, "Reply language"); err != nil {
		t.Fatalf("first entry missing after rejected merge: %v", err)
	}
	if _, _, err := readUIMemoryEntryByName(privateRoot, "Database engine"); err != nil {
		t.Fatalf("second entry missing after rejected merge: %v", err)
	}
}

func TestDeleteUISharedFileRejectsFinalOutputReference(t *testing.T) {
	t.Parallel()

	deleter := &recordingSharedFileDeleter{}
	deps := memoryHandlerDeps{
		SharedFilesDeleter: deleter,
		Orchestration: &finalOutputOrchestrationStub{
			dags: []contract.DAGSummary{{DagKey: "dag-1"}},
			runs: []contract.Run{{
				RunKey: "run-1",
				DagKey: "dag-1",
				Metadata: json.RawMessage(`{
					"final_output": {
						"kind": "file",
						"path": "reports/final.md",
						"source_node_key": "report"
					}
				}`),
			}},
		},
	}

	deleted, err := deleteUISharedFile(context.Background(), deps, uiSharedFileDeleteParams{Path: "reports/final.md"})
	if err == nil {
		t.Fatal("deleteUISharedFile() error = nil, want final_output protection")
	}
	if deleted {
		t.Fatal("deleteUISharedFile() deleted = true, want false")
	}
	if deleter.calls != 0 {
		t.Fatalf("Delete() calls = %d, want 0", deleter.calls)
	}
}

func TestDeleteUISharedFileAllowsUnreferencedSharedFile(t *testing.T) {
	t.Parallel()

	deleter := &recordingSharedFileDeleter{}
	deps := memoryHandlerDeps{
		SharedFilesDeleter: deleter,
		Orchestration: &finalOutputOrchestrationStub{
			dags: []contract.DAGSummary{{DagKey: "dag-1"}},
			runs: []contract.Run{{
				RunKey:   "run-1",
				DagKey:   "dag-1",
				Metadata: json.RawMessage(`{"final_output":{"kind":"file","path":"reports/final.md"}}`),
			}},
		},
	}

	deleted, err := deleteUISharedFile(context.Background(), deps, uiSharedFileDeleteParams{Path: "scratch/intermediate.md"})
	if err != nil {
		t.Fatalf("deleteUISharedFile() error = %v", err)
	}
	if !deleted {
		t.Fatal("deleteUISharedFile() deleted = false, want true")
	}
	if deleter.calls != 1 || deleter.paths[0] != "scratch/intermediate.md" {
		t.Fatalf("Delete() calls=%d paths=%v", deleter.calls, deleter.paths)
	}
}

func TestDeleteUISharedFileRejectsMalformedFinalOutputMetadata(t *testing.T) {
	t.Parallel()

	deleter := &recordingSharedFileDeleter{}
	deps := memoryHandlerDeps{
		SharedFilesDeleter: deleter,
		Orchestration: &finalOutputOrchestrationStub{
			dags: []contract.DAGSummary{{DagKey: "dag-1"}},
			runs: []contract.Run{{
				RunKey:   "run-1",
				DagKey:   "dag-1",
				Metadata: json.RawMessage(`{"final_output":{"sharedfile":"reports/final.md"}}`),
			}},
		},
	}

	deleted, err := deleteUISharedFile(context.Background(), deps, uiSharedFileDeleteParams{Path: "scratch/intermediate.md"})
	if err == nil || !strings.Contains(err.Error(), "final_output") {
		t.Fatalf("deleteUISharedFile() error = %v, want malformed final_output guard failure", err)
	}
	if deleted {
		t.Fatal("deleteUISharedFile() deleted = true, want false")
	}
	if deleter.calls != 0 {
		t.Fatalf("Delete() calls = %d, want 0", deleter.calls)
	}
}

func TestDeleteUISharedFileUsesDAGRuntimeGuardWithoutOrchestration(t *testing.T) {
	t.Parallel()

	deleter := &recordingSharedFileDeleter{}
	deps := memoryHandlerDeps{
		SharedFilesDeleter: deleter,
		DAGRuntime: &finalOutputOrchestrationStub{
			dags: []contract.DAGSummary{{DagKey: "dag-1"}},
			runs: []contract.Run{{
				RunKey:   "run-1",
				DagKey:   "dag-1",
				Metadata: json.RawMessage(`{"final_output":{"kind":"file","path":"reports/final.md"}}`),
			}},
		},
	}

	deleted, err := deleteUISharedFile(context.Background(), deps, uiSharedFileDeleteParams{Path: "scratch/intermediate.md"})
	if err != nil {
		t.Fatalf("deleteUISharedFile() error = %v", err)
	}
	if !deleted {
		t.Fatal("deleteUISharedFile() deleted = false, want true")
	}
	if deleter.calls != 1 {
		t.Fatalf("Delete() calls = %d, want 1", deleter.calls)
	}
}

func TestDeleteUISharedFileRequiresFinalOutputGuard(t *testing.T) {
	t.Parallel()

	deleter := &recordingSharedFileDeleter{}
	deps := memoryHandlerDeps{SharedFilesDeleter: deleter}

	deleted, err := deleteUISharedFile(context.Background(), deps, uiSharedFileDeleteParams{Path: "reports/final.md"})
	if err == nil {
		t.Fatal("deleteUISharedFile() error = nil, want missing final_output guard failure")
	}
	if deleted {
		t.Fatal("deleteUISharedFile() deleted = true, want false")
	}
	if deleter.calls != 0 {
		t.Fatalf("Delete() calls = %d, want 0", deleter.calls)
	}
}

type recordingSharedFileDeleter struct {
	calls int
	paths []string
}

func (r *recordingSharedFileDeleter) Delete(_ context.Context, path string) (int64, error) {
	r.calls++
	r.paths = append(r.paths, path)
	return 1, nil
}

type finalOutputOrchestrationStub struct {
	contract.OrchestrationService
	dags []contract.DAGSummary
	runs []contract.Run
}

func (s *finalOutputOrchestrationStub) ListDAGs(context.Context, contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
	return s.dags, nil
}

func (s *finalOutputOrchestrationStub) ListRuns(_ context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
	out := make([]contract.Run, 0, len(s.runs))
	for _, run := range s.runs {
		if run.DagKey == req.DagKey {
			out = append(out, run)
		}
	}
	return contract.ListRunsResponse{Runs: out}, nil
}

// recordingSectionInvalidator implements contract.SectionInvalidator with
// the documented concurrent-safe contract; the mutex was added in Phase
// 2.0.1 to match production behaviour. UI RPC paths fan out under the
// shared section invalidator without external synchronization, so this
// stub has to too.
type recordingSectionInvalidator struct {
	mu    sync.Mutex
	calls []recordedInvalidateCall
}

type recordedInvalidateCall struct {
	reason contract.InvalidateReason
	names  []string
}

func (r *recordingSectionInvalidator) InvalidateSections(reason contract.InvalidateReason, names ...string) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedInvalidateCall{reason: reason, names: append([]string(nil), names...)})
	return uint64(len(r.calls))
}

func errorsIsMemoryNotFound(err error) bool {
	return errors.Is(err, ErrMemoryNotFound)
}
