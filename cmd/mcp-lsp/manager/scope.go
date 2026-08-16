package manager

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// ToolScope 是注册表层使用的 LSP 路由作用域。
// 身份字段必须来自服务端可信上下文，模型传入的参数只能作为目标文件或语言线索。
type ToolScope struct {
	AgentID  string
	ThreadID string
	TurnID   string
	CallID   string
	CWD      string
	// WorkspaceRoots 是本次工具调用的可信根集合；相对路径仍优先按 CWD 解析。
	WorkspaceRoots []string
	Family         string

	LanguageID string
	TargetPath string
	TargetURI  string

	WorkspaceRoot         string
	RootKind              string
	LanguageWorkspaceRoot string
	ProjectRoot           string
	LanguageSpecific      map[string]string
}

// ResolvedToolScope 是 ManagerPool 返回的规范化路由结果。
// 注册表和工具层透传它做诊断、缓存和启动审计，不能在下游重新拼 key。
type ResolvedToolScope struct {
	ToolScope

	ScopeKey     string
	WorkspaceKey string
	ShardKey     string
	ManagerKey   string
}

// ScopedManager 绑定一次工具调用选中的 manager 和规范化 scope。
type ScopedManager struct {
	Manager       Manager
	ResolvedScope ResolvedToolScope
}

// ScopedManagerResolver 由生产 ManagerPool adapter 实现。
// manager 包只依赖该接口，避免直接 import multilsp 形成循环依赖。
type ScopedManagerResolver interface {
	ForToolScope(scope ToolScope) (ScopedManager, error)
	CurrentManagersForToolScope(scope ToolScope) ([]ScopedManager, error)
}

type resolvedToolScopeContextKey struct{}

// WithResolvedToolScope 把已解析 scope 写入 context。
// 具体 manager 可在内部转换成自身作用域类型，注册表不需要依赖实现包。
func WithResolvedToolScope(ctx context.Context, scope ResolvedToolScope) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if scope.WorkspaceKey == "" && scope.ManagerKey == "" {
		return ctx
	}
	return context.WithValue(ctx, resolvedToolScopeContextKey{}, scope)
}

// ResolvedToolScopeFromContext 从 context 读取已解析 scope。
// 缺少关键 key 时返回 false，避免下游把空 scope 当作有效路由结果。
func ResolvedToolScopeFromContext(ctx context.Context) (ResolvedToolScope, bool) {
	if ctx == nil {
		return ResolvedToolScope{}, false
	}
	scope, ok := ctx.Value(resolvedToolScopeContextKey{}).(ResolvedToolScope)
	if !ok || (scope.WorkspaceKey == "" && scope.ManagerKey == "") {
		return ResolvedToolScope{}, false
	}
	return scope, true
}

// ManagerWithResolvedScope 为 manager 包装固定的 resolved scope。
// 空 manager 或空 scope 会原样返回，避免引入无意义代理层。
func ManagerWithResolvedScope(manager Manager, scope ResolvedToolScope) Manager {
	if manager == nil || (scope.WorkspaceKey == "" && scope.ManagerKey == "") {
		return manager
	}
	return &resolvedScopeManager{manager: manager, scope: scope}
}

type resolvedScopeManager struct {
	manager Manager
	scope   ResolvedToolScope
}

type resolvedScopeLifecycle = resolvedScopeManager
type resolvedScopeNavigation = resolvedScopeManager
type resolvedScopeXRef = resolvedScopeManager
type resolvedScopeStructure = resolvedScopeManager
type resolvedScopeCompletion = resolvedScopeManager
type resolvedScopeEdit = resolvedScopeManager
type resolvedScopeDocumentLifecycle = resolvedScopeManager
type resolvedScopeDiagnostics = resolvedScopeManager

func (m *resolvedScopeManager) scoped(ctx context.Context) context.Context {
	return WithResolvedToolScope(ctx, m.scope)
}

// Close 直接关闭底层 manager；scope 只影响请求路径，不影响资源归属。
func (m *resolvedScopeLifecycle) Close() error {
	return m.manager.Close()
}

// Definition 注入 resolved scope 后转发定义查询。
func (m *resolvedScopeNavigation) Definition(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error) {
	return m.manager.Definition(m.scoped(ctx), uri, position)
}

// Implementation 注入 resolved scope 后转发实现查询。
func (m *resolvedScopeNavigation) Implementation(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error) {
	return m.manager.Implementation(m.scoped(ctx), uri, position)
}

// TypeDefinition 查找符号的类型定义。
func (m *resolvedScopeNavigation) TypeDefinition(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error) {
	return m.manager.TypeDefinition(m.scoped(ctx), uri, position)
}

// Hover 注入 resolved scope 后转发 hover 查询。
func (m *resolvedScopeNavigation) Hover(ctx context.Context, uri string, position protocol.Position) (*protocol.HoverResult, error) {
	return m.manager.Hover(m.scoped(ctx), uri, position)
}

// SignatureHelp 注入 resolved scope 后转发签名帮助查询。
func (m *resolvedScopeNavigation) SignatureHelp(ctx context.Context, uri string, position protocol.Position) (*protocol.SignatureHelpResult, error) {
	return m.manager.SignatureHelp(m.scoped(ctx), uri, position)
}

// References 注入 resolved scope 后转发引用查询。
func (m *resolvedScopeXRef) References(ctx context.Context, uri string, position protocol.Position, includeDeclaration bool) ([]protocol.LocationResult, error) {
	return m.manager.References(m.scoped(ctx), uri, position, includeDeclaration)
}

// CallHierarchy 注入 resolved scope 后转发调用层级查询。
func (m *resolvedScopeXRef) CallHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.CallHierarchyResult, error) {
	return m.manager.CallHierarchy(m.scoped(ctx), uri, position, direction)
}

// TypeHierarchy 查询符号的类型层级。
func (m *resolvedScopeXRef) TypeHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.TypeHierarchyResult, error) {
	return m.manager.TypeHierarchy(m.scoped(ctx), uri, position, direction)
}

// DocumentSymbol 读取文档符号列表。
func (m *resolvedScopeStructure) DocumentSymbol(ctx context.Context, uri string) ([]protocol.DocumentSymbol, error) {
	return m.manager.DocumentSymbol(m.scoped(ctx), uri)
}

// DocumentSymbolBestEffort 尽量读取文档符号，允许返回降级结果。
func (m *resolvedScopeStructure) DocumentSymbolBestEffort(ctx context.Context, uri string) ([]protocol.DocumentSymbol, error) {
	if bestEffort, ok := m.manager.(BestEffortDocumentSymbolManager); ok {
		return bestEffort.DocumentSymbolBestEffort(m.scoped(ctx), uri)
	}
	return m.manager.DocumentSymbol(m.scoped(ctx), uri)
}

// WorkspaceSymbol 注入 resolved scope 后转发工作区符号查询。
func (m *resolvedScopeStructure) WorkspaceSymbol(ctx context.Context, query string, languageID string) ([]protocol.WorkspaceSymbolResult, error) {
	return m.manager.WorkspaceSymbol(m.scoped(ctx), query, languageID)
}

// FoldingRange 注入 resolved scope 后转发折叠范围查询。
func (m *resolvedScopeStructure) FoldingRange(ctx context.Context, uri string) ([]protocol.FoldingRange, error) {
	return m.manager.FoldingRange(m.scoped(ctx), uri)
}

// SemanticTokens 注入 resolved scope 后转发语义 token 查询。
func (m *resolvedScopeStructure) SemanticTokens(ctx context.Context, uri string) (*protocol.SemanticTokensResult, error) {
	return m.manager.SemanticTokens(m.scoped(ctx), uri)
}

// SemanticTokensLegend 注入 resolved scope 后读取同一 server 的 initialize legend。
func (m *resolvedScopeStructure) SemanticTokensLegend(ctx context.Context, uri string) ([]string, []string, error) {
	legendManager, ok := m.manager.(SemanticTokensLegendManager)
	if !ok {
		return nil, nil, errors.New("semantic tokens legend is unavailable from manager")
	}
	return legendManager.SemanticTokensLegend(m.scoped(ctx), uri)
}

// Completion 注入 resolved scope 后转发补全查询。
func (m *resolvedScopeCompletion) Completion(ctx context.Context, uri string, position protocol.Position) (*protocol.CompletionList, error) {
	return m.manager.Completion(m.scoped(ctx), uri, position)
}

// CompletionAttribution 注入同一 resolved scope 后读取实际语言服务器归因。
func (m *resolvedScopeCompletion) CompletionAttribution(ctx context.Context, uri string) (CompletionAttribution, error) {
	provider, ok := m.manager.(CompletionAttributionManager)
	if !ok {
		return CompletionAttribution{}, nil
	}
	return provider.CompletionAttribution(m.scoped(ctx), uri)
}

// Rename 注入 resolved scope 后转发重命名请求。
func (m *resolvedScopeEdit) Rename(ctx context.Context, uri string, position protocol.Position, newName string) (*protocol.WorkspaceEdit, error) {
	return m.manager.Rename(m.scoped(ctx), uri, position, newName)
}

// CodeAction 请求当前范围内的代码动作。
func (m *resolvedScopeEdit) CodeAction(ctx context.Context, uri string, rng protocol.Range, only []string) ([]protocol.CodeActionResult, error) {
	return m.manager.CodeAction(m.scoped(ctx), uri, rng, only)
}

// Format 请求 LSP 格式化指定文档。
func (m *resolvedScopeEdit) Format(ctx context.Context, uri string, options protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	return m.manager.Format(m.scoped(ctx), uri, options)
}

// DidOpen 把文档打开事件转给 LSP。
func (m *resolvedScopeDocumentLifecycle) DidOpen(ctx context.Context, uri, languageID string, version int, text string) error {
	return m.manager.DidOpen(m.scoped(ctx), uri, languageID, version, text)
}

// DidChange 把文档变更事件转给 LSP。
func (m *resolvedScopeDocumentLifecycle) DidChange(ctx context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error {
	return m.manager.DidChange(m.scoped(ctx), uri, version, changes)
}

// DidClose 把文档关闭事件转给 LSP。
func (m *resolvedScopeDocumentLifecycle) DidClose(ctx context.Context, uri string) error {
	return m.manager.DidClose(m.scoped(ctx), uri)
}

// BootstrapDocument 确保文档已打开并完成启动检查。
func (m *resolvedScopeDocumentLifecycle) BootstrapDocument(ctx context.Context, uri string) error {
	return m.manager.BootstrapDocument(m.scoped(ctx), uri)
}

// BootstrapDocumentOpenOnly 注入 resolved scope 后只执行文档打开同步。
func (m *resolvedScopeDocumentLifecycle) BootstrapDocumentOpenOnly(ctx context.Context, uri string) error {
	return m.manager.BootstrapDocumentOpenOnly(m.scoped(ctx), uri)
}

// ReopenDocumentForDiagnostics 注入 resolved scope 后强制重开诊断文档。
func (m *resolvedScopeDocumentLifecycle) ReopenDocumentForDiagnostics(ctx context.Context, uri string) error {
	reopener, ok := m.manager.(DiagnosticDocumentReopener)
	if !ok {
		return ErrUnsupportedCapability
	}
	return reopener.ReopenDocumentForDiagnostics(m.scoped(ctx), uri)
}

// Diagnostics 汇总匹配 manager 返回的诊断。
func (m *resolvedScopeDiagnostics) Diagnostics(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error) {
	return m.manager.Diagnostics(m.scoped(ctx), uris)
}

// WaitDiagnosticsStable 等待诊断稳定状态。
func (m *resolvedScopeDiagnostics) WaitDiagnosticsStable(ctx context.Context, uris []string) error {
	return m.manager.WaitDiagnosticsStable(m.scoped(ctx), uris)
}

// CurrentDiagnosticGeneration 读取底层 manager 的当前诊断代际。
func (m *resolvedScopeDiagnostics) CurrentDiagnosticGeneration() uint64 {
	return m.manager.CurrentDiagnosticGeneration()
}

// AdvanceDiagnosticGeneration 推进诊断代际，防止旧结果覆盖新结果。
func (m *resolvedScopeDiagnostics) AdvanceDiagnosticGeneration() uint64 {
	return m.manager.AdvanceDiagnosticGeneration()
}
