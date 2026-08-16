package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

type completionParams struct {
	Pos        string `json:"pos"`
	LanguageID string `json:"language_id,omitempty"`
	MaxResults int    `json:"max_results"`
}

type completionTextProvider interface{ ToPlainText() string }

type mqlCompletionResult struct{ result completionTextProvider }

// ToPlainText 在补全文本后追加非原生 MQL 兼容性归因。
func (result mqlCompletionResult) ToPlainText() string {
	return strings.Join([]string{
		result.result.ToPlainText(),
		lineprotocol.FieldsRecord("ATTR",
			lineprotocol.Field{Key: "language_id", Value: "cpp"},
			lineprotocol.Field{Key: "server", Value: "clangd"},
			lineprotocol.Field{Key: "native", Value: "0"},
			lineprotocol.Field{Key: "compatibility", Value: "mql-via-clangd"},
		),
		lineprotocol.TextRecord("WARNING", "candidates may include C/C++ or macOS macros; not-native-MetaEditor-MQL5-semantics"),
	}, "\n")
}

// completionResultForFile 只为 .mqh 结果附加 clangd 兼容性说明。
func completionResultForFile(filePath string, result completionTextProvider) any {
	if !strings.EqualFold(filepath.Ext(filePath), ".mqh") {
		return result
	}
	return mqlCompletionResult{result: result}
}

// NewCompletionHandler 注册 completion 工具处理器。
// 输入使用 pos 统一定位文件和光标，返回结果按 max_results 裁剪为受限列表。
func NewCompletionHandler(registry lspmanager.Registry) ToolHandler {
	return newManagerTool("completion", middleware.TierNormal, registry, decodeLenient, func(ctx context.Context, registry lspmanager.Registry, req completionParams) (any, error) {
		filePath, position, err := resolveFilePositionRequest(ctx, filePositionParams{
			Pos:        req.Pos,
			LanguageID: req.LanguageID,
		})
		if err != nil {
			return nil, err
		}
		manager, err := managerForFile(ctx, registry, filePath, req.LanguageID)
		if err != nil {
			return nil, err
		}
		limit := format.CompletionLimit(req.MaxResults)
		result, err := completionWithIdentifierEndRetry(ctx, manager, filePath, position)
		if err != nil {
			return nil, err
		}
		if result == nil || len(result.Items) == 0 {
			empty := emptyListEnvelope{
				Success: true,
				Data:    []any{},
				Meta:    resultMeta{Count: 0, Message: rustDetachedWorkspaceMessage(filePath, "completions", "no completions")},
			}
			return completionResultForFile(filePath, empty), nil
		}
		total := len(result.Items)
		items := limitSlice(result.Items, limit)
		list := format.NewCompactList(
			format.CompactCompletionItems(items),
			total,
			"next: increase max_results or move to a more precise cursor",
		)
		return completionResultForFile(filePath, list), nil
	})
}

// completionWithIdentifierEndRetry 在原光标没有候选时尝试标识符边界位置。
// 某些 LSP 只在词尾或词首返回补全；重试只读文件计算位置，不修改文档。
func completionWithIdentifierEndRetry(ctx context.Context, manager lspmanager.Manager, filePath string, position protocol.Position) (*protocol.CompletionList, error) {
	result, err := manager.Completion(ctx, filePath, position)
	if err != nil {
		return nil, err
	}
	if completionHasItems(result) {
		return result, nil
	}
	retryPositions, err := identifierCompletionRetryPositions(filePath, position)
	if err != nil {
		return nil, err
	}
	for _, retryPosition := range retryPositions {
		retryResult, err := manager.Completion(ctx, filePath, retryPosition)
		if err != nil {
			return nil, err
		}
		if completionHasItems(retryResult) {
			return retryResult, nil
		}
	}
	return result, nil
}

func completionHasItems(result *protocol.CompletionList) bool {
	return result != nil && len(result.Items) > 0
}

// identifierCompletionRetryPositions 根据当前行标识符范围生成补全重试光标。
// 行列越界直接报错，避免把无效 pos 静默修正到相邻字符。
func identifierCompletionRetryPositions(filePath string, position protocol.Position) ([]protocol.Position, error) {
	if position.Line < 0 || position.Character < 0 {
		return nil, nil
	}
	mapping, err := loadLinePositionMapping(filePath, position.Line+1)
	if err != nil {
		return nil, err
	}
	originalRuneIndex, err := mapping.runeIndexFromUTF16Character(position.Character)
	if err != nil {
		return nil, fmt.Errorf("completion retry character %d is outside target line: %w", position.Character, err)
	}
	anchor, ok := completionIdentifierAnchor(mapping.runes, originalRuneIndex)
	if !ok {
		return nil, nil
	}
	start := completionIdentifierStart(mapping.runes, anchor)
	end := completionIdentifierEnd(mapping.runes, anchor)
	return completionRetryPositions(originalRuneIndex, mapping, start, end), nil
}

// completionIdentifierAnchor 选择用于扩展标识符范围的锚点字符。
// 光标位于词尾时会回退一个字符；不在标识符上则返回 false，不触发重试。
func completionIdentifierAnchor(runes []rune, character int) (int, bool) {
	anchor := character
	if anchor == len(runes) || !isCompletionIdentifierRune(runes[anchor]) {
		anchor--
	}
	if anchor < 0 || anchor >= len(runes) || !isCompletionIdentifierRune(runes[anchor]) {
		return 0, false
	}
	return anchor, true
}

func completionIdentifierEnd(runes []rune, anchor int) int {
	end := anchor
	for end+1 < len(runes) && isCompletionIdentifierRune(runes[end+1]) {
		end++
	}
	return end
}

func completionIdentifierStart(runes []rune, anchor int) int {
	start := anchor
	for start > 0 && isCompletionIdentifierRune(runes[start-1]) {
		start--
	}
	return start
}

func completionRetryPositions(original int, mapping linePositionMapping, start int, end int) []protocol.Position {
	characters := []int{
		end + 1,
		end,
		completionIdentifierSuffixStart(mapping.runes, start, end),
		start,
	}
	positions := make([]protocol.Position, 0, len(characters))
	seen := make(map[int]struct{}, len(characters))
	for _, character := range characters {
		if character == original {
			continue
		}
		if _, ok := seen[character]; ok {
			continue
		}
		seen[character] = struct{}{}
		position, err := mapping.positionFromRuneIndex(character)
		if err != nil {
			continue
		}
		positions = append(positions, position)
	}
	return positions
}

func completionIdentifierSuffixStart(runes []rune, start int, end int) int {
	for index := end; index >= start; index-- {
		if runes[index] == '_' && index < end {
			return index + 1
		}
	}
	return start
}

func isCompletionIdentifierRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
