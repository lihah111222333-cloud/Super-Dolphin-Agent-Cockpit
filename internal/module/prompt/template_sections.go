package prompt

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// mergeTemplateSections 将运行时提供的 prompt_template sections 合并进 resolver 输出。
// Blocks 会按 Region/Ordinal 稳定排序：static 进入 CachedPrefix，dynamic 进入 UncachedTail。
// 用户模板是运行时输入，不能作为管理 RPC 或 dashboard 资产面重新暴露。
func mergeTemplateSections(
	resolved []contract.ResolvedPromptSection,
	blocks []contract.BaseInstructionBlock,
	buildCtx contract.BuildCtx,
	userPrompt string,
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
	for _, block := range sorted {
		body := strings.TrimSpace(block.Body)
		if body == "" || !EvaluateEnableWhen(block.EnableWhen, buildCtx, userPrompt) {
			continue
		}
		key := strings.TrimSpace(block.Key)
		if idx, ok := builtinIndex[key]; ok {
			resolved[idx].Content = body
			resolved[idx].Region = block.Region
			continue
		}
		resolved = append(resolved, contract.ResolvedPromptSection{
			Name:    "tpl:" + key,
			Region:  block.Region,
			Content: body,
		})
	}
	return resolved
}

// indexResolvedByName 建立已解析 section 名称到位置的索引，用于模板 section 覆盖内置内容。
func indexResolvedByName(resolved []contract.ResolvedPromptSection) map[string]int {
	out := make(map[string]int, len(resolved))
	for i, section := range resolved {
		name := strings.TrimSpace(section.Name)
		if name == "" {
			continue
		}
		out[name] = i
	}
	return out
}

// promptTags 解析 tags JSON，非法或空值返回空切片。
func promptTags(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil || tags == nil {
		return []string{}
	}
	return tags
}
