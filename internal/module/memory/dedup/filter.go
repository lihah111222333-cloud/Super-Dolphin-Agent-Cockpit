// Package dedup 见 tokenizer.go。
package dedup

import "unicode/utf8"

// ScanFunc 是调用方注入的回调，用于按类型查询已有条目。
// 通过依赖注入避免 dedup 包直接依赖存储层。
type ScanFunc func(typeFilter string) ([]EntrySnapshot, error)

// Filter 是去重的门面对象，纯计算逻辑：通过注入的 ScanFunc 读取已有条目并返回决策，
// 自身不执行任何写入或删除操作。
type Filter struct {
	scanPrivate ScanFunc
	scanTeam    ScanFunc // 无 team 作用域时为 nil
}

// NewFilter 创建 Filter 实例，scanTeam 可为 nil。
func NewFilter(scanPrivate, scanTeam ScanFunc) *Filter {
	return &Filter{
		scanPrivate: scanPrivate,
		scanTeam:    scanTeam,
	}
}

// CheckResult 是 Filter.Check 的输出结果。
type CheckResult struct {
	Action      Decision       // WriteNew / Skip / Merge
	MergedEntry *EntrySnapshot // 仅 Action == Merge 时非 nil
	TargetPath  string         // 仅 Action == Merge 时非空，表示需要覆写的文件路径
}

// Check 决定候选条目应如何处理。
//
// 算法：
//  1. 收集同类型的所有已有条目（private + team）。
//  2. 调用 FindDuplicate 查找重复项。
//  3. 无匹配 → WriteNew。
//  4. 有匹配 → 按 bigram 调用 Decide：
//     - Skip → 返回 Skip。
//     - Merge 且同作用域 → 合并内容与 frontmatter，返回 Merge。
//     - Merge 但跨作用域 → WriteNew（不允许跨域合并）。
//     - WriteNew → WriteNew。
func (f *Filter) Check(candidate EntrySnapshot) (CheckResult, error) {
	// --- 1. gather existing entries ---
	existing, err := f.collectAll(candidate.Type)
	if err != nil {
		return CheckResult{}, err
	}

	// --- 2. find duplicate ---
	match := FindDuplicate(candidate, existing)
	if !match.Found {
		return CheckResult{Action: WriteNew}, nil
	}

	target := match.Target

	if target.Scope != "" && candidate.Scope != "" && target.Scope != candidate.Scope {
		return CheckResult{Action: WriteNew}, nil
	}

	// --- 3. compute bigram-level decision ---
	oldBigrams := Bigrams(Normalize(target.Content))
	newBigrams := Bigrams(Normalize(candidate.Content))
	decision := Decide(oldBigrams, newBigrams)

	switch decision {
	case Skip:
		return CheckResult{Action: Skip}, nil

	case Merge:
		// Same scope: build the merged entry.
		merged := mergeSnapshots(target, candidate)
		return CheckResult{
			Action:      Merge,
			MergedEntry: &merged,
			TargetPath:  target.Path,
		}, nil

	default: // WriteNew (shouldn't happen after a match, but handle gracefully)
		return CheckResult{Action: WriteNew}, nil
	}
}

// OverflowInstruction 描述当某类型条目超出上限时，调用方需执行的一次对合并操作。
type OverflowInstruction struct {
	KeepEntry  EntrySnapshot // 合并结果（调用方写入 KeepEntry.Path）
	DeletePath string        // 被吸收条目的路径（调用方删除）
}

// FindOverflowMerge 检查 private 作用域内指定类型的条目是否超出 MaxEntriesPerType。
// 若超出且存在可合并对（containment >= 0.4），返回合并指令；否则返回 nil。
// 仅操作 private 作用域，不执行任何写入。
func (f *Filter) FindOverflowMerge(memType string) (*OverflowInstruction, error) {
	entries, err := f.scanPrivate(memType)
	if err != nil {
		return nil, err
	}

	if len(entries) <= MaxEntriesPerType {
		return nil, nil
	}

	i, j, _, found := FindMostSimilarPair(entries)
	if !found {
		return nil, nil // allow overflow — no suitable merge pair
	}

	// Keep entries[i] (the "older"/first-indexed entry), absorb entries[j].
	keep := entries[i]
	absorb := entries[j]

	merged := mergeSnapshots(keep, absorb)

	return &OverflowInstruction{
		KeepEntry:  merged,
		DeletePath: absorb.Path,
	}, nil
}

// --- helpers ---

// collectAll 合并 private 和 team 条目，路径相同的 team 条目不重复计入 private。
func (f *Filter) collectAll(memType string) ([]EntrySnapshot, error) {
	private, err := f.scanPrivate(memType)
	if err != nil {
		return nil, err
	}

	if f.scanTeam == nil {
		all := make([]EntrySnapshot, len(private))
		copy(all, private)
		return all, nil
	}

	team, err := f.scanTeam(memType)
	if err != nil {
		return nil, err
	}
	teamPaths := make(map[string]struct{}, len(team))
	for _, entry := range team {
		if entry.Path != "" {
			teamPaths[entry.Path] = struct{}{}
		}
	}

	all := make([]EntrySnapshot, 0, len(private)+len(team))
	for _, entry := range private {
		if _, duplicatedByTeamScan := teamPaths[entry.Path]; duplicatedByTeamScan {
			continue
		}
		all = append(all, entry)
	}
	all = append(all, team...)
	return all, nil
}

// mergeSnapshots 合并两个快照：内容由 MergeContent 处理，frontmatter 由 MergeFrontmatter 处理，
// 结果超出 MaxEntryContentRunes 时截断最旧段落。
func mergeSnapshots(keep, absorb EntrySnapshot) EntrySnapshot {
	mergedBody := MergeContent(keep.Type, keep.Content, absorb.Content)
	merged := MergeFrontmatter(keep, absorb)
	merged.Content = mergedBody
	if utf8.RuneCountInString(merged.Content) > MaxEntryContentRunes {
		merged.Content = TruncateOldestParagraphs(merged.Content, MaxEntryContentRunes)
	}
	return merged
}
