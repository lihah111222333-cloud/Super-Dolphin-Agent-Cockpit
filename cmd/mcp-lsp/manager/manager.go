package manager

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

// ErrDiagnosticsNotReady 表示目标文件的诊断尚未稳定。
// 工具层收到该错误时应提示调用方稍后重试，而不是返回空诊断假装成功。
var ErrDiagnosticsNotReady = errors.New("diagnostics not ready")

// Manager 聚合 mcp-lsp 工具需要的全部 LSP 能力。
// 具体实现可拆分组合这些小接口，注册表只依赖该跨模块入口。
type Manager interface {
	LifecycleManager
	NavigationManager
	XRefManager
	StructureManager
	CompletionManager
	EditManager
	DocumentLifecycleManager
	DiagnosticsManager
}

// LifecycleManager 负责关闭 LSP 客户端、缓存和后台资源。
type LifecycleManager interface {
	Close() error
}

// NavigationManager 封装 definition、hover 等点位导航能力。
type NavigationManager interface {
	Definition(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error)
	Implementation(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error)
	TypeDefinition(ctx context.Context, uri string, position protocol.Position) ([]protocol.LocationResult, error)
	Hover(ctx context.Context, uri string, position protocol.Position) (*protocol.HoverResult, error)
	SignatureHelp(ctx context.Context, uri string, position protocol.Position) (*protocol.SignatureHelpResult, error)
}

// XRefManager 封装引用、调用层级和类型层级等跨符号查询能力。
type XRefManager interface {
	References(ctx context.Context, uri string, position protocol.Position, includeDeclaration bool) ([]protocol.LocationResult, error)
	CallHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.CallHierarchyResult, error)
	TypeHierarchy(ctx context.Context, uri string, position protocol.Position, direction string) ([]protocol.TypeHierarchyResult, error)
}

// StructureManager 封装文档结构、工作区符号、折叠范围和语义 token 查询。
type StructureManager interface {
	DocumentSymbol(ctx context.Context, uri string) ([]protocol.DocumentSymbol, error)
	WorkspaceSymbol(ctx context.Context, query string, languageID string) ([]protocol.WorkspaceSymbolResult, error)
	FoldingRange(ctx context.Context, uri string) ([]protocol.FoldingRange, error)
	SemanticTokens(ctx context.Context, uri string) (*protocol.SemanticTokensResult, error)
}

// BestEffortDocumentSymbolManager 允许实现方在语义 LSP 不可用时返回降级文档符号。
// 调用方需要显式识别该接口，避免普通 DocumentSymbol 悄悄降级。
type BestEffortDocumentSymbolManager interface {
	DocumentSymbolBestEffort(ctx context.Context, uri string) ([]protocol.DocumentSymbol, error)
}

// CompletionManager 封装补全查询能力。
type CompletionManager interface {
	Completion(ctx context.Context, uri string, position protocol.Position) (*protocol.CompletionList, error)
}

// EditManager 封装 rename、code_action 和 format 等会产生编辑结果的能力。
type EditManager interface {
	Rename(ctx context.Context, uri string, position protocol.Position, newName string) (*protocol.WorkspaceEdit, error)
	CodeAction(ctx context.Context, uri string, rng protocol.Range, only []string) ([]protocol.CodeActionResult, error)
	Format(ctx context.Context, uri string, options protocol.FormattingOptions) ([]protocol.TextEdit, error)
}

// DocumentLifecycleManager 负责把文档打开、变更、关闭和启动同步到 LSP server。
// BootstrapDocumentOpenOnly 用于只打开文档但不等待完整诊断稳定的场景。
type DocumentLifecycleManager interface {
	DidOpen(ctx context.Context, uri, languageID string, version int, text string) error
	DidChange(ctx context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error
	DidClose(ctx context.Context, uri string) error
	BootstrapDocument(ctx context.Context, uri string) error
	BootstrapDocumentOpenOnly(ctx context.Context, uri string) error
}

// DiagnosticDocumentReopener 为显式诊断提供强制文档重开能力。
// 实现必须让调用方后续只观察到重开后的诊断，不能继续暴露旧快照。
type DiagnosticDocumentReopener interface {
	ReopenDocumentForDiagnostics(ctx context.Context, uri string) error
}

// DiagnosticsManager 聚合诊断读取和代际推进能力。
// generation 用来隔离旧诊断，防止异步发布覆盖新一轮结果。
type DiagnosticsManager interface {
	Diagnostics(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error)
	WaitDiagnosticsStable(ctx context.Context, uris []string) error
	CurrentDiagnosticGeneration() uint64
	AdvanceDiagnosticGeneration() uint64
}
