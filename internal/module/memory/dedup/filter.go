package dedup

import "unicode/utf8"

// ScanFunc is the callback used to fetch existing entries for a given type.
// It is injected by the caller so the dedup package has no dependency on the
// storage layer.
type ScanFunc func(typeFilter string) ([]EntrySnapshot, error)

// Filter is the dedup facade.  It is purely computational: it reads existing
// entries via the injected ScanFunc callbacks and returns decisions; it never
// writes or deletes anything itself.
type Filter struct {
	scanPrivate ScanFunc
	scanTeam    ScanFunc // may be nil when there is no team scope
}

// NewFilter creates a new Filter.  scanTeam may be nil.
// NewFilter 创建过滤条件。
func NewFilter(scanPrivate, scanTeam ScanFunc) *Filter {
	return &Filter{
		scanPrivate: scanPrivate,
		scanTeam:    scanTeam,
	}
}

// CheckResult is the output of Filter.Check.
type CheckResult struct {
	Action      Decision       // WriteNew / Skip / Merge
	MergedEntry *EntrySnapshot // non-nil only when Action == Merge
	TargetPath  string         // file path to overwrite, non-empty only when Action == Merge
}

// Check decides what should happen to candidate.
//
// Algorithm:
//  1. Collect all same-type entries from private (and team, if available).
//  2. FindDuplicate against the combined set.
//  3. No match → WriteNew.
//  4. Match found → Decide based on bigrams.
//     - Skip → CheckResult{Action: Skip}
//     - Merge, same scope → merge content + frontmatter, return Merge result.
//     - Merge, cross scope → WriteNew (no cross-scope merge).
//     - WriteNew from Decide → WriteNew.
//
// Check 处理check。
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

// OverflowInstruction describes a pair-merge that the caller should execute
// when a type has exceeded MaxEntriesPerType.
type OverflowInstruction struct {
	KeepEntry  EntrySnapshot // the merged result (caller writes this to KeepEntry.Path)
	DeletePath string        // path of the entry that was absorbed and must be deleted
}

// FindOverflowMerge checks whether the private scope for memType has exceeded
// MaxEntriesPerType.  If it has, and a mergeable pair (containment >= 0.4) is
// found, it returns a merge instruction.  Otherwise it returns nil.
//
// It operates only on the private scope and never writes anything itself.
// FindOverflowMerge 查找overflowmerge。
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

// collectAll returns private + team entries for the given type.
// collectAll 收集all。
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

// mergeSnapshots merges two snapshots: combines content with MergeContent,
// merges frontmatter, and truncates if the result exceeds MaxEntryContentRunes.
func mergeSnapshots(keep, absorb EntrySnapshot) EntrySnapshot {
	mergedBody := MergeContent(keep.Type, keep.Content, absorb.Content)
	merged := MergeFrontmatter(keep, absorb)
	merged.Content = mergedBody
	if utf8.RuneCountInString(merged.Content) > MaxEntryContentRunes {
		merged.Content = TruncateOldestParagraphs(merged.Content, MaxEntryContentRunes)
	}
	return merged
}
