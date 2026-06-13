package tools

import (
	"context"
	"fmt"
	"os"
	"unicode"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/middleware"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

type completionParams struct {
	Pos        string `json:"pos"`
	LanguageID string `json:"language_id,omitempty"`
	MaxResults int    `json:"max_results"`
}

// NewCompletionHandler 创建补全处理器。
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
		limit := format.CompletionLimit(req.MaxResults, format.VerbosityCompact)
		result, err := completionWithIdentifierEndRetry(ctx, manager, filePath, position)
		if err != nil {
			return nil, err
		}
		if result == nil || len(result.Items) == 0 {
			return emptyListEnvelope{
				Success: true,
				Data:    []any{},
				Meta:    resultMeta{Count: 0, Message: rustDetachedWorkspaceMessage(filePath, "completions", "no completions")},
			}, nil
		}
		total := len(result.Items)
		items := limitSlice(result.Items, limit)
		return format.NewCompactList(
			format.CompactCompletionItems(items),
			total,
			"next: increase max_results or move to a more precise cursor",
		), nil
	})
}

// completionWithIdentifierEndRetry 处理带identifierend重试的补全。
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

// identifierCompletionRetryPositions 处理identifier补全重试positions。
func identifierCompletionRetryPositions(filePath string, position protocol.Position) ([]protocol.Position, error) {
	if position.Line < 0 || position.Character < 0 {
		return nil, nil
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	lines := splitNormalizedLines(string(content))
	if position.Line >= len(lines) {
		return nil, fmt.Errorf("completion retry line %d is outside file with %d lines", position.Line+1, len(lines))
	}
	runes := []rune(lines[position.Line])
	if position.Character > len(runes) {
		return nil, fmt.Errorf("completion retry character %d is outside line length %d", position.Character+1, len(runes)+1)
	}
	anchor, ok := completionIdentifierAnchor(runes, position.Character)
	if !ok {
		return nil, nil
	}
	start := completionIdentifierStart(runes, anchor)
	end := completionIdentifierEnd(runes, anchor)
	return completionRetryPositions(position.Line, position.Character, runes, start, end), nil
}

// completionIdentifierAnchor 处理补全identifier锚点。
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

func completionRetryPositions(line int, original int, runes []rune, start int, end int) []protocol.Position {
	characters := []int{
		end + 1,
		end,
		completionIdentifierSuffixStart(runes, start, end),
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
		positions = append(positions, protocol.Position{Line: line, Character: character})
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
