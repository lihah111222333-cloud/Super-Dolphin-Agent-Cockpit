package thread

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/historyjsonl"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

// runScratchpadCleanup is the shared `defer` target used by Start / SpawnIfNeeded
// to release the scratchpad snapshot when the spawn pipeline fails. active is a
// pointer so the caller can flip it to false after persistStartedSession to
// skip cleanup on success.
func runScratchpadCleanup(active *bool, cleanup func()) {
	if active == nil || !*active {
		return
	}
	if cleanup != nil {
		cleanup()
	}
}

// enrichFromSessionConfig extracts model/cwd from the session's runtime config
// when the original request values are empty. codex app-server assigns model
// server-side; the resolved value is captured in runtimeConfig during StartSession.
func enrichFromSessionConfig(session contract.Session, reqModel, reqCWD string) (model, cwd string, port int) {
	model, cwd = reqModel, reqCWD
	rc, ok := session.(interface{ RuntimeConfigSnapshot() map[string]any })
	if !ok {
		return model, resolveRelativeCWD(cwd), 0
	}
	cfg := rc.RuntimeConfigSnapshot()
	if cfg == nil {
		return model, resolveRelativeCWD(cwd), 0
	}
	if model == "" {
		model, _ = cfg["model"].(string)
	}
	if cwd == "" || cwd == "." {
		if v, _ := cfg["cwd"].(string); v != "" && v != "." {
			cwd = v
		}
	}
	if p, ok := cfg["port"].(int); ok && p > 0 {
		port = p
	}
	return model, resolveRelativeCWD(cwd), port
}

func sessionRuntimeConfigString(session contract.Session, key string) string {
	rc, ok := session.(interface{ RuntimeConfigSnapshot() map[string]any })
	if !ok {
		return ""
	}
	cfg := rc.RuntimeConfigSnapshot()
	if cfg == nil {
		return ""
	}
	value, _ := cfg[strings.TrimSpace(key)].(string)
	return strings.TrimSpace(value)
}

func (s *service) injectParentCodexIdentityForStart(ctx context.Context, req StartRequest) StartRequest {
	parentID := strings.TrimSpace(req.ParentAgentID)
	if parentID == "" || s.bindingStore == nil {
		return req
	}
	parentBinding, err := s.bindingStore.GetByAgentID(ctx, parentID)
	if err != nil || parentBinding == nil {
		if s.logger != nil {
			s.logger.Warn("thread: child start parent codex identity lookup failed",
				"agent_id", req.AgentID,
				"parent_agent_id", parentID,
				"error", err)
		}
		return req
	}
	var injected bool
	req.Config, injected = injectParentCodexIdentity(req.Config, parentBinding)
	if s.logger != nil {
		s.logger.Warn("thread: child start parent codex identity lookup",
			"agent_id", req.AgentID,
			"parent_agent_id", parentID,
			"injected", injected,
			"parent_has_codex_home", strings.TrimSpace(parentBinding.CodexHome) != "",
			"parent_has_codex_instance_key", strings.TrimSpace(parentBinding.CodexInstanceKey) != "",
			"parent_has_codex_model_provider", strings.TrimSpace(parentBinding.CodexModelProvider) != "")
	}
	return req
}

func injectParentCodexIdentity(cfg map[string]any, parent *bindingstore.Binding) (map[string]any, bool) {
	home := strings.TrimSpace(parent.CodexHome)
	instanceKey := strings.TrimSpace(parent.CodexInstanceKey)
	modelProvider := strings.TrimSpace(parent.CodexModelProvider)
	if home == "" || instanceKey == "" || modelProvider == "" {
		return cfg, false
	}
	if cfg == nil {
		cfg = make(map[string]any)
	}
	injected := false
	if firstConfigString(cfg, "codexHome") == "" {
		cfg["codexHome"] = home
		injected = true
	}
	if firstConfigString(cfg, "codexInstanceKey") == "" {
		cfg["codexInstanceKey"] = instanceKey
		injected = true
	}
	if firstConfigString(cfg, "codexModelProvider") == "" {
		cfg["codexModelProvider"] = modelProvider
		injected = true
	}
	return cfg, injected
}

func (s *service) logStartedSessionCodexIdentity(
	req StartRequest,
	agentID,
	codexHome string,
	identity providershared.CodexIdentity,
	session contract.Session,
) {
	if s.logger == nil {
		return
	}
	s.logger.Warn("thread: persist started session codex identity",
		"agent_id", agentID,
		"provider", req.Provider,
		"codex_home", codexHome,
		"has_strict_identity", identity.Home != "" && identity.InstanceKey != "" && identity.ModelProvider != "",
		"session_runtime_codex_home", sessionRuntimeConfigString(session, "codexHome"),
		"rollout_path", session.RolloutPath())
}

// resolveRelativeCWD converts "." or empty to the process working directory.
func resolveRelativeCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" && cwd != "." {
		return cwd
	}
	if abs, err := os.Getwd(); err == nil {
		return abs
	}
	return cwd
}

func comparablePromptCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || cwd == "." {
		return ""
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		return abs
	}
	return cwd
}

func promptWorktreeState(cwd string, cfg *platformconfig.Config) (string, bool) {
	resolved := comparablePromptCWD(cwd)
	if resolved == "" {
		return "", false
	}
	return resolved, resolvePromptGitContext(resolved, "", cfg).IsWorktree
}

func promptResumeRestoreRequiresInvalidation(prevCWD, nextCWD string, cfg *platformconfig.Config) bool {
	_, prevWorktree := promptWorktreeState(prevCWD, cfg)
	_, nextWorktree := promptWorktreeState(nextCWD, cfg)
	return prevWorktree || nextWorktree
}

func promptWorktreeSwitchRequiresInvalidation(prevCWD, nextCWD string, cfg *platformconfig.Config) bool {
	prevResolved, prevWorktree := promptWorktreeState(prevCWD, cfg)
	nextResolved, nextWorktree := promptWorktreeState(nextCWD, cfg)
	if prevResolved == nextResolved {
		return false
	}
	return prevWorktree || nextWorktree
}

func (s *service) lookupBindingCWD(ctx context.Context, agentID string) string {
	if s.bindingStore == nil {
		return ""
	}
	binding, err := s.bindingStore.GetByAgentID(ctx, strings.TrimSpace(agentID))
	if err != nil || binding == nil {
		return ""
	}
	s.rememberBinding(binding)
	return strings.TrimSpace(binding.Cwd)
}

func (s *service) ReadThreadStateRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error) {
	binding, _ := s.resolveBinding(ctx, threadID)
	offline, err := s.buildOfflineConfig(ctx, threadID, binding)
	if err != nil {
		return nil, err
	}
	return shared.CloneRuntimeConfigMap(offline.Runtime), nil
}

func buildLaunchRequest(
	agentID,
	cwd,
	name,
	parentID,
	agentType,
	memoryScope,
	provider,
	model string,
) (LaunchAgentRequest, error) {
	exe, err := os.Executable()
	if err != nil {
		return LaunchAgentRequest{}, err
	}
	return LaunchAgentRequest{
		AgentID:     strings.TrimSpace(agentID),
		Name:        strings.TrimSpace(name),
		ParentID:    strings.TrimSpace(parentID),
		AgentType:   strings.TrimSpace(agentType),
		MemoryScope: strings.TrimSpace(memoryScope),
		Cwd:         strings.TrimSpace(cwd),
		Command:     []string{exe},
		Env:         launchConfigEnv(provider, model),
	}, nil
}

func launchConfigEnv(provider, model string) []string {
	var env []string
	if provider = strings.TrimSpace(provider); provider != "" {
		env = append(env, "AGENT_PROVIDER="+provider)
	}
	if model = strings.TrimSpace(model); model != "" {
		env = append(env, "AGENT_MODEL="+model)
	}
	return env
}

func (s *service) launchAgent(
	ctx context.Context,
	agentID,
	cwd,
	name,
	parentID,
	agentType,
	memoryScope,
	provider,
	model string,
) error {
	if s.orchestration == nil {
		return nil
	}
	req, err := buildLaunchRequest(agentID, cwd, name, parentID, agentType, memoryScope, provider, model)
	if err != nil {
		return err
	}
	return s.orchestration.LaunchAgent(ctx, req)
}

func (s *service) recoverAgent(
	ctx context.Context,
	agentID,
	cwd,
	name,
	parentID,
	agentType,
	memoryScope string,
) error {
	if s.orchestration == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	if err := s.orchestration.Recover(ctx, agentID); err == nil {
		return nil
	}
	return s.launchAgent(ctx, agentID, cwd, name, parentID, agentType, memoryScope, "", "")
}

func bindingPublicThreadID(binding *bindingstore.Binding, fallback string) string {
	if binding == nil {
		return strings.TrimSpace(fallback)
	}
	return shared.FirstNonEmpty(binding.CodexThreadID, fallback)
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return time.Now().Unix()
}

func (s *service) maybeRegisterThreadBinding(
	ctx context.Context,
	state threadState,
	updateBinding bool,
) (bindingWriteOutcome, error) {
	if !updateBinding || s.bindingStore == nil {
		return bindingWriteOutcome{}, nil
	}
	return s.registerThreadBinding(ctx, state)
}

func (s *service) persistStartedThread(
	ctx context.Context,
	state threadState,
	bindingOutcome bindingWriteOutcome,
) error {
	if err := s.upsertPublicThread(ctx, state, bindingOutcome); err != nil {
		return err
	}
	s.rememberStartedThread(state)
	s.publishThreadStarted(state)
	return nil
}

func (s *service) upsertPublicThread(
	ctx context.Context,
	state threadState,
	bindingOutcome bindingWriteOutcome,
) error {
	if s.threadStore == nil {
		return nil
	}
	displayName := strings.TrimSpace(shared.FirstNonEmpty(state.Name, state.Prompt))
	err := s.threadStore.Upsert(ctx, newThreadUpsertParams(threadstore.Thread{
		ThreadID:        state.PublicThreadID,
		Prompt:          displayName,
		Model:           state.Model,
		Cwd:             state.CWD,
		Status:          statusCreated,
		CreatedAt:       state.CreatedAt,
		UpdatedAt:       time.Now().Unix(),
		OwnerThreadID:   state.OwnerThreadID,
		ConfigOverride:  shared.CloneRawMessage(state.ConfigOverride),
		AgentKey:        state.AgentKey,
		PromptVersionID: state.PromptVersionID,
	}))
	if err == nil {
		return nil
	}
	if rollbackErr := s.rollbackThreadBinding(ctx, bindingOutcome); rollbackErr != nil {
		return errors.Join(err, rollbackErr)
	}
	return err
}

func (s *service) rememberStartedThread(state threadState) {
	s.rememberThreadAgent(state.PublicThreadID, state.AgentID)
	s.rememberThreadAgent(state.ProviderThreadID, state.AgentID)
}

func historyTargetID(binding *bindingstore.Binding, threadID string) string {
	requestedID := strings.TrimSpace(threadID)
	if binding == nil {
		return requestedID
	}
	publicThreadID := strings.TrimSpace(binding.CodexThreadID)
	agentID := strings.TrimSpace(binding.AgentID)
	if requestedID != "" && requestedID != publicThreadID && requestedID != agentID {
		return requestedID
	}
	return shared.FirstNonEmpty(binding.ProviderThreadID, publicThreadID, agentID, requestedID)
}

func toRef(thread threadstore.Thread) Ref {
	name := strings.TrimSpace(thread.Prompt)
	if name == "" {
		name = shared.FirstNonEmpty(strings.TrimSpace(thread.ThreadID), strings.TrimSpace(thread.AgentID))
	}
	return Ref{ID: strings.TrimSpace(thread.ThreadID), Name: name, AgentID: strings.TrimSpace(thread.AgentID)}
}

func normalizeThreadID(threadID string) (string, error) {
	id := strings.TrimSpace(threadID)
	if id == "" {
		return "", errors.New("thread id is required")
	}
	return id, nil
}

func agentIDFromBinding(binding *bindingstore.Binding, fallback string) string {
	if binding == nil {
		return strings.TrimSpace(fallback)
	}
	if agentID := strings.TrimSpace(binding.AgentID); agentID != "" {
		return agentID
	}
	return strings.TrimSpace(fallback)
}

func pageCount(totalCount, limit int) int {
	if totalCount <= 0 {
		return 0
	}
	if limit <= 0 || totalCount <= limit {
		return 1
	}
	pages := totalCount / limit
	if totalCount%limit != 0 {
		pages++
	}
	return pages
}

func (s *service) readMessagesSource(ctx context.Context, threadID string, binding *bindingstore.Binding) ([]dto.Message, error) {
	session, err := s.sessionForBinding(binding)
	if err == nil {
		return session.ReadHistory(ctx, historyTargetID(binding, threadID), 0)
	}
	return s.readPersistedMessages(ctx, threadID, binding)
}

func (s *service) sessionForBinding(binding *bindingstore.Binding) (contract.Session, error) {
	if binding == nil {
		return nil, errors.New("thread binding is not configured")
	}
	if s.sessions == nil {
		return nil, errors.New("session provider is not configured")
	}
	return s.sessions.GetSession(strings.TrimSpace(binding.AgentID))
}

func (s *service) readPersistedMessages(ctx context.Context, threadID string, binding *bindingstore.Binding) ([]dto.Message, error) {
	if _, err := s.getThread(ctx, threadID); err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, errors.New("thread binding is not configured")
	}
	return historyjsonl.ReadProviderMessages(historyjsonl.ReadRequest{
		Provider:         binding.Provider,
		RolloutPath:      binding.RolloutPath,
		ThreadID:         threadID,
		ProviderThreadID: binding.ProviderThreadID,
		SessionUUID:      binding.SessionUUID,
	})
}
