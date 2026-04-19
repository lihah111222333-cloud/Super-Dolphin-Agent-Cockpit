package claudecli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
func NewSkillInjectionPort() contract.SkillInjectionPort {
	return claudecliSkillInjectionPort{}
}

// DetectNativeSkills 扫描 cwd 下的原生 skill 目录，返回已安装 skill 的名字列表。
//
// 检测规则：
//   - 目录结构：<cwd>/.claude/skills/<name>/SKILL.md
//   - 只认目录形态的 entry（跳过普通文件）
//   - 只有含 SKILL.md 的子目录才算有效 skill
//   - 返回的 name 已规范化（lower + trim）并按字典序排序，便于 tie-break 稳定
//
// 错误处理：cwd 不存在 / 无权限 / 目录不存在都返回 nil，不报错——这是常态
// （大量项目不含 .claude/skills/）。
//
// 注意事项：
//   - 跟随 symlink：filepath.WalkDir 默认不跟随，我们也不主动跟随，避免
//     `.claude/skills/foo -> /etc/passwd` 路径逃逸风险
//   - 跳过隐藏文件/目录（以 . 开头除 .claude 自身外，但 .claude/skills 下
//     不期望出现 `.` 开头子目录）
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

// ReservedTokens 见 contract.SkillInjectionPort 接口文档。
func (claudecliSkillInjectionPort) ReservedTokens() int {
	return defaultSkillManifestTokenBudget
}
