package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread/handoffrender"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func (s *service) taskHandoffInstructionBlock(ctx context.Context, meta taskHandoffMeta) (string, error) {
	store, path, err := s.requireTaskHandoffFile(meta)
	if err != nil {
		return "", err
	}
	file, err := store.Get(ctx, path)
	if err != nil {
		return "", err
	}
	if file == nil {
		return "", fmt.Errorf("thread task handoff file %q returned no content row", path)
	}
	content := handoffrender.TruncateText(file.Content, taskHandoffReadLimitChars)
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("thread task handoff file %q is empty", path)
	}
	return strings.TrimSpace(strings.Join([]string{
		"## Task Handoff",
		"You are continuing the same automated task. Use the handoff summary below as recent context and verify current repo state before acting on time-sensitive details.",
		content,
	}, "\n\n")), nil
}

func (s *service) requireTaskHandoffFile(meta taskHandoffMeta) (sharedfilestore.Store, string, error) {
	if s == nil {
		return nil, "", errors.New("thread task handoff service is not configured")
	}
	if s.sharedFiles == nil {
		return nil, "", errors.New("thread task handoff shared file store is not configured")
	}
	path := strings.TrimSpace(meta.HandoffFile)
	if path == "" {
		return nil, "", errors.New("thread task handoff file is required")
	}
	return s.sharedFiles, path, nil
}

func (s *service) ensureTaskHandoffShell(ctx context.Context, meta taskHandoffMeta, sourceThreadID string) error {
	store, path, err := s.requireTaskHandoffFile(meta)
	if err != nil {
		return err
	}
	file, err := store.Get(ctx, path)
	if err != nil && !platformdb.IsNotFound(err) {
		return err
	}
	if err == nil && file == nil {
		return fmt.Errorf("thread task handoff file %q returned no content row", path)
	}
	if err == nil && file != nil && strings.TrimSpace(file.Content) != "" {
		return nil
	}
	content := renderTaskHandoffDocument(meta, nil, taskHandoffRenderSeed{
		SourceThreadID: sourceThreadID,
		Status:         "initialized",
		Outcome:        "Task handoff initialized. No completed turns have been recorded yet.",
	}, nil)
	_, err = store.Upsert(ctx, sharedfilestore.UpsertParams{
		Path:      path,
		Content:   content,
		UpdatedBy: systemTaskHandoffUpdatedBy,
	})
	return err
}

func (s *service) refreshTaskHandoffFromThread(ctx context.Context, threadID string, seed taskHandoffRenderSeed) error {
	row, meta, err := s.refreshTaskHandoffState(ctx, threadID)
	if err != nil || meta.TaskID == "" || meta.HandoffFile == "" {
		return taskHandoffRefreshMetaError(err, meta)
	}
	if seed.SourceThreadID == "" {
		seed.SourceThreadID = strings.TrimSpace(threadID)
	}
	history, err := s.readTaskHandoffHistory(ctx, threadID)
	if err != nil {
		return err
	}
	content := renderTaskHandoffDocument(meta, row, seed, history)
	_, err = s.sharedFiles.Upsert(ctx, sharedfilestore.UpsertParams{
		Path:      meta.HandoffFile,
		Content:   content,
		UpdatedBy: systemTaskHandoffUpdatedBy,
	})
	return err
}

func taskHandoffRefreshMetaError(err error, meta taskHandoffMeta) error {
	if err != nil {
		return err
	}
	if meta.TaskID == "" && meta.HandoffFile == "" {
		return nil
	}
	return fmt.Errorf("thread task handoff metadata incomplete: task_id=%q handoff_file=%q", meta.TaskID, meta.HandoffFile)
}

func (s *service) refreshTaskHandoffState(ctx context.Context, threadID string) (*threadstore.Thread, taskHandoffMeta, error) {
	if s == nil {
		return nil, taskHandoffMeta{}, errors.New("thread task handoff service is not configured")
	}
	if s.sharedFiles == nil {
		return nil, taskHandoffMeta{}, errors.New("thread task handoff shared file store is not configured")
	}
	if s.threadStore == nil {
		return nil, taskHandoffMeta{}, errors.New("thread task handoff thread store is not configured")
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, taskHandoffMeta{}, errors.New("thread id is required")
	}
	row, err := s.threadStore.GetByThreadID(ctx, threadID)
	if err != nil {
		return nil, taskHandoffMeta{}, err
	}
	if row == nil {
		return nil, taskHandoffMeta{}, fmt.Errorf("thread %q missing", threadID)
	}
	meta, err := taskHandoffMetaFromThread(row)
	if err != nil {
		return nil, taskHandoffMeta{}, err
	}
	return row, meta, nil
}

func (s *service) readTaskHandoffHistory(ctx context.Context, threadID string) ([]dto.Message, error) {
	if s == nil {
		return nil, errors.New("thread task handoff service is not configured")
	}
	messages, err := s.ReadHistory(ctx, threadID, taskHandoffHistoryLimit)
	if err != nil || len(messages) == 0 {
		return nil, err
	}
	if len(messages) > taskHandoffHistoryLimit {
		messages = messages[len(messages)-taskHandoffHistoryLimit:]
	}
	return messages, nil
}

func renderTaskHandoffDocument(meta taskHandoffMeta, row *threadstore.Thread, seed taskHandoffRenderSeed, messages []dto.Message) string {
	now := time.Now().Format(time.RFC3339)
	status := util.FirstNonEmpty(seed.Status, handoffrender.ThreadStatus(row), "in_progress")
	outcome := util.FirstNonEmpty(seed.Outcome, "No completed turn summary is available yet.")
	lines := []string{
		"# Task Handoff",
		"",
		"- task_id: " + strings.TrimSpace(meta.TaskID),
		"- task_title: " + util.FirstNonEmpty(meta.TaskTitle, meta.TaskID),
		"- updated_at: " + now,
		"- status: " + status,
		"- source: system_handoff",
	}
	if sourceThreadID := util.FirstNonEmpty(seed.SourceThreadID, handoffrender.ThreadID(row)); sourceThreadID != "" {
		lines = append(lines, "- source_thread_id: "+sourceThreadID)
	}
	if cwd := handoffrender.ThreadCWD(row); cwd != "" {
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
			role := util.FirstNonEmpty(strings.TrimSpace(msg.Role), "message")
			lines = append(lines, "- "+role+": "+handoffrender.TruncateText(handoffrender.NormalizeText(msg.Content), 320))
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
	if taskID := strings.TrimSpace(meta.TaskID); taskID != "" {
		lines = append(lines,
			"",
			"## Long-running Progress Protocol",
			"- 进展上报：每推进一步，请用 shared_file_write 向 `_internal/progress/"+taskID+".md` 追加一行（建议：ISO 时间戳 + 一句话描述）。",
			"- 完成标记：任务真正完成时，写一份 `_internal/done/"+taskID+".md`（任意非空内容，系统仅检测文件存在）。",
			"- 作用：前端 watchdog 据此识别「长任务还在推进」——progress 增长会重置自动续命累计上限，done 出现会终止自动续命。不遵守本协议仅退化为旧上限逻辑，不会让系统出错。",
		)
	}
	return handoffrender.TruncateText(strings.Join(lines, "\n"), taskHandoffContentLimit)
}

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
		Outcome:        handoffrender.TruncateText(util.FirstNonEmpty(ev.Summary, ev.Result, ev.Message), 1200),
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
