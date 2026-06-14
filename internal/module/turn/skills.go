package turn

import (
	"context"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type skillResolver struct{}

// Resolve 解析turn。
func (r *skillResolver) Resolve(selected []dto.SkillRef, candidates []dto.SkillRef, prompt string) []dto.SkillRef {
	explicit := normalizeSkillRefs(selected)
	autoCandidates := normalizeSkillRefs(candidates)
	resolved := make([]dto.SkillRef, 0, len(explicit)+len(autoCandidates))
	seen := make(map[string]bool, len(explicit)+len(autoCandidates))

	for _, ref := range explicit {
		key := skillDedupKey(ref)
		if key == "" || seen[key] {
			continue
		}
		resolved = append(resolved, ref)
		seen[key] = true
	}
	for _, matched := range r.autoMatch(prompt, autoCandidates, seen) {
		key := skillDedupKey(matched)
		if key == "" || seen[key] {
			continue
		}
		resolved = append(resolved, matched)
		seen[key] = true
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

// normalizeSkillRefs 把多组 SkillRef 合并去重；P20.1 §4 Phase 5 起去重键升级
// 为 lower(name)+"@"+version。同 (name, version) 视为同一引用，会保留单条元数据；
// version 不同则保留为两条，避免 hash 已变的新版本被旧版本静默覆盖。
func normalizeSkillRefs(groups ...[]dto.SkillRef) []dto.SkillRef {
	total := 0
	for _, refs := range groups {
		total += len(refs)
	}
	resolved := make([]dto.SkillRef, 0, total)
	indexByKey := make(map[string]int, total)
	for _, refs := range groups {
		for _, ref := range refs {
			ref.Name = strings.TrimSpace(ref.Name)
			ref.Key = strings.TrimSpace(ref.Key)
			ref.Scope = strings.TrimSpace(ref.Scope)
			ref.PersonalType = strings.TrimSpace(ref.PersonalType)
			ref.Path = strings.TrimSpace(ref.Path)
			ref.Version = strings.TrimSpace(ref.Version)
			// Production turn payloads stay metadata-only. Legacy Prompt carriers
			// may still arrive on the wire for backward compatibility, but
			// provider-native mirror discovery owns body visibility now.
			ref.Prompt = ""
			if ref.Name == "" {
				continue
			}
			key := skillDedupKey(ref)
			if key == "" {
				continue
			}
			if idx, ok := indexByKey[key]; ok {
				resolved[idx] = mergeSkillRefMetadata(resolved[idx], ref)
				continue
			}
			indexByKey[key] = len(resolved)
			resolved = append(resolved, ref)
		}
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

// skillDedupKey 构造 SkillResolver 的去重键。
//
// P20.1 §4 Phase 5：把原来的纯 name 升级为 name@version，配合 expanded_state
// 的 (name, kind, locator, hash) 工件键，让同一 skill 不同版本 / body / resource
// 不再被错误折叠成单条。空 name 返回 ""，调用方应跳过；空 version 退化为
// `name@`，等价于历史按 name 去重的语义，保证旧 payload 兼容。
//
// V1 provider-native 选择链路继续把 UI 的 key/scope/personalType/path 纳入
// 去重键，确保项目级与个人级同名 skill 在 turn payload 中不会退化成单条
// name-only 引用。
func skillDedupKey(ref dto.SkillRef) string {
	name := strings.ToLower(strings.TrimSpace(ref.Name))
	if name == "" {
		return ""
	}
	if key := strings.ToLower(strings.TrimSpace(ref.Key)); key != "" {
		return "key:" + key + "@" + strings.TrimSpace(ref.Version)
	}
	scope := strings.ToLower(strings.TrimSpace(ref.Scope))
	personalType := strings.ToLower(strings.TrimSpace(ref.PersonalType))
	path := strings.ToLower(strings.TrimSpace(ref.Path))
	if scope != "" || personalType != "" || path != "" {
		return "ref:" + scope + ":" + personalType + ":" + name + ":" + path + "@" + strings.TrimSpace(ref.Version)
	}
	return name + "@" + strings.TrimSpace(ref.Version)
}

// autoMatch 判断auto是否匹配。
func (r *skillResolver) autoMatch(prompt string, refs []dto.SkillRef, seen map[string]bool) []dto.SkillRef {
	prompt = strings.ToLower(strings.TrimSpace(prompt))
	if prompt == "" || len(refs) == 0 {
		return nil
	}
	matches := make([]dto.SkillRef, 0, len(refs))
	for _, ref := range refs {
		name := strings.ToLower(strings.TrimSpace(ref.Name))
		if name == "" {
			continue
		}
		// seen 由 Resolve 传入，key 格式为 name@version（skillDedupKey）；
		// autoMatch 需同格式查询，避免同 name 不同 version 被误认为已被显式选中。
		if seen[skillDedupKey(ref)] || !matchesSkillPrompt(prompt, name) {
			continue
		}
		matches = append(matches, ref)
	}
	return matches
}

// mergeSkillRefMetadata 合并技能引用元数据。
func mergeSkillRefMetadata(existing, next dto.SkillRef) dto.SkillRef {
	if strings.TrimSpace(existing.Summary) == "" {
		existing.Summary = strings.TrimSpace(next.Summary)
	}
	if strings.TrimSpace(existing.Key) == "" {
		existing.Key = strings.TrimSpace(next.Key)
	}
	if strings.TrimSpace(existing.Scope) == "" {
		existing.Scope = strings.TrimSpace(next.Scope)
	}
	if strings.TrimSpace(existing.PersonalType) == "" {
		existing.PersonalType = strings.TrimSpace(next.PersonalType)
	}
	if strings.TrimSpace(existing.Path) == "" {
		existing.Path = strings.TrimSpace(next.Path)
	}
	if existing.Source == "" {
		existing.Source = next.Source
	}
	return existing
}

func matchesSkillPrompt(prompt string, skillName string) bool {
	if strings.Contains(prompt, "[skill:"+skillName+"]") {
		return true
	}
	if strings.Contains(prompt, "@"+skillName) {
		return true
	}
	return strings.Contains(prompt, skillName)
}

// hydrateSkillRefs p20.2 §5 step 2-3：给 PrepareTurn 收到的 manual skill
// 补全 Summary/Version。当 skillLookup 为 nil（optional fx 依赖未注入）
// 或所有引用均已有摘要/版本时，直接返回原列表，不做任何多余 I/O。
//
// p20.17 §7：`ErrMissingCWD`（契约违反）必须 fail-fast 传回，由 PrepareTurn
// 失败暴露；其他 scan 失败仍保持容忍（原 p20.2 行为），不阻断 turn 启动。
func (s *service) hydrateSkillRefs(ctx context.Context, refs []dto.SkillRef, manualSkillSelection bool) ([]dto.SkillRef, error) {
	if s == nil || s.skillLookup == nil || len(refs) == 0 {
		return refs, nil
	}
	if !refsNeedHydration(refs) {
		return refs, nil
	}
	scopedCtx, index, err := s.skillHydrationIndex(ctx)
	if err != nil {
		if blockingErr := blockingSkillHydrationError(err); blockingErr != nil {
			return refs, blockingErr
		}
		return refs, err
	}
	if len(index) == 0 {
		return refs, nil
	}
	return s.hydrateSkillRefsFromIndex(scopedCtx, refs, index, skillHydrationPolicy{ManualSkillSelection: manualSkillSelection})
}

func blockingSkillHydrationError(err error) error {
	if errors.Is(err, contract.ErrSkillMissingCWD) || errors.Is(err, contract.ErrSkillSameNameConflict) {
		return err
	}
	return nil
}

func (s *service) hydrateSkillRefsFromIndex(ctx context.Context, refs []dto.SkillRef, index map[string]contract.SkillInfo, policy skillHydrationPolicy) ([]dto.SkillRef, error) {
	out := make([]dto.SkillRef, 0, len(refs))
	for _, ref := range refs {
		hydrated, err := s.applyHydrationWithConflict(ctx, ref, index, policy)
		if err != nil {
			return refs, err
		}
		out = append(out, hydrated)
	}
	return out, nil
}

// skillHydrationIndex 返 err：ErrMissingCWD 代表契约违反（由 caller fail-fast），
// 其他 err 代表 scan/list 失败（由 hydrateSkillRefs 按容忍口径吞掉）。
func (s *service) skillHydrationIndex(ctx context.Context) (context.Context, map[string]contract.SkillInfo, error) {
	cwd, err := contract.RequireSkillCWD(ctx)
	if err != nil {
		return ctx, nil, err
	}
	scopedCtx := contract.WithSkillCWD(ctx, cwd)
	infos, err := s.skillLookup.ListSkills(scopedCtx)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("turn skill hydrate: ListSkills failed", "error", err)
		}
		return scopedCtx, nil, err
	}
	index, err := skillInfoIndex(infos)
	if err != nil {
		return scopedCtx, nil, err
	}
	return scopedCtx, index, nil
}

// refsNeedHydration 扫一遍判断是否有任何 skill 缺 Summary/Version；
// 全部已填充时直接省掉 ListSkills/ReadLocal 调用。
func refsNeedHydration(refs []dto.SkillRef) bool {
	for _, r := range refs {
		if strings.TrimSpace(r.Name) == "" {
			continue
		}
		if strings.TrimSpace(r.Summary) == "" ||
			strings.TrimSpace(r.Version) == "" {
			return true
		}
	}
	return false
}

// skillInfoIndex 把 ListSkills 结果按明确 ref key 和 legacy lower(name) 索引。
// name-only 同名仍 fail-closed；携带 scope/path 的 UI 选择可以精确命中同名项。
func skillInfoIndex(infos []contract.SkillInfo) (map[string]contract.SkillInfo, error) {
	index := make(map[string]contract.SkillInfo, len(infos))
	for _, info := range infos {
		key := skillInfoLookupKey(info.Name, info.Scope, info.PersonalType, info.Dir)
		if key == "" {
			continue
		}
		if _, ok := index[key]; ok {
			return nil, contract.ErrSkillSameNameConflict
		}
		index[key] = info
		legacyKey := strings.ToLower(strings.TrimSpace(info.Name))
		if legacyKey == "" || legacyKey == key {
			continue
		}
		if _, ok := index[legacyKey]; ok {
			index[legacyKey] = ambiguousSkillInfo(legacyKey)
			continue
		}
		index[legacyKey] = info
	}
	return index, nil
}

const ambiguousSkillInfoMarker = "__super_dolphin_ambiguous_skill__"

func ambiguousSkillInfo(name string) contract.SkillInfo {
	return contract.SkillInfo{Name: strings.TrimSpace(name), Dir: ambiguousSkillInfoMarker}
}

func skillInfoAmbiguous(info contract.SkillInfo) bool {
	return info.Dir == ambiguousSkillInfoMarker
}

func skillInfoLookupKey(name, scope, personalType, dir string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	personalType = strings.ToLower(strings.TrimSpace(personalType))
	dir = strings.ToLower(strings.TrimSpace(dir))
	if scope != "" || personalType != "" || dir != "" {
		return "ref:" + scope + ":" + personalType + ":" + name + ":" + dir
	}
	return name
}

type skillHydrationPolicy struct {
	ManualSkillSelection bool
}

func (p skillHydrationPolicy) allowUntrustedMetadata(ref dto.SkillRef) bool {
	return p.ManualSkillSelection && ref.Source == dto.SkillSourceManual
}

func allowHydratedSummary(info contract.SkillInfo, ref dto.SkillRef, policy skillHydrationPolicy) bool {
	if info.Trust.Trusted() {
		return true
	}
	return policy.allowUntrustedMetadata(ref)
}

// applyHydration 对单个 SkillRef 执行字段补全。未命中 lookup 的 ref 原样
// 返回；命中后按以下优先级填充空字段，旧值不覆写：
//   - Version 空 + SkillInfo.ContentHash 非空 → 前 12 位 hex
//   - trusted 或真实手选授权的 untrusted：Summary 空 + SkillInfo.Summary 非空 → 拷回
//
// PR-4 安全不变量：untrusted project/unknown skill 的作者 summary 只有在
// ManualSkillSelection=true 且 ref.Source=manual 时才允许 hydrate。legacy
// Source=Unspecified、trigger、force 等来源不得因为 name-only hydration 看到 summary。
// untrusted full body 不在 selected hydration 中注入；V1 provider runtime 也不再
// 通过 turn 注入正文，而是由 provider-native mirror 交给 Claude/Codex 自己发现。
func (s *service) applyHydrationWithConflict(ctx context.Context, ref dto.SkillRef, index map[string]contract.SkillInfo, policy skillHydrationPolicy) (dto.SkillRef, error) {
	info, found, err := hydrationInfoForRef(ref, index)
	if err != nil {
		return ref, err
	}
	if !found {
		return ref, nil
	}
	ref = hydrateSkillVersion(ref, info)
	ref = hydrateSkillSummary(ref, info, policy)
	// Source 保留不动：调用方必须显式置 Source=manual 才表示真正的 manual 选择。
	// Mode 字段已在 spec §11 cutover 后删除；provider 侧通过 provider-native
	// mirrors 让 Codex/Claude 自己发现 skills，不再由 turn 注入正文或读取工具。
	return ref, nil
}

func hydrateSkillVersion(ref dto.SkillRef, info contract.SkillInfo) dto.SkillRef {
	if strings.TrimSpace(ref.Version) == "" && info.ContentHash != "" {
		ref.Version = shortSkillHash(info.ContentHash)
	}
	return ref
}

func hydrateSkillSummary(ref dto.SkillRef, info contract.SkillInfo, policy skillHydrationPolicy) dto.SkillRef {
	if allowHydratedSummary(info, ref, policy) && strings.TrimSpace(ref.Summary) == "" && strings.TrimSpace(info.Summary) != "" {
		ref.Summary = info.Summary
	}
	return ref
}

func hydrationInfoForRef(ref dto.SkillRef, index map[string]contract.SkillInfo) (contract.SkillInfo, bool, error) {
	info, ok := index[skillInfoLookupKey(ref.Name, ref.Scope, ref.PersonalType, ref.Path)]
	if !ok {
		info, ok = index[strings.ToLower(strings.TrimSpace(ref.Name))]
	}
	if !ok {
		return contract.SkillInfo{}, false, nil
	}
	if skillInfoAmbiguous(info) {
		return contract.SkillInfo{}, false, contract.ErrSkillSameNameConflict
	}
	return info, true, nil
}

// shortSkillHash 取前 12 位 hex 作为 SkillRef.Version 的稳定版本标识。
// 完整 sha256 太长，P20.1 §3.7 的 manifest cache key 也用短 hash；保持一致
// 方便日后结合 skillRevision / approvalRevision 做统一的版本参考。
func shortSkillHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
