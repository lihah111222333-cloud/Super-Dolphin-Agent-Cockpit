package claudecli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
)

// defaultSkillManifestTokenBudget 与 P20.1 §3.7 建议保持一致。
const defaultSkillManifestTokenBudget = 3000

// claudeNativeSkillsDir 是 Claude Code CLI 原生 skill 目录的相对路径。
// 实验 B (P20.1 §0.5) 确认 Claude CLI 会自动扫描并加载该目录下的 SKILL.md，
// 且没有 flag 能关闭。harness 必须按此路径检测并避让。
const claudeNativeSkillsDir = ".claude/skills"

// skillMainFile 与 skill 模块保持一致（避免跨包依赖循环）。
const claudeSkillMainFile = "SKILL.md"

// claudecliSkillInjectionPort 实现 contract.SkillInjectionPort。
//
// 核心职责：扫 `<cwd>/.claude/skills/*/SKILL.md`，把这些 skill 名汇报给
// 上游让 Resolver 强制 Mode=None。P20.1 §4 Phase 7 + §0.5 实验 B 结论：
//   - Claude Code CLI 原生机制不可关
//   - 我们独占注入会导致双倍 token + 版本漂移
//   - 唯一正确做法：承认原生机制接管 body，L1 清单只露元数据占位
type claudecliSkillInjectionPort struct{}

// NewSkillInjectionPort 构造 claudecli provider 的 Port 实例。
func NewSkillInjectionPort() claudecliSkillInjectionPort {
	return claudecliSkillInjectionPort{}
}

func newSkillInjectionPortDescriptor() contract.SkillInjectionPortDescriptor {
	descriptor, _ := contract.NewSkillInjectionPortDescriptor("claude", NewSkillInjectionPort())
	return descriptor
}

func (claudecliSkillInjectionPort) InjectL1Manifest(baseInstructions, manifest string) string {
	baseInstructions = strings.TrimSpace(baseInstructions)
	manifest = strings.TrimSpace(manifest)
	switch {
	case baseInstructions == "":
		return manifest
	case manifest == "":
		return baseInstructions
	default:
		return baseInstructions + "\n\n" + manifest
	}
}

func (claudecliSkillInjectionPort) BuildTurnSection(refs []dto.SkillRef) (string, bool) {
	sections := make([]string, 0, len(refs))
	for _, ref := range refs {
		switch ref.Mode.Effective() {
		case dto.SkillModeFull:
			block, ok := skillpkg.RenderSkillBlock(ref.Name, ref.Prompt, ref.Summary, string(ref.Mode))
			if ok {
				sections = append(sections, block)
			}
		case dto.SkillModeSummary:
			block, ok := renderLegacySummarySkillBlock(ref.Name, ref.Summary)
			if ok {
				sections = append(sections, block)
			}
		}
	}
	if len(sections) == 0 {
		return "", false
	}
	return strings.Join(sections, "\n\n"), true
}

// DetectNativeSkills 扫描 cwd 下的原生 skill 目录，返回已安装 skill 的名字列表。
//
// 检测规则：
//   - 目录结构：<cwd>/.claude/skills/<name>/SKILL.md
//   - 只认目录形态的 entry（跳过普通文件）
//   - SKILL.md 必须是普通文件（stat.IsDir()==false）；若同名目录为 `SKILL.md/`
//     子目录则视为错误布局，跳过。
//   - 返回的 name 已规范化（lower + trim）并按字典序排序，便于 tie-break 稳定
//
// Symlink 行为（与 Claude CLI 保持一致）：
//   - os.ReadDir 默认返回 entry 元数据；IsDir()/Type() 跳过 symlink 简单指向判断
//   - os.Stat(skillMD) 会跟随 symlink，指向的 SKILL.md 如果真实存在且是文件，就认
//   - 故“`.claude/skills/foo` 是指向目录的 symlink”的场景会被识别为有效
//     skill（同 Claude CLI）
//   - 安全视角：最终 body 由 Claude CLI 原生加载，harness 只记录名字，
//     不读内容，因此 symlink 路径逃逸不会通过 harness 导致任何数据泄露
//     （防御责任回到 Claude CLI 本身，不在本函数范围）
//
// 错误处理：cwd 不存在 / 无权限 / 目录不存在都返回 nil，不报错——这是常态
// （大量项目不含 .claude/skills/）。
//
// 注意事项：
//   - 跳过隐藏文件/目录（以 . 开头，防止 `.git` / `.DS_Store` / `.hidden` 等混入）
//   - 返回字典序保证下游 Resolver ApplyNativeSkillOverride() 的覆盖顺序稳定
func (claudecliSkillInjectionPort) DetectNativeSkills(cwd string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	root := filepath.Join(cwd, claudeNativeSkillsDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		skillMD := filepath.Join(root, name, claudeSkillMainFile)
		stat, err := os.Stat(skillMD)
		if err != nil || stat.IsDir() {
			continue
		}
		names = append(names, strings.ToLower(name))
	}
	sort.Strings(names)
	return names
}

func (p claudecliSkillInjectionPort) ApplyNativeOverrides(refs []dto.SkillRef, gitRoot, cwd string) []dto.SkillRef {
	out := append([]dto.SkillRef(nil), refs...)
	names := p.detectPreferredNativeSkills(gitRoot, cwd)
	if len(names) == 0 {
		return out
	}
	native := make(map[string]struct{}, len(names))
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		if key != "" {
			native[key] = struct{}{}
		}
	}
	for i := range out {
		key := strings.ToLower(strings.TrimSpace(out[i].Name))
		if _, ok := native[key]; !ok {
			continue
		}
		out[i].Mode = dto.SkillModeNone
		out[i].Source = dto.SkillSourceNative
		out[i].Prompt = ""
		out[i].Summary = ""
	}
	return out
}

// ReservedTokens 见 contract.SkillInjectionPort 接口文档。
func (claudecliSkillInjectionPort) ReservedTokens() int {
	return defaultSkillManifestTokenBudget
}

func (p claudecliSkillInjectionPort) detectPreferredNativeSkills(gitRoot, cwd string) []string {
	seen := make(map[string]struct{}, 2)
	for _, root := range []string{strings.TrimSpace(gitRoot), strings.TrimSpace(cwd)} {
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		if names := p.DetectNativeSkills(root); len(names) > 0 {
			return names
		}
	}
	return nil
}

func renderLegacySummarySkillBlock(name, summary string) (string, bool) {
	name = strings.TrimSpace(name)
	summary = strings.TrimSpace(summary)
	if _, ok := skillpkg.RenderSkillBlock(name, "", summary, string(dto.SkillModeSummary)); !ok {
		return "", false
	}
	return fmt.Sprintf("[skill:%s]\n摘要: %s\n使用方式: Call skill_expand_body(%q) for full body", name, summary, name), true
}
