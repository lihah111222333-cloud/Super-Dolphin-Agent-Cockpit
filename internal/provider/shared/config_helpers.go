package shared

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/util/configutil"
)

var (
	executablePath = os.Executable
	lookPath       = exec.LookPath
)

var managedBinaryNames = []string{"mcp-lsp", "mcp-orch"}

func ResolveBinaryDir(cwd string, cfg map[string]any) string {
	if dir := ConfigString(cfg, "binary_dir", "binaryDir"); dir != "" {
		return dir
	}
	candidates := make([]string, 0, 3)
	if exe, err := executablePath(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	if dir := strings.TrimSpace(cwd); dir != "" {
		candidates = append(candidates, dir)
	}
	if dir := lookPathBinaryDir(); dir != "" {
		candidates = append(candidates, dir)
	}
	if dir := firstManagedBinaryDir(candidates...); dir != "" {
		return dir
	}
	for _, dir := range candidates {
		if dir = strings.TrimSpace(dir); dir != "" {
			return dir
		}
	}
	return ""
}

func lookPathBinaryDir() string {
	for _, name := range managedBinaryNames {
		if bin, err := lookPath(name); err == nil {
			return filepath.Dir(bin)
		}
	}
	return ""
}

func firstManagedBinaryDir(dirs ...string) string {
	for _, dir := range dirs {
		if hasManagedBinary(dir) {
			return dir
		}
	}
	return ""
}

func hasManagedBinary(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	for _, name := range managedBinaryNames {
		info, err := os.Stat(filepath.Join(dir, name))
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// ConfigString delegates to configutil.ConfigString.
func ConfigString(cfg map[string]any, keys ...string) string {
	return configutil.ConfigString(cfg, keys...)
}

// SanitizeConfigString delegates to configutil.SanitizeConfigString.
func SanitizeConfigString(value string) string {
	return configutil.SanitizeConfigString(value)
}

// StringMap delegates to configutil.StringMap.
func StringMap(raw any) map[string]string {
	return configutil.StringMap(raw)
}

// ConfigStringSlice delegates to configutil.ConfigStringSlice.
func ConfigStringSlice(cfg map[string]any, keys ...string) []string {
	return configutil.ConfigStringSlice(cfg, keys...)
}

// NormalizeConfigStringSlice delegates to configutil.NormalizeConfigStringSlice.
func NormalizeConfigStringSlice(values any) []string {
	return configutil.NormalizeConfigStringSlice(values)
}

// TrimConfigStringValues delegates to configutil.TrimConfigStringValues.
func TrimConfigStringValues(values []any) []string {
	return configutil.TrimConfigStringValues(values)
}

// SplitConfigStringSlice delegates to configutil.SplitConfigStringSlice.
func SplitConfigStringSlice(value string) []string {
	return configutil.SplitConfigStringSlice(value)
}

// TrimStrings delegates to configutil.TrimStrings.
func TrimStrings(values []string) []string {
	return configutil.TrimStrings(values)
}
