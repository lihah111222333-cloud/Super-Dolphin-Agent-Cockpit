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

func completionWithIdentifierEndRetry(ctx context.Context, manager lspmanager.Manager, filePath string, position protocol.Position) (*protocol.CompletionList, error) {
	result, err := manager.Completion(ctx, filePath, position)
	if err != nil {
		return nil, err
	}
	if completionHasItems(result) {
		return result, nil
	}
	retryPosition, ok, err := identifierEndCompletionRetryPosition(filePath, position)
	if err != nil {
		return nil, err
	}
	if !ok {
		return result, nil
	}
	retryResult, err := manager.Completion(ctx, filePath, retryPosition)
	if err != nil {
		return nil, err
	}
	if completionHasItems(retryResult) {
		return retryResult, nil
	}
	return result, nil
}

func completionHasItems(result *protocol.CompletionList) bool {
	return result != nil && len(result.Items) > 0
}

func identifierEndCompletionRetryPosition(filePath string, position protocol.Position) (protocol.Position, bool, error) {
	if position.Line < 0 || position.Character < 0 {
		return protocol.Position{}, false, nil
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		return protocol.Position{}, false, err
	}
	lines := splitNormalizedLines(string(content))
	if position.Line >= len(lines) {
		return protocol.Position{}, false, fmt.Errorf("completion retry line %d is outside file with %d lines", position.Line+1, len(lines))
	}
	runes := []rune(lines[position.Line])
	if position.Character > len(runes) {
		return protocol.Position{}, false, fmt.Errorf("completion retry character %d is outside line length %d", position.Character+1, len(runes)+1)
	}
	anchor, ok := completionIdentifierAnchor(runes, position.Character)
	if !ok {
		return protocol.Position{}, false, nil
	}
	end := completionIdentifierEnd(runes, anchor)
	if end == position.Character {
		return protocol.Position{}, false, nil
	}
	return protocol.Position{Line: position.Line, Character: end}, true, nil
}

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

func isCompletionIdentifierRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
