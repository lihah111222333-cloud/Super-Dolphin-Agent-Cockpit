package prompt

import (
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// mergeTemplateSections appends DB-sourced prompt_template sections into the
// resolver's output. Blocks are stable-sorted by (Region, Ordinal) so callers
// don't need to pre-sort; static blocks feed CachedPrefix, dynamic blocks feed
// UncachedTail via renderResolvedSectionsByRegion in assembler.go.
//
// Empty bodies are dropped. Blocks whose EnableWhen expression evaluates to
// false against buildCtx are also dropped (Step 3b feature gate). The Name is
// prefixed with "tpl:" to avoid colliding with registry-managed dynamic
// section names.
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
	for _, b := range sorted {
		body := strings.TrimSpace(b.Body)
		if body == "" {
			continue
		}
		if !EvaluateEnableWhen(b.EnableWhen, buildCtx) {
			continue
		}
		resolved = append(resolved, contract.ResolvedPromptSection{
			Name:    "tpl:" + b.Key,
			Region:  b.Region,
			Content: body,
		})
	}
	return resolved
}
