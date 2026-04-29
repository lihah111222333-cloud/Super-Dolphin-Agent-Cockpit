package thread

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	taskConfigKeyID               = "taskId"
	taskConfigKeyIDSnake          = "task_id"
	taskConfigKeyTitle            = "taskTitle"
	taskConfigKeyTitleSnake       = "task_title"
	taskConfigKeyHandoffFile      = "handoffFile"
	taskConfigKeyHandoffFileSnake = "handoff_file"
	taskConfigKeyContinue         = "continueTask"
	taskConfigKeyContinueSnake    = "continue_task"
	taskConfigKeyAuto             = "autoTaskHandoff"
	taskConfigKeyAutoSnake        = "auto_task_handoff"
	taskConfigKeyRoot             = "rootTaskId"
	taskConfigKeyRootSnake        = "root_task_id"
	systemTaskHandoffUpdatedBy    = "system_handoff"
	taskHandoffPrefix             = "handoff/tasks/"
	taskHandoffReadLimitChars     = 4096
	taskHandoffHistoryLimit       = 6
	taskHandoffContentLimit       = 8192
)

type taskHandoffMeta struct {
	TaskID      string
	TaskTitle   string
	HandoffFile string
	Continue    bool
	RootTaskID  string
}

type taskHandoffRenderSeed struct {
	SourceThreadID string
	Outcome        string
	Status         string
}

func (s *service) prepareTaskHandoffStart(ctx context.Context, req *StartRequest) error {
	if s == nil || req == nil {
		return nil
	}
	meta, sourceThreadID := s.resolveTaskHandoffStart(ctx, req)
	if meta.TaskID == "" {
		return nil
	}
	if meta.RootTaskID == "" {
		if sourceThreadID != "" {
			meta.RootTaskID = s.resolveRootTaskId(ctx, sourceThreadID)
		}
		if meta.RootTaskID == "" {
			meta.RootTaskID = meta.TaskID
		}
	}
	applyTaskHandoffConfig(req, meta)
	inheritTaskHandoffOwner(req, sourceThreadID)
	if s.sharedFiles == nil || meta.HandoffFile == "" {
		return nil
	}
	s.logIgnoredTaskHandoffError("ensure task handoff shell", sourceThreadID, s.ensureTaskHandoffShell(ctx, meta, sourceThreadID))
	return s.appendTaskHandoffInstructions(ctx, req, meta, sourceThreadID)
}

func (s *service) resolveTaskHandoffStart(ctx context.Context, req *StartRequest) (taskHandoffMeta, string) {
	explicit := taskHandoffMetaFromRuntimeConfig(req.Config)
	sourceThreadID := s.resolveTaskHandoffSourceThread(ctx, req)
	inherited := s.loadInheritedTaskHandoff(ctx, sourceThreadID)
	meta := mergeTaskHandoffStart(*req, explicit, inherited, sourceThreadID)
	if meta.TaskID == "" {
		return taskHandoffMeta{}, ""
	}
	return finalizeTaskHandoffStart(meta), sourceThreadID
}

func applyTaskHandoffConfig(req *StartRequest, meta taskHandoffMeta) {
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	req.Config[taskConfigKeyID] = meta.TaskID
	req.Config[taskConfigKeyTitle] = meta.TaskTitle
	req.Config[taskConfigKeyHandoffFile] = meta.HandoffFile
	if meta.Continue {
		req.Config[taskConfigKeyContinue] = true
	}
	if meta.RootTaskID != "" {
		req.Config[taskConfigKeyRoot] = meta.RootTaskID
	}
}

func inheritTaskHandoffOwner(req *StartRequest, sourceThreadID string) {
	if sourceThreadID != "" && strings.TrimSpace(req.OwnerThreadID) == "" {
		req.OwnerThreadID = sourceThreadID
	}
}

func (s *service) appendTaskHandoffInstructions(
	ctx context.Context,
	req *StartRequest,
	meta taskHandoffMeta,
	sourceThreadID string,
) error {
	if !meta.Continue {
		return nil
	}
	block, err := s.taskHandoffInstructionBlock(ctx, meta)
	if err != nil {
		s.logIgnoredTaskHandoffError("load task handoff block", sourceThreadID, err)
		return nil
	}
	req.BaseInstructions = joinTaskHandoffInstructions(req.BaseInstructions, block)
	return nil
}

func (s *service) resolveTaskHandoffSourceThread(ctx context.Context, req *StartRequest) string {
	sourceThreadID := strings.TrimSpace(req.OwnerThreadID)
	if sourceThreadID != "" || s == nil || s.bindingStore == nil {
		return sourceThreadID
	}
	parentAgentID := strings.TrimSpace(req.ParentAgentID)
	if parentAgentID == "" {
		return ""
	}
	threadID, err := s.bindingStore.GetThreadByAgent(ctx, parentAgentID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(threadID)
}

func (s *service) loadInheritedTaskHandoff(ctx context.Context, sourceThreadID string) taskHandoffMeta {
	if sourceThreadID == "" || s == nil || s.threadStore == nil {
		return taskHandoffMeta{}
	}
	row, err := s.threadStore.GetByThreadID(ctx, sourceThreadID)
	if err != nil || row == nil {
		return taskHandoffMeta{}
	}
	return taskHandoffMetaFromThread(row)
}

// resolveRootTaskId 沿 OwnerThreadID 链反查到顶端 thread，返回其 taskId（= rootTaskId）。
// 顶端 = 第一个没有 OwnerThreadID 的 thread。深度上限 10 防循环 / 异常长链。
// 任何错误（空入参 / store 错 / 顶端无 taskId / 深度超限）一律返回空字符串。
func (s *service) resolveRootTaskId(ctx context.Context, ownerThreadID string) string {
	if s == nil || s.threadStore == nil {
		return ""
	}
	cur := strings.TrimSpace(ownerThreadID)
	for i := 0; i < 10; i++ {
		if cur == "" {
			return ""
		}
		row, err := s.threadStore.GetByThreadID(ctx, cur)
		if err != nil || row == nil {
			return ""
		}
		nextOwner := strings.TrimSpace(row.OwnerThreadID)
		if nextOwner == "" {
			stored := decodeStoredThreadConfig(row.ConfigOverride)
			return firstConfigString(stored.Runtime, taskConfigKeyID, taskConfigKeyIDSnake)
		}
		cur = nextOwner
	}
	return ""
}

// backfillResumeRootTaskId: lifecycle.Resume 兼容 4.1c 之前创建的旧 task thread。
// 若 ConfigOverride.Runtime 已有 taskId 但缺 rootTaskId，按 ownerThreadID 链反查或
// fallback 自身 taskId 补回字段并重新编码 raw。任何错误一律返回原 raw。
func (s *service) backfillResumeRootTaskId(ctx context.Context, ownerThreadID string, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	stored := decodeStoredThreadConfig(raw)
	taskID := firstConfigString(stored.Runtime, taskConfigKeyID, taskConfigKeyIDSnake)
	if taskID == "" {
		return raw
	}
	if rootID := firstConfigString(stored.Runtime, taskConfigKeyRoot, taskConfigKeyRootSnake); rootID != "" {
		return raw
	}
	rootTaskID := ""
	if owner := strings.TrimSpace(ownerThreadID); owner != "" {
		rootTaskID = s.resolveRootTaskId(ctx, owner)
	}
	if rootTaskID == "" {
		rootTaskID = taskID
	}
	if stored.Runtime == nil {
		stored.Runtime = map[string]any{}
	}
	stored.Runtime[taskConfigKeyRoot] = rootTaskID
	encoded, err := encodeStoredThreadConfig(stored)
	if err != nil {
		return raw
	}
	return encoded
}

func mergeTaskHandoffStart(
	req StartRequest,
	explicit taskHandoffMeta,
	inherited taskHandoffMeta,
	sourceThreadID string,
) taskHandoffMeta {
	meta := explicit
	meta.TaskID = firstNonEmptyTaskString(meta.TaskID, inherited.TaskID)
	meta.TaskTitle = resolveTaskHandoffTitle(req, meta.TaskTitle, inherited.TaskTitle)
	meta.HandoffFile = resolveTaskHandoffFile(meta.TaskID, meta.HandoffFile, inherited.HandoffFile)
	meta.Continue = meta.Continue || shouldContinueTaskHandoff(sourceThreadID, meta.TaskID, inherited.TaskID)
	meta.RootTaskID = firstNonEmptyTaskString(meta.RootTaskID, inherited.RootTaskID)
	return autoTaskHandoffMeta(req, meta)
}

func resolveTaskHandoffTitle(req StartRequest, explicitTitle, inheritedTitle string) string {
	return firstNonEmptyTaskString(explicitTitle, inheritedTitle, defaultTaskTitle(req))
}

func resolveTaskHandoffFile(taskID, explicitFile, inheritedFile string) string {
	if explicitFile != "" {
		return explicitFile
	}
	if taskID == "" {
		return ""
	}
	return firstNonEmptyTaskString(inheritedFile, defaultTaskHandoffPath(taskID))
}

func shouldContinueTaskHandoff(sourceThreadID, taskID, inheritedTaskID string) bool {
	return sourceThreadID != "" && taskID != "" && taskID == inheritedTaskID
}

func autoTaskHandoffMeta(req StartRequest, meta taskHandoffMeta) taskHandoffMeta {
	if meta.TaskID != "" || !shouldAutoTaskHandoff(req) {
		return meta
	}
	meta.TaskID = shared.NewID("task")
	meta.TaskTitle = firstNonEmptyTaskString(meta.TaskTitle, defaultTaskTitle(req))
	meta.HandoffFile = defaultTaskHandoffPath(meta.TaskID)
	return meta
}

func finalizeTaskHandoffStart(meta taskHandoffMeta) taskHandoffMeta {
	meta.TaskTitle = firstNonEmptyTaskString(meta.TaskTitle, meta.TaskID)
	meta.HandoffFile = firstNonEmptyTaskString(meta.HandoffFile, defaultTaskHandoffPath(meta.TaskID))
	return meta
}

func taskHandoffMetaFromThread(row *threadstore.Thread) taskHandoffMeta {
	if row == nil {
		return taskHandoffMeta{}
	}
	stored := decodeStoredThreadConfig(row.ConfigOverride)
	meta := taskHandoffMetaFromRuntimeConfig(stored.Runtime)
	meta.TaskTitle = firstNonEmptyTaskString(meta.TaskTitle, strings.TrimSpace(row.Prompt))
	if meta.HandoffFile == "" && meta.TaskID != "" {
		meta.HandoffFile = defaultTaskHandoffPath(meta.TaskID)
	}
	return meta
}

func taskHandoffMetaFromRuntimeConfig(cfg map[string]any) taskHandoffMeta {
	return taskHandoffMeta{
		TaskID:      firstConfigString(cfg, taskConfigKeyID, taskConfigKeyIDSnake),
		TaskTitle:   firstConfigString(cfg, taskConfigKeyTitle, taskConfigKeyTitleSnake),
		HandoffFile: normalizeTaskHandoffPath(firstConfigString(cfg, taskConfigKeyHandoffFile, taskConfigKeyHandoffFileSnake)),
		Continue:    firstConfigBool(cfg, taskConfigKeyContinue, taskConfigKeyContinueSnake),
		RootTaskID:  firstConfigString(cfg, taskConfigKeyRoot, taskConfigKeyRootSnake),
	}
}

func shouldAutoTaskHandoff(req StartRequest) bool {
	if firstConfigBool(req.Config, taskConfigKeyAuto, taskConfigKeyAutoSnake) {
		return true
	}
	return strings.TrimSpace(req.ParentAgentID) != "" ||
		strings.TrimSpace(req.OwnerThreadID) != "" ||
		strings.TrimSpace(req.AgentType) != ""
}

func defaultTaskTitle(req StartRequest) string {
	return firstNonEmptyTaskString(strings.TrimSpace(req.Name), strings.TrimSpace(req.Prompt), strings.TrimSpace(req.AgentKey), strings.TrimSpace(req.AgentType), "Automated Task")
}

func defaultTaskHandoffPath(taskID string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ""
	}
	return taskHandoffPrefix + path.Base(taskID) + ".md"
}

func normalizeTaskHandoffPath(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" {
		return ""
	}
	cleaned := path.Clean(raw)
	return strings.TrimPrefix(cleaned, "/")
}

func firstConfigString(cfg map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if typed = strings.TrimSpace(typed); typed != "" {
				return typed
			}
		}
	}
	return ""
}

func firstConfigBool(cfg map[string]any, keys ...string) bool {
	for _, key := range keys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		flag, ok := value.(bool)
		if ok {
			return flag
		}
	}
	return false
}

func firstNonEmptyTaskString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

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
