package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/configutil"
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
	TaskID, TaskTitle, HandoffFile, RootTaskID string
	Continue                                   bool
}

type taskHandoffRenderSeed struct{ SourceThreadID, Outcome, Status string }

func (s *service) prepareTaskHandoffStart(ctx context.Context, req *StartRequest) error {
	if s == nil || req == nil {
		return nil
	}
	meta, sourceThreadID, err := s.resolveTaskHandoffStart(ctx, req)
	if err != nil || meta.TaskID == "" {
		return err
	}
	meta, err = s.withTaskHandoffRoot(ctx, meta, sourceThreadID)
	if err != nil {
		return err
	}
	applyTaskHandoffConfig(req, meta)
	inheritTaskHandoffOwner(req, sourceThreadID)
	return s.prepareTaskHandoffSharedFile(ctx, req, meta, sourceThreadID)
}

func (s *service) withTaskHandoffRoot(ctx context.Context, meta taskHandoffMeta, sourceThreadID string) (taskHandoffMeta, error) {
	if meta.RootTaskID != "" {
		return meta, nil
	}
	if sourceThreadID != "" {
		rootTaskID, err := s.resolveRootTaskId(ctx, sourceThreadID)
		if err != nil {
			return taskHandoffMeta{}, err
		}
		meta.RootTaskID = rootTaskID
	}
	if meta.RootTaskID == "" {
		meta.RootTaskID = meta.TaskID
	}
	return meta, nil
}

func (s *service) prepareTaskHandoffSharedFile(ctx context.Context, req *StartRequest, meta taskHandoffMeta, sourceThreadID string) error {
	if meta.HandoffFile == "" {
		return nil
	}
	if s.sharedFiles == nil {
		return errors.New("shared files store unavailable for task handoff")
	}
	if err := s.ensureTaskHandoffShell(ctx, meta, sourceThreadID); err != nil {
		return err
	}
	return s.appendTaskHandoffInstructions(ctx, req, meta, sourceThreadID)
}

func (s *service) resolveTaskHandoffStart(ctx context.Context, req *StartRequest) (taskHandoffMeta, string, error) {
	explicit, err := taskHandoffMetaFromRuntimeConfig(req.Config)
	if err != nil {
		return taskHandoffMeta{}, "", err
	}
	sourceThreadID, err := s.resolveTaskHandoffSourceThread(ctx, req)
	if err != nil {
		return taskHandoffMeta{}, "", err
	}
	inherited, err := s.loadInheritedTaskHandoff(ctx, sourceThreadID)
	if err != nil {
		return taskHandoffMeta{}, "", err
	}
	meta, err := mergeTaskHandoffStart(*req, explicit, inherited, sourceThreadID)
	if err != nil {
		return taskHandoffMeta{}, "", err
	}
	if meta.TaskID == "" {
		return taskHandoffMeta{}, "", nil
	}
	if err := validateTaskHandoffMeta(meta); err != nil {
		return taskHandoffMeta{}, "", err
	}
	return meta, sourceThreadID, nil
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
		return err
	}
	base := strings.TrimSpace(req.BaseInstructions)
	block = strings.TrimSpace(block)
	if base == "" {
		req.BaseInstructions = block
	} else if block != "" {
		req.BaseInstructions = base + "\n\n" + block
	}
	return nil
}

func (s *service) resolveTaskHandoffSourceThread(ctx context.Context, req *StartRequest) (string, error) {
	sourceThreadID := strings.TrimSpace(req.OwnerThreadID)
	if sourceThreadID != "" || s == nil || s.bindingStore == nil {
		return sourceThreadID, nil
	}
	parentAgentID := strings.TrimSpace(req.ParentAgentID)
	if parentAgentID == "" {
		return "", nil
	}
	threadID, err := s.bindingStore.GetThreadByAgent(ctx, parentAgentID)
	if err != nil {
		return "", err
	}
	if threadID = strings.TrimSpace(threadID); threadID == "" {
		return "", fmt.Errorf("parent agent %q has no source thread", parentAgentID)
	}
	return threadID, nil
}

func (s *service) loadInheritedTaskHandoff(ctx context.Context, sourceThreadID string) (taskHandoffMeta, error) {
	if sourceThreadID == "" {
		return taskHandoffMeta{}, nil
	}
	if s == nil || s.threadStore == nil {
		return taskHandoffMeta{}, errors.New("thread store is not configured for task handoff inheritance")
	}
	row, err := s.threadStore.GetByThreadID(ctx, sourceThreadID)
	if err != nil {
		return taskHandoffMeta{}, err
	}
	if row == nil {
		return taskHandoffMeta{}, fmt.Errorf("task handoff source thread %q missing", sourceThreadID)
	}
	return taskHandoffMetaFromThread(row)
}

func (s *service) resolveRootTaskId(ctx context.Context, ownerThreadID string) (string, error) {
	if s == nil || s.threadStore == nil {
		return "", errors.New("thread store is not configured")
	}
	cur := strings.TrimSpace(ownerThreadID)
	for i := 0; i < 10; i++ {
		if cur == "" {
			return "", errors.New("owner thread id is required")
		}
		row, err := s.threadStore.GetByThreadID(ctx, cur)
		if err != nil {
			return "", err
		}
		if row == nil {
			return "", fmt.Errorf("owner thread %q missing", cur)
		}
		nextOwner := strings.TrimSpace(row.OwnerThreadID)
		if nextOwner == "" {
			return rootTaskIDFromThread(row, cur)
		}
		cur = nextOwner
	}
	return "", errors.New("task handoff root task chain exceeds depth limit")
}

func rootTaskIDFromThread(row *threadstore.Thread, threadID string) (string, error) {
	stored, err := decodeStoredThreadConfig(row.ConfigOverride)
	if err != nil {
		return "", err
	}
	rootTaskID, err := configutil.StrictString(stored.Runtime, "task handoff config", taskConfigKeyID, taskConfigKeyIDSnake)
	if err != nil {
		return "", err
	}
	if rootTaskID == "" {
		return "", fmt.Errorf("root task id missing on thread %q", threadID)
	}
	return rootTaskID, nil
}

func (s *service) backfillResumeRootTaskId(ctx context.Context, ownerThreadID string, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	stored, err := decodeStoredThreadConfig(raw)
	if err != nil {
		return nil, err
	}
	taskID, err := configutil.StrictString(stored.Runtime, "task handoff config", taskConfigKeyID, taskConfigKeyIDSnake)
	if err != nil {
		return nil, err
	}
	rootID, err := configutil.StrictString(stored.Runtime, "task handoff config", taskConfigKeyRoot, taskConfigKeyRootSnake)
	if err != nil {
		return nil, err
	}
	if taskID == "" {
		return raw, nil
	}
	if rootID != "" {
		return raw, nil
	}
	rootTaskID, err := s.resolveBackfillRootTaskID(ctx, ownerThreadID, taskID)
	if err != nil {
		return nil, err
	}
	if stored.Runtime == nil {
		stored.Runtime = map[string]any{}
	}
	stored.Runtime[taskConfigKeyRoot] = rootTaskID
	encoded, err := encodeStoredThreadConfig(stored)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func (s *service) resolveBackfillRootTaskID(ctx context.Context, ownerThreadID, taskID string) (string, error) {
	owner := strings.TrimSpace(ownerThreadID)
	if owner == "" {
		return "", fmt.Errorf("root task id missing for task %q", taskID)
	}
	rootTaskID, err := s.resolveRootTaskId(ctx, owner)
	if err != nil {
		return "", err
	}
	if rootTaskID == "" {
		return "", fmt.Errorf("root task id missing for task %q", taskID)
	}
	return rootTaskID, nil
}

func mergeTaskHandoffStart(
	req StartRequest,
	explicit taskHandoffMeta,
	inherited taskHandoffMeta,
	sourceThreadID string,
) (taskHandoffMeta, error) {
	meta := explicit
	meta.TaskID = util.FirstNonEmpty(meta.TaskID, inherited.TaskID)
	meta.TaskTitle = util.FirstNonEmpty(meta.TaskTitle, inherited.TaskTitle, defaultTaskTitle(req))
	if meta.TaskID == "" {
		meta.HandoffFile = ""
	} else {
		meta.HandoffFile = util.FirstNonEmpty(meta.HandoffFile, inherited.HandoffFile)
	}
	meta.Continue = meta.Continue || (sourceThreadID != "" && meta.TaskID != "" && meta.TaskID == inherited.TaskID)
	meta.RootTaskID = util.FirstNonEmpty(meta.RootTaskID, inherited.RootTaskID)
	return autoTaskHandoffMeta(req, meta)
}

func autoTaskHandoffMeta(req StartRequest, meta taskHandoffMeta) (taskHandoffMeta, error) {
	auto, err := shouldAutoTaskHandoff(req)
	if err != nil {
		return taskHandoffMeta{}, err
	}
	if meta.TaskID != "" || !auto {
		return meta, nil
	}
	meta.TaskID = idgen.NewID("task")
	meta.TaskTitle = util.FirstNonEmpty(meta.TaskTitle, defaultTaskTitle(req))
	meta.HandoffFile = defaultTaskHandoffPath(meta.TaskID)
	return meta, nil
}

func validateTaskHandoffMeta(meta taskHandoffMeta) error {
	if strings.TrimSpace(meta.TaskTitle) == "" {
		return fmt.Errorf("task handoff title is required for task %q", meta.TaskID)
	}
	if strings.TrimSpace(meta.HandoffFile) == "" {
		return fmt.Errorf("task handoff file is required for task %q", meta.TaskID)
	}
	return nil
}

func taskHandoffMetaFromThread(row *threadstore.Thread) (taskHandoffMeta, error) {
	if row == nil {
		return taskHandoffMeta{}, nil
	}
	stored, err := decodeStoredThreadConfig(row.ConfigOverride)
	if err != nil {
		return taskHandoffMeta{}, err
	}
	meta, err := taskHandoffMetaFromRuntimeConfig(stored.Runtime)
	if err != nil {
		return taskHandoffMeta{}, err
	}
	meta.TaskTitle = util.FirstNonEmpty(meta.TaskTitle, strings.TrimSpace(row.Prompt))
	return meta, nil
}

func taskHandoffMetaFromRuntimeConfig(cfg map[string]any) (taskHandoffMeta, error) {
	var meta taskHandoffMeta
	for _, field := range []struct {
		out  *string
		keys []string
	}{
		{&meta.TaskID, []string{taskConfigKeyID, taskConfigKeyIDSnake}},
		{&meta.TaskTitle, []string{taskConfigKeyTitle, taskConfigKeyTitleSnake}},
		{&meta.HandoffFile, []string{taskConfigKeyHandoffFile, taskConfigKeyHandoffFileSnake}},
		{&meta.RootTaskID, []string{taskConfigKeyRoot, taskConfigKeyRootSnake}},
	} {
		value, err := configutil.StrictString(cfg, "task handoff config", field.keys...)
		if err != nil {
			return taskHandoffMeta{}, err
		}
		*field.out = value
	}
	var err error
	if meta.Continue, err = configutil.StrictBool(cfg, "task handoff config", taskConfigKeyContinue, taskConfigKeyContinueSnake); err != nil {
		return taskHandoffMeta{}, err
	}
	meta.HandoffFile = strings.TrimSpace(strings.ReplaceAll(meta.HandoffFile, "\\", "/"))
	if meta.HandoffFile != "" {
		meta.HandoffFile = strings.TrimPrefix(path.Clean(meta.HandoffFile), "/")
	}
	return meta, nil
}

func shouldAutoTaskHandoff(req StartRequest) (bool, error) {
	auto, err := configutil.StrictBool(req.Config, "task handoff config", taskConfigKeyAuto, taskConfigKeyAutoSnake)
	if err != nil {
		return false, err
	}
	if auto {
		return true, nil
	}
	return strings.TrimSpace(req.ParentAgentID) != "" ||
		strings.TrimSpace(req.OwnerThreadID) != "" ||
		strings.TrimSpace(req.AgentType) != "", nil
}

func defaultTaskTitle(req StartRequest) string {
	return util.FirstNonEmpty(strings.TrimSpace(req.Name), strings.TrimSpace(req.Prompt), strings.TrimSpace(req.AgentKey), strings.TrimSpace(req.AgentType))
}

func defaultTaskHandoffPath(taskID string) string {
	if taskID = strings.TrimSpace(taskID); taskID == "" {
		return ""
	}
	return taskHandoffPrefix + path.Base(taskID) + ".md"
}

func (s *service) EnsureHandoffExists(ctx context.Context, taskID string) error {
	if s == nil || s.sharedFiles == nil {
		return errors.New("shared files store unavailable")
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return errors.New("taskId required")
	}
	p := defaultTaskHandoffPath(taskID)
	file, err := s.sharedFiles.Get(ctx, p)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return fmt.Errorf("%w: handoff file %q not found", errTaskHandoffMissing, p)
		}
		return fmt.Errorf("handoff file %q read failed: %w", p, err)
	}
	if file == nil {
		return fmt.Errorf("%w: handoff file %q not found", errTaskHandoffMissing, p)
	}
	return nil
}

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
		if !errors.Is(err, errTaskHandoffMissing) {
			return fmt.Errorf("handoff_read_failed: %w", err)
		}
		return fmt.Errorf("handoff_missing: %w", err)
	}
	return nil
}

var errTaskHandoffMissing, errHandoffMissingSource, errHandoffMissingAgentKey = errors.New("task handoff missing"), errors.New("thread/handoff: source_thread_id is required"), errors.New("thread/handoff: target agent_key is required")

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
		Provider:      source.Provider,
		ParentAgentID: source.ParentAgentID,
		AgentType:     source.AgentType,
		Name:          displayName,
		Prompt:        displayName,
		AgentKey:      targetAgentKey,
		OwnerThreadID: sourceID,
	}

	result, err := s.Start(ctx, startReq)
	if err != nil {
		pkglogger.Warn("thread/handoff: start failed", "source_thread_id", sourceID, "target_agent_key", targetAgentKey, "error", err)
		return HandoffResult{}, err
	}

	pkglogger.Info("thread/handoff: new thread started", "source_thread_id", sourceID, "new_thread_id", result.ThreadID, "target_agent_key", result.AgentKey)
	return HandoffResult{sourceID, result.ThreadID, result.AgentID, result.AgentKey, result.PromptKey, result.PromptVersionID, result.Status}, nil
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
	binding, err := s.resolveBinding(ctx, row.ThreadID)
	if err != nil {
		return handoffSource{}, fmt.Errorf("thread/handoff: resolve source provider: %w", err)
	}
	provider := ""
	if binding != nil {
		provider = strings.TrimSpace(binding.Provider)
	}
	if provider == "" {
		return handoffSource{}, fmt.Errorf("thread/handoff: provider is required for source thread %q", strings.TrimSpace(row.ThreadID))
	}
	return handoffSource{row.ThreadID, row.Cwd, row.Model, row.AgentType, row.ParentAgentID, row.Status, provider}, nil
}

type handoffSource struct{ ThreadID, Cwd, Model, AgentType, ParentAgentID, Status, Provider string }
