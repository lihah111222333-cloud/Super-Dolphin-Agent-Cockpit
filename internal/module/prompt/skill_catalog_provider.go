package prompt

import (
	"context"
	"fmt"
	"sort"
	"strings"

	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
)

// ============================================================================
// P20.1 Phase 8: SkillCatalogProvider — L1 manifest 安全投影渲染
// ============================================================================
//
// 任务清单（§4 Phase 8 + §3.3 + §3.7）：
//   - 按 trust 分组：Core（user/signed）/ Redacted（project + unapproved）/
//     Native（Claude CLI 原生接管）/ Manual-only（disable-model-invocation=true）
//   - 对 untrusted skill 不暴露作者原始 description/summary，用固定模板提示
//   - token 预算截断
//   - CacheByName + provider 内部 revision 控制失效
//
// 本 Phase 实现数据层：provider 只接 SkillLister + NativeSkillDetector 两个
// 最小依赖。approval 集成（真正按 artifact hash 解锁 Redacted）延后到
// Phase 10（统一配置 + ApprovalCache 注入时一起做）。

// SkillLister 是 prompt provider 对 skill 扫描结果的消费契约。
// 实现：internal/module/skill.Service 已经满足；测试可提供 fake。
type SkillLister interface {
	ListSkills(ctx context.Context) ([]skillpkg.SkillInfo, error)
}

// NativeSkillDetector 是 prompt provider 对 Phase 7 SkillInjectionPort 的消费契约。
// 返回 provider 原生机制（如 Claude CLI `.claude/skills/`）已接管的 skill 名列表。
type NativeSkillDetector interface {
	DetectNativeSkills(cwd string) []string
}

// skillCatalogRedactionTemplate 是 P20.1 §3.3 规定的固定占位模板。
// 对 untrusted + unapproved 的 project skill 使用——不暴露作者原始 metadata，
// 避免攻击者通过 summary/description 做指令注入。
const skillCatalogRedactionTemplate = "Untrusted project skill. Metadata hidden until approval. " +
	"To inspect details, request approval and call `skill_expand_body(\"%s\")`."

// skillCatalogMaxSummaryBytes P20.1 §3.7：Summary 统一 ≤160 字符。
const skillCatalogMaxSummaryBytes = 160

// defaultSkillCatalogTokenBudget P20.1 §3.7：manifest 默认 token 预算 3000。
// 与 provider 侧 ReservedTokens() 保持一致；实际 provider 实现中该值为上限，
// Phase 10 config 可覆盖。本 provider 用字符预算近似（≈ token × 4，英文）。
const defaultSkillCatalogCharBudget = 12000 // ≈ 3000 tokens

// SkillCatalogProvider 实现 DynamicSectionProvider 接口，生成 L1 manifest。
//
// 线程安全：字段仅在构造期赋值后只读，Resolve() 纯函数（ListSkills/DetectNativeSkills
// 自身线程安全由实现保证）。
type SkillCatalogProvider struct {
	skills               SkillLister
	nativePort           NativeSkillDetector
	charBudget           int  // 0 = 使用 defaultSkillCatalogCharBudget
	emitMetaInstructions bool // 尾部是否追加元指令（P20.1 Phase 9）
}

var _ DynamicSectionProvider = SkillCatalogProvider{}

// NewSkillCatalogProvider 构造 provider。nativePort 可为 nil（codexapp 无原生机制
// 的场景等价传 nil），将跳过 Native 分组。charBudget ≤0 用默认 12000。
//
// 默认开启元指令（P20.1 Phase 9 行为）。如需关闭用 NewSkillCatalogProviderWithOptions。
func NewSkillCatalogProvider(skills SkillLister, nativePort NativeSkillDetector, charBudget int) SkillCatalogProvider {
	return SkillCatalogProvider{
		skills:               skills,
		nativePort:           nativePort,
		charBudget:           charBudget,
		emitMetaInstructions: true,
	}
}

// SkillCatalogOptions P20.1 Phase 9 可扩展选项结构（方便 Phase 10 config 映射）。
type SkillCatalogOptions struct {
	// EmitMetaInstructions 控制尾部是否追加元指令。默认 true。
	// 实验 A 结果表明 baseInstructions 优先级已 outrank AGENTS.md，
	// 元指令主要解决 Claude Code skill 自动触发率仅 ~50% 的问题。
	EmitMetaInstructions bool
}

// NewSkillCatalogProviderWithOptions 带选项的构造口。Phase 10 config 可直接映射。
func NewSkillCatalogProviderWithOptions(skills SkillLister, nativePort NativeSkillDetector, charBudget int, opts SkillCatalogOptions) SkillCatalogProvider {
	return SkillCatalogProvider{
		skills:               skills,
		nativePort:           nativePort,
		charBudget:           charBudget,
		emitMetaInstructions: opts.EmitMetaInstructions,
	}
}

// SectionName 返回 section 标识，对齐 contract.DynamicSectionSkillCatalog。
func (SkillCatalogProvider) SectionName() string {
	return DynamicSectionSkillCatalog
}

// Resolve 生成 L1 manifest 文本。
//
// 返回 nil 表示 "不注入"（skills 为空 / ListSkills 失败）。返回 &text 注入到
// system prompt 的对应 section。
func (p SkillCatalogProvider) Resolve(ctx context.Context, input SectionContext) (*string, error) {
	if p.skills == nil {
		return nil, nil
	}
	infos, err := p.skills.ListSkills(ctx)
	if err != nil {
		// 容忍 scan 失败：不注入 manifest，但不阻断整个 prompt 装配
		return nil, nil
	}
	if len(infos) == 0 {
		return nil, nil
	}

	// 收集 native skill 名
	nativeNames := p.collectNativeNames(input)

	// 分组
	groups := groupSkillsForManifest(infos, nativeNames)
	if groups.isEmpty() {
		return nil, nil
	}

	text := renderSkillCatalog(groups, p.effectiveCharBudget())
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	if p.emitMetaInstructions {
		text = appendSkillCatalogMetaInstructions(text, p.effectiveCharBudget())
	}
	return &text, nil
}

// skillCatalogMetaInstructions 是 P20.1 Phase 9 追加的固定元指令。
//
// 背景（P20.1 §4 Phase 9 + 审查3 实测数据）：
//   - Claude Code 原生 skill 自动触发率实测仅 ~50%（社区反馈）
//   - 模型需要主动调 skill_expand_body 才能读完整内容
//   - 不加元指令可能导致模型"看到 manifest 但不调工具"
//
// 文案原则：
//   - 明文列出两个工具名称（skill_expand_body / skill_read_resource）
//   - 鼓励 false-positive over false-negative
//   - 明确 Redacted skill 需先审批，避免模型对未审批 skill 精刀细研
const skillCatalogMetaInstructions = "\n\n" +
	"## How to use skills\n\n" +
	"Before starting a task, scan the Available Skills list above. If any skill\n" +
	"name / description / summary plausibly matches the task, call\n" +
	"`skill_expand_body(\"<name>\")` to load its full instructions before\n" +
	"proceeding. Prefer false-positive (one extra call) over false-negative\n" +
	"(missing important context).\n\n" +
	"If a skill's body references resource files (e.g. under `references/` or\n" +
	"`scripts/`), call `skill_read_resource(\"<name>\", \"<relative/path>\")`\n" +
	"to fetch them on demand — do not use generic Read / Bash tools for that.\n\n" +
	"Skills listed under \"Native (Claude CLI auto-loaded)\" do NOT need\n" +
	"skill_expand_body — their body is already available to you; use `/<name>`\n" +
	"or natural-language reference instead.\n\n" +
	"Skills listed under \"Untrusted\" have their metadata hidden until approval;\n" +
	"request approval via the UI before inspecting them."

// appendSkillCatalogMetaInstructions 安全追加元指令：超预算时不追加（避免截断
// 中间文本）。若元指令本身超 budget，宁可丢掉全部也不给不完整片段。
func appendSkillCatalogMetaInstructions(catalog string, charBudget int) string {
	if len(catalog)+len(skillCatalogMetaInstructions) <= charBudget {
		return catalog + skillCatalogMetaInstructions
	}
	// 元指令放不下 → 保留 manifest 本体，追加一条精简 fallback
	const fallback = "\n\n(Skills: call `skill_expand_body(<name>)` for full body, `skill_read_resource(<name>, <path>)` for resources.)"
	if len(catalog)+len(fallback) > charBudget {
		return catalog
	}
	return catalog + fallback
}

func (p SkillCatalogProvider) effectiveCharBudget() int {
	if p.charBudget <= 0 {
		return defaultSkillCatalogCharBudget
	}
	return p.charBudget
}

// collectNativeNames 调 nativePort（若有）拿到原生 skill 名集合，返回 lower 归一化 map。
// 仅用当前 cwd（session 工作目录，从 BuildCtx 取）。
func (p SkillCatalogProvider) collectNativeNames(input SectionContext) map[string]struct{} {
	if p.nativePort == nil {
		return nil
	}
	cwd := strings.TrimSpace(input.BuildCtx.CWD)
	if cwd == "" {
		return nil
	}
	names := p.nativePort.DetectNativeSkills(cwd)
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		normalized := strings.ToLower(strings.TrimSpace(n))
		if normalized == "" {
			continue
		}
		set[normalized] = struct{}{}
	}
	return set
}

// skillManifestGroups 是按语义分组的 skill 列表。
type skillManifestGroups struct {
	Core       []skillpkg.SkillInfo // trusted/approved：完整元数据
	Redacted   []skillpkg.SkillInfo // project + unapproved：占位符
	Native     []skillpkg.SkillInfo // Claude CLI 原生接管
	ManualOnly []skillpkg.SkillInfo // disable-model-invocation=true
}

func (g skillManifestGroups) isEmpty() bool {
	return len(g.Core) == 0 && len(g.Redacted) == 0 && len(g.Native) == 0 && len(g.ManualOnly) == 0
}

// groupSkillsForManifest 按优先级分组（P20.1 §3.3 红线 A：untrusted 元数据净化）：
//
//	1. native 命中 → Native 组（最高优先级：provider 接管 body，harness 只标注存在）
//	2. Trust=Project/Unknown → Redacted 组（净化优先级 > DisableModelInvocation）
//	   ⏱ 关键安全不变量：untrusted skill 无论是否标 disable-model-invocation，
//	   其 name + description + summary 都不得以明文或 "/" slash 提示的形式
//	   出现，否则攻击者通过 .agent/skills/evil 的恶意 frontmatter 可绕过净化。
//	3. 剩下 trusted (User/Signed) + DisableModelInvocation=true → ManualOnly
//	4. 剩下 trusted (User/Signed) → Core
//
// 同一 skill 仅进入一个组。排序在每组内按 Name 字典序。
func groupSkillsForManifest(infos []skillpkg.SkillInfo, nativeNames map[string]struct{}) skillManifestGroups {
	var g skillManifestGroups
	for _, info := range infos {
		lowerName := strings.ToLower(strings.TrimSpace(info.Name))
		if lowerName == "" {
			continue
		}
		// 1. native 最高优先级
		if _, ok := nativeNames[lowerName]; ok {
			g.Native = append(g.Native, info)
			continue
		}
		// 2. 净化优先级 > disable-model-invocation：
		// 任何 untrusted skill 先走 Redacted，无论是否标 manual-only。
		if isUntrustedScope(info.Trust) {
			g.Redacted = append(g.Redacted, info)
			// P20.1 Phase 10 Step C：每条 redacted 条目 +1。放在入栈之后才计，
			// 过滤掉 lowerName=="" 早返 continue 的器航闲置条目。
			skillmetrics.IncUntrustedManifestRedaction()
			continue
		}
		// 3. trusted + disable-model-invocation → ManualOnly
		if info.DisableModelInvocation {
			g.ManualOnly = append(g.ManualOnly, info)
			continue
		}
		// 4. trusted 普通示例
		g.Core = append(g.Core, info)
	}
	sortInfosByName(g.Core)
	sortInfosByName(g.Redacted)
	sortInfosByName(g.Native)
	sortInfosByName(g.ManualOnly)
	return g
}

// isUntrustedScope 判定 Trust 是否属于“非 User/Signed”。Project / Unknown / 任何
// 未知非合法值均视为 untrusted（默认最保守）。仅 TrustUser、TrustSigned 解锁。
func isUntrustedScope(t skillpkg.TrustScope) bool {
	switch t {
	case skillpkg.TrustUser, skillpkg.TrustSigned:
		return false
	}
	return true
}

func sortInfosByName(infos []skillpkg.SkillInfo) {
	sort.SliceStable(infos, func(i, j int) bool {
		return strings.ToLower(infos[i].Name) < strings.ToLower(infos[j].Name)
	})
}

// renderSkillCatalog 组装最终 Markdown 文本。预算超出时按 section 截断尾部。
// 顺序：Core > Native > Manual-only > Redacted（优先保留 trusted / 确认接管的）。
func renderSkillCatalog(g skillManifestGroups, charBudget int) string {
	var b strings.Builder
	b.WriteString("## Available Skills (safe metadata only — call skill_expand_body for body)\n")

	// 依次渲染每个分组；渲染前检查当前总长度
	writeSection := func(title string, items []skillpkg.SkillInfo, renderer func(skillpkg.SkillInfo) string) bool {
		if len(items) == 0 {
			return true
		}
		// 尝试写入
		var section strings.Builder
		section.WriteString("\n### ")
		section.WriteString(title)
		section.WriteString("\n")
		for _, info := range items {
			section.WriteString(renderer(info))
			section.WriteString("\n")
		}
		if b.Len()+section.Len() > charBudget {
			// 超预算 → 尾部提示 + 停止
			if b.Len()+40 <= charBudget {
				b.WriteString("\n(manifest truncated by token budget)\n")
			}
			return false
		}
		b.WriteString(section.String())
		return true
	}

	cont := true
	cont = cont && writeSection("Core (trusted)", g.Core, renderCoreEntry)
	cont = cont && writeSection("Native (Claude CLI auto-loaded)", g.Native, renderNativeEntry)
	cont = cont && writeSection("Manual-only", g.ManualOnly, renderManualOnlyEntry)
	cont = cont && writeSection("Untrusted (metadata redacted until approval)", g.Redacted, renderRedactedEntry)
	_ = cont

	return b.String()
}

// renderCoreEntry 渲染 trusted/approved skill 的完整元数据。
// 保持单行主要信息 + 可选 Summary 二行，便于模型扫读。
func renderCoreEntry(info skillpkg.SkillInfo) string {
	var b strings.Builder
	b.WriteString("- **")
	b.WriteString(info.Name)
	b.WriteString("**")
	if desc := strings.TrimSpace(info.Description); desc != "" {
		b.WriteString(" — ")
		b.WriteString(desc)
	}
	if summary := truncateCatalogSummary(info.Summary); summary != "" {
		b.WriteString("\n  Summary: ")
		b.WriteString(summary)
	}
	return b.String()
}

// renderNativeEntry：提示模型 skill 存在但由 Claude CLI 原生注入。
// 不暴露 description（它同样是 frontmatter 的一部分，净化原则适用；且已非
// harness 接管，没必要重复）。
func renderNativeEntry(info skillpkg.SkillInfo) string {
	return "- **" + info.Name + "** — body auto-loaded by Claude CLI native mechanism; use `/" + info.Name + "` or natural-language reference."
}

// renderManualOnlyEntry：disable-model-invocation=true 的 skill 只出现在索引区。
// 模型看到但不能主动调用，用户需显式 `/name` 触发。
func renderManualOnlyEntry(info skillpkg.SkillInfo) string {
	name := strings.TrimSpace(info.Name)
	return "- **" + name + "** — manual only; invoke via `/" + name + "`."
}

// renderRedactedEntry P20.1 §3.3：固定模板占位，不透传作者 description/summary。
func renderRedactedEntry(info skillpkg.SkillInfo) string {
	name := strings.TrimSpace(info.Name)
	return "- **" + name + "** — " + fmt.Sprintf(skillCatalogRedactionTemplate, name)
}

// truncateCatalogSummary 裁 summary 到 skillCatalogMaxSummaryBytes，末尾补 "…"。
// P20.1 §3.7 要求 summary ≤160 字节；超长的 auto-generated summary 会被裁。
func truncateCatalogSummary(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= skillCatalogMaxSummaryBytes {
		return s
	}
	return s[:skillCatalogMaxSummaryBytes] + "…"
}
