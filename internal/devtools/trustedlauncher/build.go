package trustedlauncher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	gateclosure "github.com/lihah111222333-cloud/super-dolphin-agent/build/gate/closure"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/godistribution"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/projectmaptrusted"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// BuildOptions selects one exact Git tree and one controlled install root.
type BuildOptions struct {
	RepositoryRoot string
	Tree           string
	InstallRoot    string
}

// BuildResult identifies the installed launcher and its strict receipt.
type BuildResult struct {
	BinaryPath  string
	ReceiptPath string
}

const launcherInstallDirectoryName = ".super-dolphin-gate-launchers"

// CurrentUserInstallRoot 返回操作系统账户绑定且不可由环境变量覆盖的 launcher 安装根。
func CurrentUserInstallRoot() (string, error) {
	uid := strconv.Itoa(os.Geteuid())
	account, err := user.LookupId(uid)
	if err != nil {
		return "", fmt.Errorf("resolve current operating-system account: %w", err)
	}
	if account.Uid != uid || account.HomeDir == "" || !filepath.IsAbs(account.HomeDir) || filepath.Clean(account.HomeDir) != account.HomeDir {
		return "", errors.New("current operating-system account has no canonical absolute home directory")
	}
	return secureInstallRoot(filepath.Join(account.HomeDir, launcherInstallDirectoryName))
}

// Build 使用精确树、编译器与完整闭包构建并原子安装受信 launcher。
func Build(ctx context.Context, options BuildOptions) (result BuildResult, resultErr error) {
	if ctx == nil {
		return BuildResult{}, errors.New("trusted launcher build context is required")
	}
	repositoryRoot, installRoot, err := canonicalBuildRoots(options)
	if err != nil {
		return BuildResult{}, err
	}
	identity, compilerPath, err := resolveBuildIdentity(ctx, repositoryRoot, options.Tree)
	if err != nil {
		return BuildResult{}, err
	}
	exact, err := projectmaptrusted.MaterializeExactTree(repositoryRoot, options.Tree, "super-dolphin-launcher-source-")
	if err != nil {
		return BuildResult{}, fmt.Errorf("materialize exact launcher tree: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, exact.Cleanup()) }()
	stagingRoot, receipt, err := buildStagedLauncher(ctx, installRoot, repositoryRoot, options.Tree, exact.SourceRoot, compilerPath, identity)
	if err != nil {
		return BuildResult{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, removeStagingRoot(stagingRoot)) }()
	final, installed, err := installStagedLauncher(ctx, installRoot, repositoryRoot, options.Tree, stagingRoot, receipt)
	if installed {
		stagingRoot = ""
	}
	return final, err
}

// buildStagedLauncher 在不可发现的暂存目录中编译、写回执并自验证。
func buildStagedLauncher(ctx context.Context, installRoot, repositoryRoot, tree, sourceRoot, compilerPath string, identity LinkedIdentity) (string, Receipt, error) {
	stagingRoot, err := os.MkdirTemp(installRoot, ".install-")
	if err != nil {
		return "", Receipt{}, fmt.Errorf("create launcher staging root: %w", err)
	}
	staging := BuildResult{BinaryPath: filepath.Join(stagingRoot, BinaryName), ReceiptPath: filepath.Join(stagingRoot, ReceiptName)}
	cacheRoot, err := launcherBuildCacheRoot(installRoot, identity)
	if err != nil {
		return stagingRoot, Receipt{}, err
	}
	receipt, err := compileLauncher(ctx, sourceRoot, repositoryRoot, compilerPath, staging.BinaryPath, cacheRoot, identity)
	if err != nil {
		return stagingRoot, Receipt{}, err
	}
	if err := writeReceiptFile(staging.ReceiptPath, receipt); err != nil {
		return stagingRoot, Receipt{}, err
	}
	if err := executeLauncherVerification(ctx, repositoryRoot, tree, staging); err != nil {
		return stagingRoot, Receipt{}, fmt.Errorf("self-verify built exact-tree launcher: %w", err)
	}
	if err := syncDirectory(stagingRoot); err != nil {
		return stagingRoot, Receipt{}, err
	}
	return stagingRoot, receipt, nil
}

// installStagedLauncher 只发布完整暂存结果，已有等价发布物必须再次验证。
func installStagedLauncher(ctx context.Context, installRoot, repositoryRoot, tree, stagingRoot string, receipt Receipt) (BuildResult, bool, error) {
	final := launcherPaths(installRoot, receipt)
	if err := ensureSecureDirectory(filepath.Dir(filepath.Dir(final.BinaryPath)), installRoot); err != nil {
		return BuildResult{}, false, fmt.Errorf("prepare content-addressed launcher directory: %w", err)
	}
	installErr := os.Rename(stagingRoot, filepath.Dir(final.BinaryPath))
	if installErr == nil {
		if err := syncDirectory(filepath.Dir(filepath.Dir(final.BinaryPath))); err != nil {
			return BuildResult{}, false, err
		}
		return final, true, nil
	}
	if _, statErr := os.Lstat(final.BinaryPath); statErr == nil && executeLauncherVerification(ctx, repositoryRoot, tree, final) == nil {
		return final, false, nil
	}
	return BuildResult{}, false, fmt.Errorf("atomically install exact-tree launcher: %w", installErr)
}

// canonicalBuildRoots 验证精确树、仓库根与受限安装根。
func canonicalBuildRoots(options BuildOptions) (string, string, error) {
	if !treePattern.MatchString(options.Tree) || strings.Trim(options.Tree, "0") == "" {
		return "", "", errors.New("trusted launcher build tree must be a canonical non-zero Git object ID")
	}
	repositoryRoot, err := filepath.Abs(options.RepositoryRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve launcher repository root: %w", err)
	}
	repositoryRoot, err = filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve launcher repository symlinks: %w", err)
	}
	if !filepath.IsAbs(options.InstallRoot) {
		return "", "", errors.New("trusted launcher install root must be absolute")
	}
	installRoot, err := secureInstallRoot(options.InstallRoot)
	if err != nil {
		return "", "", err
	}
	return repositoryRoot, installRoot, nil
}

// resolveBuildIdentity 绑定精确树闭包、锁定编译器和编译器闭包摘要。
func resolveBuildIdentity(ctx context.Context, repositoryRoot, tree string) (LinkedIdentity, string, error) {
	asset, err := godistribution.Lookup(runtime.GOOS, runtime.GOARCH)
	if err != nil || asset.Version != runtime.Version() {
		return LinkedIdentity{}, "", errors.Join(fmt.Errorf("host Go runtime must match the locked distribution %s", godistribution.Version), err)
	}
	compilerPath, err := exec.LookPath("go")
	if err != nil {
		return LinkedIdentity{}, "", fmt.Errorf("locate locked Go compiler: %w", err)
	}
	compilerPath, err = filepath.EvalSymlinks(compilerPath)
	if err != nil {
		return LinkedIdentity{}, "", fmt.Errorf("resolve locked Go compiler: %w", err)
	}
	if err := verifyCompilerIdentity(ctx, compilerPath, asset.Version, asset.GOOS, asset.GOARCH); err != nil {
		return LinkedIdentity{}, "", err
	}
	compilerDigest, err := digestFile(compilerPath)
	if err != nil {
		return LinkedIdentity{}, "", fmt.Errorf("digest locked Go compiler: %w", err)
	}
	compilerClosureDigest, err := compilerClosureDigest(ctx, compilerPath)
	if err != nil {
		return LinkedIdentity{}, "", fmt.Errorf("digest locked Go toolchain closure: %w", err)
	}
	sourceDigest, toolchainDigest, _, err := remoteci.LoadGateCLICompileClosure(ctx, repositoryRoot, tree)
	if err != nil {
		return LinkedIdentity{}, "", fmt.Errorf("load exact-tree Gate compile closure: %w", err)
	}
	identity := LinkedIdentity{
		Tree:                  tree,
		SourceSHA256:          sourceDigest,
		ToolchainSHA256:       toolchainDigest,
		CompilerSHA256:        compilerDigest,
		CompilerClosureSHA256: compilerClosureDigest,
	}
	identity.BuildArgumentsSHA256, err = buildArgumentsIdentityDigest(identity)
	if err != nil {
		return LinkedIdentity{}, "", err
	}
	return identity, compilerPath, nil
}

// compileLauncher 在物化的精确树中编译 linker 绑定的 launcher 和回执。
func compileLauncher(ctx context.Context, sourceRoot, repositoryRoot, compilerPath, binaryPath, cacheRoot string, identity LinkedIdentity) (Receipt, error) {
	arguments, err := expectedBuildArguments(identity)
	if err != nil {
		return Receipt{}, err
	}
	commandArguments := append([]string{}, arguments...)
	commandArguments = append(commandArguments[:len(commandArguments)-1], "-o", binaryPath, commandArguments[len(commandArguments)-1])
	command := exec.CommandContext(ctx, compilerPath, commandArguments...)
	command.Dir = sourceRoot
	environment, err := launcherBuildEnvironment(filepath.Dir(binaryPath), cacheRoot)
	if err != nil {
		return Receipt{}, err
	}
	command.Env = environment
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return Receipt{}, fmt.Errorf("compile exact-tree Gate launcher: %w: %s", err, strings.TrimSpace(output.String()))
	}
	if err := os.Chmod(binaryPath, 0o500); err != nil {
		return Receipt{}, fmt.Errorf("restrict Gate launcher mode: %w", err)
	}
	binaryDigest, err := digestFile(binaryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("digest Gate launcher: %w", err)
	}
	closureProvenance, err := gateclosure.GeneratorProvenanceForTree(repositoryRoot, identity.Tree)
	if err != nil {
		return Receipt{}, fmt.Errorf("resolve exact-tree closure provenance: %w", err)
	}
	return Receipt{
		SchemaVersion:         ReceiptSchemaVersion,
		Tree:                  identity.Tree,
		SourceSHA256:          identity.SourceSHA256,
		ToolchainSHA256:       identity.ToolchainSHA256,
		ClosureProvenance:     closureProvenance,
		GoVersion:             runtime.Version(),
		GOOS:                  runtime.GOOS,
		GOARCH:                runtime.GOARCH,
		CompilerPath:          compilerPath,
		CompilerSHA256:        identity.CompilerSHA256,
		CompilerClosureSHA256: identity.CompilerClosureSHA256,
		BuildArguments:        arguments,
		BuildArgumentsSHA256:  identity.BuildArgumentsSHA256,
		BinarySHA256:          binaryDigest,
	}, nil
}

func expectedBuildArguments(identity LinkedIdentity) ([]string, error) {
	linkedPayload, err := encodeLauncherLinkedPayload(identity)
	if err != nil {
		return nil, err
	}
	return launcherBuildArguments(linkedPayload, identity.BuildArgumentsSHA256), nil
}

// launcherBuildCacheRoot 将 Go package 编译缓存绑定到受信编译器闭包而非 exact tree。
// 最终 launcher 仍逐 tree 链接并生成严格回执；只有内容寻址的中间编译产物可跨
// repository、worktree 和 agent 复用。
func launcherBuildCacheRoot(installRoot string, identity LinkedIdentity) (string, error) {
	if err := validateDigestField("compiler_sha256", identity.CompilerSHA256); err != nil {
		return "", err
	}
	if err := validateDigestField("compiler_closure_sha256", identity.CompilerClosureSHA256); err != nil {
		return "", err
	}
	cacheRoot := filepath.Join(
		installRoot,
		".go-build-cache-v1",
		runtime.GOOS+"-"+runtime.GOARCH,
		strings.TrimPrefix(identity.CompilerSHA256, "sha256:"),
		strings.TrimPrefix(identity.CompilerClosureSHA256, "sha256:"),
	)
	if err := ensureSecureDirectory(cacheRoot, installRoot); err != nil {
		return "", fmt.Errorf("prepare trusted launcher Go build cache: %w", err)
	}
	return cacheRoot, nil
}

func launcherBuildEnvironment(tempRoot, cacheRoot string) ([]string, error) {
	if !filepath.IsAbs(tempRoot) {
		return nil, errors.New("launcher build temporary root must be absolute")
	}
	if !filepath.IsAbs(cacheRoot) {
		return nil, errors.New("launcher Go build cache root must be absolute")
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return nil, errors.Join(errors.New("resolve launcher builder home directory"), err)
	}
	return []string{
		"HOME=" + home,
		"PATH=/usr/bin:/bin",
		"TMPDIR=" + tempRoot,
		"GOCACHE=" + cacheRoot,
		"GOMODCACHE=" + filepath.Join(home, "go", "pkg", "mod"),
		"CGO_ENABLED=0",
		"GOARCH=" + runtime.GOARCH,
		"GOOS=" + runtime.GOOS,
		"GOENV=off",
		"GOFLAGS=",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
	}, nil
}

// verifyCompilerIdentity 拒绝未锁定的 Go 版本或目标平台。
func verifyCompilerIdentity(ctx context.Context, compilerPath, version, goos, goarch string) error {
	command := exec.CommandContext(ctx, compilerPath, "env", "GOVERSION", "GOOS", "GOARCH")
	environment, err := launcherBuildEnvironment(os.TempDir(), filepath.Join(os.TempDir(), "super-dolphin-launcher-go-build-cache"))
	if err != nil {
		return err
	}
	command.Env = environment
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("inspect locked Go compiler: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 3 || lines[0] != version || lines[1] != goos || lines[2] != goarch {
		return fmt.Errorf("locked Go compiler identity is %q, want %s %s/%s", strings.TrimSpace(string(output)), version, goos, goarch)
	}
	return nil
}

func launcherPaths(installRoot string, receipt Receipt) BuildResult {
	directory := filepath.Join(installRoot, "v1", receipt.Tree, strings.TrimPrefix(receipt.BinarySHA256, "sha256:"))
	return BuildResult{BinaryPath: filepath.Join(directory, BinaryName), ReceiptPath: filepath.Join(directory, ReceiptName)}
}

func executeLauncherVerification(ctx context.Context, repositoryRoot, tree string, paths BuildResult) error {
	command := exec.CommandContext(ctx, paths.BinaryPath, "launcher", "verify", "--repository", repositoryRoot, "--tree", tree, "--receipt", paths.ReceiptPath)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("launcher verification command: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if len(output) != 0 {
		return errors.New("launcher verification command produced unexpected output")
	}
	return nil
}

func removeStagingRoot(root string) error {
	if root == "" {
		return nil
	}
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove launcher staging root: %w", err)
	}
	return nil
}

// secureInstallRoot 创建或验证仅当前用户可写的受信安装根。
func secureInstallRoot(root string) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", errors.New("trusted launcher install root must be a clean absolute path")
	}
	if err := validateSecureAncestors(root); err != nil {
		return "", fmt.Errorf("validate launcher install root ancestors: %w", err)
	}
	if info, err := os.Lstat(root); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("trusted launcher install root must not be a symlink")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(root, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create launcher install root: %w", err)
		}
	} else {
		return "", fmt.Errorf("inspect launcher install root: %w", err)
	}
	if err := validateSecureDirectory(root); err != nil {
		return "", fmt.Errorf("validate launcher install root: %w", err)
	}
	return root, nil
}

// validateSecureAncestors 拒绝符号链接、非目录或非当前用户可信的祖先。
func validateSecureAncestors(path string) error {
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil {
			return fmt.Errorf("inspect launcher ancestor %s: %w", parent, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("launcher ancestor %s must not be a symlink", parent)
		}
		if !info.IsDir() {
			return fmt.Errorf("launcher ancestor %s must be a directory", parent)
		}
		if info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("launcher ancestor %s must not permit group or world writes", parent)
		}
		ownerUID, ok := trustedFileOwnerUID(info)
		if !ok || (ownerUID != os.Geteuid() && ownerUID != 0) {
			return fmt.Errorf("launcher ancestor %s must be owned by the current user or root", parent)
		}
		if filepath.Dir(parent) == parent {
			return nil
		}
	}
}

// ensureSecureDirectory 在安装根下创建或验证受信相对目录。
func ensureSecureDirectory(directory, root string) error {
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("launcher directory escapes its install root")
	}
	current := root
	for element := range strings.SplitSeq(relative, string(filepath.Separator)) {
		current = filepath.Join(current, element)
		if err := ensureOneSecureDirectory(current); err != nil {
			return err
		}
	}
	return nil
}

// ensureOneSecureDirectory 并发安全地创建目录并严格复核最终 owner、mode 与类型。
func ensureOneSecureDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create launcher directory %s: %w", path, err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect launcher directory %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("launcher directory %s must not be a symlink", path)
	}
	return validateSecureDirectory(path)
}

// validateSecureDirectory 验证目录不是链接、没有组/其他写权限并归当前用户所有。
func validateSecureDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("must be a non-symlink directory")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return errors.New("must not permit group or world writes")
	}
	ownerUID, ok := trustedFileOwnerUID(info)
	if !ok || ownerUID != os.Geteuid() {
		return errors.New("must be owned by the current user")
	}
	return nil
}

func compilerClosureDigest(ctx context.Context, compilerPath string) (string, error) {
	command := exec.CommandContext(ctx, compilerPath, "env", "GOROOT")
	environment, err := launcherBuildEnvironment(os.TempDir(), filepath.Join(os.TempDir(), "super-dolphin-launcher-go-build-cache"))
	if err != nil {
		return "", err
	}
	command.Env = environment
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("resolve Go toolchain root: %w", err)
	}
	root := strings.TrimSpace(string(output))
	if !filepath.IsAbs(root) {
		return "", errors.New("Go toolchain root must be absolute")
	}
	return digestDirectory(root)
}

// digestDirectory 对无链接的完整编译器目录产生稳定 closure 摘要。
func digestDirectory(root string) (string, error) {
	var entries []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("toolchain closure contains symlink %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("toolchain closure contains non-regular path %s", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries = append(entries, relative)
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(entries)
	var manifest bytes.Buffer
	for _, relative := range entries {
		digest, err := digestFile(filepath.Join(root, relative))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&manifest, "%s\\x00%s\\n", filepath.ToSlash(relative), digest)
	}
	return digestBytes(manifest.Bytes()), nil
}
