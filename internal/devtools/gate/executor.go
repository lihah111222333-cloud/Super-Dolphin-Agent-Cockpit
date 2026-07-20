package gate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	ExecutorSourcePath              = "/workspace/source"
	ExecutorWorkRoot                = "/workspace/work"
	ExecutorRuntimeSeedRoot         = "/opt/super-dolphin-gate/runtime"
	ExecutorRuntimeSeedManifestPath = ExecutorRuntimeSeedRoot + "/manifest.json"
	ExecutorSQLCBinaryPath          = ExecutorRuntimeSeedRoot + "/bin/sqlc"
	ExecutorSqruffBinaryPath        = ExecutorRuntimeSeedRoot + "/bin/sqruff"
	executorPlaywrightBrowsersPath  = ExecutorRuntimeSeedRoot + "/frontend/node_modules/.cache/ms-playwright"
	executorGoModuleProxy           = "file://" + ExecutorRuntimeSeedRoot + "/go-proxy"
	executorUID                     = 65532
	executorSearchPath              = "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
)

type executorConfig struct {
	sourcePath            string
	workRoot              string
	searchPath            string
	expectedUID           int
	requireReadOnlySource bool
	runtimeSeedRoot       string
	runtimeSeedManifest   string
	stdout                io.Writer
	stderr                io.Writer
}

type executorLayout struct {
	workRoot   string
	runRoot    string
	sourceCopy string
	home       string
	tmp        string
	goCache    string
	goModCache string
	npmCache   string
	xdgCache   string
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
	if len(args) > 0 && isPlanExecutorCommand(args[0]) {
		return ExecutePlanExecutor(ctx, args, stdout, stderr)
	}
	id, program, err := ParseExecutorCommand(args)
	if err != nil {
		return err
	}
	config := executorConfig{
		sourcePath: ExecutorSourcePath, workRoot: ExecutorWorkRoot, searchPath: executorSearchPath,
		expectedUID: executorUID, requireReadOnlySource: true,
		runtimeSeedRoot: ExecutorRuntimeSeedRoot, runtimeSeedManifest: ExecutorRuntimeSeedManifestPath,
		stdout: stdout, stderr: stderr,
	}
	return executeProgram(ctx, config, id, program)
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
	if err := installRuntimeSeeds(config, layout, program); err != nil {
		return nil, nil, "", err
	}
	environment := executorEnvironment(layout, config.searchPath)
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
		return runSQLCVerify(ctx, gitBinary, ExecutorSQLCBinaryPath, layout.sourceCopy, environment, config.stdout, config.stderr)
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
	if config.sourcePath == "" || config.workRoot == "" || config.searchPath == "" || config.runtimeSeedRoot == "" || config.runtimeSeedManifest == "" {
		return errors.New("executor paths are not configured")
	}
	if config.expectedUID < 0 || os.Geteuid() != config.expectedUID {
		return fmt.Errorf("executor uid = %d, want %d", os.Geteuid(), config.expectedUID)
	}
	if config.stdout == nil || config.stderr == nil {
		return errors.New("executor output streams are required")
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

func executorEnvironment(layout executorLayout, searchPath string) []string {
	return []string{
		"CI=true", "GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0",
		"GOCACHE=" + layout.goCache, "GOENV=off", "GOMODCACHE=" + layout.goModCache,
		"GOPROXY=" + executorGoModuleProxy, "GOSUMDB=off", "GOTOOLCHAIN=local",
		"GOTMPDIR=" + layout.tmp, "HOME=" + layout.home, "LANG=C.UTF-8", "LC_ALL=C.UTF-8",
		"NPM_CONFIG_AUDIT=false", "NPM_CONFIG_FUND=false", "NPM_CONFIG_UPDATE_NOTIFIER=false",
		"npm_config_cache=" + layout.npmCache, "npm_config_offline=true", "npm_config_userconfig=/dev/null",
		"PLAYWRIGHT_BROWSERS_PATH=" + executorPlaywrightBrowsersPath,
		"PATH=" + searchPath, "TMPDIR=" + layout.tmp, "TZ=UTC", "XDG_CACHE_HOME=" + layout.xdgCache,
	}
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
