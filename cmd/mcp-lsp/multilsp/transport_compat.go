package multilsp

import (
	"encoding/json"
	"fmt"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// 本文件集中声明多语言 LSP transport 允许自动 ACK 的服务端主动请求。
// 未列入集合的方法必须返回 ErrMethodNotSupported，由上层映射为 JSON-RPC MethodNotFound，
// 避免未知服务端请求被静默确认。

// 服务端主动请求中可用空 struct 结果 ACK 的方法集合。
const (
	LSPCompatMethodClientRegisterCapability     = "client/registerCapability"
	LSPCompatMethodClientUnregisterCapability   = "client/unregisterCapability"
	LSPCompatMethodWindowWorkDoneProgressCreate = "window/workDoneProgress/create"
)

// workspace/*/refresh 系列请求同样只需要空 struct 结果。
const (
	LSPCompatMethodWorkspaceSemanticTokensRefresh = "workspace/semanticTokens/refresh"
	LSPCompatMethodWorkspaceCodeLensRefresh       = "workspace/codeLens/refresh"
	LSPCompatMethodWorkspaceInlayHintRefresh      = "workspace/inlayHint/refresh"
	LSPCompatMethodWorkspaceDiagnosticRefresh     = "workspace/diagnostic/refresh"
)

// workspace/configuration 需要返回与请求 items 等长的空配置数组。
const LSPCompatMethodWorkspaceConfiguration = "workspace/configuration"

// lspCompatEmptyStructMethods 是 transport 会以 struct{}{} ACK 的完整方法表。
// 新增兼容方法必须先落到这里，避免分散在 transport 分支里形成隐式放行。
var lspCompatEmptyStructMethods = []string{
	LSPCompatMethodClientRegisterCapability,
	LSPCompatMethodClientUnregisterCapability,
	LSPCompatMethodWindowWorkDoneProgressCreate,
	LSPCompatMethodWorkspaceSemanticTokensRefresh,
	LSPCompatMethodWorkspaceCodeLensRefresh,
	LSPCompatMethodWorkspaceInlayHintRefresh,
	LSPCompatMethodWorkspaceDiagnosticRefresh,
}

func isLSPCompatEmptyStructMethod(method string) bool {
	for _, candidate := range lspCompatEmptyStructMethods {
		if candidate == method {
			return true
		}
	}
	return false
}

// dispatchCompatServerRequest 根据兼容方法表处理服务端主动请求。
// 命中兼容表时记录稳定事件供诊断统计；未命中的方法返回 ErrMethodNotSupported，不做静默 ACK。
func dispatchCompatServerRequest(method string, params json.RawMessage) (any, error) {
	if isLSPCompatEmptyStructMethod(method) {
		pkglogger.Get().Info("LSP compat fallback hit",
			"event", "gopls.compat_fallback.hit",
			"method", method,
			"variant", "empty_struct",
		)
		return struct{}{}, nil
	}
	if method == LSPCompatMethodWorkspaceConfiguration {
		pkglogger.Get().Info("LSP compat fallback hit",
			"event", "gopls.compat_fallback.hit",
			"method", method,
			"variant", "workspace_configuration",
		)
		return emptyConfigurationResult(params), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrMethodNotSupported, method)
}
