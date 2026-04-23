package prompt

import (
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// mergeTemplateSections folds DB-sourced prompt_template sections into the
// resolver's output. Blocks are stable-sorted by (Region, Ordinal) so callers
// don't need to pre-sort; static blocks feed CachedPrefix, dynamic blocks feed
// UncachedTail via renderResolvedSectionsByRegion in assembler.go.
//
// Semantics:
//   - Empty bodies are dropped.
//   - Blocks whose EnableWhen rejects the current BuildCtx are dropped
//     (section-level feature gate).
//   - A block whose Key matches an already-resolved built-in section REPLACES
//     the built-in content (same Name, same Region). This is the intended
//     override path when a template ships section_keys like "identity",
//     "tone_and_style", etc.: operators can edit the built-in persona instead
//     of having both copies concatenated.
//   - A block with a novel Key is appended as "tpl:<key>" so it cannot collide
//     with a future built-in addition.
func mergeTemplateSections(
	resolved []contract.ResolvedPromptSection,
	blocks []contract.BaseInstructionBlock,
	buildCtx contract.BuildCtx,
) []contract.ResolvedPromptSection {
	if len(blocks) == 0 {
		return resolved
	}
	sorted := make([]contract.BaseInstructionBlock, len(blocks))
	copy(sorted, blocks)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Region != sorted[j].Region {
			return sorted[i].Region < sorted[j].Region
		}
		return sorted[i].Ordinal < sorted[j].Ordinal
	})
	builtinIndex := indexResolvedByName(resolved)
	for _, b := range sorted {
		body := strings.TrimSpace(b.Body)
		if body == "" {
			continue
		}
		if !EvaluateEnableWhen(b.EnableWhen, buildCtx) {
			continue
		}
		key := strings.TrimSpace(b.Key)
		if idx, ok := builtinIndex[key]; ok {
			resolved[idx].Content = body
			resolved[idx].Region = b.Region
			continue
		}
		resolved = append(resolved, contract.ResolvedPromptSection{
			Name:    "tpl:" + key,
			Region:  b.Region,
			Content: body,
		})
	}
	return resolved
}

func indexResolvedByName(resolved []contract.ResolvedPromptSection) map[string]int {
	out := make(map[string]int, len(resolved))
	for i, r := range resolved {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			continue
		}
		out[name] = i
	}
	return out
}
