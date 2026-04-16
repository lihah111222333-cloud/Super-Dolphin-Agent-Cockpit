// Package memory helper bridge for the retrieval subpackage migration.
// Owned by the retrieval subpackage split; keep here until root callers no
// longer need searchTerms/contextErr/minInt proxies, then delete.
package memory

import (
	"context"
	"strings"
)

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
