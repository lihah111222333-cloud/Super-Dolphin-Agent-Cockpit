package skill

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// skillNamePattern 是 skill name 的白名单正则：仅小写字母 + 数字 + 连字符，1~64 字符。
// 严格范式对齐 Claude Code Skills、MCP tool name 规范，并能从根本上仓塞路径逾越、控制字符、
// 空格、`..`、`/`、`\` 等注入向量。若未来需要放宽（如支持下划线）在此统一扩展。
var skillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ErrInvalidSkillName 是 name 校验失败统一返回的哨兵错误，调用方可用 errors.Is 检查。
var ErrInvalidSkillName = errors.New("invalid skill name")

// validateSkillName 统一校验 skill name 标识符合法性。通过返回规范化后的名字（球除首尾空白）、
// 不通过时返回 ErrInvalidSkillName 包装的错误。调用点：
//   - ReadLocal 的 path-based 入口不直接调用（它走 resolveSkillPath）。
//   - P20 Phase 6 新增的 `Service.Expand(ctx, name, ...)` 必须先调用，
//     拒绝包含 `/`, `\`, `..` 或非法字符的 name。
//   - 单测 / RPC 参数射来的 name 亦应经此关。
func validateSkillName(name string) (string, error) {
	normalized := strings.TrimSpace(name)
	if normalized == "" {
		return "", errors.Join(ErrInvalidSkillName, errors.New("name is empty"))
	}
	if !skillNamePattern.MatchString(normalized) {
		return "", errors.Join(ErrInvalidSkillName, errors.New("name must match ^[a-z0-9][a-z0-9-]{0,63}$"))
	}
	return normalized, nil
}

// TrustScope 描述 skill 的信任边界，决定其自主调用权、审批策略、工具白名单默认值。
//
// 三档来源：
//   - TrustUser    : 位于用户级 skills root（`~/.multi-agent/skills` 或 $SKILLS_ROOT），
//                    视为本地信任域，默认允许模型自主调用。
//   - TrustProject : 位于项目级 skills root（`<cwd>/.agent/skills`），通常来自 git clone，
//                    视为不受信任源；首次扫描需弹审批，且 `skill_expand` 每次 body hash 变更重审。
//   - TrustSigned  : 由 frontmatter `trust: signed` 显式声明；验签逻辑延后到 P21，当前与
//                    TrustUser 等价处理但保留字段便于未来升级。
//
// 若 frontmatter 显式写了 `trust:`，解析时覆盖推断结果；否则根据 skill 所在 root 推断。
type TrustScope string

const (
	TrustUnknown TrustScope = ""
	TrustUser    TrustScope = "user"
	TrustProject TrustScope = "project"
	TrustSigned  TrustScope = "signed"
)

// Valid 判断是否是已知信任域。
func (t TrustScope) Valid() bool {
	switch t {
	case TrustUser, TrustProject, TrustSigned:
		return true
	}
	return false
}

// Trusted 返回该 trust 是否可跳过逐次审批（user / signed 视为受信）。
func (t TrustScope) Trusted() bool {
	return t == TrustUser || t == TrustSigned
}

// parseTrustScope 把 frontmatter 的字符串解析为 TrustScope。未知值返回 TrustUnknown，
// 调用方应据此回落到 inferTrustFromRoot 的推断结果。
func parseTrustScope(raw string) TrustScope {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "user", "trusted":
		return TrustUser
	case "project", "untrusted", "workspace":
		return TrustProject
	case "signed", "verified":
		return TrustSigned
	}
	return TrustUnknown
}

// inferTrustFromRoot 根据 skill 文件所在根目录推断默认信任域。
//
// 输入：
//   - dir：skill 目录的绝对/规范化路径（parseSkillInfo 的 dir 参数即可）
//   - projectRoot：本 session 的项目级 skills root（可为空）
//   - userRoot：用户级 skills root（`~/.multi-agent/skills` 或 env 覆盖值）
//
// 匹配策略：先看 projectRoot（优先级高，命中即 untrusted）、再看 userRoot。都不匹配
// 时返回 TrustProject 作为安全兜底——宁可多弹一次审批也不放过未知源。
func inferTrustFromRoot(dir, projectRoot, userRoot string) TrustScope {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		return TrustProject
	}
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	userRoot = filepath.Clean(strings.TrimSpace(userRoot))
	if projectRoot != "" && projectRoot != "." && platformshared.ContainsPath(projectRoot, dir) {
		return TrustProject
	}
	if userRoot != "" && userRoot != "." && platformshared.ContainsPath(userRoot, dir) {
		return TrustUser
	}
	return TrustProject
}
