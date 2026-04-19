package prompt

import (
	"sort"
	"strings"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// ============================================================================
// P20.1 Phase 10 — SkillCatalogProvider fx wiring
// ============================================================================
//
// 本文件负责：
//   1. 组合多个 provider 侧注册的 contract.SkillInjectionPort（group: "skill_injection_ports"）
//      为一个聚合 NativeSkillDetector，喂给 SkillCatalogProvider。
//   2. 按 cfg.EnableSkillProgressiveDisclosure 灰度决定是否注册 provider 到 prompt service。
//   3. flag=false 或 skill.Service 缺席（optional 未注入）时 no-op：
//      skill_catalog dynamic slot 已在 dynamicSectionSpecs 注册，但无 provider 绑定 →
//      resolveDynamicSection() 返回 (nil, nil)，section 渲染为空。
//      skill_expand_body / skill_read_resource 工具走原有 skill.Service 路径不受影响。
//
// AsSkillInjectionPortGroup 在 provider 模块侧用：
//     fx.Annotate(NewSkillInjectionPort, fx.ResultTags(prompt.SkillInjectionPortGroupTag))
// 保持 group 名 (skill_injection_ports) 与本模块内部 fx.ParamTags 一致，避免手写字符串不对齐。

// SkillInjectionPortGroupTag 是 claudecli / codexapp 等 provider 向 prompt 聚合 detector
// 注入 SkillInjectionPort 时使用的 fx result tag。保持常量供 provider.Module 引用。
const SkillInjectionPortGroupTag = `group:"skill_injection_ports"`

// skillInjectionPortGroupParamTag 本模块内部 ParamTags 字符串；与上面 ResultTag
// 对齐（两侧缺一不可，fx 不会校验；我们通过常量同源规避 drift）。
const skillInjectionPortGroupParamTag = `group:"skill_injection_ports"`

// compositeNativeSkillDetector 聚合多个 provider 的 DetectNativeSkills 结果。
//
// 语义：
//   - 任一 provider 报告的 skill 名都视为 native。
//   - codexapp 永远返回空，claudecli 扫 .claude/skills/*/SKILL.md。
//   - 同名去重（case-insensitive 归一化后 dedup），结果按字典序稳定排序。
//
// nil-safety：切片为空 / 所有 port 为 nil → 返回空切片；不 panic。
type compositeNativeSkillDetector struct {
	ports []contract.SkillInjectionPort
}

// DetectNativeSkills 见 NativeSkillDetector 接口文档。
func (d compositeNativeSkillDetector) DetectNativeSkills(cwd string) []string {
	if len(d.ports) == 0 {
		return nil
	}
	cwd = strings.TrimSpace(cwd)
	seen := make(map[string]struct{}, 8)
	out := make([]string, 0, 8)
	for _, port := range d.ports {
		if port == nil {
			continue
		}
		for _, name := range port.DetectNativeSkills(cwd) {
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, strings.TrimSpace(name))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

// NewCompositeNativeSkillDetectorParams fx 聚合参数。
type NewCompositeNativeSkillDetectorParams struct {
	fx.In
	Ports []contract.SkillInjectionPort `group:"skill_injection_ports"`
}

// NewCompositeNativeSkillDetector 构造聚合 detector。
// 入参为空时仍返回非 nil（DetectNativeSkills 会直接短路），SkillCatalogProvider
// 传入时等价传 nil — 都走 "无 native" 分支。
func NewCompositeNativeSkillDetector(p NewCompositeNativeSkillDetectorParams) NativeSkillDetector {
	filtered := make([]contract.SkillInjectionPort, 0, len(p.Ports))
	for _, port := range p.Ports {
		if port == nil {
			continue
		}
		filtered = append(filtered, port)
	}
	return compositeNativeSkillDetector{ports: filtered}
}

// skillCatalogProviderDeps fx 构造参数，skill.Service 按 optional 注入。
type skillCatalogProviderDeps struct {
	fx.In
	Cfg      *Config
	Skills   skillpkg.Service    `optional:"true"`
	Detector NativeSkillDetector `optional:"true"`
}

// NewSkillCatalogProviderFx 按 cfg 构造 SkillCatalogProvider。
//
// skills==nil（skill 模块未装载）→ 返回 zero-value provider（Resolve 直接返回 (nil, nil)）；
// 配合 fx.Invoke 的 grayscale 判断，通常不会被 register。
//
// budget：cfg.SkillCatalogTokenBudget (token) × 4 chars/token 换算为 char budget；
// ≤0 时 provider 用默认 12000 chars。
func NewSkillCatalogProviderFx(deps skillCatalogProviderDeps) SkillCatalogProvider {
	charBudget := deps.Cfg.SkillCatalogTokenBudget * 4
	return NewSkillCatalogProviderWithOptions(
		deps.Skills,
		deps.Detector,
		charBudget,
		SkillCatalogOptions{EmitMetaInstructions: deps.Cfg.EmitSkillCatalogMetaInstructions},
	)
}

// registerSkillCatalogDeps fx.Invoke 依赖。registrar 复用 contract.DynamicSectionRegistrar。
type registerSkillCatalogDeps struct {
	fx.In
	Cfg       *Config
	Registrar contract.DynamicSectionRegistrar
	Provider  SkillCatalogProvider
	Skills    skillpkg.Service `optional:"true"`
}

// RegisterSkillCatalogProviderIfEnabled 灰度注册入口。
//
// 条件：cfg.EnableSkillProgressiveDisclosure=true **且** skill.Service 已装载。
// 失败/跳过时：dynamicSectionSpec 仍在，provider 未绑定 → 渲染为空（回滚兼容）。
//
// 同时：为即使有人误设 flag=true 但 skill.Service 缺席的场景写结构化日志，方便运维定位。
func RegisterSkillCatalogProviderIfEnabled(deps registerSkillCatalogDeps) error {
	logger := pkglogger.Get()
	if deps.Cfg == nil || !deps.Cfg.EnableSkillProgressiveDisclosure {
		logger.Info("skill_catalog_provider.skipped",
			"reason", "disabled",
			"flag", "ENABLE_SKILL_PROGRESSIVE_DISCLOSURE")
		return nil
	}
	if deps.Skills == nil {
		logger.Warn("skill_catalog_provider.skipped",
			"reason", "skill_service_unavailable",
			"hint", "fx optional injection returned nil; progressive disclosure stays inert")
		return nil
	}
	if deps.Registrar == nil {
		// 不应发生；registrar 是非 optional。保守处理。
		logger.Error("skill_catalog_provider.skipped",
			"reason", "registrar_nil")
		return nil
	}
	if err := deps.Registrar.RegisterDynamicProvider(deps.Provider); err != nil {
		logger.Error("skill_catalog_provider.register_failed",
			"error", err.Error())
		return err
	}
	logger.Info("skill_catalog_provider.registered",
		"token_budget", deps.Cfg.SkillCatalogTokenBudget,
		"emit_meta_instructions", deps.Cfg.EmitSkillCatalogMetaInstructions)
	return nil
}
