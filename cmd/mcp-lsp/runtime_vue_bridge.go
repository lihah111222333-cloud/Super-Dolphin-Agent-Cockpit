package main

// 本文件故意不加 windows build tag：Vue/TypeScript 的协议桥、文档镜像和 UTF-16
// 分流是平台无关公共契约。Windows 解析器当前会提供真实 companion spec，非 Windows
// 解析器则显式返回 nil，因此共享工厂能保持同一类型边界而不会在其他平台启动 Windows 路径。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

const (
	runtimeVueTSRequestMethod  = "tsserver/request"
	runtimeVueTSResponseMethod = "tsserver/response"
	runtimeTSExecuteCommand    = "typescript.tsserverRequest"
	runtimeLSPExecuteCommand   = "workspace/executeCommand"
)

// runtimeVueTSBridgeSpec 保存同一受管 Node cohort 中 Vue 与 TypeScript companion 的显式路径。
// 该结构只由 Windows 解析 helper 填充；桥接本身不从 PATH 猜测任何依赖。
type runtimeVueTSBridgeSpec struct {
	typescriptBinary     string
	typescriptModuleRoot string
	vuePluginLocation    string
}

// runtimeVueTSBridgeClient 把 Vue LSP 与真实 TypeScript LSP 绑定为一个 manager client。
// Vue 的 tsserver/request 是通知而非 LSP request，因此由 transport 的通用通知 seam
// 调用 handleServerNotification，再将真实 TypeScript 结果原样回发给 Vue。
type runtimeVueTSBridgeClient struct {
	vue multilsp.Client
	ts  multilsp.Client

	documentsMu sync.RWMutex
	documents   map[string]string

	closeOnce sync.Once
	closeErr  error
}

func newRuntimeVueTSBridgeClient(vue, ts multilsp.Client) *runtimeVueTSBridgeClient {
	return &runtimeVueTSBridgeClient{vue: vue, ts: ts, documents: make(map[string]string)}
}

// runtimeServerStartVueTSBridge 启动同一受管 Node cohort 的 TypeScript companion。
// spec 已在 Windows 只读解析阶段 fail-fast 校验；这里不执行 PATH 探测或安装动作。
func runtimeServerStartVueTSBridge(spec runtimeVueTSBridgeSpec, dir string, env []string) (*runtimeVueTSBridgeClient, error) {
	processBinary, err := runtimeServerPlatformProcessBinary(spec.typescriptBinary)
	if err != nil {
		return nil, err
	}
	companionEnv, err := runtimeServerPrepareVueTSCompanionEnvironment(env)
	if err != nil {
		return nil, err
	}
	ts, err := multilsp.NewClientWithOptions(multilsp.Options{
		Binary:      processBinary,
		Args:        []string{"--stdio"},
		Dir:         dir,
		Env:         companionEnv,
		InitOptions: runtimeVueTSBridgeInitializationOptions(spec),
	})
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("start Vue TypeScript companion: %w", err),
			multilsp.ReleaseResourceCohortLease(companionEnv),
		)
	}
	return newRuntimeVueTSBridgeClient(nil, ts), nil
}

// handleServerNotification 转发官方 Vue tsserver/request 协议，不生成伪造的 null 结果。
func (b *runtimeVueTSBridgeClient) handleServerNotification(
	ctx context.Context,
	method string,
	params json.RawMessage,
	send multilsp.ServerNotificationSender,
) error {
	if method != runtimeVueTSRequestMethod {
		return multilsp.ErrMethodNotSupported
	}
	if b == nil || b.ts == nil {
		return errors.New("Vue TypeScript bridge companion client is unavailable")
	}
	if send == nil {
		return errors.New("Vue TypeScript bridge response sender is unavailable")
	}
	request, err := decodeRuntimeVueTSRequest(params)
	if err != nil {
		return err
	}
	var command string
	if err := json.Unmarshal(request[1], &command); err != nil || strings.TrimSpace(command) == "" {
		if err == nil {
			err = errors.New("empty Vue tsserver command")
		}
		return fmt.Errorf("decode Vue tsserver command: %w", err)
	}
	command = strings.TrimSpace(command)
	executeParams := map[string]any{
		"command": runtimeTSExecuteCommand,
		"arguments": []any{
			command,
			json.RawMessage(request[2]),
		},
	}
	body, err := b.ts.Request(ctx, runtimeLSPExecuteCommand, executeParams)
	if err != nil {
		return fmt.Errorf("forward Vue tsserver command %q to TypeScript LSP: %w", command, err)
	}
	responseBody, err := runtimeVueTSResponseBody(body)
	if err != nil {
		return fmt.Errorf("decode TypeScript LSP response for Vue tsserver command %q: %w", command, err)
	}
	response := []any{
		[]any{json.RawMessage(request[0]), responseBody},
	}
	if err := send(ctx, runtimeVueTSResponseMethod, response); err != nil {
		return fmt.Errorf("send Vue tsserver response for command %q: %w", command, err)
	}
	return nil
}

// runtimeVueTSResponseBody 只提取真实 TypeScript LSP executeCommand 的 body；缺失或 null 必须 fail-fast。
func runtimeVueTSResponseBody(raw json.RawMessage) (json.RawMessage, error) {
	var result struct {
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode executeCommand result: %w", err)
	}
	if len(result.Body) == 0 || strings.EqualFold(strings.TrimSpace(string(result.Body)), "null") {
		return nil, errors.New("TypeScript LSP executeCommand result has no body")
	}
	return append(json.RawMessage(nil), result.Body...), nil
}

// decodeRuntimeVueTSRequest 严格校验官方 [id, command, payload] 三元组，拒绝隐式补字段。
func decodeRuntimeVueTSRequest(params json.RawMessage) ([]json.RawMessage, error) {
	var request []json.RawMessage
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, fmt.Errorf("decode Vue tsserver/request params: %w", err)
	}
	if len(request) == 1 {
		var nested []json.RawMessage
		if err := json.Unmarshal(request[0], &nested); err == nil && len(nested) > 0 {
			request = nested
		}
	}
	if len(request) != 3 || len(request[0]) == 0 || len(request[2]) == 0 {
		return nil, fmt.Errorf("Vue tsserver/request params must contain id, command, and payload")
	}
	return request, nil
}

// runtimeVueTSBridgeInitializationOptions 为 TypeScript LSP 注入锁定模块与 Vue plugin。
// plugin location 使用同一 cohort 的 @vue/language-server 目录，避免工作区或 PATH 污染。
func runtimeVueTSBridgeInitializationOptions(spec runtimeVueTSBridgeSpec) map[string]any {
	return map[string]any{
		"tsserver": map[string]any{
			"fallbackPath":    spec.typescriptModuleRoot,
			"useSyntaxServer": "never",
		},
		"plugins": []any{
			map[string]any{
				"name":            "@vue/typescript-plugin",
				"location":        spec.vuePluginLocation,
				"languages":       []string{"vue"},
				"configNamespace": "typescript",
			},
		},
	}
}

// Initialize 先准备真实 TypeScript companion，再启动 Vue，保证首次 tsserver/request 有接收者。
func (b *runtimeVueTSBridgeClient) Initialize(ctx context.Context, rootURI string) error {
	if b == nil || b.vue == nil || b.ts == nil {
		return errors.New("Vue TypeScript bridge clients are incomplete")
	}
	if err := b.ts.Initialize(ctx, rootURI); err != nil {
		return fmt.Errorf("initialize Vue TypeScript companion: %w", err)
	}
	if err := b.vue.Initialize(ctx, rootURI); err != nil {
		return fmt.Errorf("initialize Vue language server: %w", err)
	}
	return nil
}

// Shutdown 按 Vue 后 TypeScript 的顺序结束两个真实 LSP 进程，并保留两侧错误。
func (b *runtimeVueTSBridgeClient) Shutdown(ctx context.Context) error {
	if b == nil {
		return nil
	}
	var vueErr, tsErr error
	if b.vue != nil {
		vueErr = b.vue.Shutdown(ctx)
	}
	if b.ts != nil {
		tsErr = b.ts.Shutdown(ctx)
	}
	return errors.Join(vueErr, tsErr)
}

func (b *runtimeVueTSBridgeClient) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if b == nil || b.vue == nil || b.ts == nil {
		return nil, errors.New("Vue language server client is unavailable")
	}
	useTypeScript, err := b.shouldRouteScriptRequest(method, params)
	if err != nil {
		return nil, err
	}
	if useTypeScript {
		return b.ts.Request(ctx, method, params)
	}
	return b.vue.Request(ctx, method, params)
}

func (b *runtimeVueTSBridgeClient) Notify(ctx context.Context, method string, params any) error {
	if b == nil || b.vue == nil {
		return errors.New("Vue language server client is unavailable")
	}
	return b.vue.Notify(ctx, method, params)
}

// DidOpen 镜像到真实 TypeScript companion，使其 tsserver project 状态与 Vue 一致。
func (b *runtimeVueTSBridgeClient) DidOpen(ctx context.Context, uri, languageID string, version int, text string) error {
	if b == nil || b.vue == nil || b.ts == nil {
		return errors.New("Vue TypeScript bridge clients are incomplete")
	}
	if err := b.ts.DidOpen(ctx, uri, languageID, version, text); err != nil {
		return fmt.Errorf("mirror Vue didOpen to TypeScript LSP: %w", err)
	}
	if err := b.vue.DidOpen(ctx, uri, languageID, version, text); err != nil {
		return err
	}
	b.documentsMu.Lock()
	b.documents[uri] = text
	b.documentsMu.Unlock()
	return nil
}

// DidChange 镜像增量或全文内容变更到真实 TypeScript companion。
func (b *runtimeVueTSBridgeClient) DidChange(ctx context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error {
	if b == nil || b.vue == nil || b.ts == nil {
		return errors.New("Vue TypeScript bridge clients are incomplete")
	}
	if err := b.ts.DidChange(ctx, uri, version, changes); err != nil {
		return fmt.Errorf("mirror Vue didChange to TypeScript LSP: %w", err)
	}
	if err := b.vue.DidChange(ctx, uri, version, changes); err != nil {
		return err
	}
	updatedText, err := b.documentAfterChanges(uri, changes)
	if err != nil {
		return fmt.Errorf("track Vue document changes for TypeScript semantic routing: %w", err)
	}
	b.documentsMu.Lock()
	b.documents[uri] = updatedText
	b.documentsMu.Unlock()
	return nil
}

// DidClose 同时释放 Vue 与 TypeScript companion 的文档状态。
func (b *runtimeVueTSBridgeClient) DidClose(ctx context.Context, uri string) error {
	if b == nil || b.vue == nil || b.ts == nil {
		return errors.New("Vue TypeScript bridge clients are incomplete")
	}
	vueErr := b.vue.DidClose(ctx, uri)
	tsErr := b.ts.DidClose(ctx, uri)
	b.documentsMu.Lock()
	delete(b.documents, uri)
	b.documentsMu.Unlock()
	return errors.Join(vueErr, tsErr)
}

// shouldRouteScriptRequest 将 Vue SFC 的脚本区标准语义请求交给同 cohort 的 TypeScript LSP。
// Vue 3 standalone server 负责模板语言服务，而官方客户端让 ts_ls 负责脚本 hover/definition/references；
// 这里按真实文档和零基 UTF-16 位置显式分流，不用空响应或错误吞掉另一侧的语义结果。
func (b *runtimeVueTSBridgeClient) shouldRouteScriptRequest(method string, params any) (bool, error) {
	if method != "textDocument/hover" && method != "textDocument/definition" && method != "textDocument/references" {
		return false, nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return false, fmt.Errorf("encode %s parameters for Vue script routing: %w", method, err)
	}
	var request struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position protocol.Position `json:"position"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return false, fmt.Errorf("decode %s parameters for Vue script routing: %w", method, err)
	}
	if strings.TrimSpace(request.TextDocument.URI) == "" {
		return false, fmt.Errorf("%s parameters have no textDocument.uri", method)
	}
	b.documentsMu.RLock()
	text, ok := b.documents[request.TextDocument.URI]
	b.documentsMu.RUnlock()
	if !ok {
		return false, fmt.Errorf("Vue script routing has no mirrored document snapshot for %s", request.TextDocument.URI)
	}
	inScript, err := runtimeVuePositionInScript(text, request.Position)
	if err != nil {
		return false, fmt.Errorf("classify %s position for Vue script routing: %w", method, err)
	}
	return inScript, nil
}

// documentAfterChanges 保持与两侧 LSP 同步的完整 SFC 快照，支持全文和 UTF-16 增量变更。
// 语义路由只基于该快照，避免 patch 后继续用旧 script/template 边界误判服务端。
func (b *runtimeVueTSBridgeClient) documentAfterChanges(uri string, changes []protocol.TextDocumentContentChangeEvent) (string, error) {
	b.documentsMu.RLock()
	text, ok := b.documents[uri]
	b.documentsMu.RUnlock()
	if !ok {
		return "", fmt.Errorf("missing mirrored document snapshot for %s", uri)
	}
	for _, change := range changes {
		if change.Range == nil {
			text = change.Text
			continue
		}
		start, err := runtimeVueUTF16Offset(text, change.Range.Start)
		if err != nil {
			return "", fmt.Errorf("resolve change start: %w", err)
		}
		end, err := runtimeVueUTF16Offset(text, change.Range.End)
		if err != nil {
			return "", fmt.Errorf("resolve change end: %w", err)
		}
		if start > end {
			return "", fmt.Errorf("change start offset %d exceeds end offset %d", start, end)
		}
		text = text[:start] + change.Text + text[end:]
	}
	return text, nil
}

// runtimeVuePositionInScript 判断零基 UTF-16 位置是否落在任一 <script> 块内。
// 只负责 SFC 结构分流；列值仍由 LSP 服务端解释，越界位置直接报错而不是静默改写。
func runtimeVuePositionInScript(text string, position protocol.Position) (bool, error) {
	if position.Line < 0 || position.Character < 0 {
		return false, fmt.Errorf("negative LSP position line=%d character=%d", position.Line, position.Character)
	}
	lines := strings.Split(text, "\n")
	if position.Line >= len(lines) {
		return false, fmt.Errorf("LSP line %d is outside document with %d lines", position.Line, len(lines))
	}
	line := strings.TrimSuffix(lines[position.Line], "\r")
	if _, err := runtimeVueUTF16Offset(line, protocol.Position{Line: 0, Character: position.Character}); err != nil {
		return false, fmt.Errorf("LSP character %d is outside line %d: %w", position.Character, position.Line, err)
	}
	inScript := false
	for index, current := range lines {
		current = strings.TrimSuffix(current, "\r")
		lower := strings.ToLower(current)
		open := strings.Index(lower, "<script")
		close := strings.Index(lower, "</script")
		if open >= 0 && runtimeVueScriptTagBoundary(lower, open+len("<script")) {
			inScript = true
		}
		if inScript && index == position.Line {
			return true, nil
		}
		if close >= 0 && inScript {
			inScript = false
		}
	}
	return false, nil
}

func runtimeVueScriptTagBoundary(line string, index int) bool {
	if index >= len(line) {
		return true
	}
	switch line[index] {
	case ' ', '\t', '>', '/':
		return true
	default:
		return false
	}
}

// runtimeVueUTF16Offset 将 LSP 零基行列转换为 UTF-8 字节偏移，供增量变更快照使用。
func runtimeVueUTF16Offset(text string, position protocol.Position) (int, error) {
	if position.Line < 0 || position.Character < 0 {
		return 0, fmt.Errorf("negative position line=%d character=%d", position.Line, position.Character)
	}
	lineStart := 0
	for line := 0; line < position.Line; line++ {
		next := strings.IndexByte(text[lineStart:], '\n')
		if next < 0 {
			return 0, fmt.Errorf("line %d is outside document", position.Line)
		}
		lineStart += next + 1
	}
	lineEnd := strings.IndexByte(text[lineStart:], '\n')
	if lineEnd < 0 {
		lineEnd = len(text) - lineStart
	}
	lineText := text[lineStart : lineStart+lineEnd]
	if strings.HasSuffix(lineText, "\r") {
		lineText = strings.TrimSuffix(lineText, "\r")
	}
	units := 0
	for byteIndex, r := range lineText {
		if units == position.Character {
			return lineStart + byteIndex, nil
		}
		runeUnits := utf16.RuneLen(r)
		if runeUnits < 0 {
			runeUnits = 1
		}
		if units+runeUnits > position.Character {
			return 0, fmt.Errorf("position splits UTF-16 code unit for rune %q", r)
		}
		units += runeUnits
	}
	if units == position.Character {
		return lineStart + len(lineText), nil
	}
	return 0, fmt.Errorf("character %d exceeds line UTF-16 length %d", position.Character, units)
}

// Close 关闭两个真实 transport；重复调用只执行一次，避免 companion 残留或重复释放。
func (b *runtimeVueTSBridgeClient) Close() error {
	if b == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		var vueErr, tsErr error
		if b.vue != nil {
			vueErr = b.vue.Close()
		}
		if b.ts != nil {
			tsErr = b.ts.Close()
		}
		b.closeErr = errors.Join(vueErr, tsErr)
	})
	return b.closeErr
}

// UnderlyingLSPClient 保留 Vue transport owner 供通用生命周期包装器继续探测。
func (b *runtimeVueTSBridgeClient) UnderlyingLSPClient() multilsp.Client {
	if b == nil {
		return nil
	}
	return b.vue
}

// Healthy 将 pool 健康判定交给 Vue 主 transport；TypeScript companion 失败时必须失效。
func (b *runtimeVueTSBridgeClient) Healthy() bool {
	if b == nil || b.vue == nil || b.ts == nil {
		return false
	}
	vueHealth, vueOK := b.vue.(multilsp.HealthCheckedClient)
	tsHealth, tsOK := b.ts.(multilsp.HealthCheckedClient)
	if !vueOK || !tsOK {
		return false
	}
	return vueHealth.Healthy() && tsHealth.Healthy()
}

// ServerCapabilities 保留 Vue 主服务的能力快照，不把 companion 能力混入外部契约。
func (b *runtimeVueTSBridgeClient) ServerCapabilities() protocol.ServerCapabilities {
	if b == nil || b.vue == nil {
		return protocol.ServerCapabilities{}
	}
	capabilities, ok := b.vue.(multilsp.ServerCapabilitiesClient)
	if !ok {
		return protocol.ServerCapabilities{}
	}
	return capabilities.ServerCapabilities()
}

var (
	_ multilsp.Client                   = (*runtimeVueTSBridgeClient)(nil)
	_ multilsp.WrappedClient            = (*runtimeVueTSBridgeClient)(nil)
	_ multilsp.HealthCheckedClient      = (*runtimeVueTSBridgeClient)(nil)
	_ multilsp.ServerCapabilitiesClient = (*runtimeVueTSBridgeClient)(nil)
)
