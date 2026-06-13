package repofingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
)

var valid128 = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Compute 计算工具。
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

// MustCompute 处理mustcompute。
func MustCompute(cwd string) string {
	fp, err := Compute(cwd)
	if err != nil {
		return ""
	}
	return fp
}

// IsValid 判断valid是否可用。
func IsValid(fp string) bool {
	return valid128.MatchString(strings.TrimSpace(fp))
}
