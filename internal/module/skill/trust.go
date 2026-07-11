package skill

import (
	"embed"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	skillidentity "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/skill/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/pathutil"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/repofingerprint"
)

// ErrInvalidSkillName 是 name 校验失败统一返回的哨兵错误，调用方可用 errors.Is 检查。
var ErrInvalidSkillName = errors.New("invalid skill name")

//go:embed all:embedded_skills
var builtInSkillFS embed.FS

// validateSkillName 校验运行时 skill 名称。
// 这里只接受 identity 包认可的稳定名称，失败时统一返回 ErrInvalidSkillName 供 RPC 映射。
func validateSkillName(name string) (string, error) {
	if normalized, ok := skillidentity.ValidateName(name); ok {
		return normalized, nil
	}
	return "", ErrInvalidSkillName
}

func normalizeSkillIdentityName(name, displayName string) (string, string, error) {
	normalizedName, normalizedDisplay, ok := skillidentity.Normalize(name, displayName)
	if !ok {
		return "", "", ErrInvalidSkillName
	}
	return normalizedName, normalizedDisplay, nil
}

// Skill artifact 审批粒度常量，直接复用 contract 层的 wire 值。
const (
	ArtifactKindMetadata = contract.ArtifactKindMetadata
	ArtifactKindBody     = contract.ArtifactKindBody
	ArtifactKindResource = contract.ArtifactKindResource
)

// IsValidArtifactKind 校验 artifact kind 是否是跨模块约定的审批粒度。
func IsValidArtifactKind(kind string) bool { return contract.IsValidArtifactKind(kind) }

// RepoFingerprint 生成项目根目录的稳定 128-bit 指纹，作为审批缓存 key 的第一维数据。
func RepoFingerprint(projectRoot string) string {
	return repofingerprint.MustCompute(projectRoot)
}

// NormalizeArtifactLocator 按 artifact kind 规范化审批定位符。
// metadata 必须为空，body 只允许 SKILL.md 及其锚点，resource 必须是 skill 目录内相对路径。
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

// normalizeBodyLocator 规范化正文审批定位符。
// 空值等价于 SKILL.md；锚点不能包含路径分隔符，避免伪装成资源路径。
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

// normalizeResourceLocator 规范化资源审批定位符。
// 资源路径必须留在 skill 目录内，任何绝对路径、空路径或目录逃逸都 fail-fast。
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

// TrustScope 是 contract.TrustScope 的别名，保持 skill 前端 DTO 的 wire 类型不变。
type TrustScope = contract.TrustScope

const (
	TrustUnknown = contract.TrustUnknown
	TrustUser    = contract.TrustUser
	TrustProject = contract.TrustProject
	TrustSigned  = contract.TrustSigned
)

// parseTrustScope 将 frontmatter 中的 trust 字符串解析为 TrustScope。
// 未知值返回 TrustUnknown，由后续根目录推断逻辑决定最终信任域。
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

// inferTrustFromRoot 根据 skill 所在根目录推断信任域。
// projectRoot 命中时视为项目技能，userRoot 命中时视为用户技能，无法识别时按项目技能处理。
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
