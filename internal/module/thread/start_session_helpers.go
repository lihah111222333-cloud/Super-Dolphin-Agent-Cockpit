package thread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
)

const startDisplayNameMaxRunes = 160

func normalizeStartDisplayName(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= startDisplayNameMaxRunes {
		return value
	}
	return string(runes[:startDisplayNameMaxRunes])
}

func (s *service) buildStartAssemblyInput(req StartRequest, threadID string) (contract.StartInput, func(), error) {
	buildCtx := buildStartCtx(req, s.cfg, s.toolRegistry)
	buildCtx, cleanup, err := s.prepareScratchpadBuildCtx(req, threadID, buildCtx)
	if err != nil {
		return contract.StartInput{}, nil, err
	}
	return buildStartAssemblyInput(req, threadID, buildCtx), cleanup, nil
}

func buildStartAssemblyInput(req StartRequest, threadID string, buildCtx contract.BuildCtx) contract.StartInput {
	return contract.StartInput{
		ThreadID:                     strings.TrimSpace(threadID),
		ParentAgentID:                req.ParentAgentID,
		AgentType:                    req.AgentType,
		AgentMemoryScope:             req.AgentMemoryScope,
		Name:                         req.Name,
		Prompt:                       req.Prompt,
		PromptKey:                    strings.TrimSpace(req.PromptKey),
		BaseInstructions:             req.BaseInstructions,
		BaseInstructionBlocks:        append([]contract.BaseInstructionBlock(nil), req.BaseInstructionBlocks...),
		DeveloperInstructions:        req.DeveloperInstructions,
		Summary:                      buildCtx.Summary,
		Provider:                     buildCtx.Provider,
		CWD:                          buildCtx.CWD,
		GitRoot:                      buildCtx.GitRoot,
		IsWorktree:                   buildCtx.IsWorktree,
		Language:                     buildCtx.Language,
		Model:                        buildCtx.Model,
		EnabledTools:                 buildCtx.EnabledTools,
		AdditionalWorkingDirectories: buildCtx.AdditionalWorkingDirectories,
		ClaudeMdExcludes:             buildCtx.ClaudeMdExcludes,
		MCPSnapshot:                  buildCtx.MCPSnapshot,
		SessionFlags:                 buildCtx.SessionFlags,
		OutputStyleConfig:            buildCtx.OutputStyleConfig,
		ScratchpadDir:                buildCtx.ScratchpadDir,
		FRCConfig:                    buildCtx.FRCConfig,
		KeepCodingInstructions:       buildCtx.KeepCodingInstructions,
		// Legacy additive carrier only. Production skill discovery is handled
		// by provider-native mirror reconciliation in provider drivers, not by
		// prompt-catalog rendering.
		LaunchSkillNames:  append([]string(nil), req.LaunchSkillNames...),
		LaunchSkillRefs:   cloneProviderSkillRefs(req.LaunchSkillRefs),
		ForceLaunchSkills: req.ForceLaunchSkills,
	}
}

func buildStartAssembly(req StartRequest) contract.StartAssembly {
	return ensureStartAssemblySnapshot(contract.StartAssembly{
		DisplayName:           normalizeStartDisplayName(req.Name),
		BaseInstructions:      strings.TrimSpace(req.BaseInstructions),
		DeveloperInstructions: strings.TrimSpace(req.DeveloperInstructions),
	}, req.Provider)
}

func resolveStartPromptAssembly(ctx context.Context, req StartRequest, input contract.StartInput) (contract.StartAssembly, error) {
	if req.PromptAssemblyRef == nil {
		return buildStartAssembly(req), nil
	}
	assembly, err := dispatchPromptAssembly(ctx, req, input)
	if err != nil {
		return contract.StartAssembly{}, err
	}
	assembly.DisplayName = normalizeStartDisplayName(util.FirstNonEmpty(strings.TrimSpace(assembly.DisplayName), req.Name))
	assembly.BaseInstructions = strings.TrimSpace(assembly.BaseInstructions)
	assembly.DeveloperInstructions = strings.TrimSpace(assembly.DeveloperInstructions)
	return ensureStartAssemblySnapshot(assembly, input.Provider), nil
}

func resolveResumeCWD(req ResumeRequest, state resumeState) (string, error) {
	requested := util.FirstNonEmpty(req.CWD, req.Path)
	stored := strings.TrimSpace(state.CWD)
	if stored != "" && requested != "" && comparablePromptCWD(stored) != comparablePromptCWD(requested) {
		return "", fmt.Errorf("thread resume cwd mismatch: stored cwd %q request cwd %q", stored, requested)
	}
	return util.FirstNonEmpty(stored, requested), nil
}

func resolveAuthoritativeResumeCWD(req ResumeRequest, state resumeState) (string, error) {
	cwd, err := resolveResumeCWD(req, state)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(state.CWD) == "" {
		return "", errors.New("thread resume cwd is required")
	}
	return cwd, nil
}

func (s *service) hydrateResumeSessionRequest(ctx context.Context, req ResumeRequest) (ResumeRequest, error) {
	req, err := trimResumeRequest(req)
	if err != nil {
		return ResumeRequest{}, err
	}
	state, err := s.lookupResumeState(ctx, req.ThreadID)
	if err != nil {
		return ResumeRequest{}, err
	}
	req = hydrateResumeIDs(req, state)
	req.CWD, err = resolveAuthoritativeResumeCWD(req, state)
	if err != nil {
		return ResumeRequest{}, err
	}
	req.ClaudeHome = util.FirstNonEmpty(req.ClaudeHome, state.ClaudeHome, resumeRuntimeConfigString(state.ConfigOverride.Runtime, "claudeHome", "claude_home", "history_dir"))
	req.CodexHome = util.FirstNonEmpty(req.CodexHome, state.CodexHome)
	req.CodexInstanceKey = util.FirstNonEmpty(req.CodexInstanceKey, state.CodexInstanceKey)
	req.CodexModelProvider = util.FirstNonEmpty(req.CodexModelProvider, state.CodexModelProvider)
	req.CodexDisabledNativeTools = resolveResumeCodexDisabledNativeTools(req.CodexDisabledNativeTools, state.ConfigOverride.Runtime)
	req.Config = mergeRuntimeConfig(clone.RuntimeConfigMap(state.ConfigOverride.Runtime), req.Config)
	req, err = s.injectDefaultCodexIdentityForResume(req)
	if err != nil {
		return ResumeRequest{}, err
	}
	req.PromptSnapshot, err = s.resolveResumePromptSnapshot(ctx, req, state)
	if err != nil {
		return ResumeRequest{}, err
	}
	req = hydrateResumeRuntimeSelection(req, state)
	if err := validateHydratedResumeRequest(req); err != nil {
		return ResumeRequest{}, err
	}
	return req, nil
}

func hydrateResumeIDs(req ResumeRequest, state resumeState) ResumeRequest {
	state.PublicThreadID = util.FirstNonEmpty(state.PublicThreadID, req.ThreadID)
	req.AgentID = util.FirstNonEmpty(req.AgentID, state.AgentID)
	req.Provider = util.FirstNonEmpty(req.Provider, state.Provider)
	req.ProviderThreadID = normalizeProviderThreadID(req.Provider, util.FirstNonEmpty(req.ProviderThreadID, state.ProviderThreadID))
	req.ThreadID = state.PublicThreadID
	return req
}

func validateHydratedResumeRequest(req ResumeRequest) error {
	if req.Provider == "" {
		return errors.New("provider is required")
	}
	if req.AgentID == "" {
		return errors.New("agent id is required")
	}
	return nil
}

func hydrateResumeRuntimeSelection(req ResumeRequest, state resumeState) ResumeRequest {
	if req.ConfigOverride.Model == nil {
		if value := sanitizeConfigStringArtifact(state.ConfigOverride.Model); value != "" {
			req.ConfigOverride.Model = &value
		}
	}
	if req.ConfigOverride.Effort == nil {
		if value := sanitizeConfigStringArtifact(state.ConfigOverride.Effort); value != "" {
			req.ConfigOverride.Effort = &value
		}
	}
	if req.Model == "" {
		req.Model = resolveResumeModel(req, state)
	}
	if req.Effort == "" {
		req.Effort = resolveResumeEffort(req, state)
	}
	return req
}

func (s *service) lookupResumeState(ctx context.Context, threadID string) (resumeState, error) {
	state, err := s.lookupResumeThreadState(ctx, threadID)
	if err != nil {
		return resumeState{}, err
	}
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return resumeState{}, err
	}
	mergeResumeBindingState(&state, binding)
	state.StoredCWD = state.CWD
	return state, nil
}

func (s *service) lookupResumeThreadState(ctx context.Context, threadID string) (resumeState, error) {
	thread, err := s.getThread(ctx, threadID)
	if err != nil {
		return resumeState{}, err
	}
	if thread == nil {
		return resumeState{}, fmt.Errorf("thread %q missing", strings.TrimSpace(threadID))
	}
	cfg, err := decodeStoredThreadConfig(thread.ConfigOverride)
	if err != nil {
		return resumeState{}, err
	}
	return resumeState{
		AgentID:           strings.TrimSpace(thread.AgentID),
		ParentAgentID:     strings.TrimSpace(thread.ParentAgentID),
		OwnerThreadID:     strings.TrimSpace(thread.OwnerThreadID),
		AgentType:         strings.TrimSpace(thread.AgentType),
		AgentMemoryScope:  strings.TrimSpace(thread.AgentMemoryScope),
		PublicThreadID:    strings.TrimSpace(thread.ThreadID),
		Prompt:            strings.TrimSpace(thread.Prompt),
		Model:             sanitizeConfigStringArtifact(thread.Model),
		ConfigOverrideRaw: clone.RawMessage(thread.ConfigOverride),
		ConfigOverride:    cfg,
		Effort:            sanitizeConfigStringArtifact(cfg.Effort),
		CWD:               strings.TrimSpace(thread.Cwd),
		CreatedAt:         thread.CreatedAt,
	}, nil
}

func mergeResumeBindingState(state *resumeState, binding *bindingstore.Binding) {
	if state == nil || binding == nil {
		return
	}
	state.AgentID = util.FirstNonEmpty(state.AgentID, binding.AgentID)
	state.ParentAgentID = util.FirstNonEmpty(state.ParentAgentID, strings.TrimSpace(binding.ParentAgentID))
	state.AgentType = util.FirstNonEmpty(state.AgentType, strings.TrimSpace(binding.AgentType))
	state.AgentMemoryScope = util.FirstNonEmpty(state.AgentMemoryScope, strings.TrimSpace(binding.AgentMemoryScope))
	state.Provider = strings.TrimSpace(binding.Provider)
	state.ProviderThreadID = util.FirstNonEmpty(state.ProviderThreadID, recoverableBindingProviderThreadID(binding))
	state.PublicThreadID = util.FirstNonEmpty(state.PublicThreadID, binding.CodexThreadID)
	state.RolloutPath = strings.TrimSpace(binding.RolloutPath)
	state.SessionUUID = strings.TrimSpace(binding.SessionUUID)
	state.CodexHome = strings.TrimSpace(binding.CodexHome)
	state.CodexInstanceKey = strings.TrimSpace(binding.CodexInstanceKey)
	state.CodexModelProvider = strings.TrimSpace(binding.CodexModelProvider)
	state.CWD = util.FirstNonEmpty(state.CWD, binding.Cwd)
}

func dispatchPromptAssembly(ctx context.Context, req StartRequest, input contract.StartInput) (contract.StartAssembly, error) {
	agentType := contract.AgentType(strings.TrimSpace(input.AgentType))
	if isKnownSubagentAgentType(agentType) {
		return req.PromptAssemblyRef.AssembleAgent(ctx, contract.AgentInput{
			StartInput: input,
			AgentType:  agentType,
		})
	}
	return req.PromptAssemblyRef.AssembleStart(ctx, input)
}

func isKnownSubagentAgentType(t contract.AgentType) bool {
	switch t {
	case contract.AgentTypeExplore, contract.AgentTypePlan:
		return true
	default:
		return false
	}
}

func toProviderStartAssembly(assembly contract.StartAssembly) dto.StartAssembly {
	return dto.StartAssembly{
		DisplayName:           strings.TrimSpace(assembly.DisplayName),
		BaseInstructions:      strings.TrimSpace(assembly.BaseInstructions),
		Boundary:              clonePromptBoundary(assembly.Boundary),
		DeveloperInstructions: strings.TrimSpace(assembly.DeveloperInstructions),
		ResolvedSections:      toProviderResolvedSections(assembly.ResolvedSections),
		Snapshot:              toProviderPromptSnapshot(assembly.Snapshot),
		SuppressedTools:       append([]string(nil), assembly.SuppressedTools...),
		UserContext:           clone.StringMap(assembly.UserContext),
		UserContextText:       strings.TrimSpace(assembly.UserContextText),
		SystemContext:         dto.SystemContext(clone.StringMap(assembly.SystemContext)),
	}
}

func toProviderPromptSnapshot(snapshot contract.PromptAssemblySnapshot) dto.PromptAssemblySnapshot {
	return dto.PromptAssemblySnapshot{
		DisplayName:           strings.TrimSpace(snapshot.DisplayName),
		BaseInstructions:      strings.TrimSpace(snapshot.BaseInstructions),
		Boundary:              clonePromptBoundary(snapshot.Boundary),
		DeveloperInstructions: strings.TrimSpace(snapshot.DeveloperInstructions),
		Provider:              strings.TrimSpace(snapshot.Provider),
		Version:               snapshot.Version,
		Hash:                  strings.TrimSpace(snapshot.Hash),
		SectionSnapshot:       clone.StringMap(snapshot.SectionSnapshot),
		Generation:            snapshot.Generation,
	}
}

func toProviderResolvedSections(sections []contract.ResolvedPromptSection) []dto.ResolvedPromptSection {
	if len(sections) == 0 {
		return nil
	}
	out := make([]dto.ResolvedPromptSection, 0, len(sections))
	for _, section := range sections {
		out = append(out, dto.ResolvedPromptSection{
			Name:     strings.TrimSpace(section.Name),
			Region:   dto.PromptRegion(section.Region),
			Volatile: section.Volatile,
			Content:  strings.TrimSpace(section.Content),
		})
	}
	return out
}

func buildStartSessionConfig(req StartRequest, input contract.StartInput, assembly contract.StartAssembly) map[string]any {
	cfg := map[string]any{}
	modelProvider := strings.TrimSpace(req.ModelProvider)
	if provider := strings.TrimSpace(input.Provider); strings.EqualFold(modelProvider, provider) {
		modelProvider = ""
	}
	for _, field := range []struct {
		value string
		keys  []string
	}{
		{req.ApprovalPolicy, []string{"approvalPolicy", "approval_policy", "approvals"}},
		{modelProvider, []string{"modelProvider"}},
		{assembly.DeveloperInstructions, []string{"developerInstructions", "developer_instructions"}},
		{req.Summary, []string{"summary"}},
		{req.Effort, []string{"effort"}},
		{req.Personality, []string{"personality"}},
		{input.ParentAgentID, []string{"parentAgentId", "parent_agent_id"}},
		{input.AgentType, []string{"agentType", "agent_type"}},
		{input.AgentMemoryScope, []string{"agentMemoryScope", "agent_memory_scope"}},
		{input.PromptKey, []string{"promptKey", "prompt_key"}},
		{input.Provider, []string{"provider"}},
		{input.CWD, []string{"cwd"}},
		{input.Model, []string{"model"}},
		{input.GitRoot, []string{"gitRoot"}},
		{input.Language, []string{"language"}},
		{input.ScratchpadDir, []string{"scratchpadDir", "scratchpad_dir"}},
		{req.ToolSurfaceMode, []string{"toolSurfaceMode", "tool_surface_mode"}},
	} {
		for _, key := range field.keys {
			putConfigString(cfg, key, field.value)
		}
	}
	threadKind := threadKindForStart(input.ParentAgentID)
	putConfigString(cfg, "threadKind", threadKind)
	putConfigString(cfg, "thread_kind", threadKind)
	putConfigBool(cfg, "isWorktree", input.IsWorktree)
	putConfigStrings(cfg, "enabledTools", applyPersistentSubagentToolPolicy(input.EnabledTools, input.SessionFlags))
	putConfigStrings(cfg, "additionalWorkingDirectories", input.AdditionalWorkingDirectories)
	for _, key := range []string{"claudeMdExcludes", "claude_md_excludes"} {
		putConfigStrings(cfg, key, input.ClaudeMdExcludes)
	}
	putConfigStrings(cfg, "mcpServers", input.MCPSnapshot.Servers)
	putConfigStrings(cfg, "mcpTools", input.MCPSnapshot.Tools)
	putConfigStringMap(cfg, "mcpInstructions", input.MCPSnapshot.Instructions)
	for _, key := range []string{"mcpInstructionsDeltaEnabled", "mcp_instructions_delta_enabled"} {
		putConfigBool(cfg, key, input.MCPSnapshot.InstructionsDeltaEnabled)
	}
	putConfigBoolMap(cfg, "sessionFlags", input.SessionFlags)
	for _, key := range []string{"outputStyleConfig", "output_style_config"} {
		putConfigOutputStyleConfig(cfg, key, input.OutputStyleConfig)
	}
	if strings.EqualFold(strings.TrimSpace(input.Provider), "codex") {
		putConfigStrings(cfg, "codexDisabledNativeTools", assembly.SuppressedTools)
	}
	putConfigJSON(cfg, "sandbox", req.Sandbox)
	for key, value := range req.Config {
		mergeConfigValueIfAbsent(cfg, key, value)
	}
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

func mergeConfigValueIfAbsent(cfg map[string]any, key string, value any) {
	if _, exists := cfg[key]; exists {
		return
	}
	if text, ok := value.(string); ok && isConfigArtifactKey(key) {
		if text = sanitizeConfigStringArtifact(text); text != "" {
			cfg[key] = text
		}
		return
	}
	cfg[key] = value
}

func isConfigArtifactKey(key string) bool {
	switch strings.TrimSpace(key) {
	case "model", "effort", "modelProvider", "model_provider", "provider":
		return true
	default:
		return false
	}
}

func buildStartStoredThreadConfig(req StartRequest, input contract.StartInput, assembly contract.StartAssembly, session ...contract.Session) storedThreadConfig {
	runtime := clone.RuntimeConfigMap(buildStartSessionConfig(req, input, assembly))
	runtime = mergeStartSessionRuntimeIdentity(runtime, firstStartStoredConfigSession(session))
	return storedThreadConfig{
		Model:           strings.TrimSpace(input.Model),
		Effort:          strings.TrimSpace(req.Effort),
		Approvals:       strings.TrimSpace(req.ApprovalPolicy),
		Personality:     strings.TrimSpace(req.Personality),
		Runtime:         runtime,
		ToolSurfaceMode: strings.TrimSpace(req.ToolSurfaceMode),
	}
}

func firstStartStoredConfigSession(session []contract.Session) contract.Session {
	if len(session) == 0 {
		return nil
	}
	return session[0]
}

func mergeStartSessionRuntimeIdentity(runtime map[string]any, session contract.Session) map[string]any {
	if session == nil {
		return runtime
	}
	for _, key := range []string{"codexHome", "codexInstanceKey", "codexModelProvider"} {
		value := sessionRuntimeConfigString(session, key)
		if value == "" {
			continue
		}
		if runtime == nil {
			runtime = map[string]any{}
		}
		runtime[key] = value
	}
	return runtime
}

func putConfigString(cfg map[string]any, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		cfg[key] = value
	}
}

func putConfigBool(cfg map[string]any, key string, value bool) {
	if value {
		cfg[key] = true
	}
}

func threadKindForStart(parentAgentID string) string {
	if strings.TrimSpace(parentAgentID) == "" {
		return ""
	}
	return "child_agent"
}

func putConfigStrings(cfg map[string]any, key string, values []string) {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			cleaned = append(cleaned, value)
		}
	}
	if len(cleaned) > 0 {
		cfg[key] = cleaned
	}
}

func putConfigStringMap(cfg map[string]any, key string, values map[string]string) {
	if len(values) == 0 {
		return
	}
	out := make(map[string]any, len(values))
	for name, value := range values {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name != "" && value != "" {
			out[name] = value
		}
	}
	if len(out) > 0 {
		cfg[key] = out
	}
}

func putConfigBoolMap(cfg map[string]any, key string, values map[string]bool) {
	if len(values) == 0 {
		return
	}
	out := make(map[string]any, len(values))
	for name, value := range values {
		name = strings.TrimSpace(name)
		if name != "" {
			out[name] = value
		}
	}
	if len(out) > 0 {
		cfg[key] = out
	}
}

func putConfigOutputStyleConfig(cfg map[string]any, key string, style *contract.OutputStyleConfig) {
	if style == nil {
		return
	}
	out := map[string]any{}
	putConfigString(out, "name", style.Name)
	putConfigString(out, "description", style.Description)
	putConfigString(out, "prompt", style.Prompt)
	putConfigString(out, "source", style.Source)
	if style.KeepCodingInstructions != nil {
		out["keepCodingInstructions"] = *style.KeepCodingInstructions
	}
	if len(out) > 0 {
		cfg[key] = out
	}
}

func putConfigJSON(cfg map[string]any, key string, raw json.RawMessage) {
	raw = trimRawJSON(raw)
	if len(raw) == 0 {
		return
	}
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		cfg[key] = value
	}
}

func trimRawJSON(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	return raw
}
