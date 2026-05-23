package skill

import (
	"embed"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

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
//   - ReadLocal 的 name-based 入口会先调用，path-based 入口走 resolveSkillPath。
//   - 写入、导入、删除、匹配预览等 host 参数射来的 name 亦应经此关。
//
// 白名单：Unicode letter/digit + '-' + '_'，长度上限 64 rune，首字符必须是
// Unicode letter/digit（不能以 '-' 或 '_' 开头）。
func isValidSkillRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	return r == '-' || r == '_' || r == ' '
}

func validateSkillName(name string) (string, error) {
	name = strings.TrimSpace(name)
	runes := []rune(name)
	if len(runes) == 0 || len(runes) > 64 || (!unicode.IsLetter(runes[0]) && !unicode.IsDigit(runes[0])) {
		return "", ErrInvalidSkillName
	}
	for _, r := range runes {
		if !isValidSkillRune(r) {
			return "", ErrInvalidSkillName
		}
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
	default:
		return "", fmt.Errorf("unreachable artifact kind: %q", kind)
	}
}

func normalizeMetadataLocator(trimmed string) (string, error) {
	if trimmed != "" {
		return "", errors.New("metadata artifact must have empty locator")
	}
	return "", nil
}

func normalizeBodyLocator(trimmed string) (string, error) {
	if trimmed == "" {
		return "SKILL.md", nil
	}
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

// TrustScope is a type alias for contract.TrustScope.
type TrustScope = contract.TrustScope

const (
	TrustUnknown = contract.TrustUnknown
	TrustUser    = contract.TrustUser
	TrustProject = contract.TrustProject
	TrustSigned  = contract.TrustSigned
)

// parseTrustScope parses frontmatter string to TrustScope.
func parseTrustScope(raw string) TrustScope {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "user", "trusted":
		return TrustUser
	case "project", "untrusted", "workspace":
		return TrustProject
	case "signed", "verified":
		return TrustSigned
	default:
		return TrustUnknown
	}
}

// inferTrustFromRoot infers trust scope from skill root directory.
func inferTrustFromRoot(dir, projectRoot, userRoot string) TrustScope {
	dir = normalizeTrustRoot(dir)
	if dir == "" {
		return TrustProject
	}
	if pRoot := normalizeTrustRoot(projectRoot); pRoot != "" && pathutil.ContainsPath(pRoot, dir) {
		return TrustProject
	}
	if uRoot := normalizeTrustRoot(userRoot); uRoot != "" && pathutil.ContainsPath(uRoot, dir) {
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
