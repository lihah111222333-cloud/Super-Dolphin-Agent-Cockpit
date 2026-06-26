package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/util"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

var (
	errHandoffMissingSource   = errors.New("thread/handoff: source_thread_id is required")
	errHandoffMissingAgentKey = errors.New("thread/handoff: target agent_key is required")
)

// Handoff 从源 thread 构造目标 agent 的交接启动请求。
// 它只读取源线程历史和 runtime 快照，不修改源线程状态；目标启动失败会把错误直接返回给调用方。
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
		displayName = fmt.Sprintf("handoff -> %s", targetAgentKey)
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

// loadThreadForHandoff 为交接加载线程。
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
