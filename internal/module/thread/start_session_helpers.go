package thread

import (
	"bytes"
	"context"
	"encoding/json"
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
		// P20.4：把 req 上的 launch skill 选择透传到 StartInput，再由 assembler
		// 的 buildStartCtx 转进 BuildCtx；SkillCatalogProvider 按 force/pin 策略
		// 决定 L1 manifest 的渲染形态。两个字段沿 req 零值语义：
		//   - 空 / false → provider 原本的全量扫盘 + 元指令不变
		//   - 非空 + Force=false → 命中的 skill 置顶，其余保留
		//   - 非空 + Force=true  → 只渲染命中的 skill，其余隐藏
		LaunchSkillNames:  append([]string(nil), req.LaunchSkillNames...),
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

// dispatchPromptAssembly routes the call between AssembleStart (main thread /
// user-defined agent types) and AssembleAgent (Claude-taxonomy subagent
// types like Explore / Plan). Unknown AgentType values — including empty,
// "main", "Writer", etc. — fall back to AssembleStart so historical callers
// keep their existing behavior.
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
		DeveloperInstructions: strings.TrimSpace(assembly.DeveloperInstructions),
		ResolvedSections:      toProviderResolvedSections(assembly.ResolvedSections),
		Snapshot:              toProviderPromptSnapshot(assembly.Snapshot),
		SuppressedTools:       append([]string(nil), assembly.SuppressedTools...),
	}
}

func toProviderPromptSnapshot(snapshot contract.PromptAssemblySnapshot) dto.PromptAssemblySnapshot {
	return dto.PromptAssemblySnapshot{
		DisplayName:           strings.TrimSpace(snapshot.DisplayName),
		BaseInstructions:      strings.TrimSpace(snapshot.BaseInstructions),
		DeveloperInstructions: strings.TrimSpace(snapshot.DeveloperInstructions),
		Provider:              strings.TrimSpace(snapshot.Provider),
		Version:               snapshot.Version,
		Hash:                  strings.TrimSpace(snapshot.Hash),
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
	for _, field := range []struct {
		value string
		keys  []string
	}{
		{req.ApprovalPolicy, []string{"approvalPolicy", "approval_policy", "approvals"}},
		{req.ModelProvider, []string{"modelProvider"}},
		{assembly.DeveloperInstructions, []string{"developerInstructions", "developer_instructions"}},
		{req.Summary, []string{"summary"}},
		{req.Effort, []string{"effort"}},
		{req.Personality, []string{"personality"}},
		{input.ParentAgentID, []string{"parentAgentId", "parent_agent_id"}},
		{input.AgentType, []string{"agentType", "agent_type"}},
		{input.AgentMemoryScope, []string{"agentMemoryScope", "agent_memory_scope"}},
		{input.Provider, []string{"provider"}},
		{input.CWD, []string{"cwd"}},
		{input.Model, []string{"model"}},
		{input.GitRoot, []string{"gitRoot"}},
		{input.Language, []string{"language"}},
		{input.ScratchpadDir, []string{"scratchpadDir", "scratchpad_dir"}},
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

func buildStartStoredThreadConfig(req StartRequest, input contract.StartInput, assembly contract.StartAssembly) storedThreadConfig {
	return storedThreadConfig{
		Model:       strings.TrimSpace(input.Model),
		Effort:      strings.TrimSpace(req.Effort),
		Approvals:   strings.TrimSpace(req.ApprovalPolicy),
		Personality: strings.TrimSpace(req.Personality),
		Runtime:     clone.RuntimeConfigMap(buildStartSessionConfig(req, input, assembly)),
	}
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
