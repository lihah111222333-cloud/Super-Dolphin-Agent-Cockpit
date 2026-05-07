package turn

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type skillResolver struct{}

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
// 为 lower(name)+"@"+version。同 (name, version) 视为同一引用，会合并 Prompt；
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
			ref.Version = strings.TrimSpace(ref.Version)
			ref.Prompt = strings.TrimSpace(ref.Prompt)
			if ref.Name == "" {
				continue
			}
			key := skillDedupKey(ref)
			if key == "" {
				continue
			}
			if idx, ok := indexByKey[key]; ok {
				resolved[idx].Prompt = mergePromptText(resolved[idx].Prompt, ref.Prompt)
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

func mergePromptText(prompt, extra string) string {
	if prompt = strings.TrimSpace(prompt); prompt == "" {
		return strings.TrimSpace(extra)
	}
	if extra = strings.TrimSpace(extra); extra == "" {
		return prompt
	}
	return prompt + "\n" + extra
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
		if errors.Is(err, contract.ErrSkillMissingCWD) {
			return refs, err
		}
		return refs, nil
	}
	if len(index) == 0 {
		return refs, nil
	}
	policy := skillHydrationPolicy{ManualSkillSelection: manualSkillSelection}
	out := make([]dto.SkillRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, s.applyHydration(scopedCtx, ref, index, policy))
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
	return scopedCtx, skillInfoIndex(infos), nil
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
func skillInfoIndex(infos []contract.SkillInfo) map[string]contract.SkillInfo {
	index := make(map[string]contract.SkillInfo, len(infos))
	for _, info := range infos {
		key := strings.ToLower(strings.TrimSpace(info.Name))
		if key == "" {
			continue
		}
		index[key] = info
	}
	return index
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

func allowHydratedPrompt(info contract.SkillInfo) bool {
	return info.Trust.Trusted()
}

// applyHydration 对单个 SkillRef 执行字段补全。未命中 lookup 的 ref 原样
// 返回；命中后按以下优先级填充空字段，旧值不覆写：
//   - Version 空 + SkillInfo.ContentHash 非空 → 前 12 位 hex
//   - trusted 或真实手选授权的 untrusted：Summary 空 + SkillInfo.Summary 非空 → 拷回
//   - trusted：Prompt 空 → 试读 `<dir>/SKILL.md`，失败保留空串
//
// PR-4 安全不变量：untrusted project/unknown skill 的作者 summary 只有在
// ManualSkillSelection=true 且 ref.Source=manual 时才允许 hydrate。legacy
// Source=Unspecified、trigger、force 等来源不得因为 name-only hydration 看到 summary。
// untrusted full body 不在 selected hydration 中注入；必须继续走 skill_expand_body + approval。
func (s *service) applyHydration(ctx context.Context, ref dto.SkillRef, index map[string]contract.SkillInfo, policy skillHydrationPolicy) dto.SkillRef {
	name := strings.ToLower(strings.TrimSpace(ref.Name))
	info, ok := index[name]
	if !ok {
		return ref
	}
	if strings.TrimSpace(ref.Version) == "" && info.ContentHash != "" {
		ref.Version = shortSkillHash(info.ContentHash)
	}
	if allowHydratedSummary(info, ref, policy) && strings.TrimSpace(ref.Summary) == "" && strings.TrimSpace(info.Summary) != "" {
		ref.Summary = info.Summary
	}
	if allowHydratedPrompt(info) && strings.TrimSpace(ref.Prompt) == "" {
		if body := s.readSkillBody(ctx, info.Dir); body != "" {
			ref.Prompt = body
		}
	}
	// Source 保留不动：调用方必须显式置 Source=manual 才表示真正的 manual 选择。
	// Mode 字段已在 spec §11 cutover 后删除，注入模式由 provider adapter 自行决定
	// （codex 走 manifest L1-C + skill_read_section，claude 走 native skills）。
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
