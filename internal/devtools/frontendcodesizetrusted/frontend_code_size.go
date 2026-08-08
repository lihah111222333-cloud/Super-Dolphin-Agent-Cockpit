// Package frontendcodesizetrusted 在精确 Git tree 上执行前端代码规模守卫。
package frontendcodesizetrusted

import (
	"archive/tar"
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

const (
	productionBaseline = ".frontend_code_size_guard_baseline.json"
	testBaseline       = ".frontend_code_size_guard_baseline_test.json"
)

var exactTreePattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

//go:embed assets/scripts/*.mjs assets/scripts/lib/*.mjs
var trustedRuntimeAssets embed.FS

// Operation 表示受信任候选树执行的操作。
type Operation string

const (
	// Check 重放 accepted 基线，并校验候选声明与 production/test 规范结果一致。
	Check Operation = "check"
	// Refresh 在候选树刷新两个基线并校验后原子替换仓库基线。
	Refresh Operation = "refresh"
	// Migrate 以候选依赖闭包校验预算并原子刷新两个基线。
	Migrate Operation = "migrate"
)

// ErrorKind 区分调用输入、基础设施和候选违规错误。
type ErrorKind string

const (
	// ErrorInput 表示 CLI 输入或受信任边界无效。
	ErrorInput ErrorKind = "input"
	// ErrorInfrastructure 表示 Git、文件系统或工具链不可用。
	ErrorInfrastructure ErrorKind = "infrastructure"
	// ErrorViolation 表示候选树未通过 canonical guard。
	ErrorViolation ErrorKind = "violation"
)

// Error 保留调用方可分类的失败原因。
type Error struct {
	Kind ErrorKind
	Err  error
}

// Error 返回稳定的错误分类前缀。
func (e *Error) Error() string { return fmt.Sprintf("frontend code size %s: %v", e.Kind, e.Err) }

// Unwrap 返回底层失败原因。
func (e *Error) Unwrap() error { return e.Err }

// Run 只从 exact tree 解包的临时候选树运行 canonical 前端代码规模守卫。
func Run(ctx context.Context, repository, tree string, operation Operation) error {
	if err := validateRequest(tree, operation); err != nil {
		return err
	}
	return classified(ErrorInput, "accepted baseline tree must be explicit", nil)
}

// RunWithAcceptedBaseline runs an exact candidate against an explicitly named accepted tree.
func RunWithAcceptedBaseline(ctx context.Context, repository, tree, acceptedTree string, operation Operation) error {
	_, err := RunWithAcceptedBaselineReceipt(ctx, repository, tree, acceptedTree, operation)
	return err
}

// RunWithAcceptedBaselineReceipt runs the guard and returns its immutable execution identity.
func RunWithAcceptedBaselineReceipt(ctx context.Context, repository, tree, acceptedTree string, operation Operation) (Receipt, error) {
	return runWithAssetsReceipt(ctx, repository, tree, acceptedTree, operation, trustedRuntimeAssets)
}

func runWithAssets(
	ctx context.Context,
	repository, tree, acceptedTree string,
	operation Operation,
	assets fs.FS,
) error {
	_, err := runWithAssetsReceipt(ctx, repository, tree, acceptedTree, operation, assets)
	return err
}

func runWithAssetsReceipt(
	ctx context.Context,
	repository, tree, acceptedTree string,
	operation Operation,
	assets fs.FS,
) (Receipt, error) {
	if err := validateRequest(tree, operation); err != nil || !exactTreePattern.MatchString(acceptedTree) {
		if err == nil {
			err = classified(ErrorInput, "--accepted-tree must be an exact Git tree SHA", nil)
		}
		return Receipt{}, err
	}
	root, err := resolveTree(ctx, repository, tree)
	if err != nil {
		return Receipt{}, err
	}
	candidate, err := materializeCandidate(ctx, root, tree)
	if err != nil {
		return Receipt{}, err
	}
	defer os.RemoveAll(candidate)
	appRoot, candidateBaselines, err := prepareBaselineWorkspace(ctx, root, candidate, acceptedTree)
	if err != nil {
		return Receipt{}, err
	}
	candidateLock, lockErr := os.ReadFile(filepath.Join(appRoot, "package-lock.json"))
	candidateManifest, candidateErr := candidateClosure(appRoot)
	if lockErr != nil || candidateErr != nil {
		return Receipt{}, classified(ErrorViolation, "candidate execution closure is invalid", errors.Join(lockErr, candidateErr))
	}
	manifest, lock := candidateManifest, candidateLock
	if operation != Migrate {
		acceptedManifest, acceptedLock, err := acceptedClosure(ctx, root, acceptedTree)
		if err != nil {
			return Receipt{}, classified(ErrorViolation, "load accepted execution closure", err)
		}
		if sha256Bytes(candidateLock) != sha256Bytes(acceptedLock) ||
			candidateManifest.PackageLockSHA256 != acceptedManifest.PackageLockSHA256 ||
			candidateManifest.GeneratorSHA256 != acceptedManifest.GeneratorSHA256 ||
			candidateManifest.ClosureSHA256 != acceptedManifest.ClosureSHA256 {
			return Receipt{}, classified(ErrorViolation, "candidate package-lock differs from accepted execution closure", nil)
		}
		manifest, lock = acceptedManifest, acceptedLock
	}
	seed, err := resolveSharedNodeModules(ctx, root)
	if err != nil {
		return Receipt{}, err
	}
	if err := verifyParserClosure(seed, manifest); err != nil {
		return Receipt{}, classified(ErrorInfrastructure, "verify accepted parser closure before execution", err)
	}
	runtimeRoot, privateClosure, err := materializeTrustedRuntime(seed, manifest, assets)
	if err != nil {
		return Receipt{}, err
	}
	defer os.RemoveAll(runtimeRoot)
	node, nodeVersion, nodePlatform, err := trustedNodeIdentity(ctx)
	if err != nil {
		return Receipt{}, classified(ErrorInfrastructure, "resolve Node execution identity", err)
	}
	before, err := receiptFor(tree, acceptedTree, node, nodeVersion, nodePlatform, lock, manifest, privateClosure, assets)
	if err != nil {
		return Receipt{}, classified(ErrorInfrastructure, "build execution receipt", err)
	}
	if err := prepareCanonicalBaselines(ctx, node, runtimeRoot, appRoot, operation, candidateBaselines); err != nil {
		return Receipt{}, err
	}
	if err := runScopes(ctx, node, runtimeRoot, appRoot, "--check"); err != nil {
		return Receipt{}, err
	}
	if err := verifyParserClosure(seed, manifest); err != nil {
		return Receipt{}, classified(ErrorInfrastructure, "verify accepted parser closure after execution", err)
	}
	after, err := receiptFor(tree, acceptedTree, node, nodeVersion, nodePlatform, lock, manifest, privateClosure, assets)
	if err != nil || before.IdentitySHA256 != after.IdentitySHA256 {
		return Receipt{}, classified(ErrorInfrastructure, "execution closure changed during guard", err)
	}
	if operation == Refresh || operation == Migrate {
		if err := publishBaselines(root, appRoot); err != nil {
			return Receipt{}, err
		}
	}
	return before, nil
}

type baselinePair struct {
	production string
	test       string
}

// prepareBaselineWorkspace 保存候选声明，并为规范重放安装 accepted 基线。
func prepareBaselineWorkspace(ctx context.Context, root, candidate, acceptedTree string) (string, baselinePair, error) {
	appRoot := filepath.Join(candidate, "frontend-app")
	pair, err := readBaselinePair(appRoot)
	if err != nil {
		return "", baselinePair{}, classified(ErrorInfrastructure, "read candidate baselines", err)
	}
	if err := installAcceptedBaselines(ctx, root, acceptedTree, appRoot); err != nil {
		return "", baselinePair{}, err
	}
	return appRoot, pair, nil
}

// readBaselinePair 读取成对基线，用于比较候选声明与 accepted-baseline 重放结果。
func readBaselinePair(appRoot string) (baselinePair, error) {
	production, err := os.ReadFile(filepath.Join(appRoot, productionBaseline))
	if err != nil {
		return baselinePair{}, err
	}
	test, err := os.ReadFile(filepath.Join(appRoot, testBaseline))
	if err != nil {
		return baselinePair{}, err
	}
	return baselinePair{production: string(production), test: string(test)}, nil
}

// prepareCanonicalBaselines 从 accepted 基线重放规范结果，并要求 check 候选精确声明同一结果。
func prepareCanonicalBaselines(
	ctx context.Context,
	node, runtimeRoot, appRoot string,
	operation Operation,
	candidate baselinePair,
) error {
	if err := runScopes(ctx, node, runtimeRoot, appRoot, "--update"); err != nil {
		return err
	}
	if operation != Check {
		return nil
	}
	canonical, err := readBaselinePair(appRoot)
	if err != nil {
		return classified(ErrorInfrastructure, "read replayed canonical baselines", err)
	}
	if candidate != canonical {
		return classified(ErrorViolation, "candidate baselines do not match canonical accepted-baseline replay", nil)
	}
	return nil
}

func installAcceptedBaselines(ctx context.Context, root, accepted, appRoot string) error {
	resolved, err := gitOutput(ctx, root, "rev-parse", "--verify", accepted+"^{tree}")
	if err != nil || strings.TrimSpace(resolved) != accepted {
		return classified(ErrorInput, "accepted baseline tree is not exact", err)
	}
	for _, name := range []string{productionBaseline, testBaseline} {
		data, err := gitBytes(ctx, root, "show", accepted+":frontend-app/"+name)
		if err != nil {
			return classified(ErrorInfrastructure, "read accepted baseline", err)
		}
		if err := os.WriteFile(filepath.Join(appRoot, name), data, 0o644); err != nil {
			return classified(ErrorInfrastructure, "install accepted baseline", err)
		}
	}
	return nil
}

// validateRequest 校验调用方未留下工作区推断入口。
func validateRequest(tree string, operation Operation) error {
	if !exactTreePattern.MatchString(tree) {
		return classified(ErrorInput, "--tree must be an exact 40 or 64 lowercase hexadecimal Git tree SHA", nil)
	}
	if operation != Check && operation != Refresh && operation != Migrate {
		return classified(ErrorInput, "operation must be check, refresh, or migrate", nil)
	}
	return nil
}

// resolveTree 确认参数指向仓库中的同一个精确 tree 对象。
func resolveTree(ctx context.Context, repository, tree string) (string, error) {
	root, err := gitOutput(ctx, repository, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", classified(ErrorInfrastructure, "resolve repository root", err)
	}
	root = strings.TrimSpace(root)
	resolved, err := gitOutput(ctx, root, "rev-parse", "--verify", tree+"^{tree}")
	if err != nil || strings.TrimSpace(resolved) != tree {
		return "", classified(ErrorInput, "--tree does not name that exact tree object", err)
	}
	return root, nil
}

// materializeCandidate 从 Git archive 解包孤立候选树。
func materializeCandidate(ctx context.Context, root, tree string) (string, error) {
	candidate, err := os.MkdirTemp("", "super-dolphin-frontend-code-size-")
	if err != nil {
		return "", classified(ErrorInfrastructure, "create candidate directory", err)
	}
	err = extractGitTree(ctx, root, tree, candidate)
	if err != nil {
		os.RemoveAll(candidate)
		return "", classified(ErrorInfrastructure, "materialize exact tree", err)
	}
	if err := requireCandidateFiles(filepath.Join(candidate, "frontend-app")); err != nil {
		os.RemoveAll(candidate)
		return "", classified(ErrorInfrastructure, "validate candidate frontend files", err)
	}
	return candidate, nil
}

// materializeTrustedRuntime 把编译进 CLI 的守卫闭包物化到独立目录。
func materializeTrustedRuntime(seed string, manifest closureManifest, assets fs.FS) (string, string, error) {
	runtimeRoot, err := os.MkdirTemp("", "super-dolphin-frontend-code-size-runtime-")
	if err != nil {
		return "", "", classified(ErrorInfrastructure, "create trusted runtime directory", err)
	}
	if err := extractTrustedRuntimeAssets(runtimeRoot, assets); err != nil {
		os.RemoveAll(runtimeRoot)
		return "", "", classified(ErrorInfrastructure, "materialize trusted runtime assets", err)
	}
	privateClosure, err := materializeParserClosure(runtimeRoot, seed, manifest)
	if err != nil {
		os.RemoveAll(runtimeRoot)
		return "", "", classified(ErrorInfrastructure, "materialize private parser closure", err)
	}
	return runtimeRoot, privateClosure, nil
}

func extractTrustedRuntimeAssets(runtimeRoot string, assets fs.FS) error {
	return fs.WalkDir(assets, "assets", func(assetPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative := strings.TrimPrefix(assetPath, "assets/")
		if relative == "assets" {
			return nil
		}
		target, err := archiveTarget(runtimeRoot, relative)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(assets, assetPath)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// resolveSharedNodeModules 从显式配置或 Git common dir 解析跨工作树共享依赖。
func resolveSharedNodeModules(ctx context.Context, root string) (string, error) {
	if configured := os.Getenv("SUPER_DOLPHIN_FRONTEND_NODE_MODULES"); configured != "" {
		if !filepath.IsAbs(configured) || filepath.Clean(configured) != configured {
			return "", classified(ErrorInfrastructure, "SUPER_DOLPHIN_FRONTEND_NODE_MODULES must be canonical and absolute", nil)
		}
		return configured, nil
	}
	commonDirectory, err := gitOutput(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", classified(ErrorInfrastructure, "resolve Git common directory for shared frontend dependencies", err)
	}
	commonDirectory = strings.TrimSpace(commonDirectory)
	if !filepath.IsAbs(commonDirectory) || filepath.Clean(commonDirectory) != commonDirectory {
		return "", classified(ErrorInfrastructure, "Git common directory is not canonical and absolute", nil)
	}
	return filepath.Join(filepath.Dir(commonDirectory), "frontend-app", "node_modules"), nil
}

// runScopes 以同一次 canonical 调用校验或更新成对基线。
func runScopes(ctx context.Context, node, runtimeRoot, appRoot, mode string) error {
	if err := runGuard(ctx, node, runtimeRoot, appRoot, mode, "--scope", "all"); err != nil {
		return classified(ErrorViolation, "candidate "+mode, err)
	}
	return nil
}

// publishBaselines 原子替换已完成候选验证的两份仓库基线。
func publishBaselines(root, appRoot string) error {
	type pair struct {
		destination    string
		next, previous []byte
	}
	pairs := make([]pair, 0, 2)
	for _, name := range []string{productionBaseline, testBaseline} {
		next, err := os.ReadFile(filepath.Join(appRoot, name))
		if err != nil {
			return classified(ErrorInfrastructure, "read candidate baseline", err)
		}
		destination := filepath.Join(root, "frontend-app", name)
		previous, err := os.ReadFile(destination)
		if err != nil {
			return classified(ErrorInfrastructure, "read repository baseline", err)
		}
		pairs = append(pairs, pair{destination, next, previous})
	}
	published := make([]pair, 0, len(pairs))
	for _, item := range pairs {
		if err := atomicReplace(item.destination, item.next); err != nil {
			for _, publishedItem := range slices.Backward(published) {
				if rollbackErr := atomicReplace(publishedItem.destination, publishedItem.previous); rollbackErr != nil {
					return classified(ErrorInfrastructure, "rollback paired baseline publication", errors.Join(err, rollbackErr))
				}
			}
			return classified(ErrorInfrastructure, "publish paired baselines", err)
		}
		published = append(published, item)
	}
	return nil
}

// classified 构造带稳定分类的错误。
func classified(kind ErrorKind, message string, err error) error {
	if err == nil {
		return &Error{Kind: kind, Err: errors.New(message)}
	}
	return &Error{Kind: kind, Err: fmt.Errorf("%s: %w", message, err)}
}

// gitOutput 运行不读取工作区源码的 Git 元数据命令。
func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	data, err := gitBytes(ctx, root, args...)
	return string(data), err
}

// gitBytes 运行 Git 并保留 stderr 作为失败上下文。
func gitBytes(ctx context.Context, root string, args ...string) ([]byte, error) {
	git, err := trustedGitPath()
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, git, append([]string{"-C", root}, args...)...)
	command.Env = trustedGitEnvironment()
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return data, nil
}

func trustedGitPath() (string, error) {
	git := os.Getenv("SUPER_DOLPHIN_FRONTEND_CODE_SIZE_GIT")
	if git == "" {
		git = os.Getenv("SUPER_DOLPHIN_GATE_GIT")
	}
	if git == "" {
		git = "/usr/bin/git"
	}
	return gateprivate.CanonicalRootExecutable("trusted frontend Git", git)
}

func trustedGitEnvironment() []string {
	return []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "HOME=/nonexistent", "PATH=/usr/bin:/bin", "LC_ALL=C"}
}

// extractGitTree 流式解包精确 tree，避免候选仓库大小转化为协调器内存峰值。
func extractGitTree(ctx context.Context, root, tree, destination string) error {
	git, err := trustedGitPath()
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, git, "-C", root, "archive", "--format=tar", tree)
	command.Env = trustedGitEnvironment()
	archive, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open Git tree archive: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Git tree archive: %w", err)
	}
	if err := unpackTree(destination, archive); err != nil {
		if killErr := command.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			err = errors.Join(err, fmt.Errorf("stop rejected Git archive: %w", killErr))
		}
		if waitErr := command.Wait(); waitErr != nil {
			err = errors.Join(err, fmt.Errorf("wait for rejected Git archive: %w", waitErr))
		}
		return err
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("Git archive failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// unpackTree 仅提取普通文件和目录，并拒绝候选树中的链接与路径逃逸。
func unpackTree(destination string, stream io.Reader) error {
	reader := tar.NewReader(stream)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := archiveTarget(destination, header.Name)
		if err != nil {
			return err
		}
		if err := writeArchiveEntry(reader, header, target); err != nil {
			return err
		}
	}
}

// archiveTarget 将 tar 路径限制在候选目录内。
func archiveTarget(destination, name string) (string, error) {
	target := filepath.Join(destination, filepath.FromSlash(name))
	rel, err := filepath.Rel(destination, target)
	unsafe := err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel)
	if unsafe {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return target, nil
}

// writeArchiveEntry 只物化目录和普通文件，拒绝所有链接类型。
func writeArchiveEntry(reader *tar.Reader, header *tar.Header, target string) error {
	if header.Typeflag == tar.TypeDir {
		return os.MkdirAll(target, 0o755)
	}
	if header.Typeflag != tar.TypeReg {
		return fmt.Errorf("archive entry %q has unsupported type %d", header.Name, header.Typeflag)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// requireCandidateFiles 确保候选树提供两份受控基线且没有归档依赖目录。
func requireCandidateFiles(appRoot string) error {
	for _, name := range []string{productionBaseline, testBaseline} {
		info, err := os.Lstat(filepath.Join(appRoot, name))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("candidate file %s is not a regular file: %w", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(appRoot, "node_modules")); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("candidate node_modules must not be archived: %w", err)
	}
	return nil
}

// runGuard 固定用 CLI 内嵌脚本扫描候选前端目录。
func runGuard(ctx context.Context, node, runtimeRoot, appRoot string, args ...string) error {
	script := filepath.Join(runtimeRoot, "scripts", "lib", "frontend-code-size-cli.mjs")
	command := exec.CommandContext(ctx, node, append([]string{script}, args...)...)
	command.Dir = appRoot
	command.Env = trustedNodeEnvironment(appRoot)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func trustedNodeIdentity(ctx context.Context) (string, string, string, error) {
	node, err := trustedNodePath()
	if err != nil {
		return "", "", "", err
	}
	command := exec.CommandContext(ctx, node, "-p", "process.version + '\\n' + process.platform + '/' + process.arch")
	command.Env = []string{"LC_ALL=C"}
	output, err := command.Output()
	parts := strings.Split(strings.TrimSpace(string(output)), "\n")
	if err != nil || len(parts) != 2 || !strings.HasPrefix(parts[0], "v") || !regexp.MustCompile(`^[a-z0-9]+/[a-z0-9_]+$`).MatchString(parts[1]) {
		return "", "", "", errors.New("trusted Node version or platform identity is invalid")
	}
	return node, parts[0], parts[1], nil
}

// trustedNodePath 只接受显式绝对配置或平台登记的绝对 Node 路径，绝不查询 PATH。
func trustedNodePath() (string, error) {
	if configured := configuredTrustedNodePath(); configured != "" {
		return gateprivate.CanonicalCurrentOrRootExecutable("trusted frontend Node", configured)
	}
	for _, candidate := range trustedNodeCandidates() {
		canonical, err := gateprivate.CanonicalCurrentOrRootExecutable("trusted frontend Node", candidate)
		if err == nil {
			return canonical, nil
		}
	}
	return "", errors.New("trusted frontend Node is unavailable at a configured system path")
}

func configuredTrustedNodePath() string {
	if configured := os.Getenv("SUPER_DOLPHIN_FRONTEND_CODE_SIZE_NODE"); configured != "" {
		return configured
	}
	return os.Getenv("SUPER_DOLPHIN_GATE_NODE")
}

func trustedNodeCandidates() []string {
	candidates := make([]string, 0, 6)
	if git := os.Getenv("SUPER_DOLPHIN_GATE_GIT"); git != "" {
		canonicalGit, err := gateprivate.CanonicalRootExecutable("trusted gate Git", git)
		if err == nil {
			runtimeRoot := filepath.Dir(filepath.Dir(canonicalGit))
			candidates = append(candidates, filepath.Join(runtimeRoot, "node", "bin", "node"))
		}
	}
	return append(candidates,
		"/usr/local/bin/node", "/opt/homebrew/bin/node", "/usr/bin/node", "/usr/local/bin/nodejs", "/usr/bin/nodejs",
	)
}

// TrustedNodePath exposes the same fixed Node resolver used by the guard.
func TrustedNodePath() (string, error) { return trustedNodePath() }

func trustedNodeEnvironment(appRoot string) []string {
	environment := []string{
		"LC_ALL=C",
		"SUPER_DOLPHIN_FRONTEND_CODE_SIZE_APP_ROOT=" + appRoot,
		"SUPER_DOLPHIN_FRONTEND_CODE_SIZE_REPRODUCIBLE=1",
	}
	if temporary := os.Getenv("TMPDIR"); temporary != "" {
		environment = append(environment, "TMPDIR="+temporary)
	}
	return environment
}

// atomicReplace 先在目标目录落盘临时文件，再以 rename 替换单个基线文件。
func atomicReplace(destination string, data []byte) error {
	info, err := os.Lstat(destination)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("baseline target is not a regular file: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".frontend-code-size-")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, destination)
}
