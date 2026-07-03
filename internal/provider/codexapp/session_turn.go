package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/toolsurface"
)

type turnStartParams struct {
	ThreadID             string                            `json:"threadId"`
	Input                []turnInputItem                   `json:"input"`
	SelectedSkills       []string                          `json:"selectedSkills,omitempty"`
	ManualSkillSelection bool                              `json:"manualSkillSelection,omitempty"`
	Model                string                            `json:"model,omitempty"`
	Effort               string                            `json:"effort,omitempty"`
	SandboxPolicy        json.RawMessage                   `json:"sandboxPolicy,omitempty"`
	OutputSchema         json.RawMessage                   `json:"outputSchema,omitempty"`
	DynamicTools         []codexprotocol.DynamicToolSchema `json:"dynamicTools,omitempty"`
}

type turnInputItem struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	URL     string `json:"url,omitempty"`
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

type turnStartResult struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

func buildTurnStartParams(threadID string, req dto.TurnRequest) (turnStartParams, error) {
	selectedSkills := selectedSkillNames(req.Skills)
	inputs, err := turnInputsFromRequest(req.Inputs, req.Skills, req.TurnAssembly)
	if err != nil {
		return turnStartParams{}, err
	}
	return turnStartParams{
		ThreadID:             threadID,
		Input:                inputs,
		SelectedSkills:       selectedSkills,
		ManualSkillSelection: req.ManualSkillSelection,
		Model:                strings.TrimSpace(req.Overrides.Model),
		Effort:               normalizeCodexAppEffort(req.Overrides.Effort),
		OutputSchema:         req.OutputSchema,
	}, nil
}

// prepareTurnDynamicTools 为每次 turn/start 重新声明 Codex dynamicTools。
// 这样启动后新增或更新的 MCP server 会随下一轮 chat 一起交给模型。
func (s *session) prepareTurnDynamicTools(ctx context.Context, req dto.TurnRequest) ([]codexprotocol.DynamicToolSchema, error) {
	if s == nil {
		return nil, nil
	}
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		cwd = s.runtimeConfigString("cwd")
	}
	input := toolsurface.TurnInput{Enabled: s.dynamicToolsEnabled, AgentID: s.agentID, UIThreadID: req.ThreadID, LocalThreadID: req.ThreadID, ProviderThreadID: s.ThreadID(), SurfaceID: s.ensureToolSurfaceID(), CWD: cwd, WorkspaceRoots: trustedWorkspaceRoots(cwd, req.AdditionalWorkingDirectories), Manifest: req.MCP, Prepare: s.prepareTools, List: s.listTools}
	return toolsurface.PrepareTurn(ctx, input)
}

func (s *session) applyTurnToolScopeRuntimeConfig(req dto.TurnRequest) error {
	cwd := strings.TrimSpace(req.CWD)
	additionalRoots := trimTurnToolScopeRoots(req.AdditionalWorkingDirectories)
	if cwd == "" {
		if len(additionalRoots) > 0 {
			return errors.New("codexapp: turn cwd is required when additional working directories are set")
		}
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtimeConfig == nil {
		s.runtimeConfig = map[string]any{}
	}
	s.runtimeConfig["cwd"] = cwd
	if len(additionalRoots) > 0 {
		s.runtimeConfig["additionalWorkingDirectories"] = additionalRoots
	} else {
		delete(s.runtimeConfig, "additionalWorkingDirectories")
		delete(s.runtimeConfig, "additional_working_directories")
	}
	return nil
}

func trimTurnToolScopeRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		if root = strings.TrimSpace(root); root != "" {
			out = append(out, root)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildTurnSteerParams(threadID string, req dto.SteerRequest) (map[string]any, error) {
	inputs, err := turnInputsFromRequest(req.Inputs, req.Skills, req.TurnAssembly)
	if err != nil {
		return nil, err
	}
	params := map[string]any{
		"threadId":       threadID,
		"expectedTurnId": strings.TrimSpace(req.ExpectedTurnID),
		"input":          inputs,
	}
	if selectedSkills := selectedSkillNames(req.Skills); len(selectedSkills) > 0 {
		params["selectedSkills"] = selectedSkills
	}
	if req.ManualSkillSelection {
		params["manualSkillSelection"] = true
	}
	return params, nil
}

func buildTurnInterruptParams(threadID, turnID, source string) map[string]any {
	params := map[string]any{}
	if threadID = strings.TrimSpace(threadID); threadID != "" {
		params["threadId"] = threadID
	}
	if turnID = strings.TrimSpace(turnID); turnID != "" {
		params["turnId"] = turnID
	}
	if source = strings.TrimSpace(source); source != "" {
		params["source"] = source
	}
	return params
}

func selectedSkillNames(skills []dto.SkillRef) []string {
	selected := make([]string, 0, len(skills))
	for _, skill := range skills {
		if name := strings.TrimSpace(skill.Name); name != "" {
			selected = append(selected, name)
		}
	}
	return selected
}

func turnInputsFromRequest(inputs []dto.InputItem, skills []dto.SkillRef, assembly dto.TurnAssembly) ([]turnInputItem, error) {
	items := make([]turnInputItem, 0, len(inputs)+len(assembly.Attachments)+3)
	// system reminder 和 git 上下文由 thread/start 的 baseInstructions 注入，turn/start 不再重复拼接。
	for _, attachment := range assembly.Attachments {
		if text := strings.TrimSpace(contract.RenderAttachmentText(attachment)); text != "" {
			items = append(items, newTextTurnInput("text", text))
		}
	}
	for i, item := range inputs {
		mapped, err := mapTurnInput(item)
		if err != nil {
			return nil, fmt.Errorf("turn input[%d]: %w", i, err)
		}
		items = append(items, mapped)
	}
	return items, nil
}

// mapTurnInput 将统一 turn 输入转换为 Codex app-server 支持的输入 item。
// 未识别类型直接报错，避免把上层契约漂移静默转换成错误 provider payload。
func mapTurnInput(item dto.InputItem) (turnInputItem, error) {
	typ, ok := shareddto.NormalizeInputType(item.Type)
	if !ok {
		return turnInputItem{}, fmt.Errorf("unsupported input type %q", strings.TrimSpace(item.Type))
	}
	switch typ {
	case "text":
		return textTurnInput(item), nil
	case "filecontent":
		return textTurnInput(dto.InputItem{Content: item.Content}), nil
	case "image":
		return imageTurnInput(item), nil
	case "local_image":
		return localImageTurnInput(item), nil
	case "mention":
		return mentionTurnInput(item), nil
	default:
		return turnInputItem{}, fmt.Errorf("unsupported input type %q", strings.TrimSpace(item.Type))
	}
}

func textTurnInput(item dto.InputItem) turnInputItem {
	return newTextTurnInput("text", item.Content)
}

func imageTurnInput(item dto.InputItem) turnInputItem {
	if url := strings.TrimSpace(item.URL); url != "" {
		return turnInputItem{Type: "image", URL: url}
	}
	path := resolvedInputPath(item)
	if shared.IsRemoteTurnInput(path) {
		return turnInputItem{Type: "image", URL: path}
	}
	return turnInputItem{Type: "localImage", Path: path}
}

func localImageTurnInput(item dto.InputItem) turnInputItem {
	return turnInputItem{Type: "localImage", Path: resolvedInputPath(item)}
}

func mentionTurnInput(item dto.InputItem) turnInputItem {
	path := resolvedInputPath(item)
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = filepath.Base(path)
	}
	return turnInputItem{Type: "mention", Path: path, Name: name}
}

func resolvedInputPath(item dto.InputItem) string {
	if path := strings.TrimSpace(item.Path); path != "" {
		return path
	}
	return strings.TrimSpace(item.Content)
}

const turnOutputAccumulatorMaxBytes = 1 << 20

type turnOutputBuffer struct {
	parts     []string
	size      int
	truncated bool
}

// appendTurnOutputDelta 将流式输出片段追加到 provider turn 的累积器。
// 超过 1 MiB 后只记录 truncated，不再继续保存文本，防止长输出撑爆内存。
func (s *session) appendTurnOutputDelta(turnID, delta string) {
	if s == nil || turnID == "" || delta == "" {
		return
	}
	s.accumulatorMu.Lock()
	defer s.accumulatorMu.Unlock()
	if s.turnOutputAccumulator == nil {
		s.turnOutputAccumulator = map[string]*turnOutputBuffer{}
	}
	buf, ok := s.turnOutputAccumulator[turnID]
	if !ok {
		buf = &turnOutputBuffer{}
		s.turnOutputAccumulator[turnID] = buf
	}
	if buf.truncated {
		return
	}
	if buf.size+len(delta) > turnOutputAccumulatorMaxBytes {
		buf.truncated = true
		return
	}
	buf.parts = append(buf.parts, delta)
	buf.size += len(delta)
}

func (s *session) consumeTurnOutputAccumulator(turnID string) (string, bool) {
	if s == nil || turnID == "" {
		return "", false
	}
	s.accumulatorMu.Lock()
	defer s.accumulatorMu.Unlock()
	buf, ok := s.turnOutputAccumulator[turnID]
	if !ok {
		return "", false
	}
	delete(s.turnOutputAccumulator, turnID)
	if buf == nil {
		return "", false
	}
	return strings.Join(buf.parts, ""), buf.truncated
}

func (s *session) dropTurnOutputAccumulator(turnID string) {
	if s == nil || turnID == "" {
		return
	}
	s.accumulatorMu.Lock()
	defer s.accumulatorMu.Unlock()
	delete(s.turnOutputAccumulator, turnID)
}
