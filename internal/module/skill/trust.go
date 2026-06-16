package skill

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	skillidentity "github.com/anthropic-ai/super-agent-v3/internal/module/skill/identity"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

// ErrInvalidSkillName 是 name 校验失败统一返回的哨兵错误，调用方可用 errors.Is 检查。
var ErrInvalidSkillName = errors.New("invalid skill name")

// validateSkillName keeps runtime skill identifiers strict.
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
// IsValidArtifactKind 判断valid产物kind是否可用。
func IsValidArtifactKind(kind string) bool { return contract.IsValidArtifactKind(kind) }

// RepoFingerprint 生成项目根目录的稳定 128-bit 指纹，作为审批缓存 key 的第一维数据。
func RepoFingerprint(projectRoot string) string {
	return kernel.MustComputeRepoFingerprint(projectRoot)
}

// NormalizeArtifactLocator 规范化产物locator。
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

// normalizeBodyLocator 规范化正文locator。
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

// normalizeResourceLocator 规范化resourcelocator。
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
