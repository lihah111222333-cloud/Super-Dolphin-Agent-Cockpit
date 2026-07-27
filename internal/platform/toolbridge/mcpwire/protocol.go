package mcpwire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

const (
	// LatestProtocolVersion 是本仓库 MCP server/client 当前首选的协议版本。
	LatestProtocolVersion = "2025-11-25"
	// LegacyProtocolVersion 保留现有 sidecar 客户端仍在使用的兼容版本。
	LegacyProtocolVersion = "2024-11-05"
)

var supportedProtocolVersions = map[string]struct{}{
	LegacyProtocolVersion: {},
	LatestProtocolVersion: {},
}

// IsSupportedProtocolVersion 判断版本是否属于当前 client/server 共同支持集合。
func IsSupportedProtocolVersion(version string) bool {
	_, ok := supportedProtocolVersions[version]
	return ok
}

// ValidateProtocolVersion 拒绝空值、空白包裹和控制字符。
func ValidateProtocolVersion(version string) error {
	if version == "" {
		return fmt.Errorf("protocolVersion is required")
	}
	if strings.TrimSpace(version) != version {
		return fmt.Errorf("protocolVersion must not contain surrounding whitespace")
	}
	for _, char := range version {
		if unicode.IsControl(char) {
			return fmt.Errorf("protocolVersion must not contain control characters")
		}
	}
	return nil
}

// NegotiateProtocolVersion 回显明确支持的版本；未知但格式安全的版本降到本端最新版本。
func NegotiateProtocolVersion(requested string) (string, error) {
	if err := ValidateProtocolVersion(requested); err != nil {
		return "", err
	}
	if IsSupportedProtocolVersion(requested) {
		return requested, nil
	}
	return LatestProtocolVersion, nil
}

// DecodeInitializeProtocolVersion 严格提取并验证 initialize result 的 server 版本。
func DecodeInitializeProtocolVersion(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return "", fmt.Errorf("initialize result must be a JSON object")
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return "", fmt.Errorf("decode initialize result: %w", err)
	}
	versionRaw, ok := payload["protocolVersion"]
	if !ok {
		return "", fmt.Errorf("initialize result protocolVersion is required")
	}
	var version string
	if err := json.Unmarshal(versionRaw, &version); err != nil {
		return "", fmt.Errorf("initialize result protocolVersion must be a string: %w", err)
	}
	if err := ValidateProtocolVersion(version); err != nil {
		return "", fmt.Errorf("initialize result: %w", err)
	}
	if !IsSupportedProtocolVersion(version) {
		return "", fmt.Errorf("initialize result protocolVersion %q is not supported", version)
	}
	return version, nil
}
