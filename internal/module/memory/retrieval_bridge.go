// Package memory compatibility bridge for the retrieval subpackage migration.
// Owned by the retrieval subpackage split; keep here until root callers move
// to direct memory/retrieval imports, then delete.
//
// This file also absorbs the former retrieval_helpers_bridge.go (searchTerms /
// contextErr / minInt) to conserve the main-package file budget.
package memory

import (
	"context"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	retrievalpkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/retrieval"
)

type ManifestBuilder = retrievalpkg.ManifestBuilder
type RelevantMemoryFinder = retrievalpkg.RelevantMemoryFinder
type PrefetchManager = retrievalpkg.PrefetchManager
type PrefetchHandle = retrievalpkg.PrefetchHandle
type transcriptSnippet = retrievalpkg.TranscriptSnippet

const (
	defaultManifestFileLimit         = retrievalpkg.DefaultManifestFileLimit
	defaultRelevantMemoryBudgetBytes = retrievalpkg.DefaultRelevantMemoryBudgetBytes
	defaultRelevantMemoryLimit       = retrievalpkg.DefaultRelevantMemoryLimit
	defaultRelevantMemoryCandidates  = retrievalpkg.DefaultRelevantMemoryCandidates
	prefetchStatePending             = retrievalpkg.PrefetchStatePending
	prefetchStateReady               = retrievalpkg.PrefetchStateReady
	prefetchStateConsumed            = retrievalpkg.PrefetchStateConsumed
	prefetchStateDiscarded           = retrievalpkg.PrefetchStateDiscarded
)

func NewManifestBuilder() *ManifestBuilder {
	return retrievalpkg.NewManifestBuilder()
}

func ScanHeadersSafe(memoryRoot string) ([]MemoryEntry, error) {
	return retrievalpkg.ScanHeadersSafe(memoryRoot)
}

func NewRelevantMemoryFinder() *RelevantMemoryFinder {
	return retrievalpkg.NewRelevantMemoryFinder()
}

func NewPrefetchManager(memoryRoot string) *PrefetchManager {
	return retrievalpkg.NewPrefetchManager(memoryRoot)
}

func freezeRelevantMemoryAttachments(entries []MemoryEntry, now time.Time) []dto.AttachmentEnvelope {
	return retrievalpkg.FreezeRelevantMemoryAttachments(entries, now)
}

func freezeTranscriptInputs(snippets []transcriptSnippet) []shareddto.InputItem {
	return retrievalpkg.FreezeTranscriptInputs(snippets)
}

func memoryHeader(now time.Time, entry MemoryEntry) string {
	return retrievalpkg.MemoryHeader(now, entry)
}

func shouldSearchPastContextQuery(query string) bool {
	return retrievalpkg.ShouldSearchPastContextQuery(query)
}

func memoryRetrievalLowConfidence(query string, entries []MemoryEntry) bool {
	return retrievalpkg.MemoryRetrievalLowConfidence(query, entries)
}

func searchTranscriptSnippets(query string, messages []dto.Message, budget int) []transcriptSnippet {
	return retrievalpkg.SearchTranscriptSnippets(query, messages, budget)
}

func memoryRenderBody(entry MemoryEntry) string {
	return retrievalpkg.MemoryRenderBody(entry)
}

// searchTerms / contextErr / minInt — moved from retrieval_helpers_bridge.go.

func searchTerms(query string) (string, []string) {
	normalized := CanonicalName(query)
	if normalized == "" {
		return "", nil
	}
	seen := map[string]struct{}{normalized: {}}
	terms := []string{normalized}
	for _, part := range strings.Fields(normalized) {
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		terms = append(terms, part)
	}
	return normalized, terms
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
