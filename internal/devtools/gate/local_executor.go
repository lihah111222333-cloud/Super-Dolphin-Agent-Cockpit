package gate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate/testtiming"
)

// LocalExecutorDependencyInputs 是本地主机明确提供的、可重验的只读依赖闭包。
type LocalExecutorDependencyInputs struct {
	GoModuleCache             string
	GoModuleCacheReceipt      string
	FrontendNodeModules       string
	FrontendNPMCache          string
	FrontendViteCache         string
	FrontendDependencyReceipt string
	FrontendEmbedRoot         string
	FrontendEmbedReceipt      string
	CGOEnabled                string
}

// ExecuteLocalGateWorkload 在调用方提供的精确 Git 根上执行一个 expanded workload；
// 该路径不接收 ECI token、不创建云端客户端，也不投影远程 authority。
func ExecuteLocalGateWorkload(ctx context.Context, nowFunc func() time.Time, sourceRoot string, id GateID) (result PlanGateExecution, retErr error) {
	return ExecuteLocalGateWorkloadWithDependencies(ctx, nowFunc, sourceRoot, id, LocalExecutorDependencyInputs{})
}

// ExecuteLocalGateWorkloadWithDependencies 在 network-off 约束下运行 canonical workload。
func ExecuteLocalGateWorkloadWithDependencies(ctx context.Context, nowFunc func() time.Time, sourceRoot string, id GateID, dependencies LocalExecutorDependencyInputs) (result PlanGateExecution, retErr error) {
	if ctx == nil {
		return PlanGateExecution{}, errors.New("local gate executor context is required")
	}
	if nowFunc == nil {
		return PlanGateExecution{}, errors.New("local gate executor clock is required")
	}
	_, program, err := executorProgramForWorkload(id)
	if err != nil {
		return PlanGateExecution{}, err
	}
	if err := validateLocalExecutorProgramSupport(program); err != nil {
		return PlanGateExecution{}, err
	}
	canonicalSourceRoot, err := canonicalLocalExecutorSourceRoot(sourceRoot)
	if err != nil {
		return PlanGateExecution{}, err
	}
	layout, cleanup, err := newLocalExecutorLayout()
	if err != nil {
		return PlanGateExecution{}, err
	}
	defer func() { retErr = errors.Join(retErr, cleanup()) }()
	if err := makeLocalExecutorDirectories(layout); err != nil {
		return PlanGateExecution{}, err
	}
	steps, environment, sandboxPath, sandboxProfile, dependencyCleanup, err := prepareLocalExecutorExecution(canonicalSourceRoot, layout, program, dependencies)
	if err != nil {
		return PlanGateExecution{}, err
	}
	defer func() { retErr = errors.Join(retErr, dependencyCleanup()) }()
	observation := runLocalExecutorSteps(ctx, nowFunc, id, steps, environment, sandboxPath, sandboxProfile)
	return finishLocalGateExecution(id, program, observation, ctx.Err())
}

// prepareLocalExecutorExecution 一次准备 sandbox、依赖 overlay、toolchain 和 canonical steps。
func prepareLocalExecutorExecution(sourceRoot string, layout executorLayout, program ExecutorProgram, dependencies LocalExecutorDependencyInputs) ([]resolvedStep, []string, string, string, func() error, error) {
	if err := validateLocalExecutorProgramSupport(program); err != nil {
		return nil, nil, "", "", nil, err
	}
	sandboxPath, err := localNetworkSandboxPath()
	if err != nil {
		return nil, nil, "", "", nil, err
	}
	dependencyCleanup, err := installLocalExecutorDependencies(sourceRoot, layout, program, dependencies)
	if err != nil {
		return nil, nil, "", "", nil, err
	}
	return prepareLocalExecutorPostDependencies(sourceRoot, layout, program, dependencies, sandboxPath, dependencyCleanup, defaultLocalExecutorPreparationHooks())
}

// joinLocalExecutorPreparationError 保留初始化失败和依赖覆盖层清理失败的双重证据。
func joinLocalExecutorPreparationError(cause error, cleanup func() error) error {
	if cleanup == nil {
		return cause
	}
	return errors.Join(cause, cleanup())
}

type localExecutorPreparationHooks struct {
	toolchain   func() (string, string, error)
	steps       func(string, string, ExecutorProgram) ([]resolvedStep, error)
	environment func(executorLayout, string, string, string) ([]string, error)
	profile     func(string, executorLayout, LocalExecutorDependencyInputs, string, []string) (string, error)
}

func defaultLocalExecutorPreparationHooks() localExecutorPreparationHooks {
	return localExecutorPreparationHooks{
		toolchain:   localExecutorGateToolchain,
		steps:       prepareLocalExecutorSteps,
		environment: localExecutorEnvironment,
		profile:     localSandboxProfile,
	}
}

// prepareLocalExecutorPostDependencies 在依赖层完成后解析工具、步骤、环境和 sandbox。
func prepareLocalExecutorPostDependencies(sourceRoot string, layout executorLayout, program ExecutorProgram, dependencies LocalExecutorDependencyInputs, sandboxPath string, dependencyCleanup func() error, hooks localExecutorPreparationHooks) ([]resolvedStep, []string, string, string, func() error, error) {
	if dependencyCleanup == nil {
		return nil, nil, "", "", nil, errors.New("local executor dependency cleanup is required")
	}
	if hooks.toolchain == nil || hooks.steps == nil || hooks.environment == nil || hooks.profile == nil {
		return nil, nil, "", "", nil, errors.New("local executor preparation hooks are incomplete")
	}
	fail := func(cause error) ([]resolvedStep, []string, string, string, func() error, error) {
		return nil, nil, "", "", nil, joinLocalExecutorPreparationError(cause, dependencyCleanup)
	}
	goRoot, goBin, err := hooks.toolchain()
	if err != nil {
		return fail(err)
	}
	searchPath := localExecutorSearchPath(goBin)
	steps, err := hooks.steps(searchPath, sourceRoot, program)
	if err != nil {
		return fail(err)
	}
	environment, err := hooks.environment(layout, searchPath, goRoot, dependencies.CGOEnabled)
	if err != nil {
		return fail(err)
	}
	sandboxProfile, err := hooks.profile(sourceRoot, layout, dependencies, goRoot, sandboxToolPaths(steps, program))
	if err != nil {
		return fail(err)
	}
	return steps, environment, sandboxPath, sandboxProfile, dependencyCleanup, nil
}

// LocalExecutorDependencyClosureDigest 将锁定依赖树纳入 local toolchain closure。
func LocalExecutorDependencyClosureDigest(dependencies LocalExecutorDependencyInputs) (string, error) {
	paths := []struct {
		name string
		path string
	}{
		{name: "go_module_cache", path: dependencies.GoModuleCache},
		{name: "frontend_node_modules", path: dependencies.FrontendNodeModules},
		{name: "frontend_npm_cache", path: dependencies.FrontendNPMCache},
		{name: "frontend_vite_cache", path: dependencies.FrontendViteCache},
		{name: "frontend_embed_root", path: dependencies.FrontendEmbedRoot},
	}
	values := make([]struct {
		Name   string `json:"name"`
		Digest string `json:"digest"`
	}, 0, len(paths))
	for _, candidate := range paths {
		if strings.TrimSpace(candidate.path) == "" {
			continue
		}
		if err := validateLocalDependencyRoot(candidate.path, candidate.name); err != nil {
			return "", err
		}
		digest, err := localDependencyReceiptDigest(candidate.name, dependencies)
		if err != nil {
			return "", err
		}
		values = append(values, struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		}{Name: candidate.name, Digest: digest})
	}
	payload, err := json.Marshal(struct {
		Domain       string `json:"domain"`
		Dependencies any    `json:"dependencies"`
	}{Domain: cicontract.LocalDependencyClosureDomain, Dependencies: values})
	if err != nil {
		return "", fmt.Errorf("encode local dependency closure: %w", err)
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func localDependencyReceiptDigest(name string, dependencies LocalExecutorDependencyInputs) (string, error) {
	var digest string
	switch name {
	case "go_module_cache":
		digest = dependencies.GoModuleCacheReceipt
	case "frontend_node_modules", "frontend_npm_cache", "frontend_vite_cache":
		digest = dependencies.FrontendDependencyReceipt
	case "frontend_embed_root":
		digest = dependencies.FrontendEmbedReceipt
	}
	if !isPrefixedSHA256Digest(digest) {
		return "", fmt.Errorf("local %s dependency receipt digest is required", name)
	}
	return digest, nil
}

// LocalExecutorRunnerSemanticDigest 绑定当前 sandbox、canonical executor 和可达工具二进制闭包。
func LocalExecutorRunnerSemanticDigest(sourceRoot string, id GateID) (string, error) {
	_, program, err := executorProgramForWorkload(id)
	if err != nil {
		return "", err
	}
	canonicalSourceRoot, err := canonicalLocalExecutorSourceRoot(sourceRoot)
	if err != nil {
		return "", err
	}
	sandboxPath, err := localNetworkSandboxPath()
	if err != nil {
		return "", err
	}
	trustedGo, err := ResolveTrustedGoBinary(context.Background())
	if err != nil {
		return "", err
	}
	paths, err := localExecutorRunnerToolPaths(canonicalSourceRoot, sandboxPath, program, trustedGo)
	if err != nil {
		return "", err
	}
	return digestLocalRunnerPaths(paths)
}

// localExecutorRunnerToolPaths 收集实际将进入 sandbox 允许集的已验证工具路径。
func localExecutorRunnerToolPaths(sourceRoot, sandboxPath string, program ExecutorProgram, trustedGo TrustedGoBinary) ([]string, error) {
	_, goBin, err := localExecutorToolchain(trustedGo)
	if err != nil {
		return nil, err
	}
	goBinary, err := trustedGo.VerifiedPath()
	if err != nil {
		return nil, fmt.Errorf("resolve local Go runner binary: %w", err)
	}
	steps, err := prepareLocalExecutorSteps(localExecutorSearchPath(goBin), sourceRoot, program)
	if err != nil {
		return nil, err
	}
	paths := []string{sandboxPath, goBinary}
	for _, step := range steps {
		paths = append(paths, step.binary)
	}
	for _, executable := range program.RequiredExecutables {
		resolved, resolveErr := filepath.EvalSymlinks(executable)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve local required executable %q: %w", executable, resolveErr)
		}
		paths = append(paths, resolved)
	}
	return paths, nil
}

// digestLocalRunnerPaths 对实际工具字节摘要排序后生成不可变 runner closure。
func digestLocalRunnerPaths(paths []string) (string, error) {
	entries := make([]struct {
		Name   string `json:"name"`
		Digest string `json:"digest"`
	}, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", fmt.Errorf("resolve local runner closure path: %w", err)
		}
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		digest, err := fileSHA256(resolved)
		if err != nil {
			return "", fmt.Errorf("digest local runner closure path: %w", err)
		}
		entries = append(entries, struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		}{Name: filepath.Base(resolved), Digest: digest})
	}
	sort.Slice(entries, func(left, right int) bool {
		if entries[left].Name == entries[right].Name {
			return entries[left].Digest < entries[right].Digest
		}
		return entries[left].Name < entries[right].Name
	})
	payload, err := json.Marshal(struct {
		Domain string `json:"domain"`
		Policy string `json:"policy"`
		Tools  any    `json:"tools"`
	}{Domain: cicontract.LocalRunnerSemanticDigestDomain, Policy: cicontract.LocalRunnerSandboxPolicy, Tools: entries})
	if err != nil {
		return "", fmt.Errorf("encode local runner closure: %w", err)
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

type localExecutorObservation struct {
	started     time.Time
	completed   time.Time
	timing      *executorExecutionTiming
	testTimings []GoTestTiming
	log         []byte
	runErr      error
}

func newLocalExecutorLayout() (executorLayout, func() error, error) {
	workRoot, err := os.MkdirTemp("", "super-dolphin-local-executor-")
	if err != nil {
		return executorLayout{}, nil, fmt.Errorf("create local executor workspace: %w", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(workRoot)
	if err != nil {
		return executorLayout{}, nil, errors.Join(fmt.Errorf("canonicalize local executor workspace: %w", err), os.RemoveAll(workRoot))
	}
	layout := newExecutorLayout(canonicalRoot)
	return layout, func() error {
		if err := os.RemoveAll(canonicalRoot); err != nil {
			return fmt.Errorf("cleanup local executor workspace: %w", err)
		}
		return nil
	}, nil
}

// installLocalExecutorDependencies 安装已验证的 Go、前端和 embed 依赖覆盖层，并返回严格清理器。
func installLocalExecutorDependencies(sourceRoot string, layout executorLayout, program ExecutorProgram, dependencies LocalExecutorDependencyInputs) (func() error, error) {
	cleanupTargets := make([]string, 0, 2)
	cleanup := func() error {
		return cleanupLocalExecutorOverlayTargets(cleanupTargets)
	}
	if program.NeedsGoSeed {
		if err := installLocalGoDependencies(layout, dependencies.GoModuleCache); err != nil {
			return nil, errors.Join(err, cleanup())
		}
	}
	if program.NeedsFrontendSeed {
		target := filepath.Join(sourceRoot, "frontend-app", "node_modules")
		if err := localExecutorOverlayTargetAvailable(target); err != nil {
			return nil, err
		}
		cleanupTargets = append(cleanupTargets, target, filepath.Join(layout.tmp, ".vite-temp"))
		if err := installLocalFrontendDependencies(sourceRoot, layout, dependencies); err != nil {
			return nil, errors.Join(fmt.Errorf("install local frontend dependencies: %w", err), cleanup())
		}
	}
	if program.NeedsFrontendEmbedSeed {
		target := filepath.Join(sourceRoot, "cmd", "agent-terminal", "web-dist")
		if err := localExecutorOverlayTargetAvailable(target); err != nil {
			return nil, err
		}
		cleanupTargets = append(cleanupTargets, target)
		if err := installLocalFrontendEmbedDependency(sourceRoot, dependencies.FrontendEmbedRoot); err != nil {
			return nil, errors.Join(fmt.Errorf("install local frontend embed seed: %w", err), cleanup())
		}
	}
	return cleanup, nil
}

func localExecutorOverlayTargetAvailable(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("local executor overlay target already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect local executor overlay target %q: %w", path, err)
	}
	return nil
}

func installLocalGoDependencies(layout executorLayout, cacheRoot string) error {
	if err := validateLocalDependencyRoot(cacheRoot, "Go module cache"); err != nil {
		return err
	}
	if err := bindSharedGoModuleCache(cacheRoot, layout.goModCache); err != nil {
		return fmt.Errorf("bind local Go module cache: %w", err)
	}
	return nil
}

func installLocalFrontendDependencies(sourceRoot string, layout executorLayout, dependencies LocalExecutorDependencyInputs) error {
	for name, path := range map[string]string{
		"frontend node_modules": dependencies.FrontendNodeModules,
		"frontend npm cache":    dependencies.FrontendNPMCache,
		"frontend Vite cache":   dependencies.FrontendViteCache,
	} {
		if err := validateLocalDependencyRoot(path, name); err != nil {
			return err
		}
	}
	targetRoot := filepath.Join(sourceRoot, "frontend-app", "node_modules")
	if err := installFrontendRuntimeOverlays(dependencies.FrontendNodeModules, dependencies.FrontendViteCache, targetRoot, filepath.Join(layout.tmp, ".vite-temp")); err != nil {
		return err
	}
	return nil
}

func installLocalFrontendEmbedDependency(sourceRoot, embedRoot string) error {
	if err := validateLocalDependencyRoot(embedRoot, "frontend embed root"); err != nil {
		return err
	}
	targetRoot := filepath.Join(sourceRoot, "cmd", "agent-terminal", "web-dist")
	if err := copyRuntimeSeed(embedRoot, targetRoot); err != nil {
		return err
	}
	return nil
}

// validateLocalDependencyRoot 校验本地依赖根是绝对、非链接的实目录。
func validateLocalDependencyRoot(path, name string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("local %s dependency root is required", name)
	}
	canonical, err := canonicalLocalSandboxPath(path, "local "+name+" dependency root")
	if err != nil {
		return err
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("local %s dependency root must be a real directory: %w", name, err)
	}
	return nil
}

func prepareLocalExecutorSteps(searchPath, sourceRoot string, program ExecutorProgram) ([]resolvedStep, error) {
	return prepareLocalExecutorStepsWithSelf(searchPath, sourceRoot, program, TrustedSelfBinary{})
}

func prepareLocalExecutorStepsWithSelf(searchPath, sourceRoot string, program ExecutorProgram, self TrustedSelfBinary) ([]resolvedStep, error) {
	steps, err := resolveExecutorStepsWithSelf(searchPath, sourceRoot, program, self)
	if err != nil {
		return nil, err
	}
	if err := validateProgramInputs(sourceRoot, program); err != nil {
		return nil, err
	}
	if err := validateRequiredExecutables(program.RequiredExecutables); err != nil {
		return nil, err
	}
	return steps, nil
}

// runLocalExecutorSteps 在隔离本地工作目录中运行 canonical steps 并收集完整 timing/profile 输入。
func runLocalExecutorSteps(ctx context.Context, nowFunc func() time.Time, id GateID, steps []resolvedStep, environment []string, sandboxPath, sandboxProfile string) localExecutorObservation {
	if nowFunc == nil {
		return localExecutorObservation{runErr: errors.New("local executor clock is required")}
	}
	started := nowFunc().UTC()
	bodyStarted := nowFunc().UTC()
	log := newBoundedPlanLog(executorPlanMaxLogBytes)
	var stdout io.Writer = log
	var timingWriter *testtiming.EventWriter
	var testTimings []GoTestTiming
	if isGoPackageTestWorkload(id) {
		timingWriter = testtiming.NewEventWriter(log)
		stdout = timingWriter
	}
	runErr := runLocalExecutorStepSet(ctx, id, steps, environment, stdout, log, sandboxPath, sandboxProfile)
	if timingWriter != nil {
		runErr = errors.Join(runErr, timingWriter.Close())
		testTimings = timingWriter.Timings()
	}
	completed := nowFunc().UTC()
	if !completed.After(bodyStarted) {
		completed = bodyStarted.Add(time.Millisecond)
	}
	timing := &executorExecutionTiming{
		setupMS: measuredExecutorPhaseMilliseconds(started, bodyStarted),
		bodyMS:  measuredExecutorPhaseMilliseconds(bodyStarted, completed),
	}
	if timing.setupMS == 0 {
		timing.setupMS = 1
	}
	if timing.bodyMS == 0 {
		timing.bodyMS = 1
	}
	timing.totalMS = timing.setupMS + timing.bodyMS
	return localExecutorObservation{started: started, completed: completed, timing: timing, testTimings: testTimings, log: log.Bytes(), runErr: runErr}
}

func runSandboxedResolvedStep(ctx context.Context, step resolvedStep, environment []string, stdout, stderr io.Writer, sandboxPath, sandboxProfile string) error {
	commandArgs := append([]string{"-p", sandboxProfile, step.binary}, step.argv[1:]...)
	command := exec.CommandContext(ctx, sandboxPath, commandArgs...)
	configureCommandCancellation(command)
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

// localNetworkSandboxPath 校验 macOS sandbox-exec 可执行文件及其最小能力探针。
func localNetworkSandboxPath() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("local canonical workload requires macOS sandbox-exec network isolation")
	}
	const sandboxPath = "/usr/bin/sandbox-exec"
	info, err := os.Stat(sandboxPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", errors.New("local canonical workload sandbox-exec is unavailable")
	}
	probe := exec.Command(sandboxPath, "-p", "(version 1) (import \"system.sb\") (deny default) (allow process*) (allow file-read* (subpath \"/usr/bin\"))", "/usr/bin/true")
	if err := probe.Run(); err != nil {
		return "", fmt.Errorf("local canonical workload sandbox-exec capability probe failed: %w", err)
	}
	return sandboxPath, nil
}

// localSandboxProfile 构造 network-off、系统运行时只读且 exact roots 可写的 sandbox profile。
func localSandboxProfile(sourceRoot string, layout executorLayout, dependencies LocalExecutorDependencyInputs, goRoot string, toolPaths []string) (string, error) {
	rawWriteRoots := []string{sourceRoot, layout.runRoot, layout.home, layout.tmp, layout.goCache, layout.goModCache, layout.npmCache, layout.xdgCache}
	writeRoots, err := canonicalLocalSandboxRoots(rawWriteRoots)
	if err != nil {
		return "", err
	}
	readRoots := append([]string(nil), writeRoots...)
	gitObjectRoots, err := localSandboxGitObjectReadRoots(sourceRoot)
	if err != nil {
		return "", err
	}
	readRoots = append(readRoots, gitObjectRoots...)
	dependencyRoots, err := localSandboxDependencyRoots(writeRoots, dependencies)
	if err != nil {
		return "", err
	}
	readRoots = append(readRoots, dependencyRoots...)
	readRoots, err = localSandboxToolRoots(readRoots, goRoot, toolPaths)
	if err != nil {
		return "", err
	}
	readRoots = append(readRoots, localSandboxSystemReadRoots()...)

	var profile strings.Builder
	profile.WriteString("(version 1) (import \"system.sb\") (deny default) (deny network*) (deny file-write*) (allow process*) (allow sysctl-read)")
	appendLocalSandboxReadRules(&profile, readRoots)
	appendLocalSandboxWriteRules(&profile, writeRoots)
	if err := appendLocalSandboxDenyRules(&profile, writeRoots, dependencyRoots); err != nil {
		return "", err
	}
	appendLocalSandboxDeviceRules(&profile)
	return profile.String(), nil
}

// canonicalLocalSandboxRoots 将 sandbox 写根逐一解析为可信 canonical 路径。
func canonicalLocalSandboxRoots(rawRoots []string) ([]string, error) {
	roots := make([]string, 0, len(rawRoots))
	for _, path := range rawRoots {
		canonical, err := canonicalLocalSandboxPath(path, "sandbox write root")
		if err != nil {
			return nil, err
		}
		roots = append(roots, canonical)
	}
	return roots, nil
}

// localSandboxDependencyRoots 校验依赖根不重叠 writable session roots。
func localSandboxDependencyRoots(writeRoots []string, dependencies LocalExecutorDependencyInputs) ([]string, error) {
	roots := make([]string, 0, 5)
	for _, path := range []string{dependencies.GoModuleCache, dependencies.FrontendNodeModules, dependencies.FrontendNPMCache, dependencies.FrontendViteCache, dependencies.FrontendEmbedRoot} {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if err := validateLocalDependencyRoot(path, "sandbox dependency"); err != nil {
			return nil, err
		}
		canonical, err := canonicalLocalSandboxPath(path, "sandbox dependency root")
		if err != nil {
			return nil, err
		}
		for _, writeRoot := range writeRoots {
			if rootsOverlap(canonical, writeRoot) {
				return nil, fmt.Errorf("sandbox dependency root %q overlaps writable session root %q", canonical, writeRoot)
			}
		}
		roots = append(roots, canonical)
	}
	return roots, nil
}

// localSandboxToolRoots 将 toolchain 和实际工具加入 sandbox 只读根。
func localSandboxToolRoots(readRoots []string, goRoot string, toolPaths []string) ([]string, error) {
	if strings.TrimSpace(goRoot) != "" {
		canonical, err := canonicalLocalSandboxPath(goRoot, "sandbox toolchain root")
		if err != nil {
			return nil, err
		}
		readRoots = append(readRoots, canonical)
	}
	for _, path := range toolPaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		canonical, err := canonicalLocalSandboxPath(path, "sandbox tool")
		if err != nil {
			return nil, err
		}
		readRoots = append(readRoots, canonical)
	}
	return readRoots, nil
}

func appendLocalSandboxReadRules(profile *strings.Builder, roots []string) {
	for _, path := range roots {
		profile.WriteString(" (allow file-read* (subpath ")
		profile.WriteString(localSandboxString(path))
		profile.WriteString("))")
	}
}

func appendLocalSandboxWriteRules(profile *strings.Builder, roots []string) {
	for _, path := range roots {
		profile.WriteString(" (allow file-write* (subpath ")
		profile.WriteString(localSandboxString(path))
		profile.WriteString("))")
	}
}

func appendLocalSandboxDenyRules(profile *strings.Builder, writeRoots, dependencyRoots []string) error {
	denyRoots := append([]string{filepath.Join(writeRoots[0], ".git")}, dependencyRoots...)
	for _, path := range denyRoots {
		canonical, err := canonicalLocalSandboxPath(path, "sandbox write deny root")
		if err != nil {
			return err
		}
		profile.WriteString(" (deny file-write* (subpath ")
		profile.WriteString(localSandboxString(canonical))
		profile.WriteString("))")
	}
	return nil
}

func appendLocalSandboxDeviceRules(profile *strings.Builder) {
	for _, path := range []string{"/dev/null", "/dev/tty", "/dev/urandom", "/dev/random"} {
		profile.WriteString(" (allow file-read* (literal ")
		profile.WriteString(localSandboxString(path))
		profile.WriteString("))")
		if path == "/dev/null" || path == "/dev/tty" {
			profile.WriteString(" (allow file-write* (literal ")
			profile.WriteString(localSandboxString(path))
			profile.WriteString("))")
		}
	}
}

func canonicalLocalSandboxPath(path, name string) (string, error) {
	resolved, err := canonicalLocalSandboxPathValue(path, name)
	if err != nil {
		return "", err
	}
	if localSandboxForbiddenRoot(resolved) {
		return "", fmt.Errorf("%s must not be a broad system or user root", name)
	}
	return resolved, nil
}

// canonicalLocalSandboxPathValue 只接受存在的绝对路径，并拒绝非受信符号链接逃逸。
func canonicalLocalSandboxPathValue(path, name string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("%s must be a canonical absolute path", name)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%s must exist and resolve canonically: %w", name, err)
	}
	if resolved != path && !localSandboxPathAlias(path, resolved) {
		return "", fmt.Errorf("%s contains an untrusted symlink", name)
	}
	return resolved, nil
}

func localSandboxPathAlias(path, resolved string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	for _, alias := range []struct{ from, to string }{
		{from: "/var", to: "/private/var"},
		{from: "/tmp", to: "/private/tmp"},
		{from: "/etc", to: "/private/etc"},
	} {
		if path != alias.from && !pathContains(alias.from, path) {
			continue
		}
		expected := filepath.Join(alias.to, strings.TrimPrefix(path, alias.from))
		return expected == resolved
	}
	return false
}

func localSandboxForbiddenRoot(path string) bool {
	switch path {
	case "/", "/private", "/private/var", "/var", "/tmp", "/private/tmp", "/dev":
		return true
	}
	if home, err := os.UserHomeDir(); err == nil {
		if canonicalHome, canonicalErr := filepath.EvalSymlinks(home); canonicalErr == nil && path == canonicalHome {
			return true
		}
	}
	return false
}

// localSandboxSystemReadRoots 收集已存在的系统运行时只读根并去重。
func localSandboxSystemReadRoots() []string {
	candidates := []string{"/bin", "/usr/bin", "/sbin", "/usr/sbin", "/usr/local/bin", "/System/Library", "/Library", "/usr/lib", "/lib"}
	roots := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			continue
		}
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		roots = append(roots, resolved)
	}
	return roots
}

func localSandboxString(path string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(path) + `"`
}

// sandboxToolPaths 收集本批次实际会被 exec 的 canonical 工具，避免继承宿主 PATH 读权限。
func sandboxToolPaths(steps []resolvedStep, program ExecutorProgram) []string {
	paths := make([]string, 0, len(steps)+len(program.RequiredExecutables))
	for _, step := range steps {
		paths = append(paths, step.binary)
	}
	paths = append(paths, program.RequiredExecutables...)
	return paths
}

func finishLocalGateExecution(id GateID, program ExecutorProgram, observation localExecutorObservation, contextErr error) (PlanGateExecution, error) {
	profile, profileErr := executionProfileOrFailedStartup(id, program, observation.testTimings, observation.started, observation.completed, observation.timing)
	runErr := errors.Join(observation.runErr, profileErr)
	status, exitCode := classifyPlanGateOutcome(runErr, contextErr)
	argvDigest, digestErr := localWorkloadExecutionDigest(string(id))
	runErr = errors.Join(runErr, digestErr)
	result := PlanGateExecution{ShardIdentity: "local/" + string(id), GateID: id, Status: status, ExitCode: exitCode, StartedAt: observation.started, CompletedAt: observation.completed, ArgvDigest: argvDigest, Log: observation.log, LogDigest: digestPlanLog(observation.log), TestTimings: observation.testTimings, ExecutionProfile: profile}
	result.CompletedAt = normalizedExecutionCompletedAt(result.StartedAt, result.CompletedAt, result.ExecutionProfile)
	result, canonicalErr := CanonicalizePlanGateExecutionTiming(result)
	return result, errors.Join(runErr, canonicalErr)
}

func localWorkloadExecutionDigest(id string) (string, error) {
	commandDigest, err := WorkloadExecutionDigest(id)
	if err != nil {
		return "", err
	}
	return WorkloadPassExecutionDigest(Workload{ID: id, CommandDigest: commandDigest}), nil
}

// configuredLocalExecutorTempRoots 返回默认临时根与可选 GOTMPDIR，保留原始配置供严格校验。
func configuredLocalExecutorTempRoots() []string {
	tempRoots := []string{filepath.Clean(os.TempDir())}
	if goTempRoot := os.Getenv("GOTMPDIR"); goTempRoot != "" {
		tempRoots = append(tempRoots, goTempRoot)
	}
	return tempRoots
}

func makeLocalExecutorDirectories(layout executorLayout) error {
	for _, path := range []string{layout.runRoot, layout.home, layout.tmp, layout.goCache, layout.goModCache, layout.npmCache, layout.xdgCache} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create local executor directory %q: %w", path, err)
		}
	}
	return nil
}
