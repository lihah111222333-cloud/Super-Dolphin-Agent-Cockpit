package multilsp

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

var emptyDocumentSymbolRetryDelay = 80 * time.Millisecond

// shouldRetryEmptyDocumentSymbols 只给 JS/TS 空大纲做一次二次请求。
// 这些语言的服务器冷启动时偶发先返回空数组，其他语言保持原来的请求语义。
func shouldRetryEmptyDocumentSymbols(languageID string, symbols []protocol.DocumentSymbol) bool {
	return len(symbols) == 0 && isJSTSDocumentSymbolFallbackLanguage(languageID)
}

// retryEmptyDocumentSymbols 在 JS/TS 空大纲时执行唯一一次二次 LSP 请求。
// 二次请求返回非空时保持 LSP 优先，仍为空时才交给 TypeScript navigation fallback。
func (m *manager) retryEmptyDocumentSymbols(ctx context.Context, client Client, ref documentRef, symbols []protocol.DocumentSymbol) ([]protocol.DocumentSymbol, error) {
	if !shouldRetryEmptyDocumentSymbols(ref.languageID, symbols) {
		return symbols, nil
	}
	if err := waitBeforeEmptyDocumentSymbolRetry(ctx); err != nil {
		return nil, err
	}
	return m.requestDocumentSymbols(ctx, client, ref)
}

// waitBeforeEmptyDocumentSymbolRetry 给语言服务器一个短窗口完成索引。
// 调用方 context 被取消时立即退出，避免 document_symbol 卡住工具层超时。
func waitBeforeEmptyDocumentSymbolRetry(ctx context.Context) error {
	if emptyDocumentSymbolRetryDelay <= 0 {
		return nil
	}
	timer := time.NewTimer(emptyDocumentSymbolRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
