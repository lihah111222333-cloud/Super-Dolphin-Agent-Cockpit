package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	"github.com/anthropic-ai/super-agent-v3/internal/util/idgen"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// PromoteTaskFromThread implements Service.PromoteTaskFromThread (Phase 2.1).
// Promotes a normal thread to a task thread by writing autoTaskHandoff +
// taskId + taskTitle + handoffFile into the thread's stored runtime config
// (decodeStoredThreadConfig.Runtime). The next sidebar projection picks up
// the new fields via applyTaskRuntimeToThreadRuntime, so the frontend
// agentRuntimeById[threadId].taskId becomes non-empty and useAutoContinue /
// useThreadWatchdog start treating the thread as a task. After the runtime
// config is persisted we initialize the handoff document via
// ensureTaskHandoffShell and emit a thread/updated event so the projector
// refreshes the patch immediately rather than waiting for the next natural
// event.
//
// Idempotency: when the thread already carries a non-empty taskId in its
// stored runtime config we return the existing fields with AlreadyTask=true
// and skip every mutation (including the handoff shell write — the file is
// either already there or will be backfilled by Phase 1.8d).
//
// Failure of ensureTaskHandoffShell is non-fatal: runtime config is the
// source of truth (the thread *is* a task once we wrote those fields), so we
// keep the promotion and surface the warning via PromoteTaskResult.
// HandoffShellWarning. Phase 1.8d FlushAndVerifyTaskHandoff is the eventual
// safety net.
func (s *service) PromoteTaskFromThread(ctx context.Context, threadID string) (PromoteTaskResult, error) {
	if s == nil {
		return PromoteTaskResult{}, errors.New("thread service unavailable")
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return PromoteTaskResult{}, errors.New("threadId required")
	}
	thread, err := s.getThread(ctx, threadID)
	if err != nil {
		return PromoteTaskResult{}, err
	}
	if thread == nil {
		return PromoteTaskResult{}, fmt.Errorf("thread %q not found", threadID)
	}

	stored := decodeStoredThreadConfig(thread.ConfigOverride)
	existing := taskHandoffMetaFromRuntimeConfig(stored.Runtime)
	if strings.TrimSpace(existing.TaskID) != "" {
		// Already a task thread: hand back what's there. Do NOT touch the
		// handoff shell — if it's missing 1.8d FlushAndVerify will catch it,
		// and we don't want repeated promote calls to overwrite a file that
		// the worker may already be appending to.
		return PromoteTaskResult{
			ThreadID:    threadID,
			TaskID:      strings.TrimSpace(existing.TaskID),
			TaskTitle:   strings.TrimSpace(existing.TaskTitle),
			HandoffFile: strings.TrimSpace(existing.HandoffFile),
			AlreadyTask: true,
		}, nil
	}

	meta := taskHandoffMeta{
		TaskID:    idgen.NewID("task"),
		TaskTitle: firstNonEmptyTaskString(strings.TrimSpace(thread.Name), strings.TrimSpace(thread.Prompt), threadID),
	}
	meta.HandoffFile = defaultTaskHandoffPath(meta.TaskID)
	meta.RootTaskID = meta.TaskID

	stored.Runtime = withPromotedTaskRuntime(stored.Runtime, meta)
	raw, err := encodeStoredThreadConfig(stored)
	if err != nil {
		return PromoteTaskResult{}, fmt.Errorf("encode promoted thread config: %w", err)
	}
	thread.ConfigOverride = raw
	thread.UpdatedAt = time.Now().Unix()
	if err := s.upsertThread(ctx, *thread); err != nil {
		return PromoteTaskResult{}, fmt.Errorf("persist promoted thread config: %w", err)
	}

	result := PromoteTaskResult{
		ThreadID:    threadID,
		TaskID:      meta.TaskID,
		TaskTitle:   meta.TaskTitle,
		HandoffFile: meta.HandoffFile,
	}

	if shellErr := s.ensureTaskHandoffShell(ctx, meta, threadID); shellErr != nil {
		// Soft-fail: keep the promotion (runtime config is already persisted)
		// and surface the warning so the caller can decide what to log /
		// show. 1.8d FlushAndVerify will eventually retry the handoff path.
		pkglogger.Warn("thread/promote-task: handoff shell init failed",
			"thread_id", threadID,
			"task_id", meta.TaskID,
			"handoff_file", meta.HandoffFile,
			"error", shellErr)
		result.HandoffShellWarning = shellErr.Error()
	}

	s.emitThreadPromotedTask(threadID)
	return result, nil
}

// withPromotedTaskRuntime returns a copy of runtime with the four task
// fields set. Existing keys are preserved (autoTaskHandoff is force-set to
// true since that is the whole point of promote). Snake-case aliases are
// not written: the read path (taskHandoffMetaFromRuntimeConfig +
// applyTaskRuntimeToThreadRuntime) accepts either, and the write path in
// applyTaskHandoffConfig also writes camelCase.
func withPromotedTaskRuntime(runtime map[string]any, meta taskHandoffMeta) map[string]any {
	next := clone.RuntimeConfigMap(runtime)
	if next == nil {
		next = map[string]any{}
	}
	next[taskConfigKeyAuto] = true
	next[taskConfigKeyID] = meta.TaskID
	if meta.TaskTitle != "" {
		next[taskConfigKeyTitle] = meta.TaskTitle
	}
	if meta.HandoffFile != "" {
		next[taskConfigKeyHandoffFile] = meta.HandoffFile
	}
	if meta.RootTaskID != "" {
		next[taskConfigKeyRoot] = meta.RootTaskID
	}
	return next
}
