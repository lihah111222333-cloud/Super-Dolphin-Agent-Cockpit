package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	codexprotocol "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/supportutil"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/toolsurface"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
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

// startingTurnState 是唯一允许在 turn/start 响应前暂存终态的 session-local owner。
// 它在响应绑定为 live turn 或 RPC 失败时立即释放，不能承载多个并发 start。
type startingTurnState struct {
	params   turnStartParams
	terminal *dto.RawProviderEvent
}

// StartTurn 启动 provider turn，并记录本地 turn handle、trace 和可重放状态。
// 动态工具和模型解析必须在远端调用前完成，失败时不会登记 active turn。
func (s *session) StartTurn(ctx context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
	params, err := s.prepareTurnStartParams(ctx, req)
	if err != nil {
		return nil, err
	}
	return s.startTurnWithProvisionalOwner(ctx, req, params)
}

// prepareTurnStartParams 在远端调用前完成 thread、动态工具和运行时模型配置校验。
func (s *session) prepareTurnStartParams(ctx context.Context, req dto.TurnRequest) (turnStartParams, error) {
	threadID, err := requireThreadID(s, req.ThreadID)
	if err != nil {
		return turnStartParams{}, err
	}
	params, err := buildTurnStartParams(threadID, req)
	if err != nil {
		return turnStartParams{}, err
	}
	if err := s.applyTurnToolScopeRuntimeConfig(req); err != nil {
		return turnStartParams{}, err
	}
	dynamicTools, err := s.prepareTurnDynamicTools(ctx, req)
	if err != nil {
		return turnStartParams{}, err
	}
	params.DynamicTools = dynamicTools
	if err := s.applyRuntimeTurnStartOverrides(ctx, &params); err != nil {
		return turnStartParams{}, err
	}
	s.logTurnStartParams(params)
	return params, nil
}

// logTurnStartParams 仅记录安全的配置形状，避免把 sandbox 内容写入日志。
func (s *session) logTurnStartParams(params turnStartParams) {
	pkglogger.Debug("codexapp: turn/start params",
		"agent_id", s.agentID,
		"model", params.Model,
		"effort", params.Effort,
		"sandbox_policy_shape", sandboxPolicyLogShape(params.SandboxPolicy),
	)
}

// startTurnWithProvisionalOwner 在 RPC 响应前登记唯一 owner，保证早到终态可延后验证和结算。
func (s *session) startTurnWithProvisionalOwner(ctx context.Context, req dto.TurnRequest, params turnStartParams) (contract.TurnHandle, error) {
	starting, err := s.reserveStartingTurn(params)
	if err != nil {
		return nil, err
	}
	defer s.discardStartingTurn(starting)
	raw, err := callWithTimeout(ctx, callTargetFunc(s.callTransport), 30*time.Second, "turn/start", params)
	if err != nil {
		return nil, supportutil.WrapCodexModelUnsupportedError(err, params.Model)
	}
	return s.bindTurnStartResponse(ctx, req, starting, raw)
}

// bindTurnStartResponse 将 RPC 响应绑定到 provisional owner，并且只分发已验证的早到终态。
func (s *session) bindTurnStartResponse(ctx context.Context, req dto.TurnRequest, starting *startingTurnState, raw []byte) (contract.TurnHandle, error) {
	resp, err := decodeTurnStartResult(raw)
	if err != nil {
		return nil, err
	}
	providerID := strings.TrimSpace(resp.Turn.ID)
	h := newTurnHandle(resolveLocalTurnID(req.LocalID, providerID), providerID)
	h.trace, _ = observability.TraceFromContext(ctx)
	earlyTerminal, err := s.bindStartingTurn(starting, h, providerID)
	if err != nil {
		return nil, err
	}
	if earlyTerminal != nil {
		s.dispatch(*earlyTerminal)
		s.finishTurn(*earlyTerminal)
	}
	return h, nil
}

// reserveStartingTurn 为尚未取得 provider turn ID 的 turn/start 建立唯一 provisional owner。
func (s *session) reserveStartingTurn(params turnStartParams) (*startingTurnState, error) {
	if s == nil {
		return nil, errors.New("codexapp: session is required for turn/start")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startingTurn != nil {
		return nil, errors.New("codexapp: turn/start already in flight")
	}
	starting := &startingTurnState{params: cloneTurnStartParams(params)}
	s.startingTurn = starting
	return starting, nil
}

// discardStartingTurn 只释放调用方自己的 provisional owner，避免失败 RPC 留下无主终态暂存。
func (s *session) discardStartingTurn(starting *startingTurnState) {
	if s == nil || starting == nil {
		return
	}
	s.mu.Lock()
	if s.startingTurn == starting {
		s.startingTurn = nil
	}
	s.mu.Unlock()
}

// bindStartingTurn 原子地把 provisional owner 绑定为 live turn，并领取同一 start 期间收到的首个终态。
func (s *session) bindStartingTurn(starting *startingTurnState, handle *turnHandle, providerID string) (*dto.RawProviderEvent, error) {
	providerID = strings.TrimSpace(providerID)
	if s == nil || starting == nil || handle == nil || providerID == "" {
		return nil, errors.New("codexapp: bind turn/start requires provisional owner, handle, and provider turn id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startingTurn != starting {
		return nil, errors.New("codexapp: turn/start provisional owner was lost")
	}
	s.startingTurn = nil
	if s.turns == nil {
		s.turns = map[string]*turnHandle{}
	}
	s.turns[providerID] = handle
	s.setActiveTurnLocked(providerID)
	s.rememberPendingTurnLocked(handle, starting.params)
	if starting.terminal == nil {
		return nil, nil
	}
	payload := decodeAnyPayload(starting.terminal.Data)
	if payloadTurnID(payload) != providerID {
		return nil, nil
	}
	handle.terminalClaimed = true
	staged := *starting.terminal
	return &staged, nil
}

// stageStartingTerminal 暂存唯一 provisional start 期间的首个同线程 terminal，等待响应提供可验证的 turn ID。
func (s *session) stageStartingTerminal(raw dto.RawProviderEvent) bool {
	if s == nil || raw.Terminal == nil {
		return false
	}
	payload := decodeAnyPayload(raw.Data)
	if payloadTurnID(payload) == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	starting := s.startingTurn
	if starting == nil {
		return false
	}
	if expected, actual := strings.TrimSpace(starting.params.ThreadID), payloadThreadID(payload); expected != "" && actual != "" && expected != actual {
		return false
	}
	if starting.terminal != nil {
		return true
	}
	staged := raw
	starting.terminal = &staged
	return true
}

func buildTurnStartParams(threadID string, req dto.TurnRequest) (turnStartParams, error) {
	selectedSkills := selectedSkillNames(req.Skills)
	inputs, err := turnInputsFromRequest(req.Inputs, req.TurnAssembly)
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
	inputs, err := turnInputsFromRequest(req.Inputs, req.TurnAssembly)
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

func turnInputsFromRequest(inputs []dto.InputItem, assembly dto.TurnAssembly) ([]turnInputItem, error) {
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
