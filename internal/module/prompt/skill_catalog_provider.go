package prompt

import (
	"context"
	"fmt"
	"sort"
	"strings"

	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
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
	skills      SkillLister
	nativePort  NativeSkillDetector
	charBudget  int // 0 = 使用 defaultSkillCatalogCharBudget
}

var _ DynamicSectionProvider = SkillCatalogProvider{}

// NewSkillCatalogProvider 构造 provider。nativePort 可为 nil（codexapp 无原生机制
// 的场景等价传 nil），将跳过 Native 分组。charBudget ≤0 用默认 12000。
func NewSkillCatalogProvider(skills SkillLister, nativePort NativeSkillDetector, charBudget int) SkillCatalogProvider {
	return SkillCatalogProvider{
		skills:     skills,
		nativePort: nativePort,
		charBudget: charBudget,
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
	return &text, nil
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

// groupSkillsForManifest 按优先级分组：
//
//	native 命中 → Native 组（最高优先级：provider 接管 body，harness 只标注存在）
//	DisableModelInvocation=true → ManualOnly 组
//	Trust=Project → Redacted 组（未审批 project skill 的 metadata 视为不可信）
//	Trust=User/Signed → Core 组
//
// 同一 skill 仅进入一个组。排序在每组内按 Name 字典序。
func groupSkillsForManifest(infos []skillpkg.SkillInfo, nativeNames map[string]struct{}) skillManifestGroups {
	var g skillManifestGroups
	for _, info := range infos {
		lowerName := strings.ToLower(strings.TrimSpace(info.Name))
		if lowerName == "" {
			continue
		}
		if _, ok := nativeNames[lowerName]; ok {
			g.Native = append(g.Native, info)
			continue
		}
		if info.DisableModelInvocation {
			g.ManualOnly = append(g.ManualOnly, info)
			continue
		}
		switch info.Trust {
		case skillpkg.TrustUser, skillpkg.TrustSigned:
			g.Core = append(g.Core, info)
		case skillpkg.TrustProject, skillpkg.TrustUnknown:
			g.Redacted = append(g.Redacted, info)
		default:
			// 未知 trust 值：按最保守 → Redacted
			g.Redacted = append(g.Redacted, info)
		}
	}
	sortInfosByName(g.Core)
	sortInfosByName(g.Redacted)
	sortInfosByName(g.Native)
	sortInfosByName(g.ManualOnly)
	return g
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
