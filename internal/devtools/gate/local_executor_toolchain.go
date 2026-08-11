package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// localExecutorGateToolchain is only for the legacy direct execution helper;
// receipt-backed sessions call localExecutorToolchain with their sealed proof.
func localExecutorGateToolchain() (string, string, error) {
	trustedGo, err := ResolveTrustedGoBinary(context.Background())
	if err != nil {
		return "", "", err
	}
	return localExecutorToolchain(trustedGo)
}

// localExecutorToolchain uses one receipt-bound Go binary for the executable,
// digest/version verification, and GOROOT. It never discovers Go from PATH.
func localExecutorToolchain(trustedGo TrustedGoBinary) (string, string, error) {
	goBinary, err := trustedGo.VerifiedPath()
	if err != nil {
		return "", "", err
	}
	goRoot, err := trustedGo.GoRoot()
	if err != nil {
		return "", "", err
	}
	return goRoot, filepath.Dir(goBinary), nil
}

// resolveLocalToolchainRoot 从 receipt 已绑定的 Go 二进制读取并规范化 GOROOT。
// 它将 PATH 固定为该二进制目录；命令失败、根路径漂移或目录缺失均立即失败，禁止回退到调用方环境。
func resolveLocalToolchainRoot(goBinary string) (string, error) {
	command := exec.Command(goBinary, "env", "GOROOT")
	command.Env = []string{"GOENV=off", "GOTOOLCHAIN=local", "PATH=" + filepath.Dir(goBinary)}
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("resolve local Go toolchain GOROOT: %w", err)
	}
	goRoot := strings.TrimSpace(string(output))
	if !filepath.IsAbs(goRoot) || filepath.Clean(goRoot) != goRoot {
		return "", errors.New("local Go toolchain GOROOT must be absolute")
	}
	resolvedRoot, err := filepath.EvalSymlinks(goRoot)
	if err != nil {
		return "", fmt.Errorf("resolve local Go toolchain GOROOT symlinks: %w", err)
	}
	if info, err := os.Stat(resolvedRoot); err != nil || !info.IsDir() {
		return "", errors.New("local Go toolchain GOROOT is not a directory")
	}
	return resolvedRoot, nil
}

func localExecutorSearchPath(goBin string) string {
	if strings.TrimSpace(goBin) == "" {
		return ""
	}
	return filepath.Clean(goBin)
}

// localExecutorSearchPathWithReceiptGo 按 receipt 封存的 Go 二进制构造受限 PATH，并将其目录固定置首。
// Go 路径为空时返回空值交由调用方 fail-fast；其他 receipt 工具目录仅去重附加，禁止其 Go 覆盖可信二进制。
func localExecutorSearchPathWithReceiptGo(goBin, receiptToolPath string) string {
	goDirectory := localExecutorSearchPath(goBin)
	if goDirectory == "" {
		return ""
	}
	directories := []string{goDirectory}
	seen := map[string]struct{}{goDirectory: {}}
	for _, directory := range filepath.SplitList(receiptToolPath) {
		canonical := filepath.Clean(directory)
		if canonical == "." || canonical == "" {
			continue
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		directories = append(directories, canonical)
	}
	return strings.Join(directories, string(filepath.ListSeparator))
}

func localExecutorEnvironment(layout executorLayout, searchPath, goRoot, cgoEnabled string) ([]string, error) {
	if cgoEnabled != "0" && cgoEnabled != "1" {
		return nil, errors.New("local executor CGO_ENABLED must be 0 or 1")
	}
	return []string{
		"GOCACHE=" + layout.goCache, "GOMODCACHE=" + layout.goModCache,
		"GOROOT=" + goRoot, "GOTMPDIR=" + layout.tmp, "HOME=" + layout.home,
		"PATH=" + searchPath, "TMPDIR=" + layout.tmp, "XDG_CACHE_HOME=" + layout.xdgCache,
		"CGO_ENABLED=" + cgoEnabled, "GOOS=" + runtime.GOOS, "GOARCH=" + runtime.GOARCH,
		"GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local",
		"SUPER_DOLPHIN_TEST_BACKEND=local-authority", "npm_config_offline=true",
		"npm_config_prefer_offline=true", "npm_config_audit=false", "npm_config_fund=false",
	}, nil
}
