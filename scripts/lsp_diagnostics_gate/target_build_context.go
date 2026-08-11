package main

import (
	"errors"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

// newTargetPolicy 基于当前 LSP 配置构造诊断目标的语言、噪声目录和宿主构建约束策略。
func newTargetPolicy() targetPolicy {
	config := platformconfig.DefaultLSPConfig()
	adapters := multilsp.NewLanguageAdapterRegistryFromConfig(config)
	supported := make(map[string]bool)
	for _, languageID := range adapters.LanguageIDs() {
		supported[languageID] = true
	}
	noise := make(map[string]bool, len(config.NoiseDirNames))
	for _, name := range config.NoiseDirNames {
		noise[name] = true
	}
	buildContext := build.Default
	buildContext.GOOS = runtime.GOOS
	buildContext.GOARCH = runtime.GOARCH
	return targetPolicy{supported: supported, noise: noise, buildContext: buildContext}
}

// classify 将单个 Git 路径裁决为诊断、主机构建约束跳过或非目标。
func (p targetPolicy) classify(root, raw string, seen map[string]bool) (string, *skippedTarget, bool, error) {
	rel, err := normalizeTargetPath(raw)
	if err != nil {
		return "", nil, false, err
	}
	if rel == "" || seen[rel] || pathContainsNoiseDir(rel, p.noise) {
		return "", nil, false, nil
	}
	abs, languageID, supported, err := p.supportedTarget(root, rel)
	if err != nil {
		return "", nil, false, err
	}
	if !supported {
		return "", nil, false, nil
	}
	seen[rel] = true
	if languageID != "go" {
		return rel, nil, true, nil
	}
	return p.classifyGoTarget(abs, rel)
}

// classifyGoTarget 将 Go 文件裁决为宿主诊断、standalone 诊断、目标平台编译或 fail-closed 拒绝。
func (p targetPolicy) classifyGoTarget(abs string, rel string) (string, *skippedTarget, bool, error) {
	matched, err := p.buildContext.MatchFile(filepath.Dir(abs), filepath.Base(abs))
	if err != nil {
		return "", nil, false, fmt.Errorf("match host build constraints for %s: %w", rel, err)
	}
	if !matched {
		source, readErr := os.ReadFile(abs)
		if readErr != nil {
			return "", nil, false, fmt.Errorf("read host-excluded Go target %s: %w", rel, readErr)
		}
		if multilsp.IsDefaultGoStandaloneMainSource(string(source)) {
			return rel, nil, true, nil
		}
		target, targetErr := matchingTargetBuildContext(filepath.Dir(abs), filepath.Base(abs))
		if targetErr != nil {
			return "", nil, false, fmt.Errorf("resolve target build constraints for %s: %w", rel, targetErr)
		}
		if target == nil {
			return "", nil, false, fmt.Errorf("changed Go file %s is excluded by host build constraints and has no supported target platform", rel)
		}
		return "", &skippedTarget{
			File: rel, Reason: "host-build-constraints", GOOS: target.GOOS, GOARCH: target.GOARCH,
			BuildTags: append([]string(nil), target.BuildTags...), BuildTagRegistryVersion: target.BuildTagRegistryVersion,
		}, false, nil
	}
	return rel, nil, true, nil
}

// matchingTargetBuildContext 为宿主构建约束排除的 Go 文件寻找首个受支持的目标平台和标签上下文。
func matchingTargetBuildContext(directory string, name string) (*targetCompileTarget, error) {
	platforms := []targetCompileTarget{
		{GOOS: "windows", GOARCH: "amd64"},
		{GOOS: "windows", GOARCH: "arm64"},
		{GOOS: "darwin", GOARCH: "arm64"},
		{GOOS: "darwin", GOARCH: "amd64"},
		{GOOS: "linux", GOARCH: "amd64"},
		{GOOS: "linux", GOARCH: "arm64"},
		{GOOS: "freebsd", GOARCH: "amd64"},
		{GOOS: "freebsd", GOARCH: "arm64"},
	}
	for _, target := range platforms {
		context := build.Default
		context.GOOS = target.GOOS
		context.GOARCH = target.GOARCH
		context.CgoEnabled = false
		matched, err := context.MatchFile(directory, name)
		if err != nil {
			return nil, err
		}
		if matched {
			return &target, nil
		}
	}
	registeredPlatforms := append([]targetCompileTarget{{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}}, platforms...)
	for _, registered := range registeredBuildTagTargets {
		for _, target := range registeredPlatforms {
			context := build.Default
			context.GOOS = target.GOOS
			context.GOARCH = target.GOARCH
			context.BuildTags = append([]string(nil), registered.tags...)
			context.CgoEnabled = false
			matched, err := context.MatchFile(directory, name)
			if err != nil {
				return nil, err
			}
			if matched {
				target.BuildTags = append([]string(nil), registered.tags...)
				target.BuildTagRegistryVersion = registered.version
				return &target, nil
			}
		}
	}
	return nil, nil
}

// supportedTarget 拒绝缺失、非普通文件、symlink 和 registry 未支持的语言路径。
func (p targetPolicy) supportedTarget(root, rel string) (string, string, bool, error) {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("stat diagnostics target %s: %w", rel, err)
	}
	languageID := lspmanager.DetectLanguageID(rel)
	if !info.Mode().IsRegular() || !p.supported[languageID] {
		return "", "", false, nil
	}
	return abs, languageID, true, nil
}

// normalizeTargetPath 将显式目标限制为仓库内相对路径，拒绝绝对路径和目录逃逸。
func normalizeTargetPath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if filepath.IsAbs(value) {
		return "", fmt.Errorf("diagnostics target must be repository-relative: %q", raw)
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("diagnostics target escapes repository root: %q", raw)
	}
	return filepath.ToSlash(clean), nil
}

// pathContainsNoiseDir 判断路径是否进入配置声明的噪声目录。
func pathContainsNoiseDir(path string, noise map[string]bool) bool {
	for part := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		if noise[part] {
			return true
		}
	}
	return false
}
