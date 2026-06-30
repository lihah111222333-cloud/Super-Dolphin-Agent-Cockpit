package thread

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
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

func (s *service) buildStartAssemblyInput(ctx context.Context, req StartRequest, threadID string) (contract.StartInput, func(), error) {
	buildCtx := buildStartCtx(req, s.cfg, s.toolRegistry)
	var err error
	buildCtx.MCPSnapshot, err = mergeConfiguredMCPServers(ctx, buildCtx.MCPSnapshot, s.mcpServers, mcpServerConfigLookupRoot(buildCtx))
	if err != nil {
		return contract.StartInput{}, nil, err
	}
	buildCtx, cleanup, err := s.prepareScratchpadBuildCtx(req, threadID, buildCtx)
	if err != nil {
		return contract.StartInput{}, nil, err
	}
	return buildStartAssemblyInput(req, threadID, buildCtx), cleanup, nil
}

// buildStartAssemblyInput 把 thread/start 的选择交给 prompt。
// memory、MCP、scratchpad、FRC 和 provider 信息都从 BuildCtx 传进去；这里不碰 session。
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
		// 兼容旧 wire 载体；生产 skill 发现由 provider 驱动的镜像同步处理，不由 prompt catalog 渲染。
		LaunchSkillNames:  append([]string(nil), req.LaunchSkillNames...),
		LaunchSkillRefs:   cloneProviderSkillRefs(req.LaunchSkillRefs),
		ForceLaunchSkills: req.ForceLaunchSkills,
	}
}

// resolveStartPromptAssembly 是 thread 调 prompt 的地方。
// 它补齐 snapshot，让 provider 和 thread store 看到同一份 start 提示。
func resolveStartPromptAssembly(ctx context.Context, req StartRequest, input contract.StartInput) (contract.StartAssembly, error) {
	if req.PromptAssemblyRef == nil {
		return contract.StartAssembly{}, errors.New("thread: prompt assembly service is not configured")
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

// hydrateResumeSessionRequest 从 thread、binding、config 和 snapshot 还原 resume 输入。
// 它不重新选 prompt，也不创建新 thread；cwd 或 snapshot 不可靠就报错。
func (s *service) hydrateResumeSessionRequest(ctx context.Context, req ResumeRequest, opts resumeHydrateOptions) (ResumeRequest, error) {
	req, err := trimResumeRequest(req)
	if err != nil {
		return ResumeRequest{}, err
	}
	state, err := s.lookupResumeState(ctx, req.ThreadID)
	if err != nil {
		return ResumeRequest{}, err
	}
	req = hydrateResumeIDs(req, state)
	if opts.validateExplicitCodexIdentity {
		err = validateExplicitResumeCodexIdentity(req)
	}
	if err != nil {
		return ResumeRequest{}, err
	}
	req.CWD, err = resolveAuthoritativeResumeCWD(req, state)
	if err != nil {
		return ResumeRequest{}, err
	}
	req.ClaudeHome = util.FirstNonEmpty(req.ClaudeHome, state.ClaudeHome, resumeRuntimeConfigString(state.ConfigOverride.Runtime, "claudeHome", "claude_home", "history_dir"))
	req = hydrateResumeCodexIdentity(req, state)
	req.CodexDisabledNativeTools = resolveResumeCodexDisabledNativeTools(req.CodexDisabledNativeTools, state.ConfigOverride.Runtime)
	req.Config = mergeRuntimeConfig(providerRuntimeConfig(state.ConfigOverride.Runtime), req.Config)
	req, err = s.canonicalizeHydratedResumeCodexIdentity(req, opts.canonicalizeCodexIdentity)
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

// validateHydratedResumeRequest 是 resume 交给 provider 前的最后检查。
// provider 和 agent id 必须来自请求、thread row 或 binding，不能临时编一个。
func validateHydratedResumeRequest(req ResumeRequest) error {
	if req.Provider == "" {
		return errors.New("provider is required")
	}
	if req.AgentID == "" {
		return errors.New("agent id is required")
	}
	if err := validateResumeProviderThreadID(req.Provider, req.ProviderThreadID); err != nil {
		return err
	}
	return nil
}

func validateResumeProviderThreadID(provider, providerThreadID string) error {
	providerThreadID = strings.TrimSpace(providerThreadID)
	if providerThreadID == "" {
		return errors.New("provider thread id is required")
	}
	if err := validateProviderThreadID(provider, providerThreadID); err != nil {
		return err
	}
	return nil
}

// hydrateResumeRuntimeSelection 从历史线程状态还原 runtime 选择。
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

func mergeResumeBindingState(state *resumeState, binding *threadBindingStoreRecord) {
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

// providerRuntimeConfig 复制 thread runtime 配置并剥离只给本地迁移流程使用的标记。
func providerRuntimeConfig(runtime map[string]any) map[string]any {
	out := clone.RuntimeConfigMap(runtime)
	delete(out, legacyPromptSnapshotMigrationRuntimeKey)
	return out
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
		PrefixShape:           toProviderPrefixShape(assembly.PrefixShape),
		UserContext:           clone.StringMap(assembly.UserContext),
		UserContextText:       strings.TrimSpace(assembly.UserContextText),
		SystemContext:         dto.SystemContext(clone.StringMap(assembly.SystemContext)),
	}
}

func toProviderPrefixShape(shape contract.PrefixShape) dto.PrefixShape {
	return dto.PrefixShape{
		Hash:                strings.TrimSpace(shape.Hash),
		StaticSectionNames:  append([]string(nil), shape.StaticSectionNames...),
		DynamicSectionNames: append([]string(nil), shape.DynamicSectionNames...),
		SuppressedToolNames: append([]string(nil), shape.SuppressedToolNames...),
		CachedPrefixBytes:   shape.CachedPrefixBytes,
		UncachedTailBytes:   shape.UncachedTailBytes,
		DeveloperBytes:      shape.DeveloperBytes,
		ChurnReason:         strings.TrimSpace(shape.ChurnReason),
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

// buildStartSessionConfig 构建 provider 启动会话所需的配置。
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
	putConfigMCPServerConfigs(cfg, "mcpConfig", input.MCPSnapshot.ServerConfigs)
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
	mergeStartRequestConfig(cfg, req.Config, input.Provider)
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

// mergeStartRequestConfig 把 thread/start config 合入 provider config。
// Codex native 工具禁用项可能同时来自 prompt assembly 和 launch env，必须合并而不是后者被同名键跳过。
func mergeStartRequestConfig(cfg map[string]any, values map[string]any, provider string) {
	codexProvider := strings.EqualFold(strings.TrimSpace(provider), "codex")
	for key, value := range values {
		if codexProvider && key == "codexDisabledNativeTools" {
			mergeConfigStringListValue(cfg, key, value)
			continue
		}
		mergeConfigValueIfAbsent(cfg, key, value)
	}
}

// mergeConfigStringListValue 合并 launch config 中的字符串列表，保留已有 assembly 值并暴露坏配置给 provider 校验。
func mergeConfigStringListValue(cfg map[string]any, key string, value any) {
	incoming, ok := configStringListValue(value)
	if !ok {
		cfg[key] = value
		return
	}
	if len(incoming) == 0 {
		return
	}
	existing, ok := cfg[key].([]string)
	if !ok {
		cfg[key] = incoming
		return
	}
	cfg[key] = appendUniqueConfigStrings(existing, incoming)
}

// configStringListValue 接受 JSON-RPC 解码后的 []any 或本地 []string，并清理空白字符串。
// 其他形态返回 ok=false，让后续 provider 校验报告原始坏配置。
func configStringListValue(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return cleanedConfigStrings(typed), true
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			values = append(values, text)
		}
		return cleanedConfigStrings(values), true
	default:
		return nil, false
	}
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
	if len(session) > 0 {
		runtime = mergeStartSessionRuntimeIdentity(runtime, session[0])
	}
	return storedThreadConfig{
		Model:           strings.TrimSpace(input.Model),
		Effort:          strings.TrimSpace(req.Effort),
		Approvals:       strings.TrimSpace(req.ApprovalPolicy),
		Personality:     strings.TrimSpace(req.Personality),
		Runtime:         runtime,
		ToolSurfaceMode: strings.TrimSpace(req.ToolSurfaceMode),
	}
}

func mergeStartSessionRuntimeIdentity(runtime map[string]any, session contract.Session) map[string]any {
	rc, ok := session.(interface{ RuntimeConfigSnapshot() map[string]any })
	if !ok {
		return runtime
	}
	cfg := rc.RuntimeConfigSnapshot()
	for _, key := range []string{"codexHome", "codexInstanceKey", "codexModelProvider"} {
		value, _ := cfg[key].(string)
		if value = strings.TrimSpace(value); value == "" {
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

func cleanedConfigStrings(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func appendUniqueConfigStrings(existing, incoming []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	merged := make([]string, 0, len(existing)+len(incoming))
	for _, item := range append(existing, incoming...) {
		tool := strings.TrimSpace(item)
		if tool == "" {
			continue
		}
		if _, exists := seen[tool]; exists {
			continue
		}
		seen[tool] = struct{}{}
		merged = append(merged, tool)
	}
	return merged
}

// putConfigStringMap 把字符串 map 写入 provider 配置。
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

func putConfigMCPServerConfigs(cfg map[string]any, key string, values map[string]contract.MCPServerConfig) {
	servers := renderMCPServerConfigMap(values)
	if len(servers) == 0 {
		return
	}
	cfg[key] = map[string]any{"mcpServers": servers}
}

// renderMCPServerConfigMap 渲染 MCP server 配置 map。
func renderMCPServerConfigMap(values map[string]contract.MCPServerConfig) map[string]any {
	if len(values) == 0 {
		return nil
	}
	names := make([]string, 0, len(values))
	rawNames := make(map[string]string, len(values))
	for rawName := range values {
		name := strings.TrimSpace(rawName)
		if name != "" {
			names = append(names, name)
			rawNames[name] = rawName
		}
	}
	sort.Strings(names)
	out := make(map[string]any, len(names))
	for _, name := range names {
		config := values[rawNames[name]]
		server := map[string]any{}
		putConfigString(server, "transport", config.Transport)
		putConfigString(server, "url", config.URL)
		if headers := renderMCPServerHeaderMap(config.Headers); len(headers) > 0 {
			server["headers"] = headers
		}
		putConfigString(server, "command", config.Command)
		if args := renderMCPServerStringList(config.Args); len(args) > 0 {
			server["args"] = args
		}
		if env := renderMCPServerHeaderMap(config.Env); len(env) > 0 {
			server["env"] = env
		}
		if len(server) > 0 {
			server[contract.RuntimeMCPTrustedServerIDKey] = name
			out[name] = server
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func renderMCPServerStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// renderMCPServerHeaderMap 渲染 MCP server header map。
func renderMCPServerHeaderMap(headers map[string]string) map[string]any {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]any, len(headers))
	for name, value := range headers {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name != "" && value != "" {
			out[name] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
