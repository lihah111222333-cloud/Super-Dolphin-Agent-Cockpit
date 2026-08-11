package gate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"golang.org/x/mod/modfile"
)

func prepareLocalReceiptPrograms(searchPath, sourceRoot string, programs map[GateID]ExecutorProgram, self TrustedSelfBinary) (map[GateID]localExecutorProgramProof, error) {
	proofs := make(map[GateID]localExecutorProgramProof, len(programs))
	for id, program := range programs {
		steps, err := prepareLocalExecutorStepsWithSelf(searchPath, sourceRoot, program, self)
		if err != nil {
			return nil, fmt.Errorf("prepare local receipt workload %q: %w", id, err)
		}
		proofs[id] = localReceiptProgramProof(id, sourceRoot, steps, program.RequiredExecutables)
	}
	return proofs, nil
}

func localReceiptProgramProof(id GateID, sourceRoot string, steps []resolvedStep, required []string) localExecutorProgramProof {
	proof := localExecutorProgramProof{id: id}
	for _, step := range steps {
		proof.steps = append(proof.steps, localExecutorStepProof{directory: relativeReceiptPath(step.directory, sourceRoot), argv: append([]string(nil), step.argv...), binary: semanticToolName(step.binary)})
		proof.toolNames = append(proof.toolNames, semanticToolName(step.binary))
	}
	for _, path := range required {
		proof.toolNames = append(proof.toolNames, semanticToolName(path))
	}
	sort.Strings(proof.toolNames)
	return proof
}

func localReceiptEnvironments(host LocalWorkloadPassHostContext, programs map[GateID]ExecutorProgram, self TrustedSelfBinary) (map[GateID]LocalWorkloadPassEnvironment, error) {
	environments := make(map[GateID]LocalWorkloadPassEnvironment, len(programs))
	for id, program := range programs {
		flags, err := ExecutorProgramGoFlags(program)
		if err != nil {
			return nil, fmt.Errorf("local receipt workload %q GoFlags: %w", id, err)
		}
		runnerDigest, err := localReceiptRunnerSemanticDigest(host.RunnerSemanticDigest, program, self)
		if err != nil {
			return nil, fmt.Errorf("local receipt workload %q runner semantic proof: %w", id, err)
		}
		environment := localReceiptEnvironment(host, flags)
		environment.RunnerSemanticDigest = runnerDigest
		environments[id] = environment
	}
	return environments, nil
}

// localReceiptRunnerSemanticDigest binds the receipt self proof only to a
// canonical program that actually resolves the gate executable as a step.
func localReceiptRunnerSemanticDigest(base string, program ExecutorProgram, self TrustedSelfBinary) (string, error) {
	if !localReceiptProgramRequiresTrustedSelf(program) {
		return base, nil
	}
	if _, err := self.VerifiedPath(); err != nil {
		return "", fmt.Errorf("verify trusted self binary: %w", err)
	}
	return digestCanonicalJSON(cicontract.LocalRunnerSelfSemanticDomain, struct {
		RunnerSemanticDigest string                   `json:"runner_semantic_digest"`
		Self                 localExecutorSelfPayload `json:"self"`
	}{
		RunnerSemanticDigest: base,
		Self: localExecutorSelfPayload{
			Name: trustedSelfBinaryLogicalName, Digest: self.digest, Version: self.version,
		},
	})
}

func localReceiptProgramRequiresTrustedSelf(program ExecutorProgram) bool {
	for _, step := range program.Steps {
		if len(step.Argv) != 0 && step.Argv[0] == ExecutorSelfCommandName {
			return true
		}
	}
	return false
}

// buildLocalExecutorReceipt 组装经精确树、工具、源码与依赖证明绑定的本地执行回执。
func buildLocalExecutorReceipt(ctx context.Context, repositoryRoot, tree string, authority CandidateObjectAuthority, workloadIDs []GateID, programs map[GateID]ExecutorProgram, dependencies LocalExecutorDependencyInputs) (LocalExecutorSessionReceipt, error) {
	bound, err := prepareLocalReceiptBoundMaterial(ctx, repositoryRoot, tree, authority, programs)
	if err != nil {
		return nil, err
	}
	tree, err = verifyGitTreeObject(ctx, bound.trustedGit, repositoryRoot, tree)
	if err != nil {
		return nil, err
	}
	trustedGo, err := trustedGoBinaryFromProofs(bound.tools)
	if err != nil {
		return nil, err
	}
	dependencyProofs, err := verifyLocalReceiptDependencies(ctx, bound.trustedGit, trustedGo, repositoryRoot, tree, programs, dependencies)
	if err != nil {
		return nil, err
	}
	host, err := localReceiptHostContext(bound.tools, bound.sources, dependencyProofs)
	if err != nil {
		return nil, err
	}
	environments, err := localReceiptEnvironments(host, programs, bound.self)
	if err != nil {
		return nil, err
	}
	authorityDigest, err := authority.Digest()
	if err != nil {
		return nil, fmt.Errorf("verify local executor receipt candidate object authority: %w", err)
	}
	_, digest, err := encodeLocalExecutorReceiptPayload(host, authorityDigest, bound.tools, bound.self, bound.sources, dependencyProofs, bound.programs, environments)
	if err != nil {
		return nil, err
	}
	ids := append([]GateID(nil), workloadIDs...)
	slices.Sort(ids)
	return &localExecutorSessionReceipt{repositoryRoot: repositoryRoot, exactTreeSHA: tree, workloadIDs: ids, environments: environments, host: host, toolPath: bound.toolPath, tools: bound.tools, self: bound.self, sources: bound.sources, dependencies: dependencyProofs, programs: bound.programs, candidateObjectAuthority: authority, digest: digest}, nil
}

type localReceiptBoundMaterial struct {
	toolPath   string
	trustedGit TrustedGitBinary
	tools      []localExecutorToolProof
	sources    []localExecutorSourceProof
	programs   map[GateID]localExecutorProgramProof
	self       TrustedSelfBinary
}

// prepareLocalReceiptBoundMaterial 收集工具、源码和程序证明，作为 receipt 的不可变绑定材料。
func prepareLocalReceiptBoundMaterial(ctx context.Context, repositoryRoot, tree string, authority CandidateObjectAuthority, programs map[GateID]ExecutorProgram) (localReceiptBoundMaterial, error) {
	self, err := resolveTrustedSelfBinary()
	if err != nil {
		return localReceiptBoundMaterial{}, err
	}
	toolPath, tools, err := discoverLocalReceiptTools(ctx, programs)
	if err != nil {
		return localReceiptBoundMaterial{}, err
	}
	trustedGit, err := trustedGitBinaryFromProofs(tools)
	if err != nil {
		return localReceiptBoundMaterial{}, err
	}
	trustedGit, err = trustedGit.withCandidateObjectAuthority(authority)
	if err != nil {
		return localReceiptBoundMaterial{}, fmt.Errorf("verify local executor receipt candidate object authority: %w", err)
	}
	if err := verifyLocalReceiptLockFiles(ctx, trustedGit, repositoryRoot, tree, programs); err != nil {
		return localReceiptBoundMaterial{}, err
	}
	if _, err := localNetworkSandboxPath(); err != nil {
		return localReceiptBoundMaterial{}, err
	}
	programProofs, err := prepareLocalReceiptPrograms(toolPath, repositoryRoot, programs, self)
	if err != nil {
		return localReceiptBoundMaterial{}, err
	}
	sources, err := readLocalReceiptRunnerSources(ctx, trustedGit, repositoryRoot, tree)
	if err != nil {
		return localReceiptBoundMaterial{}, err
	}
	return localReceiptBoundMaterial{toolPath: toolPath, trustedGit: trustedGit, tools: tools, sources: sources, programs: programProofs, self: self}, nil
}

// verifyGoModuleCacheOffline 在禁网条件下验证 Go module cache 与精确锁文件可用。
func verifyGoModuleCacheOffline(ctx context.Context, trustedGit TrustedGitBinary, trustedGo TrustedGoBinary, repositoryRoot, tree, cacheRoot string, mod, sum []byte) (string, []localExecutorLockedFile, error) {
	replaces, err := localReceiptGoReplacePaths(mod)
	if err != nil {
		return "", nil, err
	}
	temp, err := os.MkdirTemp("", "local-go-verify-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(temp)
	if err := os.WriteFile(filepath.Join(temp, "go.mod"), mod, 0o600); err != nil {
		return "", nil, err
	}
	if err := os.WriteFile(filepath.Join(temp, "go.sum"), sum, 0o600); err != nil {
		return "", nil, err
	}
	lockFiles := []localExecutorLockedFile{{path: "go.mod", digest: digestBytes(mod)}, {path: "go.sum", digest: digestBytes(sum)}}
	for _, replace := range replaces {
		files, err := materializeExactTreeLocalReplace(ctx, trustedGit, repositoryRoot, tree, temp, replace)
		if err != nil {
			return "", nil, err
		}
		lockFiles = append(lockFiles, files...)
	}
	goBinary, err := trustedGo.VerifiedPath()
	if err != nil {
		return "", nil, err
	}
	command := exec.CommandContext(ctx, goBinary, "mod", "verify")
	command.Dir = temp
	command.Env = []string{"PATH=" + filepath.Dir(goBinary), "GOMODCACHE=" + cacheRoot, "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local"}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("offline go mod verify failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return digestBytes(output), lockFiles, nil
}

// localReceiptGoReplacePaths 解析 exact go.mod 的本地 replace，并拒绝重复目标以保持物化唯一。
func localReceiptGoReplacePaths(mod []byte) ([]string, error) {
	parsed, err := modfile.Parse("go.mod", mod, nil)
	if err != nil {
		return nil, fmt.Errorf("parse exact tree go.mod for local replacements: %w", err)
	}
	paths := make([]string, 0)
	seen := make(map[string]struct{})
	for _, replacement := range parsed.Replace {
		if replacement.New.Version != "" {
			continue
		}
		replacePath, err := localReceiptGoReplacePath(replacement.New.Path)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[replacePath]; duplicate {
			return nil, fmt.Errorf("exact tree go.mod has duplicate local replacement path %q", replacePath)
		}
		seen[replacePath] = struct{}{}
		paths = append(paths, replacePath)
	}
	sort.Strings(paths)
	return paths, nil
}

// localReceiptGoReplacePath 仅接受显式 ./ 仓库内 replace，避免 receipt 读取宿主任意路径。
func localReceiptGoReplacePath(value string) (string, error) {
	if value == "" || strings.Contains(value, "\\") || path.IsAbs(value) || filepath.IsAbs(value) || !strings.HasPrefix(value, "./") {
		return "", fmt.Errorf("exact tree go.mod local replacement path %q is invalid", value)
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("exact tree go.mod local replacement path %q escapes the repository", value)
	}
	return clean, nil
}

// materializeExactTreeLocalReplace 将 replace 的完整 exact-tree 子树写入临时根，并把每个文件绑定进 lock proof。
func materializeExactTreeLocalReplace(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree, temporaryRoot, replacePath string) ([]localExecutorLockedFile, error) {
	files, err := gitTreeRegularFiles(ctx, trustedGit, repositoryRoot, tree, replacePath)
	if err != nil {
		return nil, fmt.Errorf("read exact tree local replacement %q: %w", replacePath, err)
	}
	if len(files) < 2 {
		return nil, fmt.Errorf("exact tree local replacement %q must contain go.mod and source files", replacePath)
	}
	hasMod, hasSource := false, false
	locks := make([]localExecutorLockedFile, 0, len(files))
	for _, file := range files {
		isModuleFile, lock, err := materializeExactTreeLocalReplaceFile(temporaryRoot, replacePath, file)
		if err != nil {
			return nil, err
		}
		hasMod = hasMod || isModuleFile
		hasSource = hasSource || !isModuleFile
		locks = append(locks, lock)
	}
	if !hasMod || !hasSource {
		return nil, fmt.Errorf("exact tree local replacement %q must contain go.mod and source files", replacePath)
	}
	return locks, nil
}

func materializeExactTreeLocalReplaceFile(temporaryRoot, replacePath string, file gitTreeRegularFile) (bool, localExecutorLockedFile, error) {
	target, err := localReceiptJoinRelativePath(temporaryRoot, file.path)
	if err != nil {
		return false, localExecutorLockedFile{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return false, localExecutorLockedFile{}, err
	}
	mode := os.FileMode(0o600)
	if file.executable {
		mode = 0o700
	}
	if err := os.WriteFile(target, file.content, mode); err != nil {
		return false, localExecutorLockedFile{}, err
	}
	return file.path == replacePath+"/go.mod", localExecutorLockedFile{path: file.path, digest: digestBytes(file.content)}, nil
}

func localReceiptGoLockDigest(lockFiles []localExecutorLockedFile) (string, error) {
	digests := make([]string, 0, len(lockFiles))
	for _, lock := range lockFiles {
		if lock.path == "" || !isPrefixedSHA256Digest(lock.digest) {
			return "", errors.New("local Go dependency proof contains an invalid lock file")
		}
		digests = append(digests, lock.digest)
	}
	sort.Strings(digests)
	return digestCanonicalJSON(cicontract.LocalDependencyContentDomain, digests)
}

// verifyFrontendLockOffline 在禁网条件下验证前端 npm 缓存与精确锁文件可用。
func verifyFrontendLockOffline(ctx context.Context, npmCache string, packageJSON, lock []byte) (string, error) {
	temp, err := os.MkdirTemp("", "local-npm-verify-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temp)
	if err := os.WriteFile(filepath.Join(temp, "package.json"), packageJSON, 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(temp, "package-lock.json"), lock, 0o600); err != nil {
		return "", err
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		return "", fmt.Errorf("npm is required for strict frontend lock verification: %w", err)
	}
	npm, err = resolveReceiptToolPath(npm)
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, npm, "ci", "--offline", "--ignore-scripts", "--no-audit", "--no-fund", "--dry-run")
	command.Dir = temp
	command.Env = []string{"PATH=" + filepath.Dir(npm), "npm_config_cache=" + npmCache, "npm_config_offline=true", "npm_config_prefer_offline=true", "npm_config_audit=false", "npm_config_fund=false"}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("offline npm lock verification failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return digestBytes(output), nil
}

// localReceiptHostContext 构造只含路径无关执行语义的冻结主机上下文。
func localReceiptHostContext(tools []localExecutorToolProof, sources []localExecutorSourceProof, dependencies []localExecutorDependencyProof) (LocalWorkloadPassHostContext, error) {
	goPath, err := localReceiptGoPath(tools)
	if err != nil {
		return LocalWorkloadPassHostContext{}, err
	}
	goEnv, err := localReceiptGoEnvironment(goPath)
	if err != nil {
		return LocalWorkloadPassHostContext{}, err
	}
	osBuild, err := localReceiptOSBuild()
	if err != nil {
		return LocalWorkloadPassHostContext{}, err
	}
	toolchainDigest, err := digestCanonicalJSON(cicontract.LocalToolchainClosureDomain, struct {
		Tools []localExecutorToolPayload       `json:"tools"`
		Deps  []localExecutorDependencyPayload `json:"dependencies"`
		Env   map[string]string                `json:"env"`
	}{Tools: toolPayloads(tools), Deps: dependencyPayloads(dependencies), Env: goEnv})
	if err != nil {
		return LocalWorkloadPassHostContext{}, err
	}
	runnerDigest, err := localRunnerSemanticSourceDigest(sources, tools)
	if err != nil {
		return LocalWorkloadPassHostContext{}, err
	}
	return LocalWorkloadPassHostContext{Platform: goEnv["GOOS"] + "/" + goEnv["GOARCH"], GOOS: goEnv["GOOS"], GOARCH: goEnv["GOARCH"], GOAMD64: goEnv["GOAMD64"], GOARM64: goEnv["GOARM64"], CGOEnabled: goEnv["CGO_ENABLED"], GOEXPERIMENT: goEnv["GOEXPERIMENT"], CC: goEnv["CC"], CXX: goEnv["CXX"], SDK: goEnv["SDK"], OSBuild: osBuild, GoVersion: goEnv["GOVERSION"], ToolchainClosureDigest: toolchainDigest, RunnerSemanticPolicy: LocalWorkloadRunnerSemanticPolicy, RunnerSemanticDigest: runnerDigest}, nil
}

func localRunnerSemanticSourceDigest(sources []localExecutorSourceProof, tools []localExecutorToolProof) (string, error) {
	return digestCanonicalJSON(cicontract.LocalRunnerSourceClosureDomain, struct {
		Sources []localExecutorSourcePayload `json:"sources"`
		Tools   []localExecutorToolPayload   `json:"tools"`
	}{Sources: sourcePayloads(sources), Tools: toolPayloads(tools)})
}

func localReceiptGoPath(tools []localExecutorToolProof) (string, error) {
	for _, tool := range tools {
		if tool.name == "go" {
			return tool.path, nil
		}
	}
	return "", errors.New("local receipt Go tool proof is missing")
}

func localReceiptGoEnvironment(goPath string) (map[string]string, error) {
	values := make(map[string]string)
	for _, key := range []string{"GOOS", "GOARCH", "GOAMD64", "GOARM64", "CGO_ENABLED", "GOEXPERIMENT", "CC", "CXX", "SDK", "GOROOT", "GOVERSION"} {
		value, err := localGoEnvValue(goPath, key)
		if err != nil && !localReceiptOptionalGoEnvKey(key) {
			return nil, err
		}
		values[key] = value
	}
	return values, nil
}

func localReceiptOptionalGoEnvKey(key string) bool {
	return key == "GOAMD64" || key == "GOARM64" || key == "GOEXPERIMENT" || key == "SDK"
}

func localReceiptOSBuild() (string, error) {
	if runtime.GOOS != "darwin" {
		return runtime.GOOS + "/" + runtime.GOARCH, nil
	}
	output, err := exec.Command("/usr/bin/sw_vers", "-buildVersion").Output()
	if err != nil {
		return "", fmt.Errorf("read macOS build identity: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func localReceiptEnvironment(host LocalWorkloadPassHostContext, flags string) LocalWorkloadPassEnvironment {
	return LocalWorkloadPassEnvironment{Platform: host.Platform, GOOS: host.GOOS, GOARCH: host.GOARCH, GOAMD64: host.GOAMD64, GOARM64: host.GOARM64, CGOEnabled: host.CGOEnabled, GOEXPERIMENT: host.GOEXPERIMENT, CC: host.CC, CXX: host.CXX, SDK: host.SDK, OSBuild: host.OSBuild, GoVersion: host.GoVersion, GoFlags: flags, ToolchainClosureDigest: host.ToolchainClosureDigest, RunnerSemanticPolicy: host.RunnerSemanticPolicy, BaseRunnerSemanticDigest: host.RunnerSemanticDigest, RunnerSemanticDigest: host.RunnerSemanticDigest}
}

func encodeLocalExecutorReceiptPayload(host LocalWorkloadPassHostContext, authorityDigest string, tools []localExecutorToolProof, self TrustedSelfBinary, sources []localExecutorSourceProof, dependencies []localExecutorDependencyProof, programs map[GateID]localExecutorProgramProof, environments map[GateID]LocalWorkloadPassEnvironment) ([]byte, string, error) {
	ids := make([]GateID, 0, len(programs))
	for id := range programs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	programPayloads := make([]localExecutorProgramPayload, 0, len(ids))
	environmentPayloads := make([]localExecutorEnvironmentPayload, 0, len(ids))
	for _, id := range ids {
		program := programs[id]
		steps := make([]localExecutorStepPayload, 0, len(program.steps))
		for _, step := range program.steps {
			steps = append(steps, localExecutorStepPayload{Directory: step.directory, Argv: append([]string(nil), step.argv...), Binary: step.binary})
		}
		programPayloads = append(programPayloads, localExecutorProgramPayload{WorkloadID: id, Steps: steps, Tools: append([]string(nil), program.toolNames...)})
		environmentPayloads = append(environmentPayloads, localExecutorEnvironmentPayload{WorkloadID: id, Environment: environments[id]})
	}
	payload := localExecutorSessionReceiptPayload{SchemaVersion: cicontract.LocalExecutorSessionReceiptSchemaVersion, Domain: cicontract.LocalExecutorSessionReceiptDomain, Host: host, Tools: toolPayloads(tools), Self: localExecutorSelfPayload{Name: trustedSelfBinaryLogicalName, Digest: self.digest, Version: self.version}, Sources: sourcePayloads(sources), Dependencies: dependencyPayloads(dependencies), Programs: programPayloads, Environments: environmentPayloads, CandidateObjectAuthorityDigest: authorityDigest}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	return encoded, digestBytes(encoded), nil
}

func toolPayloads(tools []localExecutorToolProof) []localExecutorToolPayload {
	result := make([]localExecutorToolPayload, 0, len(tools))
	for _, tool := range tools {
		result = append(result, localExecutorToolPayload{Name: tool.name, Digest: tool.digest, Version: tool.version})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return result
}

func sourcePayloads(sources []localExecutorSourceProof) []localExecutorSourcePayload {
	result := make([]localExecutorSourcePayload, 0, len(sources))
	for _, source := range sources {
		result = append(result, localExecutorSourcePayload{Digest: source.digest})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Digest < result[right].Digest })
	return result
}

func dependencyPayloads(dependencies []localExecutorDependencyProof) []localExecutorDependencyPayload {
	result := make([]localExecutorDependencyPayload, 0, len(dependencies))
	for _, dependency := range dependencies {
		result = append(result, localExecutorDependencyPayload{Name: dependency.name, LockDigest: dependency.lockDigest, ContentDigest: dependency.contentDigest, Verification: dependency.verification})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return result
}

func reverifyLocalReceiptSources(root string, sources []localExecutorSourceProof) error {
	for _, source := range sources {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(source.path)))
		if err != nil {
			return fmt.Errorf("reverify local runner source %q: %w", source.path, err)
		}
		if got := digestBytes(content); got != source.digest {
			return fmt.Errorf("local runner source %q drifted", source.path)
		}
	}
	return nil
}

// reverifyLocalReceiptSourcesForLookup recomputes the source closure from the
// receipt-bound exact tree. Lookup is deliberately independent of an ambient
// worktree because a valid staged candidate can exist only in its private ODB.
func reverifyLocalReceiptSourcesForLookup(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree string, sources []localExecutorSourceProof) error {
	actual, err := readLocalReceiptRunnerSources(ctx, trustedGit, repositoryRoot, tree)
	if err != nil {
		return fmt.Errorf("reverify local runner source closure from exact tree: %w", err)
	}
	if !slices.EqualFunc(actual, sources, func(left, right localExecutorSourceProof) bool {
		return left == right
	}) {
		return errors.New("local runner source exact-tree proof drifted")
	}
	return nil
}

func reverifyLocalReceiptTools(tools []localExecutorToolProof) error {
	for _, tool := range tools {
		digest, err := fileSHA256(tool.path)
		if err != nil {
			return err
		}
		if digest != tool.digest {
			return fmt.Errorf("local executor tool %q content drifted", tool.name)
		}
	}
	return nil
}

func reverifyLocalReceiptDependencies(root string, dependencies []localExecutorDependencyProof) error {
	for _, dependency := range dependencies {
		if err := reverifyLocalReceiptDependency(root, dependency); err != nil {
			return err
		}
	}
	return nil
}

// reverifyLocalReceiptDependenciesForLookup keeps host dependency-content
// proof checks, but reads every repository lock through the exact Git tree.
func reverifyLocalReceiptDependenciesForLookup(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree string, dependencies []localExecutorDependencyProof) error {
	for _, dependency := range dependencies {
		if err := validateLocalDependencyRoot(dependency.root, dependency.name); err != nil {
			return err
		}
		if err := reverifyLocalReceiptDependencyContent(dependency); err != nil {
			return err
		}
		for _, lock := range dependency.lockFiles {
			content, err := gitTreeBlob(ctx, trustedGit, repositoryRoot, tree, lock.path)
			if err != nil {
				return fmt.Errorf("reverify local dependency exact-tree lock %q: %w", lock.path, err)
			}
			if digestBytes(content) != lock.digest {
				return fmt.Errorf("local dependency exact-tree lock %q drifted", lock.path)
			}
		}
	}
	return nil
}

func reverifyLocalReceiptDependency(root string, dependency localExecutorDependencyProof) error {
	if err := validateLocalDependencyRoot(dependency.root, dependency.name); err != nil {
		return err
	}
	if err := reverifyLocalReceiptDependencyContent(dependency); err != nil {
		return err
	}
	return reverifyLocalReceiptLocks(root, dependency)
}

// reverifyLocalReceiptDependencyContent 仅重哈希 receipt-bound dependency files，不将共享缓存全量遍历放入每个 workload。
func reverifyLocalReceiptDependencyContent(dependency localExecutorDependencyProof) error {
	if len(dependency.contentFiles) == 0 {
		return fmt.Errorf("reverify local %s dependency has no sealed content manifest", dependency.name)
	}
	if dependency.name == "frontend-embed" {
		return reverifyLocalReceiptDependencyTreeManifest(dependency)
	}
	content := make([]localExecutorDependencyContentFile, 0, len(dependency.contentFiles))
	for _, expected := range dependency.contentFiles {
		current, err := reverifyLocalReceiptDependencyContentFile(dependency.root, expected)
		if err != nil {
			return err
		}
		content = append(content, current)
	}
	digest, err := digestCanonicalJSON(cicontract.LocalDependencyContentDomain, content)
	if err != nil {
		return err
	}
	if digest != dependency.contentDigest {
		return fmt.Errorf("reverify local %s dependency content digest drifted", dependency.name)
	}
	return nil
}

// reverifyLocalReceiptDependencyTreeManifest dynamically re-enumerates an
// entire sealed tree, rejecting missing, extra, mode, and content drift.
func reverifyLocalReceiptDependencyTreeManifest(dependency localExecutorDependencyProof) error {
	current, digest, err := localReceiptDependencyContentManifest(dependency.root, []string{dependency.root})
	if err != nil {
		return fmt.Errorf("reverify local %s dependency manifest: %w", dependency.name, err)
	}
	if !slices.EqualFunc(current, dependency.contentFiles, func(left, right localExecutorDependencyContentFile) bool { return left == right }) {
		return fmt.Errorf("reverify local %s dependency manifest drifted", dependency.name)
	}
	if digest != dependency.contentDigest {
		return fmt.Errorf("reverify local %s dependency content digest drifted", dependency.name)
	}
	return nil
}

// reverifyLocalReceiptDependencyContentFile 复核单个 dependency file 的相对路径、模式和内容摘要。
func reverifyLocalReceiptDependencyContentFile(root string, expected localExecutorDependencyContentFile) (localExecutorDependencyContentFile, error) {
	path, err := localReceiptJoinRelativePath(root, expected.path)
	if err != nil {
		return localExecutorDependencyContentFile{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return localExecutorDependencyContentFile{}, fmt.Errorf("reverify local dependency content %q: %w", expected.path, err)
	}
	if !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != expected.mode {
		return localExecutorDependencyContentFile{}, fmt.Errorf("reverify local dependency content %q metadata drifted", expected.path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return localExecutorDependencyContentFile{}, fmt.Errorf("reverify local dependency content %q: %w", expected.path, err)
	}
	actual := localExecutorDependencyContentFile{path: expected.path, digest: digestBytes(content), mode: expected.mode}
	if actual.digest != expected.digest {
		return localExecutorDependencyContentFile{}, fmt.Errorf("reverify local dependency content %q drifted", expected.path)
	}
	return actual, nil
}

func reverifyLocalReceiptLocks(root string, dependency localExecutorDependencyProof) error {
	for _, lock := range dependency.lockFiles {
		path, err := localReceiptJoinRelativePath(root, lock.path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reverify local dependency lock %q: %w", lock.path, err)
		}
		if digestBytes(content) != lock.digest {
			return fmt.Errorf("local dependency lock %q drifted", lock.path)
		}
	}
	return nil
}

// contentDigestTree 对依赖目录内容生成稳定摘要，忽略文件系统路径本身。
func contentDigestTree(root string) (string, error) {
	canonical, err := canonicalReceiptRepositoryRoot(root)
	if err != nil {
		return "", err
	}
	type entry struct {
		Path string `json:"path"`
		Data string `json:"data"`
		Mode uint32 `json:"mode"`
	}
	entries := make([]entry, 0)
	err = filepath.WalkDir(canonical, func(path string, directory os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if directory.IsDir() {
			return nil
		}
		if directory.Type()&os.ModeSymlink != 0 {
			return errors.New("dependency content tree contains a symlink")
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := directory.Info()
		if err != nil {
			return err
		}
		entries = append(entries, entry{Path: relativeReceiptPath(path, canonical), Data: hex.EncodeToString(content), Mode: uint32(info.Mode().Perm())})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("digest dependency content tree: %w", err)
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Path < entries[right].Path })
	return digestCanonicalJSON(cicontract.LocalDependencyContentDomain, entries)
}

func digestCanonicalJSON(domain string, value any) (string, error) {
	encoded, err := json.Marshal(struct {
		Domain string `json:"domain"`
		Value  any    `json:"value"`
	}{Domain: domain, Value: value})
	if err != nil {
		return "", err
	}
	return digestBytes(encoded), nil
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func relativeReceiptPath(path, root string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.Base(path)
	}
	return filepath.ToSlash(relative)
}

func semanticToolName(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}
