package repofingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
)

var valid128 = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Compute 以仓库绝对路径的规范形式计算 128-bit 十六进制指纹。
// 空 cwd 返回空字符串，路径解析失败时返回错误，调用方可决定是否 fail-fast。
func Compute(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", nil
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	canonical := filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])[:32], nil
}

// MustCompute 返回 Compute 结果；路径解析失败时返回空字符串用于可选观测字段。
func MustCompute(cwd string) string {
	fp, err := Compute(cwd)
	if err != nil {
		return ""
	}
	return fp
}

// IsValid 判断字符串是否是本包生成的 128-bit 十六进制指纹。
func IsValid(fp string) bool {
	return valid128.MatchString(strings.TrimSpace(fp))
}
