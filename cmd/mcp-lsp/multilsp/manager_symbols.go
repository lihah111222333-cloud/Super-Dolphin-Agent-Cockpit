package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

type managerNavigation = manager
type managerXRef = manager
type managerStructure = manager
type managerCompletion = manager
type managerEdit = manager

// Definition 查询符号定义位置，并把 LSP 返回的 location/link 统一成工具层结果。
func (m *managerNavigation) Definition(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error) {
	return m.locationQuery(ctx, uri, protocol.MethodDefinition, protocol.DefinitionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Position:     position,
	})
}

// Implementation 查询接口或抽象符号的实现位置。
// initialize 明确未声明该能力时返回结构化 capability_unsupported；已声明请求的真实空结果仍保持为空。
func (m *managerNavigation) Implementation(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error) {
	return m.locationQuery(ctx, uri, protocol.MethodImplementation, protocol.ImplementationParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Position:     position,
	})
}

// TypeDefinition 查找符号的类型定义。
func (m *managerNavigation) TypeDefinition(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error) {
	return m.locationQuery(ctx, uri, protocol.MethodTypeDefinition, protocol.TypeDefinitionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Position:     position,
	})
}

// Hover 查询指定位置的悬停信息。
// 返回值保持 LSP markup 形状，解码失败会带上 hover 上下文直接报错。
func (m *managerNavigation) Hover(ctx context.Context, uri string, position protocol.Position) (*protocol.HoverResult, error) {
	return requestDocument(ctx, m, uri, protocol.MethodHover,
		func(ref documentRef) any {
			return protocol.HoverParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
				Position:     position,
			}
		},
		func(raw json.RawMessage) (*protocol.HoverResult, error) {
			var result protocol.HoverResult
			if err := decodeInto(raw, &result); err != nil {
				return nil, fmt.Errorf("decode hover: %w", err)
			}
			return &result, nil
		},
		unsupportedDocument[*protocol.HoverResult]("hover"),
	)
}

// SignatureHelp 查询当前位置的函数签名候选。
// 语言服务器不支持时返回能力错误，调用方可据此生成空结果提示。
func (m *managerNavigation) SignatureHelp(ctx context.Context, uri string, position protocol.Position) (*protocol.SignatureHelpResult, error) {
	return requestDocument(ctx, m, uri, protocol.MethodSignatureHelp,
		func(ref documentRef) any {
			return protocol.SignatureHelpParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
				Position:     position,
			}
		},
		func(raw json.RawMessage) (*protocol.SignatureHelpResult, error) {
			var result protocol.SignatureHelpResult
			if err := decodeInto(raw, &result); err != nil {
				return nil, fmt.Errorf("decode signature help: %w", err)
			}
			return &result, nil
		},
		unsupportedDocument[*protocol.SignatureHelpResult]("signature help"),
	)
}

// References 查询符号引用并按 includeDeclaration 保留或排除声明位置。
func (m *managerXRef) References(ctx context.Context, uri string, position protocol.Position, includeDeclaration bool) ([]protocol.LocationResult, error) {
	return m.locationQuery(ctx, uri, protocol.MethodReferences, protocol.ReferenceParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Position:     position,
		Context: protocol.ReferenceContext{
			IncludeDeclaration: includeDeclaration,
		},
	})
}

// CallHierarchy 调用层级。
func (m *managerXRef) CallHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.CallHierarchyResult, error) {
	return queryHierarchy(ctx, m, uri, protocol.MethodPrepareCallHierarchy, position, direction,
		func(ctx context.Context, client Client, item protocol.CallHierarchyItem, direction string) (protocol.CallHierarchyResult, error) {
			return m.resolveCallDirections(ctx, client, item, direction)
		},
		unsupportedHierarchy[protocol.CallHierarchyResult]("call hierarchy"),
	)
}

func (m *manager) resolveCallDirections(ctx context.Context, client Client, item protocol.CallHierarchyItem, direction string) (protocol.CallHierarchyResult, error) {
	result := protocol.CallHierarchyResult{Item: item}
	return resolveHierarchyDirections(ctx, m, client, item, direction, result, []hierarchyDirectionStep[protocol.CallHierarchyItem, protocol.CallHierarchyResult]{
		{
			enabled: func(direction string) bool { return wantCallDirection(direction, "incoming") },
			method:  protocol.MethodCallHierarchyIncoming,
			params: func(item protocol.CallHierarchyItem) any {
				return protocol.CallHierarchyIncomingCallsParams{Item: item}
			},
			label: "incoming hierarchy",
			assign: func(result *protocol.CallHierarchyResult, raw json.RawMessage) error {
				return decodeInto(raw, &result.Incoming)
			},
		},
		{
			enabled: func(direction string) bool { return wantCallDirection(direction, "outgoing") },
			method:  protocol.MethodCallHierarchyOutgoing,
			params: func(item protocol.CallHierarchyItem) any {
				return protocol.CallHierarchyOutgoingCallsParams{Item: item}
			},
			label: "outgoing hierarchy",
			assign: func(result *protocol.CallHierarchyResult, raw json.RawMessage) error {
				return decodeInto(raw, &result.Outgoing)
			},
		},
	})
}

func wantCallDirection(direction, target string) bool {
	return direction == "" || direction == target || direction == "both"
}

// TypeHierarchy 查询符号的类型层级。
func (m *managerXRef) TypeHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.TypeHierarchyResult, error) {
	return queryHierarchy(ctx, m, uri, protocol.MethodPrepareTypeHierarchy, position, direction,
		func(ctx context.Context, client Client, item protocol.TypeHierarchyItem, direction string) (protocol.TypeHierarchyResult, error) {
			return m.resolveTypeDirections(ctx, client, item, direction)
		},
		unsupportedHierarchy[protocol.TypeHierarchyResult]("type hierarchy"),
	)
}

func (m *manager) resolveTypeDirections(ctx context.Context, client Client, item protocol.TypeHierarchyItem, direction string) (protocol.TypeHierarchyResult, error) {
	result := protocol.TypeHierarchyResult{Item: item}
	return resolveHierarchyDirections(ctx, m, client, item, direction, result, []hierarchyDirectionStep[protocol.TypeHierarchyItem, protocol.TypeHierarchyResult]{
		{
			enabled: func(direction string) bool { return direction == "" || direction == "supertypes" },
			method:  protocol.MethodTypeHierarchySupertypes,
			params:  func(item protocol.TypeHierarchyItem) any { return protocol.TypeHierarchySupertypesParams{Item: item} },
			label:   "supertypes",
			assign: func(result *protocol.TypeHierarchyResult, raw json.RawMessage) error {
				return decodeInto(raw, &result.Supertypes)
			},
		},
		{
			enabled: func(direction string) bool { return direction == "" || direction == "subtypes" },
			method:  protocol.MethodTypeHierarchySubtypes,
			params:  func(item protocol.TypeHierarchyItem) any { return protocol.TypeHierarchySubtypesParams{Item: item} },
			label:   "subtypes",
			assign: func(result *protocol.TypeHierarchyResult, raw json.RawMessage) error {
				return decodeInto(raw, &result.Subtypes)
			},
		},
	})
}

// DocumentSymbol 读取单文档符号列表。
// 大纲查询用于冷启动时快速定位结构，不能被诊断稳定等待拖到工具超时；需要诊断就绪的导航能力在各自路径等待。
func (m *managerStructure) DocumentSymbol(ctx context.Context, uri string) ([]protocol.DocumentSymbol, error) {
	return m.documentSymbolsWithoutDiagnosticsWait(ctx, uri)
}

// DocumentSymbolBestEffort 尽量读取文档符号，允许返回降级结果。
func (m *managerStructure) DocumentSymbolBestEffort(ctx context.Context, uri string) ([]protocol.DocumentSymbol, error) {
	return m.documentSymbolsWithoutDiagnosticsWait(ctx, uri)
}

// documentSymbolsWithoutDiagnosticsWait 在单文档大纲路径跳过诊断等待。
// LSP 符号请求本身会按调用方 context 失败，避免首轮索引未完成时先耗尽工具级 deadline。
func (m *manager) documentSymbolsWithoutDiagnosticsWait(ctx context.Context, uri string) ([]protocol.DocumentSymbol, error) {
	ref, err := m.resolveDocumentRef(ctx, uri, "")
	if err != nil {
		return nil, err
	}
	if symbols, ok, err := m.fallbackDocumentSymbols(ref); ok || err != nil {
		return symbols, err
	}
	client, ref, err := m.documentClientWithoutDiagnosticsWait(ctx, ref.uri)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, nil
	}
	symbols, err := m.requestDocumentSymbols(ctx, client, ref)
	if err != nil {
		return nil, err
	}
	symbols, err = m.retryEmptyDocumentSymbols(ctx, client, ref, symbols)
	if err != nil {
		return nil, err
	}
	if fallback, ok, err := m.fallbackEmptyLSPDocumentSymbols(ctx, ref, symbols); ok || err != nil {
		return fallback, err
	}
	return symbols, nil
}

// requestDocumentSymbols 统一执行 documentSymbol 请求和结果解码。
// 空结果是否重试或兜底由调用方决定，避免请求函数吞掉 LSP 的原始能力错误。
func (m *manager) requestDocumentSymbols(ctx context.Context, client Client, ref documentRef) ([]protocol.DocumentSymbol, error) {
	if !clientSupportsDocumentSymbols(client) {
		return nil, &common.CodedToolError{
			Err:       fmt.Errorf("%w: %s", lspmanager.ErrUnsupportedCapability, protocol.MethodDocumentSymbol),
			Code:      "capability_unsupported",
			Retryable: false,
			Hint:      "next: use a language server that advertises or dynamically registers textDocument/documentSymbol",
			Meta:      capabilityErrorMeta(protocol.MethodDocumentSymbol, client),
		}
	}
	raw, err := m.request(ctx, client, protocol.MethodDocumentSymbol, protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
	})
	if err != nil {
		return nil, unsupportedCapabilityErrorForMethod(err, protocol.MethodDocumentSymbol, client)
	}
	return decodeDocumentSymbols(raw)
}

// clientSupportsDocumentSymbols 仅在客户端已暴露 initialize 能力时拒绝未声明的大纲请求。
// 旧客户端没有能力快照，仍保留原有请求路径以维持兼容。
func clientSupportsDocumentSymbols(client Client) bool {
	return clientSupportsMethod(client, protocol.MethodDocumentSymbol)
}

// clientSupportsCompletion 只在客户端明确报告 completion 能力时允许发送补全请求。
// 没有能力快照的旧客户端按未知处理，继续保留原有兼容路径。
func clientSupportsCompletion(client Client) bool {
	return clientSupportsMethod(client, protocol.MethodCompletion)
}

// clientSupportsMethod 依据 initialize 静态能力与动态注册合并快照裁决公共 LSP 请求。
// 该规则在所有平台完全一致；没有能力快照的旧客户端和未知方法继续走服务器请求，以免改变既有兼容边界。
func clientSupportsMethod(client Client, method string) bool {
	capClient, ok := client.(ServerCapabilitiesClient)
	if !ok {
		return true
	}
	capability, known := serverCapabilityForMethod(capClient.ServerCapabilities(), method)
	return !known || serverCapabilityAvailable(capability)
}

// clientMethodCapabilityGuard 把方法级能力裁决封装成文档请求边界需要的 guard。
func clientMethodCapabilityGuard(method string) func(Client) bool {
	return func(client Client) bool {
		return clientSupportsMethod(client, method)
	}
}

// serverCapabilityForMethod 维护公共 LSP 方法到 initialize 能力字段的唯一映射。
// 平台专用代码只能负责安装和启动，不能在此修改任一语言的协议能力语义。
func serverCapabilityForMethod(capabilities protocol.ServerCapabilities, method string) (any, bool) {
	switch method {
	case protocol.MethodHover:
		return capabilities.HoverProvider, true
	case protocol.MethodDefinition:
		return capabilities.DefinitionProvider, true
	case protocol.MethodImplementation:
		return capabilities.ImplementationProvider, true
	case protocol.MethodTypeDefinition:
		return capabilities.TypeDefinitionProvider, true
	case protocol.MethodSignatureHelp:
		return capabilities.SignatureHelpProvider, true
	case protocol.MethodReferences:
		return capabilities.ReferencesProvider, true
	case protocol.MethodDocumentSymbol:
		return capabilities.DocumentSymbolProvider, true
	case protocol.MethodCompletion:
		return capabilities.CompletionProvider, true
	case protocol.MethodPrepareCallHierarchy:
		return capabilities.CallHierarchyProvider, true
	case protocol.MethodPrepareTypeHierarchy:
		return capabilities.TypeHierarchyProvider, true
	case protocol.MethodFoldingRange:
		return capabilities.FoldingRangeProvider, true
	case protocol.MethodSemanticTokensFull:
		return semanticTokensFullCapabilityAvailable(capabilities.SemanticTokensProvider), true
	case protocol.MethodRename:
		return capabilities.RenameProvider, true
	case protocol.MethodCodeAction:
		return capabilities.CodeActionProvider, true
	case protocol.MethodFormatting:
		return capabilities.DocumentFormattingProvider, true
	default:
		return nil, false
	}
}

// semanticTokensFullCapabilityAvailable 精确检查 semanticTokensProvider.full 子能力。
// 这是跨平台公共协议解析：provider 只声明 range 时不得向任何平台的服务器发送 full 请求。
func semanticTokensFullCapabilityAvailable(provider any) bool {
	switch typed := provider.(type) {
	case nil:
		return false
	case bool:
		return typed
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(typed, &decoded); err != nil {
			return false
		}
		return semanticTokensFullCapabilityAvailable(decoded)
	case map[string]any:
		full, ok := typed["full"]
		return ok && serverCapabilityAvailable(full)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return false
		}
		var decoded map[string]any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			return false
		}
		full, ok := decoded["full"]
		return ok && serverCapabilityAvailable(full)
	}
}

func capabilityErrorMeta(method string, client Client) map[string]any {
	meta := lspClientAttribution(client)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["lsp_method"] = method
	return meta
}

// WorkspaceSymbol 查询指定语言 workspace 内的符号。
// 没有文件路径时 languageID 必填，防止无界启动默认语言服务器。
func (m *managerStructure) WorkspaceSymbol(ctx context.Context, query string, languageID string) ([]protocol.WorkspaceSymbolResult, error) {
	languageID = normalizeLanguageID(languageID)
	if languageID == "" {
		return nil, fmt.Errorf("workspace symbol language is required when no file path is provided")
	}
	selection, err := m.workspaceSymbolClient(ctx, languageID)
	if err != nil {
		return nil, err
	}
	defer selection.releaseTargetReference()
	supported, err := m.workspaceSymbolCapabilityAvailable(ctx, selection)
	if err != nil {
		return nil, err
	}
	if !supported {
		return nil, m.unsupportedWorkspaceSymbolError(selection)
	}
	client, err := m.completeWorkspaceSymbolClient(ctx, selection)
	if err != nil {
		return nil, err
	}
	documentGuard, err := m.syncExplicitDocumentsForWorkspaceSymbol(ctx, selection.cfg, client)
	if err != nil {
		return nil, err
	}
	defer documentGuard.release()
	raw, err := m.requestOnce(ctx, client, protocol.MethodWorkspaceSymbol, protocol.WorkspaceSymbolParams{Query: query})
	if err != nil {
		if isClientDeadError(err) {
			documentGuard.release()
			err = m.nonReplayableDeadClientError(ctx, client, err)
		}
		return nil, unsupportedCapabilityErrorForMethod(err, protocol.MethodWorkspaceSymbol, client)
	}
	if !documentGuard.mutationsStillCurrent() {
		return nil, fmt.Errorf("managed document changed during workspace symbol request")
	}
	return decodeWorkspaceSymbols(raw)
}

func (m *manager) unsupportedWorkspaceSymbolError(selection workspaceSymbolClientSelection) error {
	if err := m.publishLanguageClientWithoutBootstrap(selection.bootstrap); err != nil {
		return fmt.Errorf("publish unsupported workspace symbol client: %w", err)
	}
	if err := m.revalidateLanguageBootstrapClient(selection.cfg, selection.client); err != nil {
		return fmt.Errorf("revalidate unsupported workspace symbol client: %w", err)
	}
	return &common.CodedToolError{
		Err:       fmt.Errorf("%w: %s", lspmanager.ErrUnsupportedCapability, protocol.MethodWorkspaceSymbol),
		Code:      "capability_unsupported",
		Retryable: false,
		Hint:      "next: use a language server that advertises or dynamically registers workspace/symbol",
		Meta:      capabilityErrorMeta(protocol.MethodWorkspaceSymbol, selection.client),
	}
}

type workspaceSymbolClientSelection struct {
	client          Client
	cfg             workspaceConfig
	bootstrap       languageClientBootstrapCandidate
	needsBootstrap  bool
	targetPath      string
	targetReference *documentOperationReference
}

func (s workspaceSymbolClientSelection) releaseTargetReference() {
	s.targetReference.release()
}

func (m *manager) completeWorkspaceSymbolClient(
	ctx context.Context,
	selection workspaceSymbolClientSelection,
) (Client, error) {
	if !selection.needsBootstrap {
		return selection.client, nil
	}
	if selection.targetPath != "" {
		return m.completeWorkspaceSymbolTargetBootstrap(ctx, selection)
	}
	return m.completeLanguageClientBootstrap(ctx, selection.cfg, selection.bootstrap)
}

func (m *manager) completeWorkspaceSymbolTargetBootstrap(
	ctx context.Context,
	selection workspaceSymbolClientSelection,
) (Client, error) {
	candidate := selection.bootstrap
	if candidate.wait {
		if _, err := candidate.attempt.wait(ctx); err != nil {
			return nil, err
		}
		if err := m.revalidateLanguageBootstrapClient(selection.cfg, candidate.client); err != nil {
			return nil, err
		}
	}
	bootstrapCtx, err := candidate.bootstrapContext(ctx)
	if err != nil {
		return nil, err
	}
	bootstrapErr := m.bootstrapLanguageDocument(
		bootstrapCtx, candidate.client, selection.targetPath, selection.cfg.languageID,
	)
	if candidate.owner {
		return m.finishOwnedLanguageClientBootstrap(candidate, bootstrapErr)
	}
	if bootstrapErr != nil {
		return nil, bootstrapErr
	}
	return candidate.client, m.revalidateLanguageBootstrapClient(selection.cfg, candidate.client)
}

// workspaceSymbolClient 为 workspace/symbol 选择或创建 client。
// 已解析 scope 会校验语言和 workspace key；缺 scope 时才回退语言级 workspace。
func (m *manager) workspaceSymbolClient(ctx context.Context, languageID string) (workspaceSymbolClientSelection, error) {
	if resolved, ok := resolvedLSPToolScopeFromContext(ctx); ok {
		cfg, err := m.workspaceSymbolConfigFromResolvedScope(ctx, resolved, languageID)
		if err != nil {
			return workspaceSymbolClientSelection{}, err
		}
		if workspaceSymbolResolvedScopeNeedsBootstrap(resolved) {
			return m.prepareWorkspaceSymbolBootstrapSelection(ctx, cfg)
		}
		targetReference, err := m.retainDocumentOperationReference(fileURIFromPath(resolved.TargetPath))
		if err != nil {
			return workspaceSymbolClientSelection{}, err
		}
		selection, err := m.prepareWorkspaceSymbolBootstrapSelection(ctx, cfg)
		if err != nil {
			targetReference.release()
			return workspaceSymbolClientSelection{}, err
		}
		selection.targetPath = resolved.TargetPath
		selection.targetReference = targetReference
		return selection, nil
	}
	cfg, err := m.resolveLanguageWorkspace(ctx, languageID)
	if err != nil {
		return workspaceSymbolClientSelection{}, err
	}
	return m.prepareWorkspaceSymbolBootstrapSelection(ctx, cfg)
}

func (m *manager) prepareWorkspaceSymbolBootstrapSelection(
	ctx context.Context,
	cfg workspaceConfig,
) (workspaceSymbolClientSelection, error) {
	candidate, err := m.prepareLanguageClientBootstrap(ctx, cfg)
	if err != nil {
		return workspaceSymbolClientSelection{}, err
	}
	return workspaceSymbolClientSelection{
		client: candidate.client, cfg: cfg, bootstrap: candidate, needsBootstrap: true,
	}, nil
}

func (m *manager) workspaceSymbolCapabilityAvailable(
	ctx context.Context,
	selection workspaceSymbolClientSelection,
) (bool, error) {
	capClient, ok := selection.client.(ServerCapabilitiesClient)
	if !ok {
		return true, nil
	}
	if !selection.bootstrap.owner || selection.bootstrap.attempt == nil {
		return serverCapabilityAvailable(capClient.ServerCapabilities().WorkspaceSymbolProvider), nil
	}
	result := make(chan bool, 1)
	safego.Go(context.Background(), nil, "mcp-lsp.workspace-symbol-capability", func(context.Context) {
		result <- serverCapabilityAvailable(capClient.ServerCapabilities().WorkspaceSymbolProvider)
	})
	select {
	case supported := <-result:
		return supported, nil
	case <-ctx.Done():
		return false, m.abortWorkspaceSymbolCapabilityCheck(selection, ctx.Err())
	case <-selection.bootstrap.attempt.ctx.Done():
		return false, m.abortWorkspaceSymbolCapabilityCheck(selection, selection.bootstrap.attempt.ctx.Err())
	}
}

func (m *manager) abortWorkspaceSymbolCapabilityCheck(
	selection workspaceSymbolClientSelection,
	cause error,
) error {
	if err := m.revalidateLanguageBootstrapClient(selection.cfg, selection.client); err != nil {
		cause = errors.Join(cause, err)
	}
	_, err := m.finishOwnedLanguageClientBootstrap(selection.bootstrap, cause)
	return err
}

func workspaceSymbolResolvedScopeNeedsBootstrap(resolved ResolvedLSPToolScope) bool {
	target := resolved.TargetPath
	return target == "" ||
		target == resolved.CWD ||
		target == resolved.WorkspaceRoot ||
		target == resolved.LanguageWorkspaceRoot ||
		target == resolved.ProjectRoot
}

// workspaceSymbolConfigFromResolvedScope 将已解析 scope 转成 workspace/symbol 可用的 client 配置。
// 语言或 workspace key 不一致会报错，避免 query 跑到相邻项目或相邻语言的缓存实例。
func (m *manager) workspaceSymbolConfigFromResolvedScope(
	ctx context.Context,
	resolved ResolvedLSPToolScope,
	languageID string,
) (workspaceConfig, error) {
	resolvedLanguageID := normalizeLanguageID(resolved.LanguageID)
	if resolvedLanguageID == "" {
		return workspaceConfig{}, fmt.Errorf("resolved workspace symbol language is empty")
	}
	if languageID != resolvedLanguageID {
		return workspaceConfig{}, fmt.Errorf("workspace symbol language %q does not match resolved scope language %q", languageID, resolvedLanguageID)
	}
	if !m.shouldUseClientForLanguage(resolvedLanguageID) {
		return workspaceConfig{}, fmt.Errorf("language %q is not managed by the LSP manager", resolvedLanguageID)
	}
	adapter, err := m.adapterForLanguage(resolvedLanguageID)
	if err != nil {
		return workspaceConfig{}, err
	}
	languageScope, err := adapter.ResolveRoot(ctx, resolved.LSPToolScope, resolved.TargetPath)
	if err != nil {
		return workspaceConfig{}, err
	}
	languageScope = completeResolvedLanguageScope(languageScope, resolved.LSPToolScope)
	languageScope.LanguageSpecific = mergeLanguageSpecific(languageScope.LanguageSpecific, adapter.CacheKeyParts(languageScope))
	cfg, err := workspaceConfigForLanguageScope(languageScope, adapter)
	if err != nil {
		return workspaceConfig{}, err
	}
	if cfg.rootPath != resolved.WorkspaceRoot {
		return workspaceConfig{}, fmt.Errorf("resolved workspace symbol root mismatch for %s", resolvedLanguageID)
	}
	if resolved.WorkspaceKey != "" && cfg.key != resolved.WorkspaceKey {
		return workspaceConfig{}, fmt.Errorf("resolved workspace symbol key mismatch for %s", resolvedLanguageID)
	}
	return cfg, nil
}

// FoldingRange 查询文档折叠区间。
// initialize 明确未声明该能力时返回结构化 capability_unsupported；已声明请求可返回真实空切片。
func (m *managerStructure) FoldingRange(ctx context.Context, uri string) ([]protocol.FoldingRange, error) {
	return requestDocument(ctx, m, uri, protocol.MethodFoldingRange,
		func(ref documentRef) any {
			return protocol.FoldingRangeParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
			}
		},
		func(raw json.RawMessage) ([]protocol.FoldingRange, error) {
			var ranges []protocol.FoldingRange
			if err := decodeInto(raw, &ranges); err != nil {
				return nil, fmt.Errorf("decode folding ranges: %w", err)
			}
			return ranges, nil
		},
		fallbackDocument[[]protocol.FoldingRange](nil),
	)
}

// SemanticTokens 查询整篇文档的语义 token。
// initialize 明确未声明该能力时返回结构化 capability_unsupported；已声明请求保留真实 LSP wire 形状。
func (m *managerStructure) SemanticTokens(ctx context.Context, uri string) (*protocol.SemanticTokensResult, error) {
	return requestDocument(ctx, m, uri, protocol.MethodSemanticTokensFull,
		func(ref documentRef) any {
			return protocol.SemanticTokensParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
			}
		},
		func(raw json.RawMessage) (*protocol.SemanticTokensResult, error) {
			var tokens protocol.SemanticTokens
			if err := decodeInto(raw, &tokens); err != nil {
				return nil, fmt.Errorf("decode semantic tokens: %w", err)
			}
			return &protocol.SemanticTokensResult{
				ResultID: tokens.ResultID,
				Data:     tokens.Data,
			}, nil
		},
		fallbackDocument(&protocol.SemanticTokensResult{}),
	)
}

// SemanticTokensLegend 从当前文档实际 client 的 initialize capability 读取 legend。
func (m *managerStructure) SemanticTokensLegend(ctx context.Context, uri string) ([]string, []string, error) {
	client, _, err := m.documentClient(ctx, uri)
	if err != nil {
		return nil, nil, err
	}
	if client == nil {
		return nil, nil, errors.New("semantic tokens legend requires an initialized LSP client")
	}
	capabilityClient, ok := client.(ServerCapabilitiesClient)
	if !ok {
		return nil, nil, errors.New("initialized LSP client does not expose server capabilities")
	}
	return semanticTokensLegendFromProvider(capabilityClient.ServerCapabilities().SemanticTokensProvider)
}

func semanticTokensLegendFromProvider(provider any) ([]string, []string, error) {
	raw, err := json.Marshal(provider)
	if err != nil {
		return nil, nil, fmt.Errorf("encode semantic tokens provider: %w", err)
	}
	var options protocol.SemanticTokensOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, nil, fmt.Errorf("decode semantic tokens provider: %w", err)
	}
	if len(options.Legend.TokenTypes) == 0 {
		return nil, nil, fmt.Errorf("%w: semantic tokens provider is missing legend tokenTypes", lspmanager.ErrSemanticTokensLegendUnavailable)
	}
	for index, tokenType := range options.Legend.TokenTypes {
		if strings.TrimSpace(tokenType) == "" {
			return nil, nil, fmt.Errorf("semantic tokens legend tokenTypes[%d] is empty", index)
		}
	}
	for index, modifier := range options.Legend.TokenModifiers {
		if strings.TrimSpace(modifier) == "" {
			return nil, nil, fmt.Errorf("semantic tokens legend tokenModifiers[%d] is empty", index)
		}
	}
	return append([]string(nil), options.Legend.TokenTypes...), append([]string(nil), options.Legend.TokenModifiers...), nil
}

// Completion 查询指定位置的补全候选。
// 响应兼容数组和 CompletionList 两种 LSP 形态；已知能力缺失时返回结构化 unsupported 错误。
func (m *managerCompletion) Completion(ctx context.Context, uri string, position protocol.Position) (*protocol.CompletionList, error) {
	return requestDocumentWithCapability(ctx, m, uri, protocol.MethodCompletion,
		func(ref documentRef) any {
			return protocol.CompletionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
				Position:     position,
			}
		},
		decodeCompletionList,
		fallbackDocument(&protocol.CompletionList{}),
		clientSupportsCompletion,
	)
}

// CompletionAttribution 从同一文档选择链读取实际语言和 initialize serverInfo。
func (m *managerCompletion) CompletionAttribution(ctx context.Context, uri string) (lspmanager.CompletionAttribution, error) {
	client, ref, err := m.documentClient(ctx, uri)
	if err != nil {
		return lspmanager.CompletionAttribution{}, err
	}
	attribution := lspmanager.CompletionAttribution{LanguageID: strings.TrimSpace(ref.languageID)}
	if client == nil {
		return attribution, nil
	}
	attrs := lspClientAttribution(client)
	attribution.ServerName, _ = attrs["server_name"].(string)
	attribution.ServerVersion, _ = attrs["server_version"].(string)
	return attribution, nil
}

// Rename 请求语言服务器生成跨文件 workspace edit。
// 不支持 rename 时返回能力错误，调用方不能自行拼接替换以免破坏语义边界。
func (m *managerEdit) Rename(ctx context.Context, uri string, position protocol.Position, newName string) (*protocol.WorkspaceEdit, error) {
	return requestDocument(ctx, m, uri, protocol.MethodRename,
		func(ref documentRef) any {
			return protocol.RenameParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
				Position:     position,
				NewName:      newName,
			}
		},
		func(raw json.RawMessage) (*protocol.WorkspaceEdit, error) {
			var edit protocol.WorkspaceEdit
			if err := decodeInto(raw, &edit); err != nil {
				return nil, fmt.Errorf("decode rename: %w", err)
			}
			return &edit, nil
		},
		unsupportedDocument[*protocol.WorkspaceEdit]("rename"),
	)
}

// CodeAction 请求当前范围内的代码动作。
func (m *managerEdit) CodeAction(ctx context.Context, uri string, rng protocol.Range, only []string) ([]protocol.CodeActionResult, error) {
	return requestDocument(ctx, m, uri, protocol.MethodCodeAction,
		func(ref documentRef) any {
			return protocol.CodeActionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
				Range:        rng,
				Context: protocol.CodeActionContext{
					Diagnostics: []protocol.Diagnostic{},
					Only:        only,
				},
			}
		},
		decodeCodeActions,
		fallbackDocument[[]protocol.CodeActionResult](nil),
	)
}

// Format 请求 LSP 格式化指定文档。
func (m *managerEdit) Format(ctx context.Context, uri string, options protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	return requestDocument(ctx, m, uri, protocol.MethodFormatting,
		func(ref documentRef) any {
			return protocol.DocumentFormattingParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
				Options:      options,
			}
		},
		func(raw json.RawMessage) ([]protocol.TextEdit, error) {
			var edits []protocol.TextEdit
			if err := decodeInto(raw, &edits); err != nil {
				return nil, fmt.Errorf("decode formatting edits: %w", err)
			}
			return edits, nil
		},
		fallbackDocument[[]protocol.TextEdit](nil),
	)
}

// Symbols 是格式化/搜索层按绝对路径读取 document symbols 的适配入口。
// 它复用 DocumentSymbol 的 LSP 与静态 fallback 策略，避免工具层重复实现解析逻辑。
func (m *managerStructure) Symbols(absPath string) ([]protocol.DocumentSymbol, error) {
	return m.DocumentSymbol(context.Background(), fileURIFromPath(absPath))
}

// locationQuery 查询位置型 LSP 方法，并在 JS/TS 空引用时用项目语义 barrier 做一次有界重试。
func (m *manager) locationQuery(ctx context.Context, uri, method string, params any) ([]protocol.LocationResult, error) {
	results, err := m.locationQueryOnce(ctx, uri, method, params)
	if err != nil || method != protocol.MethodReferences || len(results) != 0 {
		return results, err
	}
	ref, err := m.resolveDocumentRef(ctx, uri, "")
	if err != nil {
		return nil, fmt.Errorf("resolve reference document: %w", err)
	}
	if !isJSTSDocumentSymbolFallbackLanguage(ref.languageID) {
		return results, nil
	}
	return m.retryEmptyReferences(ctx, uri, method, params)
}

func (m *manager) locationQueryOnce(ctx context.Context, uri, method string, params any) ([]protocol.LocationResult, error) {
	return requestDocument(ctx, m, uri, method,
		func(ref documentRef) any {
			return normalizeLocationParams(params, ref.uri)
		},
		func(raw json.RawMessage) ([]protocol.LocationResult, error) {
			results, err := decodeLocationResults(raw)
			if err != nil {
				return nil, err
			}
			format.EnrichLocationResultsWithFuncRange(results, m)
			return results, nil
		},
		fallbackDocument[[]protocol.LocationResult](nil),
	)
}

func normalizeLocationParams(params any, documentURI string) any {
	switch typed := params.(type) {
	case protocol.TextDocumentPositionParams:
		typed.TextDocument.URI = documentURI
		return typed
	case protocol.ReferenceParams:
		typed.TextDocument.URI = documentURI
		return typed
	default:
		return params
	}
}

func prepareHierarchy[T any](ctx context.Context, m *manager, client Client, method, uri string, position protocol.Position) ([]T, error) {
	if !clientSupportsMethod(client, method) {
		return nil, unsupportedCapabilityErrorForAdvertisedMethod(method, client)
	}
	raw, err := m.request(ctx, client, method, protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Position:     position,
	})
	if err != nil {
		return nil, err
	}
	var items []T
	if err := decodeInto(raw, &items); err != nil {
		return nil, fmt.Errorf("decode %s: %w", method, err)
	}
	return items, nil
}
