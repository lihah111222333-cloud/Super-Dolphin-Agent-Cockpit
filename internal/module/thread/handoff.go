package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

var errHandoffMissingSource = errors.New("thread/handoff: source_thread_id is required")
var errHandoffMissingAgentKey = errors.New("thread/handoff: target agent_key is required")

// Handoff starts a fresh thread for a target agent_key while keeping the
// source thread alive, linking them via OwnerThreadID. It reuses the standard
// Start flow so router materialization, provider launch, session setup and
// persistence behave identically to a normal `thread/start` \u2014 the handoff is
// just a pre-filled StartRequest with an owner pointer.
//
// Semantics (MVP per decision Risk 1 (c)+(b)):
//   - Source thread stays running. User can toggle back via the sidebar.
//   - No message history is copied. The new thread starts clean, which is
//     the point of a handoff \u2014 fresh context under a different role.
//   - If target agent_key has no enabled prompt_template, router
//     fallback applies (agent_key recorded, prompt_version_id NULL).
func (s *service) Handoff(ctx context.Context, req HandoffRequest) (HandoffResult, error) {
	ctx = shared.NonNilContext(ctx)

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
		displayName = fmt.Sprintf("handoff \u2192 %s", targetAgentKey)
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

// loadThreadForHandoff isolates the store lookup so tests can stub it and so
// callers get a uniform wrap-in-error for the missing / disabled cases.
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

// handoffSource is the narrowed view of threadstore.Thread that Handoff needs.
// Keeping it local avoids leaking the full Thread DTO into the handoff API
// surface and makes the dependency on threadStore easier to fake in tests.
type handoffSource struct {
	ThreadID      string
	Cwd           string
	Model         string
	AgentType     string
	ParentAgentID string
	Status        string
}

// sourceProviderHint reads the provider name from the source thread's config
// override if present. The store does not carry provider on the thread row
// itself (provider lives on agent_provider_binding), so for MVP we return
// empty and let resolveStartProvider choose the default.
func sourceProviderHint(_ handoffSource) string { return "" }
