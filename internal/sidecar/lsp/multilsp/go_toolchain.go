package multilsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/internal/hiddenexec"
)

const goVersionProbeTimeout = 2 * time.Second

type goToolchainCandidate struct {
	binDir string
	path   string
}

func withGoToolchain(info GoRootInfo, env []string, rootErr error) (GoRootInfo, error) {
	if rootErr != nil {
		return info, rootErr
	}
	toolchain, err := resolveGoToolchainForModule(info.GoModPath, env)
	if err != nil {
		return GoRootInfo{}, err
	}
	info.GoToolchain = toolchain
	return info, nil
}

func resolveGoToolchainForModule(goModPath string, env []string) (GoToolchainInfo, error) {
	if strings.TrimSpace(goModPath) == "" {
		return GoToolchainInfo{}, nil
	}
	required, err := requiredGoVersionFromMod(goModPath)
	if err != nil {
		return GoToolchainInfo{}, err
	}
	if required == "" {
		return GoToolchainInfo{}, nil
	}
	toolchain, err := selectGoToolchainFromPATH(required, env)
	if err != nil {
		return GoToolchainInfo{}, fmt.Errorf("%s requires go %s but PATH has no matching Go executable: %w", goModPath, required, err)
	}
	return toolchain, nil
}

// requiredGoVersionFromMod 从mod处理必需go版本。
func requiredGoVersionFromMod(goModPath string) (string, error) {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("read go.mod %s: %w", goModPath, err)
	}
	required := goVersion{}
	requiredText := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		candidate, ok := goDirectiveVersionCandidate(fields)
		if !ok {
			continue
		}
		parsed, err := parseGoVersion(candidate)
		if err != nil {
			return "", fmt.Errorf("parse %s directive in %s: %w", fields[0], goModPath, err)
		}
		if requiredText == "" || parsed.compare(required) > 0 {
			required = parsed
			requiredText = parsed.String()
		}
	}
	return requiredText, nil
}

func goDirectiveVersionCandidate(fields []string) (string, bool) {
	if len(fields) < 2 {
		return "", false
	}
	switch fields[0] {
	case "go":
		return fields[1], true
	case "toolchain":
		candidate := strings.TrimPrefix(fields[1], "go")
		return candidate, candidate != "default"
	default:
		return "", false
	}
}

func selectGoToolchainFromPATH(requiredText string, env []string) (GoToolchainInfo, error) {
	required, err := parseGoVersion(requiredText)
	if err != nil {
		return GoToolchainInfo{}, err
	}
	pathValue := goToolchainPATHValue(env)
	candidates := goToolchainCandidates(pathValue)
	if strings.TrimSpace(pathValue) == "" {
		return GoToolchainInfo{}, fmt.Errorf("PATH is empty")
	}
	if len(candidates) == 0 {
		return GoToolchainInfo{}, fmt.Errorf("no go executable found in PATH")
	}
	return selectGoToolchainCandidate(required, pathValue, candidates)
}

func selectGoToolchainCandidate(required goVersion, pathValue string, candidates []goToolchainCandidate) (GoToolchainInfo, error) {
	probed := make([]string, 0, len(candidates))
	for index, candidate := range candidates {
		version, err := goExecutableVersion(candidate.path)
		if err != nil {
			probed = append(probed, fmt.Sprintf("%s: %v", candidate.path, err))
			continue
		}
		probed = append(probed, fmt.Sprintf("%s=%s", candidate.path, version.String()))
		if version.compare(required) >= 0 {
			return selectedGoToolchain(required, pathValue, candidate.binDir, index > 0), nil
		}
	}
	return GoToolchainInfo{}, fmt.Errorf("checked %s", strings.Join(probed, ", "))
}

func selectedGoToolchain(required goVersion, pathValue, binDir string, overridePATH bool) GoToolchainInfo {
	toolchain := GoToolchainInfo{
		RequiredVersion: required.String(),
		BinDir:          binDir,
	}
	if overridePATH {
		toolchain.PathEnv = prependPATHDir(binDir, pathValue)
		toolchain.ForceLocal = true
	}
	return toolchain
}

func goToolchainPATHValue(env []string) string {
	if pathValue, ok := envValue(env, "PATH"); ok {
		return pathValue
	}
	return os.Getenv("PATH")
}

func goToolchainCandidates(pathValue string) []goToolchainCandidate {
	seenDirs := map[string]struct{}{}
	candidates := make([]goToolchainCandidate, 0)
	for _, dir := range filepath.SplitList(pathValue) {
		candidate, ok := goToolchainCandidateForDir(dir, seenDirs)
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func goToolchainCandidateForDir(dir string, seenDirs map[string]struct{}) (goToolchainCandidate, bool) {
	if strings.TrimSpace(dir) == "" {
		return goToolchainCandidate{}, false
	}
	normalized, err := normalizeOptionalPath(dir, "")
	if err != nil {
		return goToolchainCandidate{}, false
	}
	if _, seen := seenDirs[normalized]; seen {
		return goToolchainCandidate{}, false
	}
	seenDirs[normalized] = struct{}{}
	for _, name := range goExecutableNames() {
		candidate := filepath.Join(normalized, name)
		if isExecutableFile(candidate) {
			return goToolchainCandidate{binDir: normalized, path: candidate}, true
		}
	}
	return goToolchainCandidate{}, false
}

// goExecutableVersion 处理go可执行文件版本。
func goExecutableVersion(path string) (goVersion, error) {
	ctx, cancel := platformconfig.WithTimeout(context.Background(), goVersionProbeTimeout)
	defer cancel()
	out, err := hiddenexec.CommandContext(ctx, path, "version").Output()
	if ctx.Err() != nil {
		return goVersion{}, ctx.Err()
	}
	if err != nil {
		return goVersion{}, err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 3 || fields[0] != "go" || fields[1] != "version" {
		return goVersion{}, fmt.Errorf("unexpected go version output %q", strings.TrimSpace(string(out)))
	}
	return parseGoVersion(strings.TrimPrefix(fields[2], "go"))
}

func prependPATHDir(dir, pathValue string) string {
	parts := []string{dir}
	for _, existing := range filepath.SplitList(pathValue) {
		if strings.TrimSpace(existing) == "" {
			continue
		}
		normalized, err := normalizeOptionalPath(existing, "")
		if err == nil && normalized == dir {
			continue
		}
		parts = append(parts, existing)
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

func goExecutableNames() []string {
	if os.PathSeparator == '\\' {
		return []string{"go.exe", "go.cmd", "go.bat"}
	}
	return []string{"go"}
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if os.PathSeparator == '\\' {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

type goVersion struct {
	major int
	minor int
	patch int
}

func parseGoVersion(text string) (goVersion, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(text), "go")
	parts := strings.Split(trimmed, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return goVersion{}, fmt.Errorf("invalid Go version %q", text)
	}
	values := [3]int{}
	for i, part := range parts {
		value, err := parseGoVersionNumber(part, text)
		if err != nil {
			return goVersion{}, err
		}
		values[i] = value
	}
	return goVersion{major: values[0], minor: values[1], patch: values[2]}, nil
}

func parseGoVersionNumber(part, original string) (int, error) {
	if part == "" {
		return 0, fmt.Errorf("invalid Go version %q", original)
	}
	value := 0
	for _, r := range part {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid Go version %q", original)
		}
		value = value*10 + int(r-'0')
	}
	return value, nil
}

func (v goVersion) compare(other goVersion) int {
	if v.major != other.major {
		return v.major - other.major
	}
	if v.minor != other.minor {
		return v.minor - other.minor
	}
	return v.patch - other.patch
}

// String 返回字符串表示。
func (v goVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}
