package gate

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
)

// localExecutorTrustedGoEnvironment is the only host-derived input accepted by
// the local dependency-input factory. The gate-owned Go binary, rather than a
// CLI caller, owns its interpretation.
type localExecutorTrustedGoEnvironment struct {
	GoModuleCache string `json:"GOMODCACHE"`
	CGOEnabled    string `json:"CGO_ENABLED"`
}

type localExecutorDependencyInputsDiscoveryHooks struct {
	eligibility             func(GateID) (LocalWorkloadExecutionEligibility, error)
	program                 func(GateID) (GateID, ExecutorProgram, error)
	trustedGoBinary         func() (string, error)
	goEnvironment           func(context.Context, string) (localExecutorTrustedGoEnvironment, error)
	canonicalDependencyRoot func(string, string) (string, error)
	frontendEmbedRoot       func() (string, error)
}

// DiscoverLocalExecutorDependencyInputs 仅从受控 Gate 映射发现依赖；任一映射、工具链或根校验失败立即拒绝。
//
// DiscoverLocalExecutorDependencyInputs discovers the gate-owned host inputs
// needed to construct a sealed local executor receipt and session.
//
// The caller supplies only workload IDs. Eligibility is checked through the
// canonical executor mapping before any host inspection. This factory never
// executes workloads, writes caches, or accesses cloud and ledger state.
func DiscoverLocalExecutorDependencyInputs(ctx context.Context, workloadIDs []GateID) (LocalExecutorDependencyInputs, error) {
	return discoverLocalExecutorDependencyInputs(ctx, workloadIDs, defaultLocalExecutorDependencyInputsDiscoveryHooks())
}

// defaultLocalExecutorDependencyInputsDiscoveryHooks 固定生产发现器，禁止由调用方替换宿主输入来源。
func defaultLocalExecutorDependencyInputsDiscoveryHooks() localExecutorDependencyInputsDiscoveryHooks {
	return localExecutorDependencyInputsDiscoveryHooks{
		eligibility:             EvaluateLocalWorkloadExecutionEligibility,
		program:                 executorProgramForWorkload,
		trustedGoBinary:         localExecutorTrustedGoBinary,
		goEnvironment:           localExecutorTrustedGoEnvironmentForBinary,
		canonicalDependencyRoot: canonicalLocalSandboxPath,
		frontendEmbedRoot:       localExecutorFrontendEmbedRoot,
	}
}

func discoverLocalExecutorDependencyInputs(ctx context.Context, workloadIDs []GateID, hooks localExecutorDependencyInputsDiscoveryHooks) (LocalExecutorDependencyInputs, error) {
	if err := validateLocalExecutorDependencyInputsDiscoveryRequest(ctx, workloadIDs, hooks); err != nil {
		return LocalExecutorDependencyInputs{}, err
	}
	needsGoSeed, needsEmbedSeed, err := validateLocalExecutorDependencyWorkloads(workloadIDs, hooks)
	if err != nil {
		return LocalExecutorDependencyInputs{}, err
	}
	if err := ctx.Err(); err != nil {
		return LocalExecutorDependencyInputs{}, err
	}
	goEnvironment, err := discoverLocalExecutorTrustedGoEnvironment(ctx, hooks)
	if err != nil {
		return LocalExecutorDependencyInputs{}, err
	}
	return buildLocalExecutorDependencyInputs(needsGoSeed, needsEmbedSeed, goEnvironment, hooks.canonicalDependencyRoot, hooks.frontendEmbedRoot)
}

func validateLocalExecutorDependencyInputsDiscoveryRequest(ctx context.Context, workloadIDs []GateID, hooks localExecutorDependencyInputsDiscoveryHooks) error {
	if ctx == nil {
		return errors.New("local executor dependency input context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(workloadIDs) == 0 {
		return errors.New("local executor dependency inputs require workload IDs")
	}
	return validateLocalExecutorDependencyInputsDiscoveryHooks(hooks)
}

func discoverLocalExecutorTrustedGoEnvironment(ctx context.Context, hooks localExecutorDependencyInputsDiscoveryHooks) (localExecutorTrustedGoEnvironment, error) {
	goBinary, err := hooks.trustedGoBinary()
	if err != nil {
		return localExecutorTrustedGoEnvironment{}, fmt.Errorf("resolve gate-owned trusted Go binary: %w", err)
	}
	goEnvironment, err := hooks.goEnvironment(ctx, goBinary)
	if err != nil {
		return localExecutorTrustedGoEnvironment{}, fmt.Errorf("read gate-owned trusted Go environment: %w", err)
	}
	if goEnvironment.CGOEnabled != "0" && goEnvironment.CGOEnabled != "1" {
		return localExecutorTrustedGoEnvironment{}, errors.New("gate-owned trusted Go CGO_ENABLED must be 0 or 1")
	}
	return goEnvironment, nil
}

// buildLocalExecutorDependencyInputs 只装配已声明的受信依赖；缺根、非目录或非规范路径立即失败。
func buildLocalExecutorDependencyInputs(needsGoSeed, needsEmbedSeed bool, goEnvironment localExecutorTrustedGoEnvironment, canonicalDependencyRoot func(string, string) (string, error), frontendEmbedRoot func() (string, error)) (LocalExecutorDependencyInputs, error) {
	inputs := LocalExecutorDependencyInputs{CGOEnabled: goEnvironment.CGOEnabled}
	if needsGoSeed {
		cacheRoot, err := canonicalDependencyRoot(goEnvironment.GoModuleCache, "gate-owned trusted Go module cache")
		if err != nil {
			return LocalExecutorDependencyInputs{}, err
		}
		if err := validateLocalDependencyRoot(cacheRoot, "Go module cache"); err != nil {
			return LocalExecutorDependencyInputs{}, err
		}
		inputs.GoModuleCache = cacheRoot
	}
	if needsEmbedSeed {
		embedRoot, err := frontendEmbedRoot()
		if err != nil {
			return LocalExecutorDependencyInputs{}, fmt.Errorf("resolve gate-owned frontend embed root: %w", err)
		}
		embedRoot, err = canonicalDependencyRoot(embedRoot, "gate-owned frontend embed root")
		if err != nil {
			return LocalExecutorDependencyInputs{}, err
		}
		if err := validateLocalDependencyRoot(embedRoot, "frontend embed"); err != nil {
			return LocalExecutorDependencyInputs{}, err
		}
		inputs.FrontendEmbedRoot = embedRoot
	}
	return inputs, nil
}

// validateLocalExecutorDependencyInputsDiscoveryHooks 要求全部发现职责存在，避免空 hook 静默跳过封存。
func validateLocalExecutorDependencyInputsDiscoveryHooks(hooks localExecutorDependencyInputsDiscoveryHooks) error {
	if hooks.eligibility == nil || hooks.program == nil || hooks.trustedGoBinary == nil || hooks.goEnvironment == nil || hooks.canonicalDependencyRoot == nil || hooks.frontendEmbedRoot == nil {
		return errors.New("local executor dependency input discovery hooks are incomplete")
	}
	return nil
}

// validateLocalExecutorDependencyWorkloads 校验可执行映射并归并种子需求；重复、漂移和零步骤立即拒绝。
func validateLocalExecutorDependencyWorkloads(workloadIDs []GateID, hooks localExecutorDependencyInputsDiscoveryHooks) (bool, bool, error) {
	ids := slices.Clone(workloadIDs)
	slices.Sort(ids)
	programs := make(map[GateID]ExecutorProgram, len(ids))
	for index, id := range ids {
		if index > 0 && id == ids[index-1] {
			return false, false, fmt.Errorf("duplicate local executor dependency workload %q", id)
		}
		eligibility, err := hooks.eligibility(id)
		if err != nil {
			return false, false, fmt.Errorf("evaluate local workload %q eligibility: %w", id, err)
		}
		if !eligibility.Eligible {
			return false, false, fmt.Errorf("local workload %q is ineligible: %s", id, eligibility.Reason)
		}
		canonicalID, program, err := hooks.program(id)
		if err != nil {
			return false, false, fmt.Errorf("resolve local workload %q program: %w", id, err)
		}
		if canonicalID != eligibility.CanonicalID || program.Strategy != eligibility.Strategy {
			return false, false, fmt.Errorf("local workload %q eligibility mapping drifted", id)
		}
		if len(program.Steps) == 0 {
			return false, false, fmt.Errorf("local workload %q is ineligible: local executor resolved zero command steps", id)
		}
		programs[id] = program
	}
	needsGoSeed, _, needsEmbedSeed := localReceiptDependencyNeeds(programs)
	return needsGoSeed, needsEmbedSeed, nil
}

//go:embed assets/frontend_embed_seed/index.html
var localExecutorFrontendEmbedFS embed.FS

// localExecutorFrontendEmbedRoot materializes the gate-compiled placeholder
// asset per sealed receipt. It never reads caller PATH, HOME, cwd, or a caller-supplied path.
func localExecutorFrontendEmbedRoot() (string, error) {
	return materializeLocalExecutorFrontendEmbedSeed()
}

// materializeLocalExecutorFrontendEmbedSeed 将编译资产写入私有只读根；读写或权限封存失败立即清理并返回。
func materializeLocalExecutorFrontendEmbedSeed() (string, error) {
	seed, err := localExecutorFrontendEmbedFS.ReadFile("assets/frontend_embed_seed/index.html")
	if err != nil || len(seed) == 0 {
		return "", errors.New("gate-owned frontend embed seed is unavailable")
	}
	root, err := os.MkdirTemp("", "super-dolphin-frontend-embed-seed-")
	if err != nil {
		return "", err
	}
	fail := func(cause error) (string, error) { return "", errors.Join(cause, os.RemoveAll(root)) }
	if err := os.WriteFile(filepath.Join(root, "index.html"), seed, 0o400); err != nil {
		return fail(err)
	}
	if err := os.Chmod(root, 0o500); err != nil {
		return fail(err)
	}
	return root, nil
}

// localExecutorTrustedGoRoot 只接受编译上下文固定 Go 根；路径、解析或目录属性异常立即拒绝。
//
// localExecutorTrustedGoRoot proves the fixed Go root bound into the running
// gate's build context. It never reads caller PATH, HOME, or runtime GOROOT.
func localExecutorTrustedGoRoot() (string, error) {
	goRoot := build.Default.GOROOT
	if !filepath.IsAbs(goRoot) || filepath.Clean(goRoot) != goRoot {
		return "", errors.New("running gate build-context Go root is not canonical")
	}
	resolved, err := filepath.EvalSymlinks(goRoot)
	if err != nil {
		return "", fmt.Errorf("canonicalize running gate build-context Go root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat running gate build-context Go root: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("running gate build-context Go root is not a directory")
	}
	return resolved, nil
}

// localExecutorTrustedGoBinary resolves Go only from the gate-owned fixed root.
// It deliberately never consults the caller PATH or HOME.
func localExecutorTrustedGoBinary() (string, error) {
	goRoot, err := localExecutorTrustedGoRoot()
	if err != nil {
		return "", err
	}
	return resolveReceiptToolPath(filepath.Join(goRoot, "bin", "go"))
}

// localExecutorTrustedGoEnvironmentForBinary 通过已验证 Go 读取受限环境；取消、路径或严格 JSON 漂移立即拒绝。
func localExecutorTrustedGoEnvironmentForBinary(ctx context.Context, goBinary string) (localExecutorTrustedGoEnvironment, error) {
	if ctx == nil {
		return localExecutorTrustedGoEnvironment{}, errors.New("trusted Go environment context is required")
	}
	if err := ctx.Err(); err != nil {
		return localExecutorTrustedGoEnvironment{}, err
	}
	if _, err := resolveReceiptToolPath(goBinary); err != nil {
		return localExecutorTrustedGoEnvironment{}, fmt.Errorf("trusted Go binary: %w", err)
	}
	command := exec.CommandContext(ctx, goBinary, "env", "-json", "GOMODCACHE", "CGO_ENABLED")
	output, err := command.Output()
	if err != nil {
		return localExecutorTrustedGoEnvironment{}, fmt.Errorf("trusted Go env: %w", err)
	}
	var environment localExecutorTrustedGoEnvironment
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&environment); err != nil {
		return localExecutorTrustedGoEnvironment{}, fmt.Errorf("decode trusted Go environment: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return localExecutorTrustedGoEnvironment{}, errors.New("trusted Go environment has trailing JSON values")
	}
	return environment, nil
}
