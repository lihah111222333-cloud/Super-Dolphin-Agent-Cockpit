package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
)

const peerReadyTimeout = 10 * time.Second
const peerPollInterval = 300 * time.Millisecond

type mcpClient interface {
	ListTools(context.Context) ([]mcpdto.MCPTool, error)
	CallTool(context.Context, string, json.RawMessage, ToolCallRequest) (*ToolCallResult, error)
	Close() error
}

type codexToolSurface struct {
	keys    []string
	cwd     string
	tools   map[string]codexToolEntry
	aliases map[string]string
	clients []mcpClient
}

type codexToolEntry struct {
	name          string
	realName      string
	executionKind string
	family        string
	client        mcpClient
}

// PrepareCodexToolSurface 准备codex工具surface。
func (h *Handler) PrepareCodexToolSurface(ctx context.Context, scope contract.CodexToolSurfaceScope) ([]contract.DynamicToolSchema, error) {
	if err := validateCodexToolSurfaceScope(scope); err != nil {
		return nil, err
	}
	surface := &codexToolSurface{tools: map[string]codexToolEntry{}, aliases: map[string]string{}}
	out := make([]contract.DynamicToolSchema, 0)
	if err := h.addHostSurfaceTools(surface, &out); err != nil {
		return nil, err
	}
	if err := h.addSkillSurfaceTools(ctx, scope, surface, &out); err != nil {
		return nil, err
	}
	if err := h.addMCPSurfaceTools(ctx, scope, surface, &out); err != nil {
		_ = surface.Close()
		return nil, err
	}
	surface.cwd = normalizeToolCallCWD(scope.CWD)
	surface.keys = codexSurfaceKeys(scope)
	if err := h.storeCodexToolSurface(surface); err != nil {
		h.removeCodexToolSurface(surface)
		if closeErr := surface.Close(); closeErr != nil {
			return nil, fmt.Errorf("%w; additionally close new codex tool surface: %v", err, closeErr)
		}
		return nil, err
	}
	return out, nil
}

func validateCodexToolSurfaceScope(scope contract.CodexToolSurfaceScope) error {
	if strings.TrimSpace(scope.AgentID) == "" {
		return fmt.Errorf("toolbridge: codex tool surface agent id is required")
	}
	if strings.TrimSpace(scope.CWD) == "" {
		return fmt.Errorf("toolbridge: codex tool surface cwd is required")
	}
	if len(scope.Manifest.Binaries) == 0 {
		return fmt.Errorf("toolbridge: codex tool surface manifest is empty")
	}
	return nil
}

func (h *Handler) addHostSurfaceTools(surface *codexToolSurface, out *[]contract.DynamicToolSchema) error {
	if h == nil || h.hostTools == nil {
		return nil
	}
	for _, tool := range h.hostTools.ListHostTools() {
		if err := addSurfaceTool(surface, out, tool, codexToolEntry{name: tool.Name, realName: tool.Name, executionKind: "host", family: "host"}); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) addMCPSurfaceTools(ctx context.Context, scope contract.CodexToolSurfaceScope, surface *codexToolSurface, out *[]contract.DynamicToolSchema) error {
	factory := h.stdioClientFactory
	if factory == nil {
		factory = h.defaultStdioClientFactory
	}
	results, err := prepareMCPSurfaceBinaries(ctx, factory, scope.Manifest.Binaries)
	if err != nil {
		return err
	}
	for _, result := range results {
		surface.clients = append(surface.clients, result.client)
		if err := addMCPToolsToSurface(surface, out, result.binary.Name, result.client, result.tools); err != nil {
			return err
		}
	}
	return nil
}

type mcpSurfaceBinaryResult struct {
	binary providerdto.MCPBinary
	client mcpClient
	tools  []mcpdto.MCPTool
}

// prepareMCPSurfaceBinaries 准备MCPsurface二进制。
func prepareMCPSurfaceBinaries(
	ctx context.Context,
	factory func(context.Context, providerdto.MCPBinary) (mcpClient, error),
	binaries []providerdto.MCPBinary,
) ([]mcpSurfaceBinaryResult, error) {
	if len(binaries) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]mcpSurfaceBinaryResult, len(binaries))
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	recordErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		errMu.Unlock()
	}

	for i, binary := range binaries {
		i, binary := i, binary
		wg.Add(1)
		safego.Go(ctx, nil, "toolbridge.prepareMCPSurfaceBinary", func(workerCtx context.Context) {
			defer wg.Done()
			result := mcpSurfaceBinaryResult{binary: binary}
			client, err := factory(workerCtx, binary)
			if err != nil {
				recordErr(wrapMCPSurfaceBinaryError(binary, err))
				return
			}
			if client == nil {
				recordErr(wrapMCPSurfaceBinaryError(binary, errMCPSurfaceClientNotConfigured))
				return
			}
			result.client = client
			results[i] = result
			tools, err := client.ListTools(workerCtx)
			if err != nil {
				recordErr(wrapMCPSurfaceBinaryError(binary, err))
				return
			}
			result.tools = tools
			results[i] = result
		})
	}
	wg.Wait()
	if firstErr != nil {
		closeMCPClients(results)
		return nil, firstErr
	}
	return results, nil
}

func closeMCPClients(results []mcpSurfaceBinaryResult) {
	for _, result := range results {
		if result.client != nil {
			_ = result.client.Close()
		}
	}
}

// addMCPToolsToSurface 把MCP工具添加为surface。
func addMCPToolsToSurface(surface *codexToolSurface, out *[]contract.DynamicToolSchema, family string, client mcpClient, tools []mcpdto.MCPTool) error {
	for _, tool := range tools {
		if _, reserved := reservedHostOnlySurfaceToolCanonicalName(family, tool.Name); reserved {
			continue
		}
		canonical := canonicalCodexToolName(family, tool.Name)
		if shouldNamespaceExternalMCPTool(surface, family, canonical) {
			canonical = wrappedMCPToolName(family, tool.Name)
		}
		entry := codexToolEntry{name: canonical, realName: tool.Name, executionKind: "stdio", family: strings.TrimSpace(family), client: client}
		if err := addSurfaceTool(surface, out, tool, entry); err != nil {
			return err
		}
		if err := addMCPToolAlias(surface, family, tool.Name, canonical); err != nil {
			return err
		}
		if err := addSurfaceAlias(surface, wrappedMCPToolName(family, tool.Name), canonical); err != nil {
			return err
		}
		for _, alias := range legacyCodexToolAliases(family, canonical) {
			if err := addSurfaceAlias(surface, alias, canonical); err != nil {
				return err
			}
		}
	}
	return nil
}

func addSurfaceTool(surface *codexToolSurface, out *[]contract.DynamicToolSchema, tool mcpdto.MCPTool, entry codexToolEntry) error {
	name := strings.TrimSpace(entry.name)
	if name == "" {
		return fmt.Errorf("toolbridge: codex surface tool name is empty")
	}
	if _, exists := surface.tools[name]; exists {
		return fmt.Errorf("toolbridge: duplicate codex surface tool %q", name)
	}
	if existing, ok := surface.aliases[name]; ok && existing != name {
		return fmt.Errorf("toolbridge: codex surface alias %q maps to both %q and %q", name, existing, name)
	}
	surface.tools[name] = entry
	surface.aliases[name] = name
	*out = append(*out, contract.DynamicToolSchema{
		Name:         name,
		Description:  codexSurfaceDescription(entry, tool.Description),
		InputSchema:  tool.InputSchema,
		OutputSchema: tool.OutputSchema,
	})
	return nil
}

func codexSurfaceDescription(entry codexToolEntry, description string) string {
	description = strings.TrimSpace(description)
	name := strings.TrimSpace(entry.name)
	if description == "" {
		return description
	}
	if strings.Contains(description, "Recommended tool:") && strings.Contains(description, "Why:") {
		return description
	}
	if strings.TrimSpace(entry.family) != mcpdto.ClientKindLSP {
		return description
	}
	return fmt.Sprintf("Recommended tool: %s. Why: %s", name, description)
}

func addSurfaceAlias(surface *codexToolSurface, alias, canonical string) error {
	alias = strings.TrimSpace(alias)
	canonical = strings.TrimSpace(canonical)
	if alias == "" || alias == canonical {
		return nil
	}
	if existing, ok := surface.aliases[alias]; ok && existing != canonical {
		return fmt.Errorf("toolbridge: codex surface alias %q maps to both %q and %q", alias, existing, canonical)
	}
	surface.aliases[alias] = canonical
	return nil
}

func canonicalCodexToolName(family, name string) string {
	name = canonicalToolName(name)
	if strings.TrimSpace(family) == mcpdto.ClientKindOrch {
		return canonicalOrchestrationToolName(name)
	}
	return name
}

// canonicalOrchestrationToolName 处理canonicalorchestration工具名称。
func canonicalOrchestrationToolName(name string) string {
	switch strings.TrimSpace(name) {
	case "orchestration_launch_agent":
		return "launch_agent"
	case "orchestration_send_message":
		return "send_message"
	case "orchestration_stop_agent":
		return "stop_agent"
	case "orchestration_recover_agent":
		return "recover_agent"
	case "orchestration_interrupt_agent":
		return "interrupt_agent"
	case "orchestration_list_agents":
		return "list_agents"
	case "orchestration_get_agent_report":
		return "get_agent_report"
	case "orchestration_get_agent_reports":
		return "get_agent_reports"
	default:
		return strings.TrimSpace(name)
	}
}

func legacyCodexToolAliases(family, canonical string) []string {
	switch strings.TrimSpace(family) {
	case mcpdto.ClientKindLSP:
		if legacy := legacyLSPName(canonical); legacy != "" {
			return []string{legacy, "mcp__lsp__" + legacy}
		}
	case mcpdto.ClientKindOrch:
		if legacy := legacyOrchName(canonical); legacy != "" {
			return []string{legacy, "mcp__orch__" + legacy}
		}
	}
	return nil
}

func codexSurfaceKeys(scope contract.CodexToolSurfaceScope) []string {
	return nonEmptyUnique(surfaceIDKey(scope.SurfaceID), scope.AgentID, scope.ProviderThreadID, scope.LocalThreadID, scope.UIThreadID)
}

func surfaceIDKey(surfaceID string) string {
	surfaceID = strings.TrimSpace(surfaceID)
	if surfaceID == "" {
		return ""
	}
	return "surface:" + surfaceID
}

func nonEmptyUnique(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// routeCodexSurfaceToolCall 处理routecodexsurface工具call。
func (h *Handler) routeCodexSurfaceToolCall(ctx context.Context, req ToolCallRequest) (*ToolCallResult, bool, error) {
	surface := h.lookupCodexToolSurface(req)
	if surface == nil {
		if _, reserved := reservedHostOnlyToolCanonicalName(req.Name); reserved {
			return nil, false, nil
		}
		if req.Scoped && requiresCodexToolSurface(req.Name) {
			return nil, true, fmt.Errorf("toolbridge: codex tool surface is not prepared for agent %q thread %q", req.AgentID, req.ThreadID)
		}
		return nil, false, nil
	}
	if surface.aliases[strings.TrimSpace(req.Name)] == "" {
		if _, reserved := reservedHostOnlyToolCanonicalName(req.Name); reserved {
			return nil, false, nil
		}
	}
	result, err := h.callCodexSurfaceTool(ctx, surface, req)
	return result, true, err
}

func (h *Handler) lookupCodexToolSurface(req ToolCallRequest) *codexToolSurface {
	if h == nil {
		return nil
	}
	h.surfaceMu.Lock()
	defer h.surfaceMu.Unlock()
	keys := nonEmptyUnique(req.ThreadID)
	if len(keys) == 0 {
		keys = nonEmptyUnique(req.AgentID)
	}
	for _, key := range keys {
		if surface := h.surfaces[key]; surface != nil {
			return surface
		}
	}
	return nil
}

// callCodexSurfaceTool 调用codexsurface工具。
func (h *Handler) callCodexSurfaceTool(ctx context.Context, surface *codexToolSurface, req ToolCallRequest) (*ToolCallResult, error) {
	canonical := surface.aliases[strings.TrimSpace(req.Name)]
	if canonical == "" {
		return nil, fmt.Errorf("toolbridge: unknown codex surface tool %q", req.Name)
	}
	entry := surface.tools[canonical]
	eventReq := req
	eventReq.Name = entry.name
	eventReq.ThreadID = codexSurfaceLifecycleThreadID(req)
	started := time.Now()
	publishLifecycle := !contract.ToolLifecycleAlreadyPublished(ctx)
	if publishLifecycle {
		h.publishProxyToolCallBegin(eventReq, started)
	}
	var result *ToolCallResult
	var err error
	defer func() {
		if publishLifecycle {
			h.publishProxyToolCallEnd(eventReq, started, result, err)
		}
	}()
	req.Name = entry.realName
	req = h.injectManagedLaunchContext(ctx, req)
	if entry.executionKind == "host" {
		result, err = h.callHostTool(ctx, req)
		return result, err
	}
	if entry.executionKind == "skill" {
		result, err = h.callSkillSurfaceTool(ctx, surface, req)
		return result, err
	}
	result, err = entry.client.CallTool(ctx, entry.realName, req.Arguments, req)
	return result, err
}

func codexSurfaceLifecycleThreadID(req ToolCallRequest) string {
	if agentID := strings.TrimSpace(req.AgentID); agentID != "" {
		return agentID
	}
	return strings.TrimSpace(req.ThreadID)
}

// Close 关闭平台toolbridge资源。
func (s *codexToolSurface) Close() error {
	if s == nil {
		return nil
	}
	var err error
	for _, client := range s.clients {
		if closeErr := client.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return err
}

func (h *Handler) listPeerTools(ctx context.Context, clientKind string) ([]mcpdto.MCPTool, error) {
	if h == nil || h.registry == nil {
		return nil, ErrNoPeerAvailable
	}
	// Peer processes may still be starting up. Poll with a short timeout.
	peers, err := h.waitForPeer(ctx, clientKind)
	if err != nil {
		return nil, err
	}

	// Bootstrap peer callback returns {"tools":[...]} wrapper.
	var result peerToolsListResult
	if err := peers[0].Peer.Callback(ctx, "tools/list", nil, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

// waitForPeer 等待平台 toolbridge peer 可用。
func (h *Handler) waitForPeer(ctx context.Context, clientKind string) ([]*mcpcontrol.ToolInstance, error) {
	deadline := time.Now().Add(peerReadyTimeout)
	for {
		peers := h.registry.FindActiveByKind(clientKind)
		if len(peers) >= 1 {
			// Use the first active peer. Multiple peers can exist when the
			// codex app-server also spawns MCP sidecars from a resumed thread.
			return peers[:1], nil
		}
		if time.Now().After(deadline) {
			return nil, ErrNoPeerAvailable
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(peerPollInterval):
		}
	}
}

func adaptMCPResponse(resp peerToolCallResponse) (*ToolCallResult, error) {
	items := make([]ToolCallContentItem, 0, len(resp.Content))
	for _, item := range resp.Content {
		items = append(items, ToolCallContentItem{
			Type: "inputText",
			Text: strings.TrimSpace(item.Text),
		})
	}
	if peerToolCallResponseIsEmptySuccess(resp, items) {
		return toolCallEmptyPeerResult(), nil
	}
	structuredContent, err := normalizeToolResultStructuredContent(resp.StructuredContent)
	if err != nil {
		return nil, err
	}
	structuredFailure, err := structuredContentReportsFailure(structuredContent)
	if err != nil {
		return nil, err
	}
	return &ToolCallResult{
		ContentItems:      items,
		StructuredContent: structuredContent,
		Success:           !resp.IsError && !structuredFailure,
	}, nil
}

func peerToolCallResponseIsEmptySuccess(resp peerToolCallResponse, items []ToolCallContentItem) bool {
	if resp.IsError {
		return false
	}
	structured := bytes.TrimSpace(resp.StructuredContent)
	if !(len(structured) == 0 || bytes.Equal(structured, []byte("null")) || bytes.Equal(structured, []byte("{}"))) {
		return false
	}
	if len(items) == 0 {
		return true
	}
	for _, item := range items {
		text := strings.TrimSpace(item.Text)
		if text != "" && text != "null" {
			return false
		}
	}
	return true
}

func toolCallEmptyPeerResult() *ToolCallResult {
	const message = "toolbridge: peer tool returned empty result"
	return &ToolCallResult{
		ContentItems: []ToolCallContentItem{{
			Type: "inputText",
			Text: message,
		}},
		StructuredContent: json.RawMessage(`{"success":false,"error":"toolbridge: peer tool returned empty result"}`),
		Success:           false,
	}
}

func normalizeToolResultStructuredContent(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	return common.StructuredContentFromRaw(raw)
}

// structuredContentReportsFailure 判断 structured content 是否报告失败。
func structuredContentReportsFailure(raw json.RawMessage) (bool, error) {
	trimmed, isObject, err := normalizedStructuredContentObject(raw)
	if err != nil || !isObject {
		return false, err
	}
	var payload struct {
		Success *bool `json:"success"`
		IsError *bool `json:"isError"`
		OK      *bool `json:"ok"`
	}
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return false, fmt.Errorf("toolbridge: decode structuredContent: %w", err)
	}
	return (payload.Success != nil && !*payload.Success) ||
		(payload.IsError != nil && *payload.IsError) ||
		(payload.OK != nil && !*payload.OK), nil
}

func normalizedStructuredContentObject(raw json.RawMessage) (json.RawMessage, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if !json.Valid(trimmed) {
		return nil, false, fmt.Errorf("toolbridge: decode structuredContent: invalid JSON")
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false, nil
	}
	return trimmed, true, nil
}

func toCodexDynamicTools(tools []mcpdto.MCPTool) []contract.DynamicToolSchema {
	out := make([]contract.DynamicToolSchema, 0, len(tools))
	for _, tool := range tools {
		schema := contract.DynamicToolSchema{
			Name:         tool.Name,
			Description:  tool.Description,
			InputSchema:  tool.InputSchema,
			OutputSchema: tool.OutputSchema,
		}

		setDynamicToolDeferLoading(&schema, toolDeferLoading(tool))
		out = append(out, schema)
	}
	return out
}

// decodeToolCallRequest 解码工具call请求。
func decodeToolCallRequest(params json.RawMessage) (ToolCallRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ToolCallRequest{}, fmt.Errorf("toolbridge: missing tool call params")
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(params, &payload); err != nil {
		return ToolCallRequest{}, fmt.Errorf("toolbridge: decode tool call params: %w", err)
	}

	req := ToolCallRequest{
		Name:           firstString(payload, "name", "tool", "toolName", "tool_name"),
		Arguments:      firstRaw(payload, "arguments", "args"),
		AgentID:        firstString(payload, MetadataKeyAgentID, "agentId", "agent_id"),
		ThreadID:       firstString(payload, MetadataKeyThreadID, "threadId", "thread_id"),
		TurnID:         firstString(payload, "turnId", "turn_id"),
		CallID:         firstString(payload, MetadataKeyCallID, "callId", "call_id"),
		CWD:            firstString(payload, MetadataKeyCWD),
		WorkspaceRoots: firstStringSlice(payload, MetadataKeyWorkspaceRoots, "_workspace_roots"),
		ClientKind:     firstString(payload, "clientKind", "client_kind", "family"),
		Scoped:         hasPrivateScopeMetadata(payload),
	}
	if req.Name == "" {
		req.Name = nestedString(payload, "item", "name", "tool", "toolName")
	}
	if req.ThreadID == "" {
		req.ThreadID = nestedString(payload, "thread", "id")
	}
	if req.TurnID == "" {
		req.TurnID = nestedString(payload, "turn", "id")
	}
	if req.CallID == "" {
		req.CallID = nestedString(payload, "item", "callId", "call_id")
	}
	if len(bytes.TrimSpace(req.Arguments)) == 0 {
		req.Arguments = json.RawMessage(`{}`)
	}
	req.CWD = normalizeToolCallCWD(req.CWD)
	req.WorkspaceRoots = normalizeToolCallWorkspaceRoots(req.CWD, req.WorkspaceRoots)
	if strings.TrimSpace(req.Name) == "" {
		return ToolCallRequest{}, fmt.Errorf("toolbridge: missing tool name")
	}
	return req, nil
}

func hasPrivateScopeMetadata(payload map[string]json.RawMessage) bool {
	for _, key := range []string{MetadataKeyAgentID, MetadataKeyThreadID, MetadataKeyCallID} {
		if len(bytes.TrimSpace(payload[key])) != 0 {
			return true
		}
	}
	return false
}
