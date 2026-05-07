package thread

import (
	"context"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func (s *service) taskHandoffInstructionBlock(ctx context.Context, meta taskHandoffMeta) (string, error) {
	if s == nil || s.sharedFiles == nil || meta.HandoffFile == "" {
		return "", nil
	}
	file, err := s.sharedFiles.Get(ctx, meta.HandoffFile)
	if err != nil || file == nil {
		return "", err
	}
	content := truncateTaskHandoffText(file.Content, taskHandoffReadLimitChars)
	if strings.TrimSpace(content) == "" {
		return "", nil
	}
	return strings.TrimSpace(strings.Join([]string{
		"## Task Handoff",
		"You are continuing the same automated task. Use the handoff summary below as recent context and verify current repo state before acting on time-sensitive details.",
		content,
	}, "\n\n")), nil
}

func joinTaskHandoffInstructions(base, block string) string {
	base = strings.TrimSpace(base)
	block = strings.TrimSpace(block)
	switch {
	case base == "":
		return block
	case block == "":
		return base
	default:
		return base + "\n\n" + block
	}
}

func (s *service) ensureTaskHandoffShell(ctx context.Context, meta taskHandoffMeta, sourceThreadID string) error {
	if s == nil || s.sharedFiles == nil || meta.HandoffFile == "" {
		return nil
	}
	if file, err := s.sharedFiles.Get(ctx, meta.HandoffFile); err == nil && file != nil && strings.TrimSpace(file.Content) != "" {
		return nil
	}
	content := renderTaskHandoffDocument(meta, nil, taskHandoffRenderSeed{
		SourceThreadID: sourceThreadID,
		Status:         "initialized",
		Outcome:        "Task handoff initialized. No completed turns have been recorded yet.",
	}, nil)
	_, err := s.sharedFiles.Upsert(ctx, sharedfilestore.UpsertParams{
		Path:      meta.HandoffFile,
		Content:   content,
		UpdatedBy: systemTaskHandoffUpdatedBy,
	})
	return err
}

func (s *service) refreshTaskHandoffFromThread(ctx context.Context, threadID string, seed taskHandoffRenderSeed) error {
	if s == nil || s.sharedFiles == nil || s.threadStore == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	row, err := s.threadStore.GetByThreadID(ctx, threadID)
	if err != nil || row == nil {
		return err
	}
	meta := taskHandoffMetaFromThread(row)
	if meta.TaskID == "" || meta.HandoffFile == "" {
		return nil
	}
	if seed.SourceThreadID == "" {
		seed.SourceThreadID = threadID
	}
	history := s.readTaskHandoffHistory(ctx, threadID)
	content := renderTaskHandoffDocument(meta, row, seed, history)
	_, err = s.sharedFiles.Upsert(ctx, sharedfilestore.UpsertParams{
		Path:      meta.HandoffFile,
		Content:   content,
		UpdatedBy: systemTaskHandoffUpdatedBy,
	})
	return err
}

func (s *service) readTaskHandoffHistory(ctx context.Context, threadID string) []dto.Message {
	if s == nil {
		return nil
	}
	messages, err := s.ReadHistory(ctx, threadID, taskHandoffHistoryLimit)
	if err != nil || len(messages) == 0 {
		return nil
	}
	if len(messages) > taskHandoffHistoryLimit {
		messages = messages[len(messages)-taskHandoffHistoryLimit:]
	}
	return messages
}

func renderTaskHandoffDocument(meta taskHandoffMeta, row *threadstore.Thread, seed taskHandoffRenderSeed, messages []dto.Message) string {
	now := time.Now().Format(time.RFC3339)
	status := firstNonEmptyTaskString(seed.Status, threadStatusForHandoff(row), "in_progress")
	outcome := firstNonEmptyTaskString(seed.Outcome, "No completed turn summary is available yet.")
	lines := []string{
		"# Task Handoff",
		"",
		"- task_id: " + strings.TrimSpace(meta.TaskID),
		"- task_title: " + firstNonEmptyTaskString(meta.TaskTitle, meta.TaskID),
		"- updated_at: " + now,
		"- status: " + status,
		"- source: system_handoff",
	}
	if sourceThreadID := firstNonEmptyTaskString(seed.SourceThreadID, threadIDForHandoff(row)); sourceThreadID != "" {
		lines = append(lines, "- source_thread_id: "+sourceThreadID)
	}
	if cwd := threadCWDForHandoff(row); cwd != "" {
		lines = append(lines, "- cwd: "+cwd)
	}
	lines = append(lines,
		"",
		"## Latest Outcome",
		outcome,
		"",
		"## Recent Context",
	)
	if len(messages) == 0 {
		lines = append(lines, "- No recent message history is available.")
	} else {
		for _, msg := range messages {
			role := firstNonEmptyTaskString(strings.TrimSpace(msg.Role), "message")
			lines = append(lines, "- "+role+": "+truncateTaskHandoffText(normalizeTaskHandoffText(msg.Content), 320))
		}
	}
	lines = append(lines,
		"",
		"## Next",
		"- Continue this task using the same task_id and verify current project state before acting on time-sensitive details.",
		"",
		"## Risks",
		"- This handoff is auto-generated from recent thread activity and may omit earlier context. Re-open the source thread when the next step depends on older details.",
	)
	// Phase 3.10a: 长任务进度协议。仅在 task_id 非空时追加，避免出现 _internal/progress/.md 这种坏路径。
	if taskID := strings.TrimSpace(meta.TaskID); taskID != "" {
		lines = append(lines,
			"",
			"## Long-running Progress Protocol",
			"- 进展上报：每推进一步，请用 shared_file_write 向 `_internal/progress/"+taskID+".md` 追加一行（建议：ISO 时间戳 + 一句话描述）。",
			"- 完成标记：任务真正完成时，写一份 `_internal/done/"+taskID+".md`（任意非空内容，系统仅检测文件存在）。",
			"- 作用：前端 watchdog 据此识别「长任务还在推进」——progress 增长会重置自动续命累计上限，done 出现会终止自动续命。不遵守本协议仅退化为旧上限逻辑，不会让系统出错。",
		)
	}
	return truncateTaskHandoffText(strings.Join(lines, "\n"), taskHandoffContentLimit)
}

func threadStatusForHandoff(row *threadstore.Thread) string {
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row.Status)
}

func threadIDForHandoff(row *threadstore.Thread) string {
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row.ThreadID)
}

func threadCWDForHandoff(row *threadstore.Thread) string {
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row.Cwd)
}

func normalizeTaskHandoffText(raw string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n")), " ")
}

func truncateTaskHandoffText(raw string, limit int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || limit <= 0 {
		return ""
	}
	runes := []rune(raw)
	if len(runes) <= limit {
		return raw
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

func latestTurnOutcome(ev turndto.TurnCompleted) string {
	return truncateTaskHandoffText(firstNonEmptyTaskString(ev.Summary, ev.Result, ev.Message), 1200)
}

// onTurnCompleted is the bus callback for turndto.TurnCompleted. P22 P2
// thread S3: the callback is cheap Enqueue only. The taskHandoffWorker
// owns the refreshTaskHandoffFromThread slow-path (threadStore read +
// document render + sharedFiles.Upsert) and runs it off the dispatcher
// goroutine. Multiple events for the same threadID coalesce to the
// latest seed (last-write-wins matches pre-P22 behavior).
func (s *service) onTurnCompleted(ev turndto.TurnCompleted) {
	if s == nil || s.taskHandoffWorker == nil || s.sharedFiles == nil || !ev.Success {
		return
	}
	threadID := strings.TrimSpace(ev.ThreadID)
	if threadID == "" {
		return
	}
	s.taskHandoffWorker.Enqueue(threadID, taskHandoffRenderSeed{
		SourceThreadID: threadID,
		Status:         strings.TrimSpace(ev.Status),
		Outcome:        latestTurnOutcome(ev),
	})
}

func (s *service) logIgnoredTaskHandoffError(action, threadID string, err error) {
	if err == nil {
		return
	}
	logger := pkglogger.Get()
	if s != nil && s.logger != nil {
		logger = s.logger
	}
	logger.Warn("thread: task handoff operation failed", "action", action, "thread_id", strings.TrimSpace(threadID), "error", err)
}
