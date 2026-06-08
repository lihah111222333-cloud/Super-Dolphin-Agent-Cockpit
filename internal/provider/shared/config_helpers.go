package shared

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/util/configutil"
)

const peerBinDirEnv = "GO_AGENT_PEER_BIN_DIR"

const (
	projectRootEnv          = "PROJECT_ROOT"
	requireBundledCodexEnv  = "SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX"
	runtimeManifestFilename = "runtime-manifest.json"
)

type binaryDirResolver struct {
	executablePath func() (string, error)
	lookPath       func(string) (string, error)
}

func ResolveBinaryDir(cwd string, cfg map[string]any) string {
	return defaultBinaryDirResolver().ResolveBinaryDir(cwd, cfg)
}

func defaultBinaryDirResolver() binaryDirResolver {
	return binaryDirResolver{
		executablePath: os.Executable,
		lookPath:       exec.LookPath,
	}
}

func (r binaryDirResolver) ResolveBinaryDir(cwd string, cfg map[string]any) string {
	if dir := r.packagedBinaryDir(); dir != "" {
		return dir
	}
	if dir := ConfigString(cfg, "binary_dir", "binaryDir"); dir != "" {
		return dir
	}
	candidates := make([]string, 0, 4)
	candidates = append(candidates, peerBinDirCandidates()...)
	if exe, err := r.executablePath(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	if dir := strings.TrimSpace(cwd); dir != "" {
		candidates = append(candidates, dir)
	}
	if dir := r.lookPathBinaryDir(); dir != "" {
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

func (r binaryDirResolver) packagedBinaryDir() string {
	if dir := packagedBinaryDirFromProjectRoot(); dir != "" {
		return dir
	}
	if strings.TrimSpace(os.Getenv(requireBundledCodexEnv)) != "1" {
		return ""
	}
	candidates := peerBinDirCandidates()
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func packagedBinaryDirFromProjectRoot() string {
	root := strings.TrimSpace(os.Getenv(projectRootEnv))
	if root == "" {
		return ""
	}
	info, err := os.Stat(filepath.Join(root, runtimeManifestFilename))
	if err != nil || info.IsDir() {
		return ""
	}
	return filepath.Join(root, "bin")
}

func peerBinDirCandidates() []string {
	raw := strings.TrimSpace(os.Getenv(peerBinDirEnv))
	if raw == "" {
		return nil
	}
	dirs := make([]string, 0, 1)
	for _, part := range filepath.SplitList(raw) {
		if dir := strings.TrimSpace(part); dir != "" {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func (r binaryDirResolver) lookPathBinaryDir() string {
	for _, name := range managedBinaryNames() {
		if bin, err := r.lookPath(name); err == nil {
			return filepath.Dir(bin)
		}
	}
	return ""
}

func managedBinaryNames() [2]string {
	return [2]string{"mcp-lsp", "mcp-orch"}
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
	for _, name := range managedBinaryNames() {
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
