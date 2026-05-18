package skill

import (
	"embed"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
	"github.com/anthropic-ai/super-agent-v3/internal/util/repofingerprint"
)

// ErrInvalidSkillName 是 name 校验失败统一返回的哨兵错误，调用方可用 errors.Is 检查。
var ErrInvalidSkillName = errors.New("invalid skill name")

//go:embed all:embedded_skills
var builtInSkillFS embed.FS

// validateSkillName 统一校验 skill name 标识符合法性。通过返回规范化后的名字（剥除首尾空白）、
// 不通过时返回 ErrInvalidSkillName 包装的错误。调用点：
//   - ReadLocal 的 path-based 入口不直接调用（它走 resolveSkillPath）。
//   - P20 Phase 6 新增的 `Service.Expand(ctx, name, ...)` 必须先调用，
//     拒绝包含 `/`, `\`, `..`、控制字符或危险字符的 name。
//   - 单测 / RPC 参数射来的 name 亦应经此关。
//
// 白名单：Unicode letter/digit + '-' + '_'，长度上限 64 rune，首字符必须是
// Unicode letter/digit（不能以 '-' 或 '_' 开头）。
func validateSkillName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrInvalidSkillName
	}
	if utf8.RuneCountInString(name) > 64 {
		return "", ErrInvalidSkillName
	}
	runes := []rune(name)
	if !unicode.IsLetter(runes[0]) && !unicode.IsDigit(runes[0]) {
		return "", ErrInvalidSkillName
	}
	for _, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		if r == '-' || r == '_' {
			continue
		}
		return "", ErrInvalidSkillName
	}
	return name, nil
}

// ============================================================================
// P20.1 §3.2 审批粒度升级：artifact-level identity helpers
// ============================================================================

// ArtifactKind constants — aliases for the canonical contract values.
const (
	ArtifactKindMetadata = contract.ArtifactKindMetadata
	ArtifactKindBody     = contract.ArtifactKindBody
	ArtifactKindResource = contract.ArtifactKindResource
)

// IsValidArtifactKind delegates to contract.IsValidArtifactKind.
func IsValidArtifactKind(kind string) bool { return contract.IsValidArtifactKind(kind) }

// RepoFingerprint 生成项目根目录的稳定 128-bit 指纹，作为审批缓存 key 的第一维数据。
func RepoFingerprint(projectRoot string) string {
	return repofingerprint.MustCompute(projectRoot)
}

// NormalizeArtifactLocator 将 kind + 原始 locator 规范化为审批 key 中的稳定字符串。
//
// 规则：
//   - metadata ：locator 必须为空（整个 skill 级元数据是单一产物）。
//   - body：`SKILL.md` 或 `SKILL.md#Anchor`；anchor 不包含 `/` 或 `..`；空 anchor 时仅
//     返回 `SKILL.md`。
//   - resource：相对路径，filepath.Clean 后不得包含 `..` 段；不得以 `/` 开头。
func NormalizeArtifactLocator(kind, locator string) (string, error) {
	if !IsValidArtifactKind(kind) {
		return "", fmt.Errorf("invalid artifact kind: %q", kind)
	}
	trimmed := strings.TrimSpace(locator)
	switch kind {
	case ArtifactKindMetadata:
		return normalizeMetadataLocator(trimmed)
	case ArtifactKindBody:
		return normalizeBodyLocator(trimmed)
	case ArtifactKindResource:
		return normalizeResourceLocator(trimmed)
	}
	return "", fmt.Errorf("unreachable artifact kind: %q", kind)
}

// normalizeMetadataLocator 验证 metadata 产物的 locator 必须为空。
func normalizeMetadataLocator(trimmed string) (string, error) {
	if trimmed != "" {
		return "", errors.New("metadata artifact must have empty locator")
	}
	return "", nil
}

// normalizeBodyLocator 规范化 body 产物的 locator：`SKILL.md` 或 `SKILL.md#Anchor`。
func normalizeBodyLocator(trimmed string) (string, error) {
	if trimmed == "" {
		return "SKILL.md", nil
	}
	// 允许 SKILL.md#Anchor 或 #Anchor 或 SKILL.md
	base, anchor, hasAnchor := strings.Cut(trimmed, "#")
	base = strings.TrimSpace(base)
	if base == "" {
		base = "SKILL.md"
	}
	if base != "SKILL.md" {
		return "", fmt.Errorf("body locator must reference SKILL.md, got %q", base)
	}
	if !hasAnchor {
		return base, nil
	}
	anchor = strings.TrimSpace(anchor)
	if strings.ContainsAny(anchor, "/\\") || strings.Contains(anchor, "..") {
		return "", fmt.Errorf("anchor must not contain path separators: %q", anchor)
	}
	if anchor == "" {
		return base, nil
	}
	return base + "#" + anchor, nil
}

// normalizeResourceLocator 规范化 resource 产物的 locator：必须为相对路径，不得逃逸 skill 目录。
func normalizeResourceLocator(trimmed string) (string, error) {
	if trimmed == "" {
		return "", errors.New("resource locator cannot be empty")
	}
	if strings.HasPrefix(trimmed, "/") {
		return "", fmt.Errorf("resource locator must be relative, got %q", trimmed)
	}
	cleaned := filepath.ToSlash(filepath.Clean(trimmed))
	if cleaned == "." || cleaned == "" {
		return "", errors.New("resource locator normalized to empty")
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("resource locator escapes skill dir: %q", trimmed)
	}
	return cleaned, nil
}

// TrustScope is a type alias for contract.TrustScope. The canonical definition
// now lives in internal/contract so that cross-module consumers (dashboard,
// prompt) do not need to import internal/module/skill.
type TrustScope = contract.TrustScope

// Trust-scope constants — aliases for the canonical contract values.
const (
	TrustUnknown = contract.TrustUnknown
	TrustUser    = contract.TrustUser
	TrustProject = contract.TrustProject
	TrustSigned  = contract.TrustSigned
)

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
//   - userRoot：用户级 skills root（`~/.super-dolphin/skills/personal/...` 或 env 覆盖值）
//
// 匹配策略：先看 projectRoot（优先级高，命中即 untrusted）、再看 userRoot。都不匹配
// 时返回 TrustProject 作为安全兜底——宁可多弹一次审批也不放过未知源。
func inferTrustFromRoot(dir, projectRoot, userRoot string) TrustScope {
	dir = normalizeTrustRoot(dir)
	if dir == "" {
		return TrustProject
	}
	projectRoot = normalizeTrustRoot(projectRoot)
	userRoot = normalizeTrustRoot(userRoot)
	if projectRoot != "" && pathutil.ContainsPath(projectRoot, dir) {
		return TrustProject
	}
	if userRoot != "" && pathutil.ContainsPath(userRoot, dir) {
		return TrustUser
	}
	return TrustProject
}

func normalizeTrustRoot(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." {
		return ""
	}
	return path
}
