package manager

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

// ToolScope is the registry-facing LSP scope assembled from trusted
// server-side tool metadata plus the tool target. Model-supplied arguments are
// intentionally not identity inputs; callers should populate AgentID/ThreadID
// from common.ToolScopeFromContext.
type ToolScope struct {
	AgentID  string
	ThreadID string
	TurnID   string
	CallID   string
	CWD      string
	// WorkspaceRoots is the trusted root set for this tool call. CWD remains
	// the primary root for relative paths; absolute targets may resolve against
	// any root in this set.
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

// ResolvedToolScope is the canonical scoped routing result returned by a
// production ManagerPool adapter. Registry/tool callers pass this value through
// for diagnostics/cache/bootstrap auditing instead of rebuilding keys.
type ResolvedToolScope struct {
	ToolScope

	ScopeKey     string
	WorkspaceKey string
	ShardKey     string
	ManagerKey   string
}

// ScopedManager couples the manager selected for a tool call with the canonical
// resolved scope produced by the pool.
type ScopedManager struct {
	Manager       Manager
	ResolvedScope ResolvedToolScope
}

// ScopedManagerResolver is implemented by the production multilsp ManagerPool
// adapter. It lets the registry route through ManagerPool.ForScope without
// importing multilsp and creating an import cycle.
type ScopedManagerResolver interface {
	ForToolScope(scope ToolScope) (ScopedManager, error)
	CurrentManagersForToolScope(scope ToolScope) ([]ScopedManager, error)
}

type resolvedToolScopeContextKey struct{}

// WithResolvedToolScope attaches the canonical scope returned by a scoped
// resolver to ctx. Concrete manager implementations can convert this
// registry-level value to their internal scope type without forcing the
// registry package to import those implementations.
// WithResolvedToolScope 设置已解析工具作用域。
func WithResolvedToolScope(ctx context.Context, scope ResolvedToolScope) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if scope.WorkspaceKey == "" && scope.ManagerKey == "" {
		return ctx
	}
	return context.WithValue(ctx, resolvedToolScopeContextKey{}, scope)
}

// ResolvedToolScopeFromContext 从上下文处理已解析工具作用域。
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

// ManagerWithResolvedScope 处理带已解析作用域的manager。
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

func (m *resolvedScopeManager) scoped(ctx context.Context) context.Context {
	return WithResolvedToolScope(ctx, m.scope)
}

// Close 关闭 LSP 管理器资源。
func (m *resolvedScopeManager) Close() error {
	return m.manager.Close()
}

// Definition 处理定义。
func (m *resolvedScopeManager) Definition(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error) {
	return m.manager.Definition(m.scoped(ctx), uri, position)
}

// Implementation 处理实现。
func (m *resolvedScopeManager) Implementation(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error) {
	return m.manager.Implementation(m.scoped(ctx), uri, position)
}

// TypeDefinition 查找符号的类型定义。
func (m *resolvedScopeManager) TypeDefinition(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error) {
	return m.manager.TypeDefinition(m.scoped(ctx), uri, position)
}

// Hover 处理悬停。
func (m *resolvedScopeManager) Hover(ctx context.Context, uri string, position protocol.Position) (*protocol.HoverResult, error) {
	return m.manager.Hover(m.scoped(ctx), uri, position)
}

// SignatureHelp 处理签名帮助。
func (m *resolvedScopeManager) SignatureHelp(ctx context.Context, uri string, position protocol.Position) (*protocol.SignatureHelpResult, error) {
	return m.manager.SignatureHelp(m.scoped(ctx), uri, position)
}

// References 处理引用。
func (m *resolvedScopeManager) References(ctx context.Context, uri string, position protocol.Position, includeDeclaration bool) ([]protocol.LocationResult, error) {
	return m.manager.References(m.scoped(ctx), uri, position, includeDeclaration)
}

// CallHierarchy 调用层级。
func (m *resolvedScopeManager) CallHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.CallHierarchyResult, error) {
	return m.manager.CallHierarchy(m.scoped(ctx), uri, position, direction)
}

// TypeHierarchy 查询符号的类型层级。
func (m *resolvedScopeManager) TypeHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.TypeHierarchyResult, error) {
	return m.manager.TypeHierarchy(m.scoped(ctx), uri, position, direction)
}

// DocumentSymbol 读取文档符号列表。
func (m *resolvedScopeManager) DocumentSymbol(ctx context.Context, uri string) ([]protocol.DocumentSymbol, error) {
	return m.manager.DocumentSymbol(m.scoped(ctx), uri)
}

// DocumentSymbolBestEffort 尽量读取文档符号，允许返回降级结果。
func (m *resolvedScopeManager) DocumentSymbolBestEffort(ctx context.Context, uri string) ([]protocol.DocumentSymbol, error) {
	if bestEffort, ok := m.manager.(BestEffortDocumentSymbolManager); ok {
		return bestEffort.DocumentSymbolBestEffort(m.scoped(ctx), uri)
	}
	return m.manager.DocumentSymbol(m.scoped(ctx), uri)
}

// WorkspaceSymbol 处理工作区符号。
func (m *resolvedScopeManager) WorkspaceSymbol(ctx context.Context, query string, languageID string) ([]protocol.WorkspaceSymbolResult, error) {
	return m.manager.WorkspaceSymbol(m.scoped(ctx), query, languageID)
}

// FoldingRange 处理折叠范围。
func (m *resolvedScopeManager) FoldingRange(ctx context.Context, uri string) ([]protocol.FoldingRange, error) {
	return m.manager.FoldingRange(m.scoped(ctx), uri)
}

// SemanticTokens 处理语义令牌。
func (m *resolvedScopeManager) SemanticTokens(ctx context.Context, uri string) (*protocol.SemanticTokensResult, error) {
	return m.manager.SemanticTokens(m.scoped(ctx), uri)
}

// Completion 处理补全。
func (m *resolvedScopeManager) Completion(ctx context.Context, uri string, position protocol.Position) (*protocol.CompletionList, error) {
	return m.manager.Completion(m.scoped(ctx), uri, position)
}

// Rename 处理重命名。
func (m *resolvedScopeManager) Rename(ctx context.Context, uri string, position protocol.Position, newName string) (*protocol.WorkspaceEdit, error) {
	return m.manager.Rename(m.scoped(ctx), uri, position, newName)
}

// CodeAction 请求当前范围内的代码动作。
func (m *resolvedScopeManager) CodeAction(ctx context.Context, uri string, rng protocol.Range, only []string) ([]protocol.CodeActionResult, error) {
	return m.manager.CodeAction(m.scoped(ctx), uri, rng, only)
}

// Format 请求 LSP 格式化指定文档。
func (m *resolvedScopeManager) Format(ctx context.Context, uri string, options protocol.FormattingOptions) ([]protocol.TextEdit, error) {
	return m.manager.Format(m.scoped(ctx), uri, options)
}

// DidOpen 把文档打开事件转给 LSP。
func (m *resolvedScopeManager) DidOpen(ctx context.Context, uri, languageID string, version int, text string) error {
	return m.manager.DidOpen(m.scoped(ctx), uri, languageID, version, text)
}

// DidChange 把文档变更事件转给 LSP。
func (m *resolvedScopeManager) DidChange(ctx context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error {
	return m.manager.DidChange(m.scoped(ctx), uri, version, changes)
}

// DidClose 把文档关闭事件转给 LSP。
func (m *resolvedScopeManager) DidClose(ctx context.Context, uri string) error {
	return m.manager.DidClose(m.scoped(ctx), uri)
}

// BootstrapDocument 确保文档已打开并完成启动检查。
func (m *resolvedScopeManager) BootstrapDocument(ctx context.Context, uri string) error {
	return m.manager.BootstrapDocument(m.scoped(ctx), uri)
}

// BootstrapDocumentOpenOnly 处理启动document打开only。
func (m *resolvedScopeManager) BootstrapDocumentOpenOnly(ctx context.Context, uri string) error {
	return m.manager.BootstrapDocumentOpenOnly(m.scoped(ctx), uri)
}

// Diagnostics 汇总匹配 manager 返回的诊断。
func (m *resolvedScopeManager) Diagnostics(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error) {
	return m.manager.Diagnostics(m.scoped(ctx), uris)
}

// WaitDiagnosticsStable 等待诊断稳定状态。
func (m *resolvedScopeManager) WaitDiagnosticsStable(ctx context.Context, uris []string) error {
	return m.manager.WaitDiagnosticsStable(m.scoped(ctx), uris)
}

// CurrentDiagnosticGeneration 处理当前诊断代际。
func (m *resolvedScopeManager) CurrentDiagnosticGeneration() uint64 {
	return m.manager.CurrentDiagnosticGeneration()
}

// AdvanceDiagnosticGeneration 推进诊断代际，防止旧结果覆盖新结果。
func (m *resolvedScopeManager) AdvanceDiagnosticGeneration() uint64 {
	return m.manager.AdvanceDiagnosticGeneration()
}
