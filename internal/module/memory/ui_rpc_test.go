package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/similarity"
)

func TestBuildUIMemorySnapshotIncludesDurableAndAgentMemories(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := newUIMemorySnapshotConfig(t, projectRoot, privateRoot)
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}

	privateStore := mustNewTestDiskStore(t, privateRoot)
	createStructuredMemoryForTest(t, privateStore, MemoryWriteRequest{
		Name:        "Keep replies concise",
		Description: "User prefers direct answers",
		Type:        MemoryTypeFeedback,
		Body:        "rule\nWhy: concise answers reduce back-and-forth.\nHow to apply: lead with the fix.",
	}, "private")

	teamRoot := mustConfiguredTeamMemoryRoot(t, cfg)
	if err := os.MkdirAll(teamRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(teamRoot) error = %v", err)
	}
	teamStore := mustNewTestDiskStore(t, teamRoot)
	createStructuredMemoryForTest(t, teamStore, MemoryWriteRequest{
		Name:        "Core dashboard owner",
		Description: "Who owns the dashboard area",
		Type:        MemoryTypeProject,
		Body:        "fact\nWhy: onboarding and review routing.\nHow to apply: ask the dashboard owner for cross-team changes.",
	}, "team")

	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	assertDurableAndAgentMemorySnapshot(t, snapshot)
}

func TestRegisterUIMemoryMutationHandlersDoesNotExposeSharedFilePromote(t *testing.T) {
	handlers := registerUIMemoryMutationHandlers(memoryHandlerDeps{})
	if _, ok := handlers["ui/memory/shared-file/promote"]; ok {
		t.Fatal("ui/memory/shared-file/promote should not be registered")
	}
	if _, ok := handlers["ui/memory/shared-file/get"]; !ok {
		t.Fatal("ui/memory/shared-file/get should remain registered")
	}
	if _, ok := handlers["ui/memory/shared-file/delete"]; !ok {
		t.Fatal("ui/memory/shared-file/delete should remain registered")
	}
	if _, ok := handlers["ui/memory/shared-file/cleanup-preview"]; !ok {
		t.Fatal("ui/memory/shared-file/cleanup-preview should be registered")
	}
	if _, ok := handlers["ui/memory/shared-file/cleanup-apply"]; !ok {
		t.Fatal("ui/memory/shared-file/cleanup-apply should be registered")
	}
	if _, ok := handlers["ui/memory/similarity/consolidate-all/start"]; !ok {
		t.Fatal("ui/memory/similarity/consolidate-all/start should be registered")
	}
	if _, ok := handlers["ui/memory/similarity/consolidate-all/status"]; !ok {
		t.Fatal("ui/memory/similarity/consolidate-all/status should be registered")
	}
}

func TestRedactedMemoryIntentErrorUsesContractFailureIdentity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		cause error
		want  string
	}{
		{cause: contract.ErrMemoryOverflowDeleteFailed, want: "memory_overflow_delete_failed"},
		{cause: contract.ErrMemoryOverflowMergeFailed, want: "memory_overflow_merge_failed"},
		{cause: contract.ErrMemoryIndexUpdateFailed, want: "memory_index_update_failed"},
	} {
		err := opaqueMemoryIntentError{cause: tc.cause}
		if got := redactedMemoryIntentError(err); got != tc.want {
			t.Fatalf("redactedMemoryIntentError() = %q, want %q", got, tc.want)
		}
	}
}

type opaqueMemoryIntentError struct {
	cause error
}

func (e opaqueMemoryIntentError) Error() string { return "opaque memory intent failure" }

func (e opaqueMemoryIntentError) Unwrap() error { return e.cause }

func TestUIMemoryConsolidationJobStoreReturnsRunningBeforeWorkCompletes(t *testing.T) {
	store, started, release := newBlockingMemoryConsolidationJobStore()

	start, err := store.start(memoryHandlerDeps{}, uiSimilarityConsolidateAllParams{CWD: "/repo/app"})
	if err != nil {
		t.Fatalf("start() error = %v", err)
	}
	if start.JobID == "" || start.Status != uiMemoryConsolidationStatusRunning {
		t.Fatalf("start() = %+v, want running job id", start)
	}

	waitForMemoryConsolidationSignal(t, started, "background consolidation did not start")
	assertMemoryConsolidationStatus(t, store, start.JobID, uiMemoryConsolidationStatusRunning)

	close(release)
	status := waitForMemoryConsolidationJobStatus(t, store, start.JobID, uiMemoryConsolidationStatusSucceeded)
	if status.Result == nil || status.Result.Merged != 1 {
		t.Fatalf("status(succeeded) = %+v, want merged result", status)
	}
}

func TestSimilarityAdapterDreamExecutePassesRequestedProviderOptions(t *testing.T) {
	dream := &recordingOptionsDreamExecutor{output: `{"decisions":[]}`}
	adapter := newSimilarityAdapter(memoryHandlerDeps{DreamExecutor: dream}, contract.DreamOptions{
		Provider:      "codex",
		Model:         "gpt-5.5",
		ModelProvider: "openai",
	})

	if _, err := adapter.DreamExecute(context.Background(), "memory prompt"); err != nil {
		t.Fatalf("DreamExecute() error = %v", err)
	}
	if len(dream.options) != 1 {
		t.Fatalf("recorded options = %d, want 1", len(dream.options))
	}
	if got := dream.options[0]; got.Provider != "codex" || got.Model != "gpt-5.5" || got.ModelProvider != "openai" {
		t.Fatalf("dream options = %+v, want codex/gpt-5.5/openai", got)
	}
	if got := dream.options[0].RuntimePolicy; !got.ToolsDisabled || !got.ReadOnlySandbox || !got.MinEnv {
		t.Fatalf("RuntimePolicy = %+v, want strict dream policy", got)
	}
}

func TestProvideDreamExtractFuncUsesStrictRuntimePolicy(t *testing.T) {
	dream := &recordingOptionsDreamExecutor{output: `{"decisions":[]}`}
	extractFn := provideDreamExtractFunc(dreamExtractParams{Executor: dream})

	if _, err := extractFn(context.Background(), "memory prompt"); err != nil {
		t.Fatalf("extractFn() error = %v", err)
	}
	if len(dream.options) != 1 {
		t.Fatalf("recorded options = %d, want 1", len(dream.options))
	}
	if got := dream.options[0].RuntimePolicy; !got.ToolsDisabled || !got.ReadOnlySandbox || !got.MinEnv {
		t.Fatalf("RuntimePolicy = %+v, want strict dream policy", got)
	}
}

func TestBuildUIMemorySnapshotSurfacesAutoDreamHealth(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}
	cfg := newUIMemorySnapshotConfig(t, projectRoot, privateRoot)
	hooks := newMemoryLifecycleHooks(cfg, nil, nil, nil, nil, nil, NewMemoryExtractor(), NewManifestBuilder())
	lastAt := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	hooks.recordAutoDreamSchedulerHealth(autoDreamHealthSnapshot{
		DroppedTotal:   1,
		ProcessedTotal: 2,
		ScheduledTotal: 3,
		LastError:      "provider failed",
		LastAt:         lastAt,
		LastThreadID:   "thread-1",
	})

	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, hooks), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	got := snapshot.Overview.Health.AutoDream
	if got == nil {
		t.Fatal("Overview.Health.AutoDream = nil, want health snapshot")
	}
	if got.DroppedTotal != 1 || got.ProcessedTotal != 2 || got.ScheduledTotal != 3 {
		t.Fatalf("AutoDream totals = %+v, want dropped=1 processed=2 scheduled=3", got)
	}
	if got.LastError != "provider failed" || got.LastThreadID != "thread-1" || !got.LastAt.Equal(lastAt) {
		t.Fatalf("AutoDream last snapshot = %+v, want error/thread/time", got)
	}
}

func TestBuildUIMemorySnapshotSurfacesNestedIngestHealth(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}
	cfg := newUIMemorySnapshotConfig(t, projectRoot, privateRoot)
	hooks := newMemoryLifecycleHooks(cfg, nil, nil, nil, nil, nil, NewMemoryExtractor(), NewManifestBuilder())
	before := time.Now().UTC()
	hooks.recordNestedIngestRejection("thread-1", errors.New("nested ingest: pending queue limit exceeded"))
	after := time.Now().UTC()

	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, hooks), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	got := snapshot.Overview.Health.NestedIngest
	if got == nil {
		t.Fatal("Overview.Health.NestedIngest = nil, want rejection health")
	}
	if got.RejectedTotal != 1 || got.LastError != "nested ingest: pending queue limit exceeded" || got.LastThreadID != "thread-1" || got.LastAt.Before(before) || got.LastAt.After(after) {
		t.Fatalf("NestedIngest = %+v, want total/error/thread/time", got)
	}
}

// TestNestedIngestHealthProjectionCoversProducerFields prevents producer fields
// from silently disappearing before the UIMemoryHealth wire DTO.
func TestNestedIngestHealthProjectionCoversProducerFields(t *testing.T) {
	producer := NestedIngestHealthSnapshot{
		RejectedTotal: 7,
		LastError:     "nested ingest: queue full",
		LastAt:        time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
		LastThreadID:  "thread-7",
	}
	consumer := uiNestedIngestHealthFromSnapshot(producer)
	if consumer == nil {
		t.Fatal("uiNestedIngestHealthFromSnapshot() = nil, want projected health")
	}

	consumerFieldByProducerField := map[string]string{
		"RejectedTotal": "RejectedTotal",
		"LastError":     "LastError",
		"LastAt":        "LastAt",
		"LastThreadID":  "LastThreadID",
	}
	producerValue := reflect.ValueOf(producer)
	consumerValue := reflect.ValueOf(*consumer)
	producerType := reflect.TypeFor[NestedIngestHealthSnapshot]()
	consumerType := reflect.TypeFor[UINestedIngestHealth]()
	for index := range producerType.NumField() {
		producerField := producerType.Field(index)
		consumerName, ok := consumerFieldByProducerField[producerField.Name]
		if !ok {
			t.Fatalf("producer field %q has no UINestedIngestHealth consumer", producerField.Name)
		}
		consumerField, ok := consumerType.FieldByName(consumerName)
		if !ok {
			t.Fatalf("producer field %q maps to missing consumer field %q", producerField.Name, consumerName)
		}
		if got, want := consumerValue.FieldByIndex(consumerField.Index).Interface(), producerValue.Field(index).Interface(); !reflect.DeepEqual(got, want) {
			t.Fatalf("producer field %q projected as %#v, want %#v", producerField.Name, got, want)
		}
	}
	for producerName := range consumerFieldByProducerField {
		if _, ok := producerType.FieldByName(producerName); !ok {
			t.Fatalf("stale producer field registry entry %q", producerName)
		}
	}
	healthField, ok := reflect.TypeFor[UIMemoryHealth]().FieldByName("NestedIngest")
	if !ok || strings.Split(healthField.Tag.Get("json"), ",")[0] != "nestedIngest" {
		t.Fatalf("UIMemoryHealth.NestedIngest json field = %q, want nestedIngest", healthField.Tag.Get("json"))
	}
}

func TestConsolidateAllDreamFailureUsesUserFacingError(t *testing.T) {
	err := publicConsolidateAllError(fmt.Errorf("%w: provider failed", similarity.ErrLLMConsolidate))
	if err == nil {
		t.Fatal("publicConsolidateAllError() = nil, want user-facing error")
	}
	if strings.Contains(err.Error(), "durable memory") || strings.Contains(err.Error(), "LLM consolidate") {
		t.Fatalf("error %q still exposes internal wording", err.Error())
	}
	if !strings.Contains(err.Error(), "模型") {
		t.Fatalf("error %q should explain model failure", err.Error())
	}
}

type recordingOptionsDreamExecutor struct {
	output  string
	options []contract.DreamOptions
}

func (e *recordingOptionsDreamExecutor) ExecuteDream(ctx context.Context, prompt string) (string, error) {
	return e.ExecuteDreamWithOptions(ctx, prompt, contract.DreamOptions{})
}

func (e *recordingOptionsDreamExecutor) ExecuteDreamWithOptions(_ context.Context, _ string, options contract.DreamOptions) (string, error) {
	e.options = append(e.options, options)
	return e.output, nil
}

func newBlockingMemoryConsolidationJobStore() (*uiMemoryConsolidationJobStore, <-chan struct{}, chan<- struct{}) {
	started := make(chan struct{})
	release := make(chan struct{})
	store := newUIMemoryConsolidationJobStore(func(ctx context.Context, _ memoryHandlerDeps, _ uiSimilarityConsolidateAllParams) (uiSimilarityConsolidateAllResult, error) {
		close(started)
		select {
		case <-release:
			return uiSimilarityConsolidateAllResult{Merged: 1}, nil
		case <-ctx.Done():
			return uiSimilarityConsolidateAllResult{}, ctx.Err()
		}
	}, time.Minute)
	return store, started, release
}

func waitForMemoryConsolidationSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func assertMemoryConsolidationStatus(t *testing.T, store *uiMemoryConsolidationJobStore, jobID, want string) {
	t.Helper()
	status, err := store.status(uiSimilarityConsolidateAllStatusParams{CWD: "/repo/app", JobID: jobID})
	if err != nil {
		t.Fatalf("status(%s) error = %v", want, err)
	}
	if status.Status != want {
		t.Fatalf("status(%s) = %+v", want, status)
	}
}

func waitForMemoryConsolidationJobStatus(t *testing.T, store *uiMemoryConsolidationJobStore, jobID, want string) uiSimilarityConsolidateAllStatusResult {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, err := store.status(uiSimilarityConsolidateAllStatusParams{CWD: "/repo/app", JobID: jobID})
		if err != nil {
			t.Fatalf("status() error = %v", err)
		}
		if status.Status == want {
			return status
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach %s", jobID, want)
	return uiSimilarityConsolidateAllStatusResult{}
}

func newUIMemorySnapshotConfig(t *testing.T, projectRoot, privateRoot string) *Config {
	t.Helper()
	return &Config{
		Enabled:             true,
		EnableTools:         true,
		ExtractOnStop:       true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: privateRoot,
		Features:            MemoryFeatureFlags{TeamMemory: true},
	}
}

func mustNewTestDiskStore(t *testing.T, root string) *diskStore {
	t.Helper()
	store, err := newDiskStore(root, nil)
	if err != nil {
		t.Fatalf("newDiskStore(%q, nil) error = %v", root, err)
	}
	return store
}

func createStructuredMemoryForTest(t *testing.T, store *diskStore, req MemoryWriteRequest, label string) {
	t.Helper()
	if _, err := store.CreateStructured(req); err != nil {
		t.Fatalf("CreateStructured(%s) error = %v", label, err)
	}
}

func mustConfiguredTeamMemoryRoot(t *testing.T, cfg *Config) string {
	t.Helper()
	teamRoot, err := configuredTeamMemRoot(cfg)
	if err != nil {
		t.Fatalf("configuredTeamMemRoot() error = %v", err)
	}
	return teamRoot
}

func assertDurableAndAgentMemorySnapshot(t *testing.T, snapshot UIMemorySnapshot) {
	t.Helper()
	if !snapshot.Overview.Enabled || !snapshot.Overview.ToolsEnabled || !snapshot.Overview.AutoDreamEnabled {
		t.Fatalf("Overview = %#v, want enabled tools-enabled auto-dream snapshot", snapshot.Overview)
	}
	if got := len(snapshot.Private.Entries); got != 1 {
		t.Fatalf("len(private entries) = %d, want 1", got)
	}
	if got := snapshot.Private.Entries[0].Name; got != "Keep replies concise" {
		t.Fatalf("private entry name = %q, want %q", got, "Keep replies concise")
	}
	if got := len(snapshot.Team.Entries); got != 1 {
		t.Fatalf("len(team entries) = %d, want 1", got)
	}
	if got := snapshot.Team.Entries[0].Name; got != "Core dashboard owner" {
		t.Fatalf("team entry name = %q, want %q", got, "Core dashboard owner")
	}
}

func TestBuildUIMemorySnapshotAutoDreamReflectsConfigGates(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}

	cases := []struct {
		name          string
		enabled       bool
		extractOnStop bool
		wantAuto      bool
	}{
		{"all-on", true, true, true},
		{"extract-on-stop-off", true, false, false},
		{"system-off", false, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Enabled:             tc.enabled,
				EnableTools:         true,
				ExtractOnStop:       tc.extractOnStop,
				RootDir:             t.TempDir(),
				ProjectRoot:         projectRoot,
				AutoMemPathOverride: privateRoot,
			}
			snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
			if err != nil {
				t.Fatalf("buildUIMemorySnapshot() error = %v", err)
			}
			if got := snapshot.Overview.AutoDreamEnabled; got != tc.wantAuto {
				t.Fatalf("Overview.AutoDreamEnabled = %v, want %v (cfg=%+v)", got, tc.wantAuto, cfg)
			}
			if snapshot.Overview.AutoDreamIntent != nil {
				t.Fatalf("Overview.AutoDreamIntent = %v, want nil with no persisted intent", *snapshot.Overview.AutoDreamIntent)
			}
		})
	}
}

func TestBuildUIMemorySnapshotSurfacesPersistedAutoDreamIntent(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}
	root := t.TempDir()
	if err := WriteAutoDreamIntent(root, true); err != nil {
		t.Fatalf("WriteAutoDreamIntent error = %v", err)
	}
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ExtractOnStop:       false, // intent overrides this only via NewConfig; here we just check surfacing.
		RootDir:             root,
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: privateRoot,
	}
	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	if snapshot.Overview.AutoDreamIntent == nil || *snapshot.Overview.AutoDreamIntent != true {
		t.Fatalf("Overview.AutoDreamIntent = %v, want *true", snapshot.Overview.AutoDreamIntent)
	}
}

func TestUIMemoryEntryCRUD(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: privateRoot,
	}
	deps := memoryHandlerDeps{
		Service:  newServiceWithConsolidator(cfg, nil, nil, nil),
		Sections: &recordingSectionInvalidator{},
	}

	created, err := upsertUIMemoryEntry(context.Background(), deps, uiMemoryEntryUpsertParams{
		CWD:         projectRoot,
		Target:      "private",
		Name:        "Release owner",
		Description: "Who owns production release decisions",
		Type:        "reference",
		Content:     "Release owner for this project is listed in the runbook.",
	})
	if err != nil {
		t.Fatalf("upsertUIMemoryEntry(create) error = %v", err)
	}
	if created.Path == "" || created.Name != "Release owner" {
		t.Fatalf("created = %#v", created)
	}

	loaded, err := getUIMemoryEntry(context.Background(), deps, uiMemoryEntryGetParams{
		CWD:    projectRoot,
		Target: "private",
		Path:   created.Path,
	})
	if err != nil {
		t.Fatalf("getUIMemoryEntry() error = %v", err)
	}
	if loaded.Content != "Release owner for this project is listed in the runbook." {
		t.Fatalf("loaded.Content = %q", loaded.Content)
	}

	updated, err := upsertUIMemoryEntry(context.Background(), deps, uiMemoryEntryUpsertParams{
		CWD:          projectRoot,
		Target:       "private",
		ExistingPath: created.Path,
		Name:         "Release owner",
		Description:  "Who owns production release approvals",
		Type:         "reference",
		Content:      "Primary source is the production runbook and release checklist.",
	})
	if err != nil {
		t.Fatalf("upsertUIMemoryEntry(update) error = %v", err)
	}
	if updated.Description != "Who owns production release approvals" {
		t.Fatalf("updated = %#v", updated)
	}

	if err := deleteUIMemoryEntry(context.Background(), deps, uiMemoryEntryDeleteParams{
		CWD:    projectRoot,
		Target: "private",
		Path:   updated.Path,
	}); err != nil {
		t.Fatalf("deleteUIMemoryEntry() error = %v", err)
	}
	if _, _, err := readUIMemoryEntryByName(privateRoot, "Release owner"); !errorsIsMemoryNotFound(err) {
		t.Fatalf("readUIMemoryEntryByName(after delete) error = %v, want not found", err)
	}
}

func TestUIMemoryEntryRejectsTeamTargetWhenTeamMemoryDisabled(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
		Features:            MemoryFeatureFlags{TeamMemory: false},
	}
	_, err := upsertUIMemoryEntry(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryUpsertParams{
		CWD:         projectRoot,
		Target:      "team",
		Name:        "Team memory disabled",
		Description: "Should not write team memory when disabled",
		Type:        "project",
		Content:     "Team writes are disabled.\nWhy: the feature gate is off.\nHow to apply: reject target=team mutations.",
	})
	if err == nil {
		t.Fatal("upsertUIMemoryEntry(team disabled) error = nil, want rejection")
	}
	if _, statErr := os.Stat(filepath.Join(privateRoot, teamMemoryRootDirName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("team memory root stat error = %v, want not exist", statErr)
	}
}

func TestUpdateUIMemoryEntryReturnsRequestedPathWhenDuplicateNamesExist(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
	}
	olderPath := filepath.Join(privateRoot, string(MemoryTypeProject), "edit-old.md")
	newerPath := filepath.Join(privateRoot, string(MemoryTypeProject), "edit-new.md")
	writeTestTopicFile(t, olderPath, testMemoryEntry("Editable Duplicate", "old duplicate", MemoryTypeProject, "old body\nWhy: old duplicate.\nHow to apply: update this specific old file."))
	writeTestTopicFile(t, newerPath, testMemoryEntry("Editable Duplicate", "new duplicate", MemoryTypeProject, "new body\nWhy: newer duplicate should remain untouched.\nHow to apply: do not update this file."))
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	if err := os.Chtimes(olderPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(olderPath) error = %v", err)
	}
	if err := os.Chtimes(newerPath, newTime, newTime); err != nil {
		t.Fatalf("Chtimes(newerPath) error = %v", err)
	}

	updated, err := upsertUIMemoryEntry(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryUpsertParams{
		CWD:          projectRoot,
		Target:       "private",
		ExistingPath: memoryEntryDisplayPath(privateRoot, olderPath),
		Name:         "Editable Duplicate",
		Description:  "old duplicate edited",
		Type:         "project",
		Content:      "edited old body\nWhy: old duplicate should be updated by path.\nHow to apply: return the requested path after saving.",
	})
	if err != nil {
		t.Fatalf("upsertUIMemoryEntry(update duplicate) error = %v", err)
	}
	if updated.Path != memoryEntryDisplayPath(privateRoot, olderPath) {
		t.Fatalf("updated.Path = %q, want requested path", updated.Path)
	}
	older, err := readMemoryEntryFile(olderPath)
	if err != nil {
		t.Fatalf("read older path error = %v", err)
	}
	if !strings.Contains(older.Content, "edited old body") {
		t.Fatalf("older path was not updated:\n%s", older.Content)
	}
	newer, err := readMemoryEntryFile(newerPath)
	if err != nil {
		t.Fatalf("read newer path error = %v", err)
	}
	if strings.Contains(newer.Content, "edited old body") {
		t.Fatalf("newer duplicate was modified:\n%s", newer.Content)
	}
}

func TestDeleteUIMemoryEntryDeletesRequestedPathWhenDuplicateNamesExist(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		ProjectRoot:         projectRoot,
		RootDir:             t.TempDir(),
		AutoMemPathOverride: privateRoot,
	}
	olderPath := filepath.Join(privateRoot, string(MemoryTypeProject), "duplicate-old.md")
	newerPath := filepath.Join(privateRoot, string(MemoryTypeProject), "duplicate-new.md")
	writeTestTopicFile(t, olderPath, testMemoryEntry("Duplicate Memory", "old duplicate", MemoryTypeProject, "old body\nWhy: old duplicate.\nHow to apply: keep the old file until specifically deleted."))
	writeTestTopicFile(t, newerPath, testMemoryEntry("Duplicate Memory", "new duplicate", MemoryTypeProject, "new body\nWhy: new duplicate.\nHow to apply: keep the new file unless its path is requested."))
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	if err := os.Chtimes(olderPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(olderPath) error = %v", err)
	}
	if err := os.Chtimes(newerPath, newTime, newTime); err != nil {
		t.Fatalf("Chtimes(newerPath) error = %v", err)
	}

	err := deleteUIMemoryEntry(context.Background(), memoryHandlerDeps{Service: newServiceWithConsolidator(cfg, nil, nil, nil)}, uiMemoryEntryDeleteParams{
		CWD:    projectRoot,
		Target: "private",
		Path:   memoryEntryDisplayPath(privateRoot, olderPath),
	})
	if err != nil {
		t.Fatalf("deleteUIMemoryEntry() error = %v", err)
	}
	if _, err := os.Stat(olderPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("older path still exists or stat error = %v", err)
	}
	if _, err := os.Stat(newerPath); err != nil {
		t.Fatalf("newer path was deleted or inaccessible: %v", err)
	}
}

func TestComputeUIMemoryHealthUsesFullContentNotPreviewOnly(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: privateRoot,
	}
	store, err := newDiskStore(privateRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(privateRoot, nil) error = %v", err)
	}
	sharedPreview := strings.Repeat("sharedpreview ", 40)
	alphaTail := uniqueTokenRun("alpha", 120)
	betaTail := uniqueTokenRun("beta", 120)
	_, err = store.CreateStructured(MemoryWriteRequest{
		Name:        "Long memory A",
		Description: "Long memory first",
		Type:        MemoryTypeProject,
		Body:        sharedPreview + "\n\n" + alphaTail + "\nWhy: " + alphaTail + "\nHow to apply: " + alphaTail,
	})
	if err != nil {
		t.Fatalf("CreateStructured(first) error = %v", err)
	}
	_, err = store.CreateStructured(MemoryWriteRequest{
		Name:        "Long memory B",
		Description: "Long memory second",
		Type:        MemoryTypeProject,
		Body:        sharedPreview + "\n\n" + betaTail + "\nWhy: " + betaTail + "\nHow to apply: " + betaTail,
	})
	if err != nil {
		t.Fatalf("CreateStructured(second) error = %v", err)
	}

	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	if got := len(snapshot.Overview.Health.SimilarGroups); got != 0 {
		t.Fatalf("SimilarGroups = %#v, want none when only the truncated preview matches", snapshot.Overview.Health.SimilarGroups)
	}
}

func TestUIMemoryGetRespectsEntryLimit(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := newUIMemorySnapshotConfig(t, projectRoot, privateRoot)
	for i := range 260 {
		writeUIMemoryScanEntry(t, privateRoot, i, "small memory body\nWhy: scan budget fixture.\nHow to apply: keep enough files to cross the UI cap.")
	}

	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	if got := len(snapshot.Private.Entries); got >= 260 {
		t.Fatalf("private entries = %d, want authoritative cap before every body is exposed", got)
	}
	assertUIMemoryScanReason(t, snapshot, "memory_scan_truncated")
}

func TestUIMemoryGetRejectsOversizedEntry(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := newUIMemorySnapshotConfig(t, projectRoot, privateRoot)
	writeUIMemoryScanEntry(t, privateRoot, 1, strings.Repeat("oversized ", 40*1024))

	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	if got := len(snapshot.Private.Entries); got != 0 {
		t.Fatalf("private entries = %d, want oversized entry omitted from listing", got)
	}
	assertUIMemoryScanReason(t, snapshot, "memory_scan_truncated")
}

func TestUIMemoryGetStopsOnContextCancel(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := newUIMemorySnapshotConfig(t, projectRoot, privateRoot)
	writeUIMemoryScanEntry(t, privateRoot, 1, "small memory body\nWhy: cancellation fixture.\nHow to apply: scan should stop before reading entries.")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	snapshot, err := buildUIMemorySnapshot(ctx, newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	if got := len(snapshot.Private.Entries); got != 0 {
		t.Fatalf("private entries = %d, want canceled scan to avoid exposing partial bodies", got)
	}
	assertUIMemoryScanReason(t, snapshot, "memory_scan_canceled")
}

func TestUIMemorySimilarityHealthRespectsScanBudget(t *testing.T) {
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := newUIMemorySnapshotConfig(t, projectRoot, privateRoot)
	for i := range 260 {
		writeUIMemoryScanEntry(t, privateRoot, i, "same body for similarity\nWhy: all entries share enough text to be similar.\nHow to apply: similarity must be skipped after scan truncates.")
	}

	snapshot, err := buildUIMemorySnapshot(context.Background(), newServiceWithConsolidator(cfg, nil, nil, nil), nil, projectRoot)
	if err != nil {
		t.Fatalf("buildUIMemorySnapshot() error = %v", err)
	}
	assertUIMemoryScanReason(t, snapshot, "memory_scan_truncated")
	if health := snapshot.Overview.Health; health == nil || len(health.SimilarGroups) != 0 {
		t.Fatalf("health = %#v, want similarity groups omitted after scan budget truncates", health)
	}
}

// TestPopulateUIMemoryHealthSimilarGroupsDegradesOnCorruptIgnoredSet verifies corrupt ignore state is never treated as an empty set.
func TestPopulateUIMemoryHealthSimilarGroupsDegradesOnCorruptIgnoredSet(t *testing.T) {
	privateRoot := filepath.Join(t.TempDir(), "private-secret-root")
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(privateRoot) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(privateRoot, ".similarity-ignored.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatalf("WriteFile(ignored set) error = %v", err)
	}
	entries := []UIMemoryEntry{
		{Name: "first", Type: "project", Path: "project/first.md", scanContent: "shared durable memory body with enough matching words for similarity detection"},
		{Name: "second", Type: "project", Path: "project/second.md", scanContent: "shared durable memory body with enough matching words for similarity detection"},
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	health := &UIMemoryHealth{}

	populateUIMemoryHealthSimilarGroups(health, logger, privateRoot, entries, "", nil, nil)

	if !health.SimilarityDegraded {
		t.Fatal("SimilarityDegraded = false, want true for corrupt ignored set")
	}
	if health.SimilarGroups != nil {
		t.Fatalf("SimilarGroups = %#v, want nil when ignored filtering is unavailable", health.SimilarGroups)
	}
	if got := logs.String(); !strings.Contains(got, "memory similarity ignored set unavailable") {
		t.Fatalf("logs = %q, want stable degraded warning", got)
	} else if strings.Contains(got, privateRoot) || strings.Contains(got, "{broken") {
		t.Fatalf("logs leaked ignored-set path or content: %q", got)
	}
}

func TestPopulateUIMemoryHealthSimilarGroupsAllowsMissingIgnoredSet(t *testing.T) {
	privateRoot := filepath.Join(t.TempDir(), "private")
	entries := []UIMemoryEntry{
		{Name: "first", Type: "project", Path: "project/first.md", scanContent: "shared durable memory body with enough matching words for similarity detection"},
		{Name: "second", Type: "project", Path: "project/second.md", scanContent: "shared durable memory body with enough matching words for similarity detection"},
	}
	health := &UIMemoryHealth{}

	populateUIMemoryHealthSimilarGroups(health, nil, privateRoot, entries, "", nil, nil)

	if health.SimilarityDegraded {
		t.Fatal("SimilarityDegraded = true, want false when ignored set has not been created")
	}
}

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
