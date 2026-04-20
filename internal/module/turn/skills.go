package turn

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
)

type skillResolver struct{}

type resolvedAutoMatch struct {
	Ref          dto.SkillRef
	MatchedTerms []string
	ContentHash  string
}

const (
	forceTopK   = 5
	triggerTopK = 3
)

func (r *skillResolver) Resolve(selected []dto.SkillRef, candidates []dto.SkillRef, prompt string) []dto.SkillRef {
	return r.resolve("", 0, nil, selected, legacyCandidateSkills(candidates, prompt))
}

func (r *skillResolver) ResolveThread(
	threadID string,
	turnSeq uint64,
	state *expandedStateStore,
	selected []dto.SkillRef,
	candidates []resolvedAutoMatch,
) []dto.SkillRef {
	return r.resolve(threadID, turnSeq, state, selected, candidates)
}

func (r *skillResolver) resolve(
	threadID string,
	turnSeq uint64,
	state *expandedStateStore,
	selected []dto.SkillRef,
	candidates []resolvedAutoMatch,
) []dto.SkillRef {
	explicit := normalizeSkillRefs(selected)
	resolved := make([]dto.SkillRef, 0, len(explicit)+len(candidates))
	indexByKey := make(map[string]int, len(explicit)+len(candidates))
	for _, ref := range explicit {
		appendResolvedSkill(&resolved, indexByKey, normalizeExplicitSkillRef(ref))
	}
	r.appendAutoMatches(&resolved, indexByKey, splitAutoMatches(candidates, dto.SkillSourceManual), 0, false, threadID, turnSeq, state)
	r.appendAutoMatches(&resolved, indexByKey, splitAutoMatches(candidates, dto.SkillSourceForce), forceTopK, true, threadID, turnSeq, state)
	r.appendAutoMatches(&resolved, indexByKey, splitAutoMatches(candidates, dto.SkillSourceTrigger), triggerTopK, true, threadID, turnSeq, state)
	r.appendAutoMatches(&resolved, indexByKey, splitLegacyMatches(candidates), 0, false, threadID, turnSeq, state)
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

// normalizeSkillRefs 把多组 SkillRef 合并去重；P20.1 §4 Phase 5 起去重键升级
// 为 lower(name)+"@"+version。同 (name, version) 视为同一引用，会合并 Prompt，
// 并按 Full > Summary > None / manual > force > trigger > unspecified 叠加
// Mode / Source 优先级。version 不同则保留为两条，避免 hash 已变的新版本被旧
// 版本静默覆盖。
func normalizeSkillRefs(groups ...[]dto.SkillRef) []dto.SkillRef {
	total := 0
	for _, refs := range groups {
		total += len(refs)
	}
	resolved := make([]dto.SkillRef, 0, total)
	indexByKey := make(map[string]int, total)
	for _, refs := range groups {
		for _, ref := range refs {
			ref = normalizeResolvedSkillRef(ref)
			if ref.Name == "" {
				continue
			}
			key := skillDedupKey(ref)
			if key == "" {
				continue
			}
			if idx, ok := indexByKey[key]; ok {
				resolved[idx] = mergeSkillRefs(resolved[idx], ref)
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

// skillDedupKey 构造 SkillResolver 的 lower(name)+"@"+version 去重键。
//
// P20.1 §4 Phase 5：把原来的纯 name 升级为 name@version，配合 expanded_state
// 的 (name, kind, locator, hash) 工件键，让同一 skill 不同版本 / body / resource
// 不再被错误折叠成单条。空 name 返回 ""，调用方应跳过；空 version 退化为
// `name@`，等价于历史按 name 去重的语义，保证旧 payload 兼容。
func skillDedupKey(ref dto.SkillRef) string {
	name := strings.ToLower(strings.TrimSpace(ref.Name))
	if name == "" {
		return ""
	}
	return name + "@" + strings.TrimSpace(ref.Version)
}

func runtimeMatchesToCandidates(matches []skillpkg.RuntimeSkillMatch) []resolvedAutoMatch {
	if len(matches) == 0 {
		return nil
	}
	resolved := make([]resolvedAutoMatch, 0, len(matches))
	for _, match := range matches {
		ref, ok := runtimeMatchToSkillRef(match)
		if !ok {
			continue
		}
		resolved = append(resolved, resolvedAutoMatch{
			Ref:          ref,
			MatchedTerms: append([]string(nil), match.MatchedTerms...),
			ContentHash:  strings.TrimSpace(match.Skill.ContentHash),
		})
	}
	return resolved
}

func legacyCandidateSkills(candidates []dto.SkillRef, prompt string) []resolvedAutoMatch {
	prompt = strings.ToLower(strings.TrimSpace(prompt))
	normalized := normalizeSkillRefs(candidates)
	if len(normalized) == 0 {
		return nil
	}
	matches := make([]resolvedAutoMatch, 0, len(normalized))
	for _, ref := range normalized {
		match, ok := legacyCandidateMatch(ref, prompt)
		if !ok {
			continue
		}
		matches = append(matches, match)
	}
	if len(matches) == 0 {
		return nil
	}
	return matches
}

func legacyCandidateMatch(ref dto.SkillRef, prompt string) (resolvedAutoMatch, bool) {
	ref = normalizeAutoSkillRef(ref)
	if ref.Name == "" {
		return resolvedAutoMatch{}, false
	}
	name := strings.ToLower(strings.TrimSpace(ref.Name))
	if ref.Source == dto.SkillSourceManual || ref.Source == dto.SkillSourceForce || ref.Source == dto.SkillSourceTrigger {
		return resolvedAutoMatch{Ref: ref, MatchedTerms: []string{name}}, true
	}
	if prompt == "" || !matchesSkillPrompt(prompt, name) {
		return resolvedAutoMatch{}, false
	}
	if ref.Mode == dto.SkillModeUnspecified {
		ref.Mode = dto.SkillModeFull
	}
	return resolvedAutoMatch{Ref: ref, MatchedTerms: []string{name}}, true
}

func runtimeMatchToSkillRef(match skillpkg.RuntimeSkillMatch) (dto.SkillRef, bool) {
	name := strings.TrimSpace(match.Skill.Name)
	if name == "" {
		return dto.SkillRef{}, false
	}
	ref := dto.SkillRef{
		Name:    name,
		Version: shortSkillHash(match.Skill.ContentHash),
		Summary: strings.TrimSpace(match.Skill.Summary),
	}
	switch match.Kind {
	case skillpkg.RuntimeMatchKindExplicit:
		ref.Mode = dto.SkillModeFull
		ref.Source = dto.SkillSourceManual
	case skillpkg.RuntimeMatchKindForce:
		ref.Mode = dto.SkillModeFull
		ref.Source = dto.SkillSourceForce
	case skillpkg.RuntimeMatchKindTrigger:
		ref.Mode = dto.SkillModeSummary
		ref.Source = dto.SkillSourceTrigger
	default:
		return dto.SkillRef{}, false
	}
	return ref, true
}

func mergePromptText(prompt, extra string) string {
	if prompt = strings.TrimSpace(prompt); prompt == "" {
		return strings.TrimSpace(extra)
	}
	if extra = strings.TrimSpace(extra); extra == "" {
		return prompt
	}
	return prompt + "\n" + extra
}

func (r *skillResolver) appendAutoMatches(
	resolved *[]dto.SkillRef,
	indexByKey map[string]int,
	candidates []resolvedAutoMatch,
	limit int,
	applyCarry bool,
	threadID string,
	turnSeq uint64,
	state *expandedStateStore,
) {
	if len(candidates) == 0 {
		return
	}
	filtered := filterCarryCandidates(candidates, applyCarry, threadID, turnSeq, state)
	sortResolvedAutoMatches(filtered)
	added := 0
	for _, candidate := range filtered {
		key := skillDedupKey(candidate.Ref)
		if key == "" {
			continue
		}
		if limitExceeded(limit, added, key, indexByKey) {
			continue
		}
		if appendResolvedSkill(resolved, indexByKey, normalizeAutoSkillRef(candidate.Ref)) {
			added++
		}
	}
}

func filterCarryCandidates(
	candidates []resolvedAutoMatch,
	applyCarry bool,
	threadID string,
	turnSeq uint64,
	state *expandedStateStore,
) []resolvedAutoMatch {
	if !applyCarry || state == nil {
		return candidates
	}
	filtered := make([]resolvedAutoMatch, 0, len(candidates))
	for _, candidate := range candidates {
		if state.ShouldInject(threadID, turnSeq, candidate.Ref, candidate.ContentHash) {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func limitExceeded(limit, added int, key string, indexByKey map[string]int) bool {
	if limit <= 0 {
		return false
	}
	if _, ok := indexByKey[key]; ok {
		return false
	}
	return added >= limit
}

func appendResolvedSkill(resolved *[]dto.SkillRef, indexByKey map[string]int, ref dto.SkillRef) bool {
	key := skillDedupKey(ref)
	if key == "" {
		return false
	}
	if idx, ok := indexByKey[key]; ok {
		(*resolved)[idx] = mergeResolvedSkillRef((*resolved)[idx], ref)
		return false
	}
	indexByKey[key] = len(*resolved)
	*resolved = append(*resolved, ref)
	return true
}

func splitAutoMatches(candidates []resolvedAutoMatch, source dto.SkillSource) []resolvedAutoMatch {
	if len(candidates) == 0 {
		return nil
	}
	out := make([]resolvedAutoMatch, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Ref.Source == source {
			out = append(out, candidate)
		}
	}
	return out
}

func splitLegacyMatches(candidates []resolvedAutoMatch) []resolvedAutoMatch {
	if len(candidates) == 0 {
		return nil
	}
	out := make([]resolvedAutoMatch, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Ref.Source == dto.SkillSourceUnspecified {
			out = append(out, candidate)
		}
	}
	return out
}

func sortResolvedAutoMatches(matches []resolvedAutoMatch) {
	sort.SliceStable(matches, func(i, j int) bool {
		if len(matches[i].MatchedTerms) != len(matches[j].MatchedTerms) {
			return len(matches[i].MatchedTerms) > len(matches[j].MatchedTerms)
		}
		left := strings.ToLower(strings.TrimSpace(matches[i].Ref.Name))
		right := strings.ToLower(strings.TrimSpace(matches[j].Ref.Name))
		if left != right {
			return left < right
		}
		return strings.TrimSpace(matches[i].Ref.Version) < strings.TrimSpace(matches[j].Ref.Version)
	})
}

func normalizeResolvedSkillRef(ref dto.SkillRef) dto.SkillRef {
	ref.Name = strings.TrimSpace(ref.Name)
	ref.Version = strings.TrimSpace(ref.Version)
	ref.Prompt = strings.TrimSpace(ref.Prompt)
	ref.Summary = strings.TrimSpace(ref.Summary)
	return ref
}

func normalizeExplicitSkillRef(ref dto.SkillRef) dto.SkillRef {
	ref = normalizeResolvedSkillRef(ref)
	if ref.Name == "" {
		return ref
	}
	ref.Mode = dto.SkillModeFull
	ref.Source = dto.SkillSourceManual
	return ref
}

func normalizeAutoSkillRef(ref dto.SkillRef) dto.SkillRef {
	ref = normalizeResolvedSkillRef(ref)
	if ref.Mode != dto.SkillModeUnspecified {
		ref.Mode = ref.Mode.Effective()
	}
	return ref
}

func mergeSkillRefs(current, incoming dto.SkillRef) dto.SkillRef {
	current = normalizeResolvedSkillRef(current)
	incoming = normalizeResolvedSkillRef(incoming)
	preferred, fallback := preferSkillRef(current, incoming)
	out := preferred
	out.Prompt = mergePromptText(current.Prompt, incoming.Prompt)
	out.Summary = preferredText(preferred.Summary, fallback.Summary)
	out.Version = preferredText(preferred.Version, fallback.Version)
	out.Mode = maxSkillMode(current.Mode, incoming.Mode)
	out.Source = maxSkillSource(current.Source, incoming.Source)
	return out
}

func mergeResolvedSkillRef(current, incoming dto.SkillRef) dto.SkillRef {
	current = normalizeResolvedSkillRef(current)
	incoming = normalizeResolvedSkillRef(incoming)
	preferred, fallback := preferSkillRef(current, incoming)
	out := mergeSkillRefs(current, incoming)
	out.Prompt = preferredText(preferred.Prompt, fallback.Prompt)
	return out
}

func preferSkillRef(left, right dto.SkillRef) (dto.SkillRef, dto.SkillRef) {
	if sourcePriority(right.Source) > sourcePriority(left.Source) {
		return right, left
	}
	if sourcePriority(right.Source) < sourcePriority(left.Source) {
		return left, right
	}
	if modePriority(right.Mode) > modePriority(left.Mode) {
		return right, left
	}
	if modePriority(right.Mode) < modePriority(left.Mode) {
		return left, right
	}
	if len(strings.TrimSpace(right.Prompt)) > len(strings.TrimSpace(left.Prompt)) {
		return right, left
	}
	return left, right
}

func preferredText(primary, fallback string) string {
	if primary = strings.TrimSpace(primary); primary != "" {
		return primary
	}
	return strings.TrimSpace(fallback)
}

func maxSkillMode(left, right dto.SkillMode) dto.SkillMode {
	if modePriority(right) > modePriority(left) {
		return right.Effective()
	}
	return left.Effective()
}

func maxSkillSource(left, right dto.SkillSource) dto.SkillSource {
	if sourcePriority(right) > sourcePriority(left) {
		return right
	}
	return left
}

func modePriority(mode dto.SkillMode) int {
	switch mode.Effective() {
	case dto.SkillModeFull:
		return 3
	case dto.SkillModeSummary:
		return 2
	case dto.SkillModeNone:
		return 1
	default:
		return 0
	}
}

func sourcePriority(source dto.SkillSource) int {
	switch source {
	case dto.SkillSourceManual:
		return 4
	case dto.SkillSourceForce:
		return 3
	case dto.SkillSourceTrigger:
		return 2
	case dto.SkillSourceUnspecified:
		return 1
	default:
		return 0
	}
}

// ApplyNativeSkillOverride 对命中 nativeNames 的 SkillRef 强制覆盖为
// Mode=SkillModeNone + Source=SkillSourceNative。其他字段原样保留。
//
// P20.1 §4 Phase 7 + §0.5 实验 B 的核心退化策略：
//   - Claude Code CLI 会自动加载 `.claude/skills/<name>/SKILL.md`、无 flag 可关
//   - harness 若再注入同名 skill body 会造成双倍 token + 版本漂移
//   - 对策：通过 SkillInjectionPort.DetectNativeSkills(cwd) 拿到原生名单，
//     本函数将这些 skill 降为 Mode=None（不注入 body）+ Source=Native
//     （提示下游 L1 清单标注来源为 “Claude native”）。
//
// nativeNames 为空或 refs 为空时原样返回。name 匹配大小写不敏感，
// 经 trim。调用方应在 skillResolver.Resolve() 之后、写回 dto.TurnRequest
// 前调用（service 层 Phase 8 fx 集成时接线）。
//
// Source 覆盖决策（重要）：
// 若用户在 UI 手动勾选了某个 skill（上游给 Source=SkillSourceManual），而同
// 名 skill 恰好在 `.claude/skills/` 里被 Claude CLI 原生接管，本函数会
// **覆盖 Source 为 Native**。这是 P20.1 §4 明文要求的行为——因为一旦 Mode=None，
// harness 就不再注入 body：从“harness 内部责任归属”的视角，Source=Manual 是前提“
// harness 正在代用户注入”，不再注入时维持 Manual 就不准确。用户“想要 foo”
// 这个意图仍然会被满足（Claude CLI 原生照样注入），只是“谁注入”变了。
//
// 若未来需保留“用户显式勾选 vs 自动检测”的分组，建议另加独立字段
// （如 dto.SkillRef.UserSelected bool），而不是动 Source 枚举语义。
func ApplyNativeSkillOverride(refs []dto.SkillRef, nativeNames []string) []dto.SkillRef {
	if len(nativeNames) == 0 || len(refs) == 0 {
		return refs
	}
	native := make(map[string]struct{}, len(nativeNames))
	for _, n := range nativeNames {
		normalized := strings.ToLower(strings.TrimSpace(n))
		if normalized == "" {
			continue
		}
		native[normalized] = struct{}{}
	}
	if len(native) == 0 {
		return refs
	}
	out := make([]dto.SkillRef, len(refs))
	for i, ref := range refs {
		if _, hit := native[strings.ToLower(strings.TrimSpace(ref.Name))]; hit {
			ref.Mode = dto.SkillModeNone
			ref.Source = dto.SkillSourceNative
		}
		out[i] = ref
	}
	return out
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
// 补全 Prompt/Summary/Version。当 skillLookup 为 nil（optional fx 依赖未注入）
// 或所有引用均已有正文/摘要/版本时，直接返回原列表，不做任何多余 I/O。
func (s *service) hydrateSkillRefs(ctx context.Context, refs []dto.SkillRef) []dto.SkillRef {
	if s == nil || s.skillLookup == nil || len(refs) == 0 {
		return refs
	}
	if !refsNeedHydration(refs) {
		return refs
	}
	infos, err := s.skillLookup.ListSkills(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("turn skill hydrate: ListSkills failed", "error", err)
		}
		return refs
	}
	if len(infos) == 0 {
		return refs
	}
	index := skillInfoIndex(infos)
	out := make([]dto.SkillRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, s.applyHydration(ctx, ref, index))
	}
	return out
}

// refsNeedHydration 扫一遍判断是否有任何 skill 缺 Prompt/Summary/Version；
// 全部已填充时直接省掉 ListSkills/ReadLocal 调用。
func refsNeedHydration(refs []dto.SkillRef) bool {
	for _, r := range refs {
		if strings.TrimSpace(r.Name) == "" {
			continue
		}
		if strings.TrimSpace(r.Prompt) == "" ||
			strings.TrimSpace(r.Summary) == "" ||
			strings.TrimSpace(r.Version) == "" {
			return true
		}
	}
	return false
}

// skillInfoIndex 把 ListSkills 结果按 lower(name) 索引，空名跳过。
func skillInfoIndex(infos []skillpkg.SkillInfo) map[string]skillpkg.SkillInfo {
	index := make(map[string]skillpkg.SkillInfo, len(infos))
	for _, info := range infos {
		key := strings.ToLower(strings.TrimSpace(info.Name))
		if key == "" {
			continue
		}
		index[key] = info
	}
	return index
}

// applyHydration 对单个 SkillRef 执行字段补全。未命中 lookup 的 ref 原样
// 返回；命中后按以下优先级填充空字段，旧值不覆写：
//   - Summary 空 + SkillInfo.Summary 非空 → 拷回
//   - Version 空 + SkillInfo.ContentHash 非空 → 前 12 位 hex
//   - Prompt 空 → 试读 `<dir>/SKILL.md`，失败保留空串
//   - Source == Unspecified → SkillSourceManual
func (s *service) applyHydration(ctx context.Context, ref dto.SkillRef, index map[string]skillpkg.SkillInfo) dto.SkillRef {
	name := strings.ToLower(strings.TrimSpace(ref.Name))
	info, ok := index[name]
	if !ok {
		return ref
	}
	if strings.TrimSpace(ref.Summary) == "" && strings.TrimSpace(info.Summary) != "" {
		ref.Summary = info.Summary
	}
	if strings.TrimSpace(ref.Version) == "" && info.ContentHash != "" {
		ref.Version = shortSkillHash(info.ContentHash)
	}
	if strings.TrimSpace(ref.Prompt) == "" {
		if body := s.readSkillBody(ctx, info.Dir); body != "" {
			ref.Prompt = body
		}
	}
	if ref.Source == dto.SkillSourceUnspecified {
		ref.Source = dto.SkillSourceManual
	}
	return ref
}

// readSkillBody 通过 skill.Service.ReadLocal 读取 <dir>/SKILL.md 正文。
// ReadLocal 返回 `map[string]any{"skill": map[string]any{"content":...}}`；
// 失败、空结果、类型断言不中等情形均返回空串，不影响调用链。
func (s *service) readSkillBody(ctx context.Context, dir string) string {
	if s == nil || s.skillLookup == nil {
		return ""
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	target := filepath.Join(dir, "SKILL.md")
	result, err := s.skillLookup.ReadLocal(ctx, target)
	if err != nil || result == nil {
		if err != nil && s.logger != nil {
			s.logger.Debug("turn skill hydrate: ReadLocal failed", "path", target, "error", err)
		}
		return ""
	}
	outer, ok := result.(map[string]any)
	if !ok {
		return ""
	}
	inner, ok := outer["skill"].(map[string]any)
	if !ok {
		return ""
	}
	body, _ := inner["content"].(string)
	return strings.TrimSpace(body)
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
