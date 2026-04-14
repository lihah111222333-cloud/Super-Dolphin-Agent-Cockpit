package thread

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
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
		DeveloperInstructions:        req.DeveloperInstructions,
		Provider:                     buildCtx.Provider,
		CWD:                          buildCtx.CWD,
		GitRoot:                      buildCtx.GitRoot,
		IsWorktree:                   buildCtx.IsWorktree,
		Language:                     buildCtx.Language,
		Model:                        buildCtx.Model,
		EnabledTools:                 buildCtx.EnabledTools,
		AdditionalWorkingDirectories: buildCtx.AdditionalWorkingDirectories,
		MCPSnapshot:                  buildCtx.MCPSnapshot,
		SessionFlags:                 buildCtx.SessionFlags,
		OutputStyleConfig:            buildCtx.OutputStyleConfig,
		ScratchpadDir:                buildCtx.ScratchpadDir,
		KeepCodingInstructions:       buildCtx.KeepCodingInstructions,
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
	assembly, err := req.PromptAssemblyRef.AssembleStart(ctx, input)
	if err != nil {
		return contract.StartAssembly{}, err
	}
	assembly.DisplayName = normalizeStartDisplayName(shared.FirstNonEmpty(strings.TrimSpace(assembly.DisplayName), req.Name, req.Prompt))
	assembly.BaseInstructions = strings.TrimSpace(assembly.BaseInstructions)
	assembly.DeveloperInstructions = strings.TrimSpace(assembly.DeveloperInstructions)
	return ensureStartAssemblySnapshot(assembly, input.Provider), nil
}

func toProviderStartAssembly(assembly contract.StartAssembly) dto.StartAssembly {
	return dto.StartAssembly{
		DisplayName:           strings.TrimSpace(assembly.DisplayName),
		BaseInstructions:      strings.TrimSpace(assembly.BaseInstructions),
		DeveloperInstructions: strings.TrimSpace(assembly.DeveloperInstructions),
		ResolvedSections:      toProviderResolvedSections(assembly.ResolvedSections),
		Snapshot:              toProviderPromptSnapshot(assembly.Snapshot),
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
	putConfigString(cfg, "approvalPolicy", req.ApprovalPolicy)
	putConfigString(cfg, "approval_policy", req.ApprovalPolicy)
	putConfigString(cfg, "approvals", req.ApprovalPolicy)
	putConfigString(cfg, "modelProvider", req.ModelProvider)
	putConfigString(cfg, "developerInstructions", assembly.DeveloperInstructions)
	putConfigString(cfg, "developer_instructions", assembly.DeveloperInstructions)
	putConfigString(cfg, "summary", req.Summary)
	putConfigString(cfg, "effort", req.Effort)
	putConfigString(cfg, "personality", req.Personality)
	putConfigString(cfg, "provider", input.Provider)
	putConfigString(cfg, "cwd", input.CWD)
	putConfigString(cfg, "model", input.Model)
	putConfigString(cfg, "gitRoot", input.GitRoot)
	putConfigString(cfg, "parentAgentId", input.ParentAgentID)
	putConfigString(cfg, "parent_agent_id", input.ParentAgentID)
	putConfigString(cfg, "agentType", input.AgentType)
	putConfigString(cfg, "agent_type", input.AgentType)
	putConfigString(cfg, "threadKind", startThreadKind(input))
	putConfigString(cfg, "thread_kind", startThreadKind(input))
	putConfigBool(cfg, "isWorktree", input.IsWorktree)
	putConfigString(cfg, "language", input.Language)
	putConfigStrings(cfg, "enabledTools", input.EnabledTools)
	putConfigStrings(cfg, "additionalWorkingDirectories", input.AdditionalWorkingDirectories)
	putConfigStrings(cfg, "claudeMdExcludes", input.ClaudeMdExcludes)
	putConfigStrings(cfg, "claude_md_excludes", input.ClaudeMdExcludes)
	putConfigStrings(cfg, "mcpServers", input.MCPSnapshot.Servers)
	putConfigStrings(cfg, "mcpTools", input.MCPSnapshot.Tools)
	putConfigStringMap(cfg, "mcpInstructions", input.MCPSnapshot.Instructions)
	putConfigBoolMap(cfg, "sessionFlags", input.SessionFlags)
	putConfigOutputStyleConfig(cfg, "outputStyleConfig", input.OutputStyleConfig)
	putConfigOutputStyleConfig(cfg, "output_style_config", input.OutputStyleConfig)
	putConfigString(cfg, "scratchpadDir", input.ScratchpadDir)
	putConfigString(cfg, "scratchpad_dir", input.ScratchpadDir)
	putConfigJSON(cfg, "sandbox", req.Sandbox)
	for key, value := range req.Config {
		if _, exists := cfg[key]; !exists {
			cfg[key] = value
		}
	}
	if len(cfg) == 0 {
		return nil
	}
	return cfg
}

func startThreadKind(input contract.StartInput) string {
	if strings.TrimSpace(input.ParentAgentID) != "" {
		return "child_agent"
	}
	return "main"
}

func buildStartStoredThreadConfig(req StartRequest, input contract.StartInput, assembly contract.StartAssembly) storedThreadConfig {
	return storedThreadConfig{
		Model:       strings.TrimSpace(input.Model),
		Effort:      strings.TrimSpace(req.Effort),
		Approvals:   strings.TrimSpace(req.ApprovalPolicy),
		Personality: strings.TrimSpace(req.Personality),
		Runtime:     shared.CloneRuntimeConfigMap(buildStartSessionConfig(req, input, assembly)),
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

// looksLikeUUID returns true when s resembles a UUID (hex-and-dashes, 32+ hex chars).
// It rejects agent_id placeholders like "agent_17754..." that are not valid provider UUIDs.
func looksLikeUUID(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 32 {
		return false
	}
	hex := 0
	for _, c := range s {
		switch {
		case (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'):
			hex++
		case c == '-':
			// ok
		default:
			return false
		}
	}
	return hex >= 32
}
