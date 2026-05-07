package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/idgen"
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
	meta.TaskID = idgen.NewID("task")
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

// EnsureHandoffExists verifies that the handoff document for the given
// taskID exists in shared file store. Phase 1.8d fork-pre-check：fork 前
// 一并调 worker.FlushForThread + EnsureHandoffExists 防 "文件不存在 / 陈旧"
// 两类问题（共识 4 修法 4）。
func (s *service) EnsureHandoffExists(ctx context.Context, taskID string) error {
	if s == nil || s.sharedFiles == nil {
		return errors.New("shared files store unavailable")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return errors.New("taskId required")
	}
	p := defaultTaskHandoffPath(taskID)
	if p == "" {
		return fmt.Errorf("invalid handoff path for taskId %q", taskID)
	}
	file, err := s.sharedFiles.Get(ctx, p)
	if err != nil {
		return fmt.Errorf("handoff file %q not found: %w", p, err)
	}
	if file == nil {
		return fmt.Errorf("handoff file %q not found", p)
	}
	return nil
}

// FlushAndVerifyTaskHandoff 实现 Service.FlushAndVerifyTaskHandoff —— Phase
// 1.8d fork 前预检的双保险：先 flush worker pending（等 turn 写盘）然后
// stat handoff 文件存在性。任一失败返回带关键字 "handoff_flush_failed" /
// "handoff_missing" 的 error，让前端 classifyError 识别 permanent 不重试。
func (s *service) FlushAndVerifyTaskHandoff(ctx context.Context, threadID, taskID string) error {
	if s == nil {
		return errors.New("thread service unavailable")
	}
	threadID = strings.TrimSpace(threadID)
	taskID = strings.TrimSpace(taskID)
	if threadID == "" {
		return errors.New("threadId required")
	}
	if taskID == "" {
		return errors.New("taskId required")
	}
	if s.taskHandoffWorker != nil {
		if err := s.taskHandoffWorker.FlushForThread(ctx, threadID); err != nil {
			return fmt.Errorf("handoff_flush_failed: thread %q flush worker: %w", threadID, err)
		}
	}
	if err := s.EnsureHandoffExists(ctx, taskID); err != nil {
		return fmt.Errorf("handoff_missing: %w", err)
	}
	return nil
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

// ---------------------------------------------------------------------------
// Thread handoff (was handoff.go)
// ---------------------------------------------------------------------------

var errHandoffMissingSource = errors.New("thread/handoff: source_thread_id is required")
var errHandoffMissingAgentKey = errors.New("thread/handoff: target agent_key is required")

func (s *service) Handoff(ctx context.Context, req HandoffRequest) (HandoffResult, error) {
	ctx = util.NonNilContext(ctx)

	sourceID := strings.TrimSpace(req.SourceThreadID)
	if sourceID == "" {
		return HandoffResult{}, errHandoffMissingSource
	}
	targetAgentKey := strings.TrimSpace(req.TargetAgentKey)
	if targetAgentKey == "" {
		return HandoffResult{}, errHandoffMissingAgentKey
	}

	source, err := s.loadThreadForHandoff(ctx, sourceID)
	if err != nil {
		return HandoffResult{}, err
	}

	displayName := strings.TrimSpace(req.InitialMessage)
	if displayName == "" {
		displayName = fmt.Sprintf("handoff → %s", targetAgentKey)
	}

	startReq := StartRequest{
		CWD:           source.Cwd,
		Model:         source.Model,
		Provider:      sourceProviderHint(source),
		ParentAgentID: source.ParentAgentID,
		AgentType:     source.AgentType,
		Name:          displayName,
		Prompt:        displayName,
		AgentKey:      targetAgentKey,
		OwnerThreadID: sourceID,
	}

	result, err := s.Start(ctx, startReq)
	if err != nil {
		pkglogger.Warn("thread/handoff: start failed",
			"source_thread_id", sourceID,
			"target_agent_key", targetAgentKey,
			"error", err,
		)
		return HandoffResult{}, err
	}

	pkglogger.Info("thread/handoff: new thread started",
		"source_thread_id", sourceID,
		"new_thread_id", result.ThreadID,
		"target_agent_key", result.AgentKey,
	)

	return HandoffResult{
		SourceThreadID:  sourceID,
		NewThreadID:     result.ThreadID,
		AgentID:         result.AgentID,
		AgentKey:        result.AgentKey,
		PromptKey:       result.PromptKey,
		PromptVersionID: result.PromptVersionID,
		Status:          result.Status,
	}, nil
}

func (s *service) loadThreadForHandoff(ctx context.Context, threadID string) (handoffSource, error) {
	if s == nil || s.threadStore == nil {
		return handoffSource{}, errors.New("thread/handoff: thread store unavailable")
	}
	row, err := s.threadStore.GetByThreadID(ctx, threadID)
	if err != nil {
		return handoffSource{}, fmt.Errorf("thread/handoff: load source: %w", err)
	}
	if row == nil {
		return handoffSource{}, fmt.Errorf("thread/handoff: source thread %q not found", threadID)
	}
	return handoffSource{
		ThreadID:      row.ThreadID,
		Cwd:           row.Cwd,
		Model:         row.Model,
		AgentType:     row.AgentType,
		ParentAgentID: row.ParentAgentID,
		Status:        row.Status,
	}, nil
}

type handoffSource struct {
	ThreadID      string
	Cwd           string
	Model         string
	AgentType     string
	ParentAgentID string
	Status        string
}

func sourceProviderHint(_ handoffSource) string { return "" }
