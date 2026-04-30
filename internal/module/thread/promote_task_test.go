package thread

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

// promoteTaskTestService spins up a service wired with the minimum stubs
// needed by PromoteTaskFromThread: thread store, shared file store, and an
// emitUpdated capture so the test can assert on the projector nudge.
func promoteTaskTestService(t *testing.T, thread *threadstore.Thread, files *stubSharedFileStore) (*service, *[]threaddto.Updated) {
	t.Helper()
	store := &stubThreadStore{thread: thread}
	emitted := &[]threaddto.Updated{}
	svc := &service{
		threadStore: store,
		sharedFiles: files,
		emitUpdated: func(ev threaddto.Updated) {
			*emitted = append(*emitted, ev)
		},
	}
	return svc, emitted
}

func TestPromoteTaskFromThreadHappyPath(t *testing.T) {
	t.Parallel()
	thread := &threadstore.Thread{
		ThreadID: "thread-promo",
		Name:     "Refactor billing",
		Prompt:   "Refactor billing",
	}
	files := &stubSharedFileStore{}
	svc, emitted := promoteTaskTestService(t, thread, files)

	result, err := svc.PromoteTaskFromThread(context.Background(), "thread-promo")
	if err != nil {
		t.Fatalf("PromoteTaskFromThread() error = %v", err)
	}
	if result.AlreadyTask {
		t.Fatalf("AlreadyTask = true, want false on first promote")
	}
	if !strings.HasPrefix(result.TaskID, "task_") {
		t.Fatalf("TaskID = %q, want task_ prefix", result.TaskID)
	}
	if result.TaskTitle != "Refactor billing" {
		t.Fatalf("TaskTitle = %q, want Refactor billing", result.TaskTitle)
	}
	if !strings.HasPrefix(result.HandoffFile, taskHandoffPrefix) {
		t.Fatalf("HandoffFile = %q, want %s prefix", result.HandoffFile, taskHandoffPrefix)
	}
	if result.HandoffShellWarning != "" {
		t.Fatalf("HandoffShellWarning = %q, want empty", result.HandoffShellWarning)
	}

	// verify ConfigOverride was upserted with task fields
	stored := decodeStoredThreadConfig(svc.threadStore.(*stubThreadStore).thread.ConfigOverride)
	meta := taskHandoffMetaFromRuntimeConfig(stored.Runtime)
	if meta.TaskID != result.TaskID {
		t.Fatalf("stored taskId = %q, want %q", meta.TaskID, result.TaskID)
	}
	if meta.HandoffFile != result.HandoffFile {
		t.Fatalf("stored handoffFile = %q, want %q", meta.HandoffFile, result.HandoffFile)
	}
	if got, _ := stored.Runtime[taskConfigKeyAuto].(bool); !got {
		t.Fatalf("stored autoTaskHandoff = %v, want true", stored.Runtime[taskConfigKeyAuto])
	}

	// handoff shell created
	if len(files.upserts) != 1 {
		t.Fatalf("shared file upserts = %d, want 1", len(files.upserts))
	}
	if files.upserts[0].Path != result.HandoffFile {
		t.Fatalf("upsert path = %q, want %q", files.upserts[0].Path, result.HandoffFile)
	}

	// emitUpdated nudge sent so projector refreshes runtime patch
	if len(*emitted) != 1 || (*emitted)[0].ThreadID != "thread-promo" {
		t.Fatalf("emitUpdated = %#v, want one event for thread-promo", *emitted)
	}
	if (*emitted)[0].Model != nil {
		t.Fatalf("emitUpdated.Model = %v, want nil (refresh-only nudge)", *(*emitted)[0].Model)
	}
}

func TestPromoteTaskFromThreadIdempotentWhenAlreadyTask(t *testing.T) {
	t.Parallel()
	rawCfg, err := encodeStoredThreadConfig(storedThreadConfig{
		Runtime: map[string]any{
			taskConfigKeyAuto:        true,
			taskConfigKeyID:          "task_existing",
			taskConfigKeyTitle:       "Existing task",
			taskConfigKeyHandoffFile: defaultTaskHandoffPath("task_existing"),
		},
	})
	if err != nil {
		t.Fatalf("encode pre-existing config: %v", err)
	}
	thread := &threadstore.Thread{
		ThreadID:       "thread-existing",
		Name:           "Already a task",
		ConfigOverride: rawCfg,
	}
	files := &stubSharedFileStore{}
	svc, emitted := promoteTaskTestService(t, thread, files)

	result, err := svc.PromoteTaskFromThread(context.Background(), "thread-existing")
	if err != nil {
		t.Fatalf("PromoteTaskFromThread() error = %v", err)
	}
	if !result.AlreadyTask {
		t.Fatalf("AlreadyTask = false, want true on repeat promote")
	}
	if result.TaskID != "task_existing" {
		t.Fatalf("TaskID = %q, want task_existing", result.TaskID)
	}
	if result.HandoffFile != defaultTaskHandoffPath("task_existing") {
		t.Fatalf("HandoffFile = %q, want default for task_existing", result.HandoffFile)
	}

	// No mutations: thread store untouched, no shell upsert, no emit.
	if got := svc.threadStore.(*stubThreadStore).upsert.ThreadID; got != "" {
		t.Fatalf("threadStore.Upsert called with thread_id=%q, want no upsert on idempotent call", got)
	}
	if len(files.upserts) != 0 {
		t.Fatalf("shared file upserts = %d, want 0 on idempotent call", len(files.upserts))
	}
	if len(*emitted) != 0 {
		t.Fatalf("emitUpdated = %#v, want no events on idempotent call", *emitted)
	}
}

func TestPromoteTaskFromThreadNotFound(t *testing.T) {
	t.Parallel()
	svc := &service{threadStore: &stubThreadStore{}}
	_, err := svc.PromoteTaskFromThread(context.Background(), "missing-thread")
	if err == nil {
		t.Fatalf("err = nil, want error for unknown thread")
	}
}

func TestPromoteTaskFromThreadBlankID(t *testing.T) {
	t.Parallel()
	svc := &service{threadStore: &stubThreadStore{}}
	_, err := svc.PromoteTaskFromThread(context.Background(), "   ")
	if err == nil || !strings.Contains(err.Error(), "threadId") {
		t.Fatalf("err = %v, want error mentioning threadId", err)
	}
}

func TestPromoteTaskFromThreadHandoffShellWarningSoftFails(t *testing.T) {
	t.Parallel()
	thread := &threadstore.Thread{ThreadID: "thread-shellfail", Name: "Soft fail"}
	files := &stubSharedFileStore{upsertErr: errors.New("disk full")}
	svc, emitted := promoteTaskTestService(t, thread, files)

	result, err := svc.PromoteTaskFromThread(context.Background(), "thread-shellfail")
	if err != nil {
		t.Fatalf("PromoteTaskFromThread() error = %v, want soft-fail success", err)
	}
	if result.AlreadyTask {
		t.Fatalf("AlreadyTask = true, want false")
	}
	if result.TaskID == "" {
		t.Fatalf("TaskID empty, want generated")
	}
	if !strings.Contains(result.HandoffShellWarning, "disk full") {
		t.Fatalf("HandoffShellWarning = %q, want to mention disk full", result.HandoffShellWarning)
	}

	// runtime config still persisted (decision a)
	stored := decodeStoredThreadConfig(svc.threadStore.(*stubThreadStore).thread.ConfigOverride)
	if got, _ := stored.Runtime[taskConfigKeyID].(string); got != result.TaskID {
		t.Fatalf("stored taskId = %q, want %q (runtime config must persist on shell soft-fail)", got, result.TaskID)
	}

	// emit still fires so frontend can refresh agentRuntimeById
	if len(*emitted) != 1 {
		t.Fatalf("emitUpdated = %#v, want one event even on shell soft-fail", *emitted)
	}
}

func TestPromoteTaskFromThreadNilEmitUpdatedSafe(t *testing.T) {
	t.Parallel()
	thread := &threadstore.Thread{ThreadID: "thread-nil-emit", Name: "Nil emit safe"}
	svc := &service{
		threadStore: &stubThreadStore{thread: thread},
		sharedFiles: &stubSharedFileStore{},
		// emitUpdated intentionally nil — must not panic
	}
	if _, err := svc.PromoteTaskFromThread(context.Background(), "thread-nil-emit"); err != nil {
		t.Fatalf("PromoteTaskFromThread() error = %v, want no panic when emitUpdated nil", err)
	}
}

func TestPromoteTaskRPCDispatch(t *testing.T) {
	t.Parallel()
	stub := &stubThreadService{
		promoteResult: PromoteTaskResult{
			ThreadID:    "thread-rpc",
			TaskID:      "task_minted",
			TaskTitle:   "From RPC",
			HandoffFile: "handoff/tasks/task_minted.md",
		},
	}
	server := newThreadTestServer(stub)
	raw, err := server.Dispatch(context.Background(), "ui/thread/promote-task", json.RawMessage(`{"threadId":"thread-rpc"}`))
	if err != nil {
		t.Fatalf("dispatch err = %v", err)
	}
	var resp map[string]any
	if jerr := json.Unmarshal(raw, &resp); jerr != nil {
		t.Fatalf("unmarshal: %v", jerr)
	}
	if got, _ := resp["taskId"].(string); got != "task_minted" {
		t.Fatalf("resp.taskId = %q, want task_minted", got)
	}
	if got, _ := resp["task_id"].(string); got != "task_minted" {
		t.Fatalf("resp.task_id (snake alias) = %q, want task_minted", got)
	}
	if got, _ := resp["handoffFile"].(string); got != "handoff/tasks/task_minted.md" {
		t.Fatalf("resp.handoffFile = %q", got)
	}
	if got, _ := resp["alreadyTask"].(bool); got {
		t.Fatalf("resp.alreadyTask = true, want false")
	}
	if stub.promoteThread != "thread-rpc" {
		t.Fatalf("svc.PromoteTaskFromThread called with thread_id=%q, want thread-rpc", stub.promoteThread)
	}
}

func TestPromoteTaskRPCDispatchSnakeCase(t *testing.T) {
	t.Parallel()
	stub := &stubThreadService{
		promoteResult: PromoteTaskResult{ThreadID: "thread-snake", TaskID: "task_x"},
	}
	server := newThreadTestServer(stub)
	// snake-case thread_id alias must also work for symmetry with other RPCs.
	if _, err := server.Dispatch(context.Background(), "ui/thread/promote-task", json.RawMessage(`{"thread_id":"thread-snake"}`)); err != nil {
		t.Fatalf("dispatch err = %v", err)
	}
	if stub.promoteThread != "thread-snake" {
		t.Fatalf("svc.PromoteTaskFromThread called with thread_id=%q, want thread-snake (snake alias parse)", stub.promoteThread)
	}
}

func TestPromoteTaskRPCDispatchSurfacesError(t *testing.T) {
	t.Parallel()
	stub := &stubThreadService{promoteErr: errors.New("not found")}
	server := newThreadTestServer(stub)
	_, err := server.Dispatch(context.Background(), "ui/thread/promote-task", json.RawMessage(`{"threadId":"missing"}`))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("dispatch err = %v, want not-found error", err)
	}
}
