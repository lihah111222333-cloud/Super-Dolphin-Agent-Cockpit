package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const defaultExtractMaxItems = 8

type RootManager struct {
	svc Service
}

type MemoryExtractor struct {
	MaxItems int
}

type extractEnvelope struct {
	Memories []ExtractedMemory `json:"memories"`
}

func NewRootManager(svc Service) *RootManager {
	return &RootManager{svc: svc}
}

func NewMemoryExtractor() *MemoryExtractor {
	return &MemoryExtractor{MaxItems: defaultExtractMaxItems}
}

func (m *RootManager) RootDir() string {
	if m == nil || m.svc == nil {
		return ""
	}
	return m.svc.RootDir()
}

func (m *RootManager) EnsureRoot(ctx context.Context) error {
	if m == nil || m.svc == nil {
		return errors.New("memory service is nil")
	}
	return m.svc.EnsureRoot(ctx)
}

func (e *MemoryExtractor) Extract(ctx context.Context, fn ExtractFunc, params ExtractParams) ([]ExtractedMemory, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if fn == nil {
		return nil, errors.New("extract func is nil")
	}
	if strings.TrimSpace(params.Transcript) == "" {
		return nil, nil
	}
	raw, err := fn(ctx, buildExtractPrompt(params, e.limit()))
	if err != nil {
		return nil, err
	}
	return parseExtractedMemories(raw, extractLimit(params.MaxItems, e.limit()))
}

func (e *MemoryExtractor) limit() int {
	if e == nil || e.MaxItems <= 0 {
		return defaultExtractMaxItems
	}
	return e.MaxItems
}

func buildExtractPrompt(params ExtractParams, fallbackLimit int) string {
	limit := extractLimit(params.MaxItems, fallbackLimit)
	parts := []string{
		"Distill only durable memory worth carrying into future sessions.",
		"Return JSON in the form {\"memories\": [{\"content\":\"...\",\"type\":\"user|feedback|project|reference\",\"tags\":[\"...\"]}] }.",
		fmt.Sprintf("Limit the response to %d memory items.", limit),
		"Conversation transcript:",
		strings.TrimSpace(params.Transcript),
	}
	return strings.Join(parts, "\n\n")
}

func parseExtractedMemories(raw string, limit int) ([]ExtractedMemory, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	items, err := decodeExtractedMemories(raw)
	if err != nil {
		return nil, err
	}
	return normalizeExtractedMemories(items, limit), nil
}

func decodeExtractedMemories(raw string) ([]ExtractedMemory, error) {
	if strings.Contains(raw, `"memories"`) {
		var envelope extractEnvelope
		if err := json.Unmarshal([]byte(raw), &envelope); err == nil {
			return envelope.Memories, nil
		}
	}
	var list []ExtractedMemory
	if err := json.Unmarshal([]byte(raw), &list); err == nil {
		return list, nil
	}
	var single ExtractedMemory
	if err := json.Unmarshal([]byte(raw), &single); err == nil {
		return []ExtractedMemory{single}, nil
	}
	return nil, fmt.Errorf("invalid extractor response")
}

func normalizeExtractedMemories(items []ExtractedMemory, limit int) []ExtractedMemory {
	if len(items) == 0 || limit <= 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	normalized := make([]ExtractedMemory, 0, minInt(len(items), limit))
	for _, item := range items {
		item = normalizeExtractedMemory(item)
		if strings.TrimSpace(item.Content) == "" {
			continue
		}
		key := CanonicalName(firstNonEmptyLine(item.Content))
		if key == "" {
			key = CanonicalName(item.Content)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, item)
		if len(normalized) >= limit {
			break
		}
	}
	return normalized
}

func normalizeExtractedMemory(item ExtractedMemory) ExtractedMemory {
	item.Content = normalizeHookContent(item.Content)
	item.Type = ParseMemoryType(string(item.Type))
	if !item.Type.IsKnown() {
		item.Type = inferMemoryType(item.Content)
	}
	item.Tags = normalizeStringSlice(item.Tags)
	if len(item.Tags) > 6 {
		item.Tags = item.Tags[:6]
	}
	return item
}

func extractLimit(limit, fallback int) int {
	if limit > 0 {
		return limit
	}
	if fallback > 0 {
		return fallback
	}
	return defaultExtractMaxItems
}
