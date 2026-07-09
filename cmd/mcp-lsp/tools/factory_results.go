package tools

import (
	"context"
	"errors"
	"strings"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

// requireFilePath 验证路径非空，返回 trim 后的路径。
func requireFilePath(raw string) (string, error) {
	filePath := strings.TrimSpace(raw)
	if filePath == "" {
		return "", errors.New("file_path is required; pass an absolute or workspace-relative path")
	}
	return filePath, nil
}

// normalizeAction 把 action 字符串规范化为小写无空白。
func normalizeAction(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// renderListResult 截取列表至 limit，空时返回标准空列表信封。
func renderListResult[T any](items []T, limit int, emptyMessage string, render func([]T, int) any) (any, error) {
	total := len(items)
	items = limitSlice(items, limit)
	if len(items) == 0 {
		return emptyListEnvelope{
			Success: true,
			Data:    []any{},
			Meta:    resultMeta{Count: 0, Message: emptyMessage},
		}, nil
	}
	return render(items, total), nil
}

// funcRangeEnricher 按需读取并缓存 DocumentSymbols。
// inspect/xref 等位置型工具用它给 LocationResult 附加 FuncStart/FuncEnd，同一请求内同文件只查一次。
type funcRangeEnricher struct {
	ctx      context.Context
	registry lspmanager.Registry
	cache    map[string][]protocol.DocumentSymbol
}

// newFuncRangeEnricher 创建 funcRangeEnricher，registry 为 nil 时返回 nil。
func newFuncRangeEnricher(ctx context.Context, registry lspmanager.Registry) *funcRangeEnricher {
	if registry == nil {
		return nil
	}
	return &funcRangeEnricher{
		ctx:      ctx,
		registry: registry,
		cache:    make(map[string][]protocol.DocumentSymbol),
	}
}

// Symbols 返回文件的 DocumentSymbols，并在请求级缓存中复用结果。
// registry 或语言服务器错误会直接返回给调用方，避免生成没有函数范围的伪成功结果。
func (p *funcRangeEnricher) Symbols(absPath string) ([]protocol.DocumentSymbol, error) {
	if p == nil {
		return nil, errors.New("funcRangeEnricher is nil")
	}
	if cached, ok := p.cache[absPath]; ok {
		return cached, nil
	}
	mgr, err := p.registry.GetManagerForFile(p.ctx, absPath)
	if err != nil {
		return nil, err
	}
	symbols, err := mgr.DocumentSymbol(p.ctx, fileURI(absPath))
	if err != nil {
		return nil, err
	}
	p.cache[absPath] = symbols
	return symbols, nil
}

// ToPlainText 渲染为纯文本。
func (e emptyListEnvelope) ToPlainText() string {
	if e.Meta.Message != "" {
		return e.Meta.Message
	}
	return "No items found."
}
