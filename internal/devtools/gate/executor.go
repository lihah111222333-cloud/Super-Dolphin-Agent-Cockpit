package gate

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

const (
	ExecutorRoot                    = "/opt/super-dolphin-gate"
	ExecutorGateBinaryPath          = ExecutorRoot + "/bin/super-dolphin-gate"
	ExecutorSourcePath              = "/workspace/source"
	ExecutorWorkRoot                = "/workspace/work"
	ExecutorGoWorkloadSourcePath    = ExecutorWorkRoot + "/lanes/lane-0/run/source"
	ExecutorRuntimeSeedRoot         = ExecutorRoot + "/runtime"
	ExecutorRuntimeSeedManifestPath = ExecutorRuntimeSeedRoot + "/manifest.json"
	ExecutorPortableGoRoot          = ExecutorRuntimeSeedRoot + "/go"
	ExecutorPortableRootFS          = ExecutorRuntimeSeedRoot + "/rootfs"
	ExecutorPortableLibraryPath     = ExecutorPortableRootFS + "/usr/lib/x86_64-linux-gnu:" + ExecutorPortableRootFS + "/lib/x86_64-linux-gnu:" + ExecutorPortableRootFS + "/usr/lib/aarch64-linux-gnu:" + ExecutorPortableRootFS + "/lib/aarch64-linux-gnu:" + ExecutorPortableRootFS + "/usr/lib:" + ExecutorPortableRootFS + "/lib"
	ExecutorPortableSearchPath      = ExecutorRoot + "/bin:" + ExecutorRuntimeSeedRoot + "/bin:" + ExecutorRuntimeSeedRoot + "/go/bin:" + ExecutorRuntimeSeedRoot + "/node/bin:" + ExecutorPortableRootFS + "/usr/bin:" + ExecutorPortableRootFS + "/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
	ExecutorGoBuildCacheSeedRoot    = ExecutorRoot + "/cache-seed/go-build"
	ExecutorGoBuildCacheSeedsRoot   = ExecutorRoot + "/cache-seeds"
	ExecutorFrontendEmbedSeedRoot   = ExecutorRoot + "/frontend-embed"
	ExecutorActionlintBinaryPath    = ExecutorRuntimeSeedRoot + "/bin/actionlint"
	ExecutorBashBinaryPath          = ExecutorPortableRootFS + "/usr/bin/bash"
	ExecutorGitBinaryPath           = ExecutorRuntimeSeedRoot + "/bin/git"
	ExecutorNodeBinaryPath          = ExecutorRuntimeSeedRoot + "/node/bin/node"
	ExecutorSQLCBinaryPath          = ExecutorRuntimeSeedRoot + "/bin/sqlc"
	ExecutorSqruffBinaryPath        = ExecutorRuntimeSeedRoot + "/bin/sqruff"
	ExecutorXvfbRunBinaryPath       = ExecutorRuntimeSeedRoot + "/bin/xvfb-run"
	executorPlaywrightBrowsersPath  = ExecutorRuntimeSeedRoot + "/frontend/node_modules/.cache/ms-playwright"
	executorGoProxyMode             = "off"
	executorUID                     = 65532
	executorSearchPath              = ExecutorPortableSearchPath
)

type executorConfig struct {
	sourcePath            string
	workRoot              string
	searchPath            string
	expectedUID           int
	requireReadOnlySource bool
	runtimeSeedRoot       string
	runtimeSeedManifest   string
	goRoot                string
	preparedRuntimeSeeds  *executorPreparedRuntimeSeeds
	goBuildCacheSeedRoots []string
	// goBuildCacheSeedRoot keeps plan-executor callers built before the seed-chain materializer compatible.
	goBuildCacheSeedRoot    string
	goBuildCacheRoot        string
	goBuildCacheProxy       string
	goBuildCacheMetricsPath string
	frontendEmbedSeedRoot   string
	stdout                  io.Writer
	stderr                  io.Writer
}

type executorWorkloadTimeoutKey struct{}

// WithExecutorWorkloadTimeout 记录 worker 的实际 workload 时限，但不在准备缓存时提前计时。
func WithExecutorWorkloadTimeout(parent context.Context, timeout time.Duration) (context.Context, error) {
	if parent == nil {
		return nil, errors.New("executor workload parent context is required")
	}
	if err := ValidateExecutorWorkloadTimeout(timeout); err != nil {
		return nil, err
	}
	return context.WithValue(parent, executorWorkloadTimeoutKey{}, timeout), nil
}

// executorWorkloadContext 在不可变 seed 和私有缓存准备完成后启动 workload 时限。
func executorWorkloadContext(parent context.Context) (context.Context, context.CancelFunc) {
	timeout, configured := parent.Value(executorWorkloadTimeoutKey{}).(time.Duration)
	if !configured {
		return parent, func() {}
	}
	return gateprivate.WithTimeout(parent, timeout)
}

type executorLayout struct {
	workRoot    string
	runRoot     string
	sourceCopy  string
	home        string
	tmp         string
	goCache     string
	goModCache  string
	npmCache    string
	xdgCache    string
	ownsGoCache bool
}

type resolvedStep struct {
	directory   string
	argv        []string
	binary      string
	environment []string
}

// ExecuteExecutor 严格解析一个规范门禁并在隔离的可写快照中执行。
func ExecuteExecutor(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	if ctx == nil || stdout == nil || stderr == nil {
		return errors.New("executor context and output streams are required")
	}
	if handled, err := executeExecutorSubcommand(ctx, args, stdout, stderr); handled {
		return err
	}
	id, program, err := ParseExecutorCommand(args)
	if err != nil {
		return err
	}
	cacheProxy, err := executorGoBuildCacheProxyLauncher()
	if err != nil {
		return err
	}
	seedRoots, err := discoverExecutorGoBuildCacheSeedRoots(ExecutorGoBuildCacheSeedsRoot, ExecutorGoBuildCacheSeedRoot)
	if err != nil {
		return err
	}
	config := executorConfig{
		sourcePath: ExecutorSourcePath, workRoot: ExecutorWorkRoot, searchPath: executorSearchPath,
		expectedUID: executorUID, requireReadOnlySource: true,
		runtimeSeedRoot: ExecutorRuntimeSeedRoot, runtimeSeedManifest: ExecutorRuntimeSeedManifestPath,
		goRoot:                ExecutorPortableGoRoot,
		goBuildCacheSeedRoots: seedRoots,
		goBuildCacheProxy:     cacheProxy,
		frontendEmbedSeedRoot: ExecutorFrontendEmbedSeedRoot,
		stdout:                stdout, stderr: stderr,
	}
	return executeProgram(ctx, config, id, program)
}

// executeExecutorSubcommand 分派不进入隔离工作区的受限执行器子命令。
func executeExecutorSubcommand(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "runtime-seed":
		return true, executeRuntimeSeedCommand(args[1:])
	case "go-module-overlay":
		return true, executeGoModuleOverlayCommand(args[1:])
	default:
		if isPlanExecutorCommand(args[0]) {
			return true, ExecutePlanExecutor(ctx, args, stdout, stderr)
		}
		return false, nil
	}
}

// ExecutorExitCode 将子进程失败映射为容器进程退出码。
func ExecutorExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		return 1
	}
	if code := exitError.ExitCode(); code >= 0 {
		return code
	}
	return 1
}

// executeProgram 建立一次性工作区，校验可信输入并执行固定门禁程序。
func executeProgram(ctx context.Context, config executorConfig, id GateID, program ExecutorProgram) (retErr error) {
	if err := validateExecutorConfig(config); err != nil {
		return err
	}
	if err := validateExecutorProgram(program); err != nil {
		return fmt.Errorf("gate %q executor program: %w", id, err)
	}
	if program.Strategy == ExecutorStrategyReleaseAttestation {
		return errors.New("release attestation requires canonical prerequisites from the plan executor")
	}
	layout, err := prepareExecutorWorkspace(config)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, cleanupExecutorWorkspace(layout))
	}()
	environment, steps, gitBinary, err := prepareExecutorRun(ctx, config, layout, program)
	if err != nil {
		return err
	}
	fmt.Fprintf(config.stderr, "[gate-executor] gate=%s cwd=%s env=%s\n", id, layout.sourceCopy, strings.Join(environmentKeys(environment), ","))
	return runExecutorProgram(ctx, config, id, program, layout, environment, steps, gitBinary)
}

// prepareExecutorRun 安装锁定依赖并完成命令、Git 快照和必需路径校验。
func prepareExecutorRun(
	ctx context.Context,
	config executorConfig,
	layout executorLayout,
	program ExecutorProgram,
) ([]string, []resolvedStep, string, error) {
	if err := installExecutorSeeds(config, layout, program); err != nil {
		return nil, nil, "", err
	}
	environment, err := prepareExecutorEnvironment(config, layout, program)
	if err != nil {
		return nil, nil, "", err
	}
	steps, err := resolveExecutorSteps(config.searchPath, layout.sourceCopy, program)
	if err != nil {
		return nil, nil, "", err
	}
	gitBinary, err := resolveExecutable("git", config.searchPath)
	if err != nil {
		return nil, nil, "", err
	}
	if err := validateCopiedSnapshot(ctx, gitBinary, layout.sourceCopy, environment); err != nil {
		return nil, nil, "", err
	}
	if err := validateProgramInputs(layout.sourceCopy, program); err != nil {
		return nil, nil, "", err
	}
	if err := validateRequiredExecutables(program.RequiredExecutables); err != nil {
		return nil, nil, "", err
	}
	return environment, steps, gitBinary, nil
}

// prepareExecutorEnvironment 只为声明需要的运行时 seed 构造隔离执行环境。
func prepareExecutorEnvironment(config executorConfig, layout executorLayout, program ExecutorProgram) ([]string, error) {
	frontendSeedRoot := ""
	if program.NeedsFrontendSeed {
		frontendSeedRoot = filepath.Join(config.runtimeSeedRoot, "frontend", "node_modules")
	}
	cacheProgram, err := executorGoBuildCacheProgram(config, layout, program.NeedsGoSeed)
	if err != nil {
		return nil, err
	}
	return executorEnvironment(layout, config.searchPath, layout.goModCache, config.goRoot, frontendSeedRoot, cacheProgram), nil
}

// executorGoBuildCacheProgram 创建可选的 Go 缓存代理命令。
func executorGoBuildCacheProgram(config executorConfig, layout executorLayout, required bool) (string, error) {
	if !required {
		return "", nil
	}
	seedRoots, err := executorGoBuildCacheSeedRoots(config)
	if err != nil {
		return "", err
	}
	command, err := executorGoBuildCacheProxyCommand(config.goBuildCacheProxy, seedRoots, layout.goCache)
	if err != nil || config.goBuildCacheMetricsPath == "" {
		return command, err
	}
	return command + " --metrics " + strconv.Quote(config.goBuildCacheMetricsPath), nil
}

// runExecutorProgram 分派内建策略或逐步运行无需 shell 的固定命令。
func runExecutorProgram(
	ctx context.Context,
	config executorConfig,
	id GateID,
	program ExecutorProgram,
	layout executorLayout,
	environment []string,
	steps []resolvedStep,
	gitBinary string,
) error {
	switch program.Strategy {
	case ExecutorStrategyFullTreeWhitespace:
		return runFullTreeWhitespace(ctx, gitBinary, layout.sourceCopy, environment, config.stdout, config.stderr)
	case ExecutorStrategyChangedDiagnostics:
		return runChangedDiagnostics(ctx, gitBinary, layout.sourceCopy, environment, config.searchPath, config.stdout, config.stderr)
	case ExecutorStrategySQLCVerify:
		return runSQLCVerify(ctx, gitBinary, ExecutorSQLCBinaryPath, ExecutorBashBinaryPath, layout.sourceCopy, environment, config.stdout, config.stderr)
	}
	for _, step := range steps {
		if err := runResolvedStep(ctx, step, environment, config.stdout, config.stderr); err != nil {
			return fmt.Errorf("gate %q command %q: %w", id, step.argv, err)
		}
	}
	return nil
}

// validateExecutorProgram 拒绝空步骤、未知策略以及非规范输入路径。
func validateExecutorProgram(program ExecutorProgram) error {
	if err := validateExecutorStrategy(program); err != nil {
		return err
	}
	if err := validateExecutorSteps(program.Steps); err != nil {
		return err
	}
	if err := validateRequiredPaths(program.RequiredPaths); err != nil {
		return err
	}
	return validateRequiredExecutablePaths(program.RequiredExecutables)
}

func validateExecutorStrategy(program ExecutorProgram) error {
	switch program.Strategy {
	case ExecutorStrategyCommands:
		if len(program.Steps) == 0 {
			return errors.New("command strategy has no steps")
		}
	case ExecutorStrategyChangedDiagnostics, ExecutorStrategyFullTreeWhitespace, ExecutorStrategySQLCVerify,
		ExecutorStrategyReleaseAttestation:
		if len(program.Steps) != 0 {
			return errors.New("built-in strategy must not declare command steps")
		}
	default:
		return fmt.Errorf("unsupported executor strategy %q", program.Strategy)
	}
	return nil
}

func validateExecutorSteps(steps []ExecutorStep) error {
	for index, step := range steps {
		if len(step.Argv) == 0 || step.Argv[0] == "" {
			return fmt.Errorf("step %d has no command", index)
		}
		if err := validateExecutorStepEnvironment(step.Environment); err != nil {
			return fmt.Errorf("step %d environment: %w", index, err)
		}
	}
	return nil
}

// validateExecutorStepEnvironment 只接受已登记的完整 Go 资源 profile。
func validateExecutorStepEnvironment(environment []string) error {
	if len(environment) == 0 || isAllowedExecutorStepEnvironment(environment) {
		return nil
	}
	return errors.New("environment profile is not an allowed resource bound")
}

func validateRequiredPaths(paths []string) error {
	for _, relative := range paths {
		if !canonicalRelativePath(relative) {
			return fmt.Errorf("required path %q is not canonical", relative)
		}
	}
	return nil
}

func validateRequiredExecutablePaths(paths []string) error {
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("required executable %q is not canonical", path)
		}
	}
	return nil
}

func canonicalRelativePath(path string) bool {
	return !filepath.IsAbs(path) && filepath.Clean(path) == path && path != "." && path != ".." &&
		!strings.HasPrefix(path, ".."+string(filepath.Separator))
}

// validateExecutorConfig 固定运行身份、路径配置和输出流均须完整有效。
func validateExecutorConfig(config executorConfig) error {
	if !hasExecutorPaths(config) {
		return errors.New("executor paths are not configured")
	}
	if err := validateExecutorUID(config.expectedUID); err != nil {
		return err
	}
	if config.stdout == nil || config.stderr == nil {
		return errors.New("executor output streams are required")
	}
	return nil
}

// hasExecutorPaths 确认执行器所需的只读根路径全部显式配置。
func hasExecutorPaths(config executorConfig) bool {
	return config.sourcePath != "" && config.workRoot != "" && config.searchPath != "" && config.runtimeSeedRoot != "" &&
		config.runtimeSeedManifest != "" && config.goRoot != ""
}

// validateExecutorUID 拒绝未固定或与当前进程不一致的执行身份。
func validateExecutorUID(expectedUID int) error {
	if expectedUID < 0 || os.Geteuid() != expectedUID {
		return fmt.Errorf("executor uid = %d, want %d", os.Geteuid(), expectedUID)
	}
	return nil
}

func resolveExecutorSteps(searchPath string, sourceCopy string, program ExecutorProgram) ([]resolvedStep, error) {
	steps := make([]resolvedStep, len(program.Steps))
	for index, step := range program.Steps {
		if len(step.Argv) == 0 {
			return nil, fmt.Errorf("executor step %d has no argv", index)
		}
		directory, err := executorStepDirectory(sourceCopy, step.Directory)
		if err != nil {
			return nil, fmt.Errorf("executor step %d: %w", index, err)
		}
		binary, err := resolveStepExecutable(sourceCopy, step.Argv[0], searchPath)
		if err != nil {
			return nil, fmt.Errorf("executor step %d: %w", index, err)
		}
		steps[index] = resolvedStep{
			directory: directory, argv: append([]string(nil), step.Argv...), binary: binary,
			environment: append([]string(nil), step.Environment...),
		}
	}
	return steps, nil
}

// resolveStepExecutable 仅解析固定 PATH 工具或快照内的规范相对可执行文件。
func resolveStepExecutable(sourceCopy string, name string, searchPath string) (string, error) {
	if filepath.Base(name) == name {
		return resolveExecutable(name, searchPath)
	}
	if !canonicalRepositoryExecutable(name) {
		return "", fmt.Errorf("command %q is not a canonical repository executable", name)
	}
	candidate := filepath.Join(sourceCopy, filepath.Clean(name))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil || !pathContains(sourceCopy, resolved) {
		return "", fmt.Errorf("required repository command %q is missing or escapes the snapshot", name)
	}
	if !regularExecutable(resolved) {
		return "", fmt.Errorf("required repository command %q is not executable", name)
	}
	return resolved, nil
}

func canonicalRepositoryExecutable(name string) bool {
	return !filepath.IsAbs(name) && strings.HasPrefix(name, "./") && filepath.Clean(name) == strings.TrimPrefix(name, "./")
}

func regularExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// validateMountInfo 要求 source 自身是 mountinfo 中明确标记为只读的挂载点。
func validateMountInfo(reader io.Reader, path string) error {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 || decodeMountPath(fields[4]) != path {
			continue
		}
		if slices.Contains(strings.Split(fields[5], ","), "ro") {
			return nil
		}
		return errors.New("source mount is not read-only")
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("source path is not an explicit mount point")
}

// decodeMountPath 还原 mountinfo 中使用八进制转义的路径。
func decodeMountPath(path string) string {
	replacer := strings.NewReplacer("\\040", " ", "\\011", "\t", "\\012", "\n", "\\134", "\\")
	return replacer.Replace(path)
}

// executorStepDirectory 将规范相对目录约束在可写快照根内。
func executorStepDirectory(sourceCopy string, relative string) (string, error) {
	if relative == "" {
		return sourceCopy, nil
	}
	if filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("executor step directory is not a canonical relative path")
	}
	directory := filepath.Join(sourceCopy, relative)
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return "", errors.New("executor step directory is missing")
	}
	return directory, nil
}

// resolveExecutable 只在固定搜索路径中解析普通可执行文件。
func resolveExecutable(name string, searchPath string) (string, error) {
	if name == "" || filepath.Base(name) != name {
		return "", fmt.Errorf("command %q is not a fixed executable name", name)
	}
	for _, directory := range filepath.SplitList(searchPath) {
		candidate := filepath.Join(directory, name)
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("required command %q is missing", name)
}

// validateRequiredExecutables 确认固定绝对工具路径存在且可执行。
func validateRequiredExecutables(paths []string) error {
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("required executable %q is not canonical", path)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("required executable %q is missing", path)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("required executable %q is not executable", path)
		}
	}
	return nil
}

func runResolvedStep(ctx context.Context, step resolvedStep, environment []string, stdout io.Writer, stderr io.Writer) error {
	command := exec.CommandContext(ctx, step.binary, step.argv[1:]...)
	configureCommandCancellation(command)
	command.Args[0] = step.argv[0]
	command.Dir = step.directory
	stepEnvironment, err := mergeExecutorStepEnvironment(environment, step.environment)
	if err != nil {
		return err
	}
	command.Env = stepEnvironment
	command.Stdout = stdout
	command.Stderr = stderr
	return runConfiguredCommand(command)
}

// mergeExecutorStepEnvironment 合并已验证资源上限并拒绝覆盖 executor 环境。
func mergeExecutorStepEnvironment(environment []string, stepEnvironment []string) ([]string, error) {
	if err := validateExecutorStepEnvironment(stepEnvironment); err != nil {
		return nil, err
	}
	keys := make(map[string]bool, len(environment)+len(stepEnvironment))
	for _, assignment := range environment {
		name, _, ok := strings.Cut(assignment, "=")
		if !ok || name == "" || keys[name] {
			return nil, errors.New("executor environment is malformed or duplicated")
		}
		keys[name] = true
	}
	merged := append([]string(nil), environment...)
	for _, assignment := range stepEnvironment {
		name, _, _ := strings.Cut(assignment, "=")
		if keys[name] {
			return nil, fmt.Errorf("step environment duplicates executor key %q", name)
		}
		keys[name] = true
		merged = append(merged, assignment)
	}
	return merged, nil
}

func validateProgramInputs(sourceCopy string, program ExecutorProgram) error {
	for _, relative := range program.RequiredPaths {
		path, err := executorStepDirectory(sourceCopy, filepath.Dir(relative))
		if err != nil {
			return fmt.Errorf("required path %q: %w", relative, err)
		}
		if _, err := os.Lstat(filepath.Join(path, filepath.Base(relative))); err != nil {
			return fmt.Errorf("required path %q is missing: %w", relative, err)
		}
	}
	return nil
}

// executorEnvironment 构造不继承宿主秘密的固定执行环境，并按需启用共享构建缓存代理。
func executorEnvironment(
	layout executorLayout,
	searchPath string,
	goModCacheRoot string,
	goRoot string,
	frontendSeedRoot string,
	goCacheProgram string,
) []string {
	npmCacheRoot := layout.npmCache
	if frontendSeedRoot != "" {
		npmCacheRoot = filepath.Join(filepath.Dir(frontendSeedRoot), "npm-cache")
	}
	environment := []string{
		"CI=true", "GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Super Dolphin Gate Executor", "GIT_AUTHOR_EMAIL=gate-executor@super-dolphin.invalid",
		"GIT_AUTHOR_DATE=946684800 +0000", "GIT_COMMITTER_NAME=Super Dolphin Gate Executor",
		"GIT_COMMITTER_EMAIL=gate-executor@super-dolphin.invalid", "GIT_COMMITTER_DATE=946684800 +0000",
		"GOCACHE=" + layout.goCache, "GOENV=off", "GOMODCACHE=" + goModCacheRoot,
		"GOPROXY=" + executorGoProxyMode, "GOSUMDB=off", "GOTOOLCHAIN=local",
		"GOROOT=" + goRoot, "GOTMPDIR=" + layout.tmp,
		"HOME=" + layout.home, "LANG=C.UTF-8", "LC_ALL=C.UTF-8",
		"LD_LIBRARY_PATH=" + ExecutorPortableLibraryPath,
		"FONTCONFIG_SYSROOT=" + ExecutorPortableRootFS,
		"FONTCONFIG_FILE=fonts.conf",
		"FONTCONFIG_PATH=" + ExecutorPortableRootFS + "/etc/fonts",
		"XDG_DATA_DIRS=" + ExecutorPortableRootFS + "/usr/local/share:" + ExecutorPortableRootFS + "/usr/share",
		"GSETTINGS_SCHEMA_DIR=" + ExecutorPortableRootFS + "/usr/share/glib-2.0/schemas",
		"NPM_CONFIG_AUDIT=false", "NPM_CONFIG_FUND=false", "NPM_CONFIG_UPDATE_NOTIFIER=false",
		"npm_config_cache=" + npmCacheRoot, "npm_config_logs_dir=" + filepath.Join(layout.npmCache, "_logs"),
		"npm_config_offline=true", "npm_config_userconfig=/dev/null",
		"PLAYWRIGHT_BROWSERS_PATH=" + executorPlaywrightBrowsersPath,
		"SUPER_DOLPHIN_GATE_GIT=" + ExecutorGitBinaryPath,
		"SUPER_DOLPHIN_GATE_NODE=" + ExecutorNodeBinaryPath,
		"SUPER_DOLPHIN_GATE_XVFB_RUN=" + ExecutorXvfbRunBinaryPath,
		"SUPER_DOLPHIN_TEST_BACKEND=remote-worker",
		"PATH=" + searchPath, "TMPDIR=" + layout.tmp, "TZ=UTC", "XDG_CACHE_HOME=" + layout.xdgCache,
	}
	if frontendSeedRoot != "" {
		environment = append(environment, "SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED="+frontendSeedRoot)
	}
	if goCacheProgram != "" {
		environment = append(environment, "GOCACHEPROG="+goCacheProgram)
	}
	return environment
}

// executorGoBuildCacheProxyLauncher 返回同一 worker 二进制的隐藏缓存代理入口。
func executorGoBuildCacheProxyLauncher() (string, error) {
	binary, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executor binary for Go build cache proxy: %w", err)
	}
	if !filepath.IsAbs(binary) {
		return "", errors.New("executor binary for Go build cache proxy must be absolute")
	}
	return strconv.Quote(binary) + " worker go-cache-proxy", nil
}

// executorGoBuildCacheProxyCommand 构造只含受信任缓存根的 GOCACHEPROG 启动命令。
func executorGoBuildCacheProxyCommand(launcher string, seedRoots []string, privateRoot string) (string, error) {
	if strings.TrimSpace(launcher) == "" || len(seedRoots) == 0 || len(seedRoots) > goBuildCacheProxyMaxSeedRoots || !filepath.IsAbs(privateRoot) {
		return "", errors.New("Go build cache proxy launcher and absolute cache roots are required")
	}
	var command strings.Builder
	command.WriteString(launcher)
	for _, seedRoot := range seedRoots {
		if !filepath.IsAbs(seedRoot) {
			return "", errors.New("Go build cache proxy launcher and absolute cache roots are required")
		}
		command.WriteString(" --seed ")
		command.WriteString(strconv.Quote(seedRoot))
	}
	command.WriteString(" --private ")
	command.WriteString(strconv.Quote(privateRoot))
	return command.String(), nil
}

func environmentKeys(environment []string) []string {
	keys := make([]string, 0, len(environment))
	for _, item := range environment {
		key, _, _ := strings.Cut(item, "=")
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
