package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	goversion "go/version"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

var errNoUsableProductionGoToolchain = errors.New("no usable Go toolchain found in explicit configuration, system installations, or PATH")

// resolveProductionGoToolchain 解析生产自更新所需的本机 Go 工具链。
func resolveProductionGoToolchain(requirement productionGoRequirement) (productionGoToolchain, error) {
	return resolveProductionGoToolchainWithDeps(requirement, liveProductionGoResolverDeps())
}

// liveProductionGoResolverDeps 构造访问真实环境和 Go 命令的依赖。
func liveProductionGoResolverDeps() productionGoResolverDeps {
	return productionGoResolverDeps{
		getenv:           os.Getenv,
		systemCandidates: productionSystemGoCandidates,
		run: func(program string, args ...string) ([]byte, error) {
			home, err := productionGoProbeHome()
			if err != nil {
				return nil, err
			}
			command := exec.Command(program, args...)
			command.Env = productionGoProbeEnvironment(program, home)
			return command.CombinedOutput()
		},
	}
}

// resolveProductionGoToolchainWithDeps 按候选树约束从显式配置和 PATH 选择工具链。
func resolveProductionGoToolchainWithDeps(
	requirement productionGoRequirement,
	deps productionGoResolverDeps,
) (productionGoToolchain, error) {
	if deps.getenv == nil || deps.run == nil || deps.systemCandidates == nil {
		return productionGoToolchain{}, errors.New("production Go resolver dependencies are required")
	}
	if err := validateProductionGoRequirement(requirement); err != nil {
		return productionGoToolchain{}, err
	}
	if explicit := deps.getenv("SUPER_DOLPHIN_GATE_GO"); explicit != "" {
		return resolveExplicitProductionGoToolchain(explicit, requirement, deps)
	}
	return resolveProductionGoToolchainFromCandidates(requirement, deps)
}

func resolveExplicitProductionGoToolchain(
	explicit string,
	requirement productionGoRequirement,
	deps productionGoResolverDeps,
) (productionGoToolchain, error) {
	toolchain, err := probeProductionGoToolchain(explicit, deps)
	if err != nil {
		return productionGoToolchain{}, fmt.Errorf("probe explicitly configured Go toolchain: %w", err)
	}
	if err := validateProductionGoToolchainRequirement(toolchain, requirement); err != nil {
		return productionGoToolchain{}, fmt.Errorf("explicitly configured Go toolchain: %w", err)
	}
	return toolchain, nil
}

// resolveProductionGoToolchainFromCandidates 先扫描系统安装，再以 PATH 作为补充候选。
func resolveProductionGoToolchainFromCandidates(
	requirement productionGoRequirement,
	deps productionGoResolverDeps,
) (productionGoToolchain, error) {
	seen := make(map[string]struct{})
	var selected productionGoToolchain
	candidates := append([]string(nil), deps.systemCandidates()...)
	for _, directory := range filepath.SplitList(deps.getenv("PATH")) {
		if directory == "" {
			continue
		}
		candidates = append(candidates, filepath.Join(directory, "go"))
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		toolchain, err := probeProductionGoToolchain(candidate, deps)
		if err != nil {
			continue
		}
		if validateProductionGoToolchainRequirement(toolchain, requirement) != nil {
			continue
		}
		if selected.Executable == "" || productionGoToolchainBetter(toolchain, selected, requirement) {
			selected = toolchain
		}
	}
	if selected.Executable != "" {
		return selected, nil
	}
	return productionGoToolchain{}, fmt.Errorf(
		"%w: candidate tree requires minimum %s and prefers %s",
		errNoUsableProductionGoToolchain,
		requirement.Minimum,
		requirement.Preferred,
	)
}

// productionSystemGoCandidates 返回不依赖调用 shell PATH 的系统级 Go 安装入口。
func productionSystemGoCandidates() []string {
	if runtime.GOOS == "darwin" {
		return []string{
			"/usr/local/go/bin/go",
			"/opt/homebrew/bin/go",
			"/opt/homebrew/opt/go/libexec/bin/go",
			"/usr/local/bin/go",
			"/usr/local/opt/go/libexec/bin/go",
		}
	}
	return []string{
		"/usr/local/go/bin/go",
		"/usr/local/bin/go",
		"/usr/bin/go",
	}
}

// productionGoProbeHome 为无 HOME 的探测提供不继承宿主配置的私有根目录。
func productionGoProbeHome() (string, error) {
	temporary, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil || !filepath.IsAbs(temporary) || filepath.Clean(temporary) != temporary {
		return "", fmt.Errorf("resolve production Go probe temporary directory: %w", err)
	}
	return filepath.Join(temporary, "super-dolphin-gate-go-home"), nil
}

// productionGoProbeEnvironment 隔离宿主 PATH、HOME、GOROOT 和 Go 配置，避免裁剪启动器污染探测。
func productionGoProbeEnvironment(executable, home string) []string {
	return []string{
		"HOME=" + home,
		"PATH=" + strings.Join([]string{filepath.Dir(executable), "/usr/bin", "/bin"}, string(os.PathListSeparator)),
		"LC_ALL=C",
		"GOENV=off",
		"GOTOOLCHAIN=local",
	}
}

func validateProductionGoRequirement(requirement productionGoRequirement) error {
	if !goversion.IsValid(requirement.Minimum) || !goversion.IsValid(requirement.Preferred) {
		return errors.New("candidate Go requirement is invalid")
	}
	if goversion.Compare(requirement.Preferred, requirement.Minimum) < 0 {
		return errors.New("candidate preferred Go toolchain is older than its minimum")
	}
	return nil
}

func validateProductionGoToolchainRequirement(
	toolchain productionGoToolchain,
	requirement productionGoRequirement,
) error {
	candidate, err := productionGoToolchainVersion(toolchain)
	if err != nil {
		return err
	}
	if goversion.Compare(candidate, requirement.Minimum) < 0 {
		return fmt.Errorf("Go toolchain %s is older than required %s", candidate, requirement.Minimum)
	}
	return nil
}

func productionGoToolchainVersion(toolchain productionGoToolchain) (string, error) {
	fields := strings.Fields(toolchain.Version)
	if len(fields) != 4 || fields[0] != "go" || fields[1] != "version" || !goversion.IsValid(fields[2]) {
		return "", errors.New("Go version probe is not canonical")
	}
	return fields[2], nil
}

func productionGoToolchainBetter(
	candidate productionGoToolchain,
	current productionGoToolchain,
	requirement productionGoRequirement,
) bool {
	candidateVersion, candidateErr := productionGoToolchainVersion(candidate)
	currentVersion, currentErr := productionGoToolchainVersion(current)
	if candidateErr != nil || currentErr != nil {
		return false
	}
	candidateClass := productionGoToolchainPreferenceClass(candidateVersion, requirement.Preferred)
	currentClass := productionGoToolchainPreferenceClass(currentVersion, requirement.Preferred)
	if candidateClass != currentClass {
		return candidateClass < currentClass
	}
	comparison := goversion.Compare(candidateVersion, currentVersion)
	if candidateClass == 2 {
		return comparison > 0
	}
	return comparison < 0
}

func productionGoToolchainPreferenceClass(candidate, preferred string) int {
	comparison := goversion.Compare(candidate, preferred)
	if comparison == 0 {
		return 0
	}
	if comparison > 0 {
		return 1
	}
	return 2
}

// probeProductionGoToolchain 以真实 Go 命令探测并校验完整生产工具链。
func probeProductionGoToolchain(path string, deps productionGoResolverDeps) (productionGoToolchain, error) {
	executable, version, err := probeProductionGoExecutable(path, deps)
	if err != nil {
		return productionGoToolchain{}, err
	}
	environment, err := probeProductionGoEnvironment(executable, deps)
	if err != nil {
		return productionGoToolchain{}, err
	}
	directories, err := resolveProductionGoDirectories(environment, deps)
	if err != nil {
		return productionGoToolchain{}, err
	}
	if err := validateProductionGoPlatform(environment); err != nil {
		return productionGoToolchain{}, err
	}
	toolchain := productionGoToolchain{
		Executable: executable, Version: version,
		GoRoot: directories[0], GoPath: directories[1], GoCache: directories[2],
		GoModCache: directories[3], GoToolDir: directories[4],
		GOOS: environment[5], GOARCH: environment[6],
	}
	if err := validateProductionGoToolDir(toolchain); err != nil {
		return productionGoToolchain{}, err
	}
	return toolchain, nil
}

// probeProductionGoExecutable 校验 Go 可执行文件并读取其版本标识。
func probeProductionGoExecutable(path string, deps productionGoResolverDeps) (string, string, error) {
	executable, err := canonicalProductionToolPath("Go executable", path)
	if err != nil {
		return "", "", err
	}
	output, err := deps.run(executable, "version")
	if err != nil {
		return "", "", fmt.Errorf("run Go version probe: %w", err)
	}
	version, err := strictProductionGoProbeLine(output)
	if err != nil {
		return "", "", fmt.Errorf("parse Go version probe: %w", err)
	}
	return executable, version, nil
}

// probeProductionGoEnvironment 读取 Go 命令报告的目录和平台环境。
func probeProductionGoEnvironment(executable string, deps productionGoResolverDeps) ([]string, error) {
	output, err := deps.run(executable, "env", "GOROOT", "GOPATH", "GOCACHE", "GOMODCACHE", "GOTOOLDIR", "GOOS", "GOARCH")
	if err != nil {
		return nil, fmt.Errorf("run Go environment probe: %w", err)
	}
	return strictProductionGoProbeLines(output, 7)
}

// resolveProductionGoDirectories 将显式目录配置优先应用到探测结果。
func resolveProductionGoDirectories(environment []string, deps productionGoResolverDeps) ([5]string, error) {
	specifications := [...]struct {
		name, override, discovered string
		create                     bool
	}{
		{"GOROOT", "SUPER_DOLPHIN_GATE_GOROOT", environment[0], false},
		{"GOPATH", "SUPER_DOLPHIN_GATE_GOPATH", environment[1], true},
		{"GOCACHE", "SUPER_DOLPHIN_GATE_GOCACHE", environment[2], true},
		{"GOMODCACHE", "SUPER_DOLPHIN_GATE_GOMODCACHE", environment[3], true},
		{"GOTOOLDIR", "", environment[4], false},
	}
	var directories [5]string
	for index, specification := range specifications {
		directory, err := resolveProductionGoDirectoryOverride(specification.name, specification.override, specification.discovered, specification.create, deps)
		if err != nil {
			return [5]string{}, err
		}
		directories[index] = directory
	}
	return directories, nil
}

// resolveProductionGoDirectoryOverride 解析一个可选显式覆盖的安全 Go 目录。
func resolveProductionGoDirectoryOverride(name, override, discovered string, create bool, deps productionGoResolverDeps) (string, error) {
	if configured := deps.getenv(override); configured != "" {
		discovered = configured
	}
	return canonicalProductionGoDirectory(name, discovered, create)
}

// validateProductionGoPlatform 拒绝与当前主机不匹配的 Go 平台。
func validateProductionGoPlatform(environment []string) error {
	if environment[5] == runtime.GOOS && environment[6] == runtime.GOARCH {
		return nil
	}
	return fmt.Errorf("Go platform %s/%s does not match local platform %s/%s", environment[5], environment[6], runtime.GOOS, runtime.GOARCH)
}

// validateProductionGoToolDir 要求编译器目录来自同一 Go 发行版和本机平台。
func validateProductionGoToolDir(toolchain productionGoToolchain) error {
	expected := filepath.Join(toolchain.GoRoot, "pkg", "tool", toolchain.GOOS+"_"+toolchain.GOARCH)
	if toolchain.GoToolDir == expected {
		return nil
	}
	return fmt.Errorf("GOTOOLDIR %q does not match GOROOT platform tool directory %q", toolchain.GoToolDir, expected)
}

// strictProductionGoProbeLine 校验只含一个字段的 Go 探测输出。
func strictProductionGoProbeLine(data []byte) (string, error) {
	lines, err := strictProductionGoProbeLines(data, 1)
	if err != nil {
		return "", err
	}
	return lines[0], nil
}

// strictProductionGoProbeLines 校验固定字段数的规范 Go 探测输出。
func strictProductionGoProbeLines(data []byte, count int) ([]string, error) {
	if len(data) == 0 || bytes.IndexByte(data, 0) >= 0 || bytes.IndexByte(data, '\r') >= 0 {
		return nil, errors.New("Go probe output is empty or non-canonical")
	}
	text := strings.TrimSuffix(string(data), "\n")
	if strings.HasSuffix(text, "\n") {
		return nil, errors.New("Go probe output has extra lines")
	}
	lines := strings.Split(text, "\n")
	if len(lines) != count {
		return nil, fmt.Errorf("Go probe returned %d lines, want %d", len(lines), count)
	}
	if slices.Contains(lines, "") {
		return nil, errors.New("Go probe output contains an empty field")
	}
	return lines, nil
}

// canonicalProductionGoDirectory 校验或初始化没有软链接的安全 Go 目录。
func canonicalProductionGoDirectory(name, path string, create bool) (string, error) {
	if err := requireCanonicalProductionPath(name, path); err != nil {
		return "", err
	}
	if create {
		if err := createProductionGoDirectory(path); err != nil {
			return "", fmt.Errorf("initialize %s: %w", name, err)
		}
	}
	return resolveProductionGoDirectory(name, path)
}

// requireCanonicalProductionPath 拒绝相对路径和非规范化路径。
func requireCanonicalProductionPath(name, path string) error {
	if filepath.IsAbs(path) && filepath.Clean(path) == path {
		return nil
	}
	return fmt.Errorf("%s must be canonical and absolute", name)
}

// resolveProductionGoDirectory 在解析宿主合法别名后验证真实目录和祖先权限。
func resolveProductionGoDirectory(name, path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", name, err)
	}
	if !filepath.IsAbs(resolved) || filepath.Clean(resolved) != resolved {
		return "", fmt.Errorf("%s must resolve canonically", name)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() || !productionToolOwnedByCurrentOrRoot(info) || info.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("%s is not owner-safe directory", name)
	}
	if err := validateProductionPathAncestors(name, filepath.Dir(resolved)); err != nil {
		return "", err
	}
	return resolved, nil
}

// createProductionGoDirectory 以 0700 权限初始化缺失的 Go 缓存目录。
func createProductionGoDirectory(path string) error {
	_, needsCreate, err := nearestProductionGoDirectory(path)
	if err != nil {
		return err
	}
	if !needsCreate {
		return nil
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

// nearestProductionGoDirectory 定位不穿越软链接的最近真实缓存祖先。
func nearestProductionGoDirectory(path string) (string, bool, error) {
	_, initialErr := os.Lstat(path)
	needsCreate := errors.Is(initialErr, os.ErrNotExist)
	existing := path
	for {
		info, err := os.Lstat(existing)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", false, errors.New("nearest existing cache ancestor is not a real directory")
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", false, errors.New("no existing cache ancestor")
		}
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil || resolved != existing {
		return "", false, errors.Join(errors.New("cache ancestor must not traverse symlinks"), err)
	}
	if err := validateProductionPathAncestors("cache", existing); err != nil {
		return "", false, err
	}
	return existing, needsCreate, nil
}

// productionLocalToolchainDigest 生成绑定本机工具链身份的摘要。
func productionLocalToolchainDigest(lockDigest string, toolchain productionGoToolchain) (string, error) {
	if err := validateProductionCLIIdentity(lockDigest, lockDigest); err != nil {
		return "", err
	}
	binary, err := productionBinaryDigest(toolchain.Executable)
	if err != nil {
		return "", err
	}
	toolManifest, err := productionGoToolManifestDigest(toolchain.GoToolDir)
	if err != nil {
		return "", err
	}
	distribution, err := productionGoDistributionDigest(toolchain.GoRoot)
	if err != nil {
		return "", err
	}
	fields := []string{lockDigest, binary, toolManifest, distribution, toolchain.Version, toolchain.GOOS, toolchain.GOARCH, toolchain.GoRoot}
	for _, field := range fields {
		if field == "" || strings.ContainsAny(field, "\r\n\x00") {
			return "", errors.New("local toolchain identity is invalid")
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(fields, "\n") + "\n"))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// canonicalProductionToolPath 校验可执行工具文件及其安全祖先目录。
func canonicalProductionToolPath(name, path string) (string, error) {
	if err := requireCanonicalProductionPath(name+" path", path); err != nil {
		return "", err
	}
	resolved, err := resolveCanonicalProductionToolPath(name, path)
	if err != nil {
		return "", err
	}
	return validateProductionToolFile(name, resolved)
}

// resolveCanonicalProductionToolPath 解析工具路径并拒绝非规范解析结果。
func resolveCanonicalProductionToolPath(name, path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !filepath.IsAbs(resolved) || filepath.Clean(resolved) != resolved {
		return "", fmt.Errorf("%s path must resolve canonically", name)
	}
	return resolved, nil
}

// validateProductionToolFile 校验工具文件的归属、权限和可执行位。
func validateProductionToolFile(name, path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !productionToolOwnedByCurrentOrRoot(info) || info.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("%s path is not owner-safe", name)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%s path is not executable", name)
	}
	if err := validateProductionPathAncestors(name, filepath.Dir(path)); err != nil {
		return "", err
	}
	return path, nil
}

// validateProductionPathAncestors 校验路径每级祖先均可安全信任。
func validateProductionPathAncestors(name, start string) error {
	for directory := start; ; directory = filepath.Dir(directory) {
		info, err := os.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !productionToolOwnedByCurrentOrRoot(info) {
			return errors.Join(fmt.Errorf("%s parent is not owner-safe", name), err)
		}
		if info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
			return fmt.Errorf("%s parent is group- or other-writable without sticky protection", name)
		}
		if directory == filepath.Dir(directory) {
			return nil
		}
	}
}

// productionToolOwnedByCurrentOrRoot 判断文件是否由当前用户或 root 所有。
func productionToolOwnedByCurrentOrRoot(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && (stat.Uid == uint32(os.Getuid()) || stat.Uid == 0)
}

// controlledProductionGoEnvironment 构造隔离且确定的 Go 执行环境。
func controlledProductionGoEnvironment(toolchain productionGoToolchain) []string {
	return []string{
		"HOME=", "PATH=" + filepath.Dir(toolchain.Executable), "GOROOT=" + toolchain.GoRoot,
		"GOPATH=" + toolchain.GoPath, "GOCACHE=" + toolchain.GoCache, "GOMODCACHE=" + toolchain.GoModCache,
		"GOOS=" + toolchain.GOOS, "GOARCH=" + toolchain.GOARCH,
		"LC_ALL=C", "GIT_TERMINAL_PROMPT=0", "GOENV=off", "GOWORK=off", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0",
	}
}

// controlledProductionGitEnvironment 构造隔离且确定的 Git 执行环境。
func controlledProductionGitEnvironment(gitExecutable string) []string {
	return []string{
		"HOME=", "PATH=" + controlledProductionGitSearchPath(gitExecutable), "LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=",
	}
}

func controlledProductionGitSearchPath(gitExecutable string) string {
	binDirectory := filepath.Dir(gitExecutable)
	runtimeRoot := filepath.Dir(binDirectory)
	if filepath.Base(binDirectory) != "bin" || filepath.Base(runtimeRoot) != "runtime" {
		return binDirectory
	}
	return strings.Join([]string{
		binDirectory,
		filepath.Join(runtimeRoot, "rootfs", "usr", "bin"),
		filepath.Join(runtimeRoot, "rootfs", "bin"),
	}, string(os.PathListSeparator))
}

// runProductionSelfUpdateProgram 在受控目录和环境中执行自更新命令。
func runProductionSelfUpdateProgram(ctx context.Context, program string, args []string, directory string, environment []string) ([]byte, error) {
	command := exec.CommandContext(ctx, program, args...)
	command.Dir, command.Env = directory, environment
	gateprivate.ConfigureCommandCancellation(command, 500*time.Millisecond)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", filepath.Base(program), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
