package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

const (
	productionCurrentGateCLI      = ".super-dolphin-gate-current"
	productionPreviousGateCLI     = ".super-dolphin-gate-previous"
	productionSelfUpdateStateFile = ".super-dolphin-gate-state.json"
	productionSelfUpdateStateV1   = 1
	productionSelfUpdateDeadline  = 45 * time.Second
)

type productionSelfUpdateState struct {
	SchemaVersion           uint32 `json:"schema_version"`
	Remote                  string `json:"remote"`
	TrustedRef              string `json:"trusted_ref"`
	Commit                  string `json:"commit"`
	Tree                    string `json:"tree"`
	SourceDigest            string `json:"source_digest"`
	LockDigest              string `json:"lock_digest"`
	ToolchainDigest         string `json:"toolchain_digest"`
	Platform                string `json:"platform"`
	BinaryDigest            string `json:"binary_digest"`
	PreviousBinaryDigest    string `json:"previous_binary_digest"`
	PreviousSourceDigest    string `json:"previous_source_digest,omitempty"`
	PreviousToolchainDigest string `json:"previous_toolchain_digest,omitempty"`
	Current                 string `json:"current"`
	Previous                string `json:"previous"`
}

// Validate 校验生产自更新状态的完整性与身份字段。
func (state productionSelfUpdateState) Validate() error {
	if err := state.validateRequiredFields(); err != nil {
		return err
	}
	if err := state.validateDigests(); err != nil {
		return err
	}
	return state.validatePreviousIdentity()
}

// validateRequiredFields 校验状态中不可缺失且必须固定的字段。
func (state productionSelfUpdateState) validateRequiredFields() error {
	if state.SchemaVersion != productionSelfUpdateStateV1 ||
		validateProductionBootstrapRemoteURL(state.Remote) != nil ||
		state.TrustedRef == "" ||
		!productionGitObjectIDValid(state.Commit) ||
		!productionGitObjectIDValid(state.Tree) ||
		state.Platform == "" ||
		state.Current != productionCurrentGateCLI ||
		state.Previous != productionPreviousGateCLI {
		return errors.New("production update state is incomplete")
	}
	return nil
}

func (state productionSelfUpdateState) validateDigests() error {
	for _, digest := range []string{
		state.SourceDigest,
		state.LockDigest,
		state.ToolchainDigest,
		state.BinaryDigest,
		state.PreviousBinaryDigest,
	} {
		if err := validateProductionDigest(digest); err != nil {
			return err
		}
	}
	return nil
}

func (state productionSelfUpdateState) validatePreviousIdentity() error {
	if (state.PreviousSourceDigest == "") != (state.PreviousToolchainDigest == "") {
		return errors.New("production update previous identity must be complete or absent")
	}
	if state.PreviousSourceDigest == "" {
		return nil
	}
	return validateProductionCLIIdentity(state.PreviousSourceDigest, state.PreviousToolchainDigest)
}

type productionCLICompileClosure func(context.Context, string, string) (string, string, []sourceexport.TreeEntry, error)

type productionSelfUpdateDeps struct {
	loadClosure      productionCLICompileClosure
	loadConfig       func() (productionCoordinatorConfig, error)
	loadRoot         func(string, []productionTrustedKey) (productionBootstrapRoot, error)
	resolveToolchain func(productionGoRequirement) (productionGoToolchain, error)
	run              func(context.Context, string, []string, string, []string) ([]byte, error)
	executable       func() (string, error)
}

type productionGoToolchain struct {
	Executable, Version, GoRoot, GoPath, GoCache, GoModCache, GoToolDir, GOOS, GOARCH string
}

type productionGoRequirement struct{ Minimum, Preferred string }

type productionGoResolverDeps struct {
	getenv           func(string) string
	run              func(string, ...string) ([]byte, error)
	systemCandidates func() []string
	bootstrap        func(productionGoRequirement) (productionGoToolchain, error)
}

func liveProductionSelfUpdateDeps() productionSelfUpdateDeps {
	return productionSelfUpdateDeps{
		loadClosure:      localci.LoadGateCLICompileClosure,
		loadConfig:       loadProductionCoordinatorConfig,
		loadRoot:         loadProductionBootstrapRoot,
		resolveToolchain: resolveProductionGoToolchain,
		executable:       os.Executable,
		run:              runProductionSelfUpdateProgram,
	}
}

func isProductionSelfUpdateCommand(args []string) bool {
	return len(args) > 0 && args[0] == "_production-update"
}

func runProductionSelfUpdateCLI(args []string, stderr io.Writer) int {
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := gateprivate.WithTimeout(signalContext, productionSelfUpdateDeadline)
	defer cancel()
	err := updateProductionGateCLI(ctx, args, liveProductionSelfUpdateDeps(), stderr)
	if err != nil {
		_ = writeCLIError(stderr, gatecontract.WithExitCode(gatecontract.ExitInfrastructure, err))
		return int(gatecontract.ExitInfrastructure)
	}
	return int(gatecontract.ExitOK)
}

type productionSelfUpdateSession struct {
	repository string
	current    string
	statePath  string
	git        string
	config     productionCoordinatorConfig
	root       productionBootstrapRoot
}

type productionSelfUpdateSource struct {
	commit        string
	tree          string
	sourceDigest  string
	lockDigest    string
	goRequirement productionGoRequirement
	entries       []sourceexport.TreeEntry
}

type productionSelfUpdateInputs struct {
	expected      productionSelfUpdateState
	entries       []sourceexport.TreeEntry
	toolchain     productionGoToolchain
	previousState *productionSelfUpdateState
}

// updateProductionGateCLI 依次完成可信源检查、缓存判定、构建验证和原子切换。
func updateProductionGateCLI(ctx context.Context, args []string, deps productionSelfUpdateDeps, stderr io.Writer) (resultErr error) {
	if err := validateProductionSelfUpdateDeps(deps); err != nil {
		return err
	}
	started := time.Now()
	if err := updateEvent(stderr, "check", started); err != nil {
		return err
	}
	session, err := prepareProductionSelfUpdateSession(ctx, args, deps)
	if err != nil {
		return err
	}
	lock, acquired, err := tryAcquireProductionSelfUpdateLock(session.current)
	if err != nil {
		return err
	}
	if !acquired {
		if err := reuseProductionCurrentDuringUpdate(ctx, session, deps.run); err != nil {
			return err
		}
		return updateEvent(stderr, "cache-hit", started)
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Release()) }()
	inputs, matched, err := loadProductionSelfUpdateInputs(ctx, session, deps)
	if err != nil {
		return err
	}
	if matched {
		return updateEvent(stderr, "cache-hit", started)
	}
	return buildAndSwitchProductionCLI(ctx, session, inputs, deps, stderr, started)
}

// validateProductionSelfUpdateDeps 确保生产更新没有缺失可注入依赖。
func validateProductionSelfUpdateDeps(deps productionSelfUpdateDeps) error {
	if deps.loadClosure == nil ||
		deps.loadConfig == nil ||
		deps.loadRoot == nil ||
		deps.resolveToolchain == nil ||
		deps.run == nil ||
		deps.executable == nil {
		return errors.New("production self-update dependencies are required")
	}
	return nil
}

// prepareProductionSelfUpdateSession 绑定固定 current、生产配置与签名信任根。
func prepareProductionSelfUpdateSession(
	ctx context.Context,
	args []string,
	deps productionSelfUpdateDeps,
) (productionSelfUpdateSession, error) {
	repository, err := productionSelfUpdateRepository(args)
	if err != nil {
		return productionSelfUpdateSession{}, err
	}
	current, err := deps.executable()
	if err != nil {
		return productionSelfUpdateSession{}, fmt.Errorf("locate production current CLI: %w", err)
	}
	if err := verifyProductionCurrentCLI(current); err != nil {
		return productionSelfUpdateSession{}, err
	}
	config, err := deps.loadConfig()
	if err != nil {
		return productionSelfUpdateSession{}, err
	}
	if repository == "" {
		repository = config.TrustedRepository
	}
	if err := verifyProductionSelfUpdateRepository(repository, config); err != nil {
		return productionSelfUpdateSession{}, err
	}
	gitExecutable, err := canonicalProductionGitExecutable(config.GitExecutable)
	if err != nil {
		return productionSelfUpdateSession{}, err
	}
	root, err := deps.loadRoot(config.BootstrapRootFile, config.AcceptedImageSigners)
	if err != nil {
		return productionSelfUpdateSession{}, fmt.Errorf("load signed production bootstrap root: %w", err)
	}
	if err := verifyProductionSelfUpdateTrust(ctx, repository, gitExecutable, config, root, deps.run); err != nil {
		return productionSelfUpdateSession{}, err
	}
	return productionSelfUpdateSession{
		repository: repository,
		current:    current,
		statePath:  filepath.Join(filepath.Dir(current), productionSelfUpdateStateFile),
		git:        gitExecutable,
		config:     config,
		root:       root,
	}, nil
}

// tryAcquireProductionSelfUpdateLock 仅允许一位更新者进入 fetch、build 与切换流程。
func tryAcquireProductionSelfUpdateLock(current string) (*gateprivate.ExclusiveFileLock, bool, error) {
	lock, acquired, err := gateprivate.TryAcquireExclusiveFileLock(filepath.Join(filepath.Dir(current), ".super-dolphin-gate-update.lock"))
	if err != nil {
		return nil, false, fmt.Errorf("try production self-update lock: %w", err)
	}
	return lock, acquired, nil
}

// loadProductionSelfUpdateSource 读取已固定可信树的 CLI 编译闭包和 Go 约束。
func loadProductionSelfUpdateSource(
	ctx context.Context,
	session productionSelfUpdateSession,
	head productionSelfUpdateHead,
	deps productionSelfUpdateDeps,
) (productionSelfUpdateSource, error) {
	sourceDigest, lockDigest, entries, err := deps.loadClosure(ctx, session.repository, head.tree)
	if err != nil {
		return productionSelfUpdateSource{}, fmt.Errorf("load gate CLI compile closure: %w", err)
	}
	if err := validateProductionCLIIdentity(sourceDigest, lockDigest); err != nil {
		return productionSelfUpdateSource{}, err
	}
	goRequirement, err := productionGoRequirementFromEntries(entries)
	if err != nil {
		return productionSelfUpdateSource{}, fmt.Errorf("load candidate Go requirement: %w", err)
	}
	return productionSelfUpdateSource{
		commit:        head.commit,
		tree:          head.tree,
		sourceDigest:  sourceDigest,
		lockDigest:    lockDigest,
		goRequirement: goRequirement,
		entries:       entries,
	}, nil
}

// loadProductionSelfUpdateInputs 计算本机工具链身份并判定 current 是否可直接复用。
func loadProductionSelfUpdateInputs(
	ctx context.Context,
	session productionSelfUpdateSession,
	deps productionSelfUpdateDeps,
) (inputs productionSelfUpdateInputs, matched bool, resultErr error) {
	head, release, err := fetchProductionSelfUpdateHead(ctx, session, deps.run)
	if err != nil {
		return productionSelfUpdateInputs{}, false, err
	}
	defer func() { resultErr = errors.Join(resultErr, release()) }()
	previousState, currentMatched, err := inspectProductionCurrentHeadState(ctx, session, head, deps.run)
	if err != nil {
		return productionSelfUpdateInputs{}, false, err
	}
	if currentMatched {
		return productionSelfUpdateInputs{}, true, nil
	}
	source, err := loadProductionSelfUpdateSource(ctx, session, head, deps)
	if err != nil {
		return productionSelfUpdateInputs{}, false, err
	}
	toolchain, err := deps.resolveToolchain(source.goRequirement)
	if err != nil {
		return productionSelfUpdateInputs{}, false, err
	}
	toolchain, err = bindProductionSelfUpdateGoCache(session.config.TrustedSourceRoot, toolchain)
	if err != nil {
		return productionSelfUpdateInputs{}, false, err
	}
	localToolchainDigest, err := productionLocalToolchainDigest(source.lockDigest, toolchain)
	if err != nil {
		return productionSelfUpdateInputs{}, false, fmt.Errorf("bind local Go toolchain identity: %w", err)
	}
	expected := productionSelfUpdateState{
		SchemaVersion:   productionSelfUpdateStateV1,
		Remote:          session.root.RemoteURL,
		TrustedRef:      session.root.TrustedRef,
		Commit:          source.commit,
		Tree:            source.tree,
		SourceDigest:    source.sourceDigest,
		LockDigest:      source.lockDigest,
		ToolchainDigest: localToolchainDigest,
		Platform:        runtime.GOOS + "/" + runtime.GOARCH,
		Current:         productionCurrentGateCLI,
		Previous:        productionPreviousGateCLI,
	}
	matched, err = refreshProductionCurrentCacheHit(session, previousState, expected)
	if err != nil {
		return productionSelfUpdateInputs{}, false, err
	}
	if matched {
		return productionSelfUpdateInputs{}, true, nil
	}
	return productionSelfUpdateInputs{
		expected:      expected,
		entries:       source.entries,
		toolchain:     toolchain,
		previousState: previousState,
	}, false, nil
}

// refreshProductionCurrentCacheHit 在已完成 current 身份与祖先校验后，仅刷新同闭包状态的对象身份。
func refreshProductionCurrentCacheHit(
	session productionSelfUpdateSession,
	currentState *productionSelfUpdateState,
	expected productionSelfUpdateState,
) (bool, error) {
	if currentState == nil {
		return false, nil
	}
	currentDigest, err := productionBinaryDigest(session.current)
	if err != nil {
		return false, fmt.Errorf("redigest current production gate CLI: %w", err)
	}
	persisted, err := loadProductionSelfUpdateState(session.statePath)
	if err != nil {
		return false, fmt.Errorf("reload production update state for cache hit: %w", err)
	}
	if persisted != *currentState {
		return false, errors.New("production update state changed after strict current validation")
	}
	stableDigest, err := productionBinaryDigest(session.current)
	if err != nil {
		return false, fmt.Errorf("redigest current production gate CLI after state reload: %w", err)
	}
	if stableDigest != currentDigest {
		return false, errors.New("production current CLI changed during cache-hit validation")
	}
	if !productionUpdateStateMatchesExpected(persisted, expected) {
		return false, nil
	}
	if err := refreshProductionCacheHitState(session.statePath, &persisted, expected); err != nil {
		return false, err
	}
	stableDigest, err = productionBinaryDigest(session.current)
	if err != nil {
		return false, fmt.Errorf("redigest current production gate CLI after cache-hit refresh: %w", err)
	}
	if stableDigest != currentDigest {
		return false, errors.New("production current CLI changed while refreshing cache-hit state")
	}
	return true, nil
}

// buildAndSwitchProductionCLI 离线构建候选并在完整验证后提交切换事务。
func buildAndSwitchProductionCLI(
	ctx context.Context,
	session productionSelfUpdateSession,
	inputs productionSelfUpdateInputs,
	deps productionSelfUpdateDeps,
	stderr io.Writer,
	started time.Time,
) (resultErr error) {
	if err := updateEvent(stderr, "build", started); err != nil {
		return err
	}
	candidate, cleanup, err := buildProductionCLICandidate(
		ctx,
		filepath.Dir(session.current),
		inputs.expected.SourceDigest,
		inputs.expected.ToolchainDigest,
		inputs.entries,
		inputs.toolchain,
		deps,
	)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, cleanup())
	}()
	if err := updateEvent(stderr, "verify", started); err != nil {
		return err
	}
	candidateDigest, err := verifyProductionCLICandidate(
		ctx,
		candidate,
		inputs.expected.SourceDigest,
		inputs.expected.ToolchainDigest,
		deps.run,
	)
	if err != nil {
		return err
	}
	currentDigest, err := productionBinaryDigest(session.current)
	if err != nil {
		return fmt.Errorf("digest current production gate CLI: %w", err)
	}
	inputs.expected.BinaryDigest = candidateDigest
	inputs.expected.PreviousBinaryDigest = currentDigest
	if inputs.previousState != nil {
		inputs.expected.PreviousSourceDigest = inputs.previousState.SourceDigest
		inputs.expected.PreviousToolchainDigest = inputs.previousState.ToolchainDigest
	}
	if err := updateEvent(stderr, "switch", started); err != nil {
		return err
	}
	return switchProductionCurrentCLI(candidate, session.current, session.statePath, inputs.expected, liveProductionSwitchOps())
}

func updateEvent(stderr io.Writer, phase string, started time.Time) error {
	_, err := fmt.Fprintf(stderr, "production-update phase=%s elapsed=%s\n", phase, time.Since(started).Round(time.Millisecond))
	return err
}

func loadProductionCurrentUpdateState(
	statePath string,
	currentDigest string,
	bootstrapController string,
) (*productionSelfUpdateState, error) {
	state, err := loadProductionSelfUpdateState(statePath)
	if errors.Is(err, errProductionSelfUpdateStateNotFound) || errors.Is(err, os.ErrNotExist) {
		if err := verifyProductionBootstrapCurrentCLI(currentDigest, bootstrapController); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load production update state: %w", err)
	}
	return &state, nil
}

func verifyProductionBootstrapCurrentCLI(currentDigest, bootstrapController string) error {
	bootstrapDigest, err := productionBinaryDigest(bootstrapController)
	if err != nil {
		return fmt.Errorf("digest bootstrap controller for first migration: %w", err)
	}
	if currentDigest != bootstrapDigest {
		return errors.New("production update state is missing for a non-bootstrap current CLI")
	}
	return nil
}

func validateProductionCurrentStateMetadata(state, expected productionSelfUpdateState) error {
	if state.Remote != expected.Remote || state.TrustedRef != expected.TrustedRef {
		return errors.New("production update remote or ref drifted from signed root")
	}
	if state.Platform != expected.Platform {
		return errors.New("production update state platform does not match this machine")
	}
	return nil
}

func verifyProductionCurrentStateIdentity(
	ctx context.Context,
	current string,
	currentDigest string,
	state productionSelfUpdateState,
	run productionSelfUpdateDepsRun,
) error {
	if currentDigest != state.BinaryDigest {
		return errors.New("production current CLI digest does not match strict update state")
	}
	matched, err := productionCurrentIdentityMatches(ctx, current, state.SourceDigest, state.ToolchainDigest, run)
	if err != nil || !matched {
		return errors.Join(errors.New("production current CLI identity does not match strict update state"), err)
	}
	return nil
}

func productionUpdateStateMatchesExpected(state, expected productionSelfUpdateState) bool {
	return state.SourceDigest == expected.SourceDigest &&
		state.LockDigest == expected.LockDigest &&
		state.ToolchainDigest == expected.ToolchainDigest
}

func refreshProductionCacheHitState(
	statePath string,
	state *productionSelfUpdateState,
	expected productionSelfUpdateState,
) error {
	if state.Commit != expected.Commit || state.Tree != expected.Tree {
		state.Commit = expected.Commit
		state.Tree = expected.Tree
		if err := writeProductionSelfUpdateState(statePath, *state); err != nil {
			return fmt.Errorf("refresh cache-hit production update state: %w", err)
		}
	}
	return nil
}

// verifyInterruptedProductionSwitch 确认中断切换后仍保留可验证的前一 CLI。
func verifyInterruptedProductionSwitch(
	ctx context.Context,
	current string,
	bootstrapController string,
	currentDigest string,
	state productionSelfUpdateState,
	run productionSelfUpdateDepsRun,
) (*productionSelfUpdateState, error) {
	if state.PreviousSourceDigest == "" {
		bootstrapDigest, err := productionBinaryDigest(bootstrapController)
		if err != nil || bootstrapDigest != currentDigest {
			return nil, errors.Join(errors.New("interrupted first production switch does not retain the bootstrap controller"), err)
		}
		return nil, nil
	}
	matched, err := productionCurrentIdentityMatches(
		ctx,
		current,
		state.PreviousSourceDigest,
		state.PreviousToolchainDigest,
		run,
	)
	if err != nil || !matched {
		return nil, errors.Join(errors.New("interrupted production switch does not retain the previous CLI identity"), err)
	}
	previous := state
	previous.SourceDigest = state.PreviousSourceDigest
	previous.ToolchainDigest = state.PreviousToolchainDigest
	previous.BinaryDigest = currentDigest
	return &previous, nil
}

// productionSelfUpdateRepository 解析可选的受信生产仓库路径。
func productionSelfUpdateRepository(args []string) (string, error) {
	for index := range args {
		if args[index] != "--production-repo" {
			continue
		}
		if index+1 >= len(args) || !filepath.IsAbs(args[index+1]) || filepath.Clean(args[index+1]) != args[index+1] {
			return "", errors.New("--production-repo must name a canonical absolute path")
		}
		return args[index+1], nil
	}
	return "", nil
}

func verifyProductionSelfUpdateRepository(repository string, config productionCoordinatorConfig) error {
	if repository == "" || repository != config.TrustedRepository {
		return errors.New("production update repository must be the configured trusted repository")
	}
	return verifyProductionProvisionPrivateDirectory(repository, false)
}

// verifyProductionCurrentCLI 校验 current CLI 的固定名称、所有者和权限。
func verifyProductionCurrentCLI(path string) error {
	if filepath.Base(path) != productionCurrentGateCLI {
		return errors.New("production update must execute the fixed current CLI")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o700 || !productionProvisionOwnedByCurrentUser(info) {
		return errors.Join(errors.New("production current CLI is not an owner-only regular executable"), err)
	}
	return nil
}

func validateProductionCLIIdentity(sourceDigest, toolchainDigest string) error {
	for _, value := range []string{sourceDigest, toolchainDigest} {
		if err := validateProductionDigest(value); err != nil {
			return err
		}
	}
	return nil
}

func validateProductionDigest(value string) error {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return errors.New("production gate CLI identity digest is invalid")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:")); err != nil {
		return errors.New("production gate CLI identity digest is invalid")
	}
	return nil
}

func productionGitObjectIDValid(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func productionCurrentIdentityMatches(ctx context.Context, binary, sourceDigest, toolchainDigest string, run productionSelfUpdateDepsRun) (bool, error) {
	output, err := run(ctx, binary, []string{"worker", "cli-identity"}, "", nil)
	if err != nil {
		return false, fmt.Errorf("read current gate CLI identity: %w", err)
	}
	identity, err := parseProductionCLIIdentity(output)
	if err != nil {
		return false, err
	}
	return identity.SourceDigest == sourceDigest && identity.ToolchainDigest == toolchainDigest, nil
}

type productionSelfUpdateDepsRun func(context.Context, string, []string, string, []string) ([]byte, error)

type productionCLIIdentity struct {
	SourceDigest    string
	Platform        string
	ToolchainDigest string
}

// parseProductionCLIIdentity 解析并严格校验当前 CLI 输出的固定身份字段。
func parseProductionCLIIdentity(data []byte) (productionCLIIdentity, error) {
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 3 {
		return productionCLIIdentity{}, errors.New("gate CLI identity has unexpected fields")
	}
	values, err := parseProductionCLIIdentityFields(lines)
	if err != nil {
		return productionCLIIdentity{}, err
	}
	if err := validateProductionCLIIdentityFields(values); err != nil {
		return productionCLIIdentity{}, err
	}
	return productionCLIIdentity{
		SourceDigest:    values["gate_source_sha256"],
		Platform:        values["platform"],
		ToolchainDigest: values["toolchain_digest"],
	}, nil
}

// parseProductionCLIIdentityFields 将身份输出的每行解析为唯一字段。
func parseProductionCLIIdentityFields(lines []string) (map[string]string, error) {
	values := map[string]string{}
	for _, line := range lines {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || value == "" {
			return nil, errors.New("gate CLI identity is malformed")
		}
		if _, exists := values[key]; exists {
			return nil, errors.New("gate CLI identity duplicates a field")
		}
		values[key] = value
	}
	return values, nil
}

func validateProductionCLIIdentityFields(values map[string]string) error {
	if len(values) != 3 || values["gate_source_sha256"] == "" || values["toolchain_digest"] == "" || values["platform"] != runtime.GOOS+"/"+runtime.GOARCH {
		return errors.New("gate CLI identity has unknown or missing fields")
	}
	return nil
}

func buildProductionCLICandidate(
	ctx context.Context,
	directory string,
	sourceDigest string,
	toolchainDigest string,
	entries []sourceexport.TreeEntry,
	toolchain productionGoToolchain,
	deps productionSelfUpdateDeps,
) (string, func() error, error) {
	staging, err := os.MkdirTemp(directory, ".super-dolphin-gate-build-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() error { return os.RemoveAll(staging) }
	if err := materializeProductionCLIEntries(staging, entries); err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	environment := controlledProductionGoEnvironment(toolchain)
	if _, err := deps.run(ctx, toolchain.Executable, []string{"mod", "verify"}, staging, environment); err != nil {
		return "", nil, errors.Join(fmt.Errorf("verify production gate CLI modules: %w", err), cleanup())
	}
	candidate := filepath.Join(staging, productionCurrentGateCLI)
	args := []string{"build", "-mod=readonly", "-buildvcs=false", "-trimpath", "-ldflags", "-X main.gateSourceDigest=" + sourceDigest + " -X main.gateToolchainDigest=" + toolchainDigest, "-o", candidate, "./cmd/super-dolphin-gate"}
	if _, err := deps.run(ctx, toolchain.Executable, args, staging, environment); err != nil {
		return "", nil, errors.Join(fmt.Errorf("build production gate CLI: %w", err), cleanup())
	}
	if err := os.Chmod(candidate, 0o700); err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	return candidate, cleanup, nil
}

func verifyProductionCLICandidate(
	ctx context.Context,
	candidate string,
	sourceDigest string,
	toolchainDigest string,
	run productionSelfUpdateDepsRun,
) (string, error) {
	if err := verifyProductionCurrentCLI(candidate); err != nil {
		return "", fmt.Errorf("verify built production gate CLI file: %w", err)
	}
	matched, err := productionCurrentIdentityMatches(ctx, candidate, sourceDigest, toolchainDigest, run)
	if err != nil || !matched {
		return "", errors.Join(errors.New("built production gate CLI identity mismatch"), err)
	}
	digest, err := productionBinaryDigest(candidate)
	if err != nil {
		return "", fmt.Errorf("digest built production gate CLI: %w", err)
	}
	return digest, nil
}

// materializeProductionCLIEntries 以受限路径和权限将可信编译闭包写入 staging。
func materializeProductionCLIEntries(root string, entries []sourceexport.TreeEntry) error {
	if len(entries) == 0 {
		return errors.New("production gate CLI compile closure is empty")
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if err := validateProductionCLIEntry(entry, seen); err != nil {
			return err
		}
		path, err := productionCLIEntryPath(root, entry.Path)
		if err != nil {
			return err
		}
		if err := writeProductionCLIEntry(path, entry); err != nil {
			return err
		}
	}
	return nil
}

// validateProductionCLIEntry 拒绝不安全或重复的编译闭包条目。
func validateProductionCLIEntry(entry sourceexport.TreeEntry, seen map[string]struct{}) error {
	if entry.Path == "" || filepath.IsAbs(entry.Path) || filepath.Clean(entry.Path) != entry.Path ||
		strings.HasPrefix(entry.Path, "../") || (entry.Mode != "100644" && entry.Mode != "100755") {
		return errors.New("production gate CLI compile closure has unsafe entry")
	}
	if _, exists := seen[entry.Path]; exists {
		return errors.New("production gate CLI compile closure duplicates an entry")
	}
	seen[entry.Path] = struct{}{}
	return nil
}

func productionCLIEntryPath(root, entryPath string) (string, error) {
	path := filepath.Join(root, entryPath)
	if !strings.HasPrefix(path, root+string(filepath.Separator)) {
		return "", errors.New("production gate CLI compile closure escapes staging")
	}
	return path, nil
}

func writeProductionCLIEntry(path string, entry sourceexport.TreeEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if entry.Mode == "100755" {
		mode = 0o700
	}
	return os.WriteFile(path, entry.Data, mode)
}
