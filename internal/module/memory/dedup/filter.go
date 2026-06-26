// Package dedup 实现 durable memory 写入前的重复检测、合并和溢出处理。
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

// NewFilter 创建 Filter 实例。
// scanPrivate 必须可用；scanTeam 为 nil 时只在 private 作用域内查重。
func NewFilter(scanPrivate, scanTeam ScanFunc) *Filter {
	return &Filter{
		scanPrivate: scanPrivate,
		scanTeam:    scanTeam,
	}
}

// CheckResult 是 Filter.Check 的决策结果。
// Merge 时 MergedEntry 和 TargetPath 指向调用方需要覆盖的既有条目；其他动作不会触发写入。
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
	// 收集同类型条目时同时看 private 和 team，但写入决策仍保留作用域边界。
	existing, err := f.collectAll(candidate.Type)
	if err != nil {
		return CheckResult{}, err
	}

	// 重复查找只返回最佳候选，后续再决定能否跨作用域合并。
	match := FindDuplicate(candidate, existing)
	if !match.Found {
		return CheckResult{Action: WriteNew}, nil
	}

	target := match.Target

	if target.Scope != "" && candidate.Scope != "" && target.Scope != candidate.Scope {
		return CheckResult{Action: WriteNew}, nil
	}

	// bigram 决策只判断内容增量，不处理作用域和写入路径。
	oldBigrams := Bigrams(Normalize(target.Content))
	newBigrams := Bigrams(Normalize(candidate.Content))
	decision := Decide(oldBigrams, newBigrams)

	switch decision {
	case Skip:
		return CheckResult{Action: Skip}, nil

	case Merge:
		// 同作用域才允许覆盖既有条目，避免 team/private 互相吞并。
		merged := mergeSnapshots(target, candidate)
		return CheckResult{
			Action:      Merge,
			MergedEntry: &merged,
			TargetPath:  target.Path,
		}, nil

	default: // 匹配后理论上不会 WriteNew；保守返回新写入，避免丢失 candidate。
		return CheckResult{Action: WriteNew}, nil
	}
}

// OverflowInstruction 是条目超出上限时返回给调用方的合并指令。
// 它只说明应保留和删除的路径，不直接执行写入或删除，避免 dedup 包依赖存储层。
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
		return nil, nil // 没有合适合并对时允许短期溢出，由后续写入再尝试处理。
	}

	// 保留切片中更早出现的条目，吸收另一条，降低路径抖动。
	keep := entries[i]
	absorb := entries[j]

	merged := mergeSnapshots(keep, absorb)

	return &OverflowInstruction{
		KeepEntry:  merged,
		DeletePath: absorb.Path,
	}, nil
}

// ----- 去重辅助函数 -----

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
