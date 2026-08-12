package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"golang.org/x/mod/module"
)

// verifyLocalReceiptDependencies 根据 workload 需求验证离线依赖根并冻结其证明。
func verifyLocalReceiptDependencies(ctx context.Context, trustedGit TrustedGitBinary, trustedGo TrustedGoBinary, repositoryRoot, tree string, programs map[GateID]ExecutorProgram, dependencies LocalExecutorDependencyInputs) ([]localExecutorDependencyProof, error) {
	needsGo, needsFrontend, needsEmbed := localReceiptDependencyNeeds(programs)
	proofs := make([]localExecutorDependencyProof, 0, 3)
	if needsGo {
		proof, err := verifyLocalReceiptGoDependency(ctx, trustedGit, trustedGo, repositoryRoot, tree, dependencies.GoModuleCache)
		if err != nil {
			return nil, err
		}
		proofs = append(proofs, proof)
	}
	if needsFrontend {
		proof, err := verifyLocalReceiptFrontendDependency(ctx, trustedGit, repositoryRoot, tree, dependencies)
		if err != nil {
			return nil, err
		}
		proofs = append(proofs, proof)
	}
	if needsEmbed {
		proof, err := verifyLocalReceiptEmbedDependency(dependencies.FrontendEmbedRoot)
		if err != nil {
			return nil, err
		}
		proofs = append(proofs, proof)
	}
	sort.Slice(proofs, func(left, right int) bool { return proofs[left].name < proofs[right].name })
	return proofs, nil
}

func localReceiptDependencyNeeds(programs map[GateID]ExecutorProgram) (bool, bool, bool) {
	needsGo, needsFrontend, needsEmbed := false, false, false
	for _, program := range programs {
		needsGo = needsGo || program.NeedsGoSeed
		needsFrontend = needsFrontend || program.NeedsFrontendSeed
		needsEmbed = needsEmbed || program.NeedsFrontendEmbedSeed
	}
	return needsGo, needsFrontend, needsEmbed
}

// verifyLocalReceiptGoDependency 只以 exact Git tree 的锁和 local replace 源码构造 Go proof，拒绝工作区漂移。
func verifyLocalReceiptGoDependency(ctx context.Context, trustedGit TrustedGitBinary, trustedGo TrustedGoBinary, repositoryRoot, tree, cacheRoot string) (localExecutorDependencyProof, error) {
	if err := validateLocalDependencyRoot(cacheRoot, "Go module cache"); err != nil {
		return localExecutorDependencyProof{}, err
	}
	canonicalCacheRoot, err := canonicalLocalSandboxPath(cacheRoot, "local Go module cache dependency root")
	if err != nil {
		return localExecutorDependencyProof{}, err
	}
	mod, err := gitTreeBlob(ctx, trustedGit, repositoryRoot, tree, "go.mod")
	if err != nil {
		return localExecutorDependencyProof{}, err
	}
	sum, err := gitTreeBlob(ctx, trustedGit, repositoryRoot, tree, "go.sum")
	if err != nil {
		return localExecutorDependencyProof{}, err
	}
	verification, lockFiles, err := verifyGoModuleCacheOffline(ctx, trustedGit, trustedGo, repositoryRoot, tree, canonicalCacheRoot, mod, sum)
	if err != nil {
		return localExecutorDependencyProof{}, err
	}
	contentFiles, contentDigest, err := localReceiptGoModuleCacheManifest(ctx, trustedGit, trustedGo, repositoryRoot, tree, canonicalCacheRoot, mod, sum)
	if err != nil {
		return localExecutorDependencyProof{}, err
	}
	lockDigest, err := localReceiptGoLockDigest(lockFiles)
	if err != nil {
		return localExecutorDependencyProof{}, err
	}
	return localExecutorDependencyProof{name: "go", root: canonicalCacheRoot, lockDigest: lockDigest, contentDigest: contentDigest, verification: verification, lockFiles: lockFiles, contentFiles: contentFiles}, nil
}

type localReceiptGoModule struct {
	Path    string
	Version string
	Dir     string
	GoMod   string
	Replace *localReceiptGoModule
}

// localReceiptGoModuleCacheManifest binds only resolved module content, never
// the whole mutable GOMODCACHE, so pre-execution revalidation stays bounded.
func localReceiptGoModuleCacheManifest(ctx context.Context, trustedGit TrustedGitBinary, trustedGo TrustedGoBinary, repositoryRoot, tree, cacheRoot string, mod, sum []byte) (content []localExecutorDependencyContentFile, digest string, err error) {
	temporaryRoot, err := localReceiptGoTemporaryModule(ctx, trustedGit, repositoryRoot, tree, mod, sum)
	if err != nil {
		return nil, "", err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(temporaryRoot); cleanupErr != nil {
			content, digest, err = nil, "", errors.Join(err, cleanupErr)
		}
	}()
	modules, err := listLocalReceiptGoModules(ctx, trustedGo, temporaryRoot, cacheRoot)
	if err != nil {
		return nil, "", err
	}
	paths, err := localReceiptGoModuleClosurePaths(cacheRoot, modules)
	if err != nil {
		return nil, "", err
	}
	return localReceiptDependencyContentManifest(cacheRoot, paths)
}

// localReceiptGoTemporaryModule 在临时根写入 exact tree 锁文件和 local replace，任一步失败即清理临时目录并返回。
func localReceiptGoTemporaryModule(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree string, mod, sum []byte) (string, error) {
	temporaryRoot, err := os.MkdirTemp("", "local-go-manifest-")
	if err != nil {
		return "", err
	}
	fail := func(cause error) (string, error) { return "", errors.Join(cause, os.RemoveAll(temporaryRoot)) }
	if err := os.WriteFile(filepath.Join(temporaryRoot, "go.mod"), mod, 0o600); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(temporaryRoot, "go.sum"), sum, 0o600); err != nil {
		return fail(err)
	}
	replaces, err := localReceiptGoReplacePaths(mod)
	if err != nil {
		return fail(err)
	}
	for _, replace := range replaces {
		if _, err := materializeExactTreeLocalReplace(ctx, trustedGit, repositoryRoot, tree, temporaryRoot, replace); err != nil {
			return fail(err)
		}
	}
	return temporaryRoot, nil
}

// listLocalReceiptGoModules 使用受信 Go 和离线缓存列出解析闭包，验证、命令或解码失败立即返回。
func listLocalReceiptGoModules(ctx context.Context, trustedGo TrustedGoBinary, temporaryRoot, cacheRoot string) ([]localReceiptGoModule, error) {
	goBinary, err := trustedGo.VerifiedPath()
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, goBinary, "list", "-mod=mod", "-m", "-json", "all")
	command.Dir = temporaryRoot
	command.Env = []string{"PATH=" + filepath.Dir(goBinary), "GOMODCACHE=" + cacheRoot, "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local"}
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list offline Go module closure: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	modules := make([]localReceiptGoModule, 0)
	for {
		var resolved localReceiptGoModule
		err := decoder.Decode(&resolved)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode offline Go module closure: %w", err)
		}
		modules = append(modules, resolved)
	}
	return modules, nil
}

// localReceiptGoModuleClosurePaths 生成非 replace 模块的缓存闭包路径，转义或缓存元数据缺失立即拒绝。
func localReceiptGoModuleClosurePaths(cacheRoot string, modules []localReceiptGoModule) ([]string, error) {
	paths := make([]string, 0, len(modules)*5)
	for _, resolved := range modules {
		if resolved.Version == "" || resolved.Replace != nil {
			continue
		}
		escapedPath, err := module.EscapePath(resolved.Path)
		if err != nil {
			return nil, fmt.Errorf("escape Go module path %q: %w", resolved.Path, err)
		}
		escapedVersion, err := module.EscapeVersion(resolved.Version)
		if err != nil {
			return nil, fmt.Errorf("escape Go module version %q: %w", resolved.Version, err)
		}
		if resolved.Dir == "" || resolved.GoMod == "" {
			return nil, fmt.Errorf("offline Go module %s@%s has incomplete cache paths", resolved.Path, resolved.Version)
		}
		metadataRoot := filepath.Join(cacheRoot, "cache", "download", filepath.FromSlash(escapedPath), "@v", escapedVersion)
		paths = append(paths, resolved.Dir, resolved.GoMod, metadataRoot+".zip", metadataRoot+".ziphash", metadataRoot+".mod", metadataRoot+".info")
	}
	return paths, nil
}

// localReceiptDependencyContentManifest 枚举受信根内的普通文件清单；根、路径、遍历或摘要失败立即拒绝。
func localReceiptDependencyContentManifest(root string, paths []string) ([]localExecutorDependencyContentFile, string, error) {
	canonicalRoot, err := canonicalLocalSandboxPath(root, "local dependency closure root")
	if err != nil {
		return nil, "", err
	}
	entries := make(map[string]localExecutorDependencyContentFile)
	for _, required := range paths {
		canonicalRequired, err := canonicalLocalSandboxPath(required, "local dependency closure path")
		if err != nil {
			return nil, "", err
		}
		if canonicalRequired == canonicalRoot {
			err = collectLocalReceiptDependencyRoot(canonicalRoot, entries)
		} else {
			err = collectLocalReceiptDependencyContent(canonicalRoot, canonicalRequired, entries)
		}
		if err != nil {
			return nil, "", err
		}
	}
	content := make([]localExecutorDependencyContentFile, 0, len(entries))
	for _, entry := range entries {
		content = append(content, entry)
	}
	sort.Slice(content, func(left, right int) bool { return content[left].path < content[right].path })
	digest, err := digestCanonicalJSON(cicontract.LocalDependencyContentDomain, content)
	if err != nil {
		return nil, "", err
	}
	return content, digest, nil
}

// collectLocalReceiptDependencyRoot 遍历整个受信根，用于需要拒绝额外文件的封存清单。
func collectLocalReceiptDependencyRoot(root string, entries map[string]localExecutorDependencyContentFile) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		return addLocalReceiptDependencyContentFile(root, path, entries)
	})
}

// collectLocalReceiptDependencyContent 仅收集缓存根内的依赖文件，路径规范化、边界或遍历失败立即返回。
func collectLocalReceiptDependencyContent(root, required string, entries map[string]localExecutorDependencyContentFile) error {
	canonical, err := canonicalLocalSandboxPath(required, "local dependency closure path")
	if err != nil {
		return err
	}
	if !pathContains(root, canonical) {
		return fmt.Errorf("local dependency closure path %q escapes cache root", required)
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return fmt.Errorf("inspect local dependency closure path %q: %w", required, err)
	}
	if info.IsDir() {
		return filepath.WalkDir(canonical, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			return addLocalReceiptDependencyContentFile(root, path, entries)
		})
	}
	return addLocalReceiptDependencyContentFile(root, canonical, entries)
}

// addLocalReceiptDependencyContentFile 将根内普通文件按相对路径和摘要封入闭包，越界或内容漂移立即拒绝。
func addLocalReceiptDependencyContentFile(root, path string, entries map[string]localExecutorDependencyContentFile) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("local dependency closure file %q is not regular", path)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	relative, err = localReceiptDependencyRelativePath(relative)
	if err != nil {
		return fmt.Errorf("local dependency closure file %q escapes cache root", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	entry := localExecutorDependencyContentFile{path: filepath.ToSlash(relative), digest: digestBytes(content), mode: uint32(info.Mode().Perm())}
	if previous, exists := entries[entry.path]; exists && previous != entry {
		return fmt.Errorf("local dependency closure file %q changed while sealing", path)
	}
	entries[entry.path] = entry
	return nil
}

func localReceiptDependencyRelativePath(relative string) (string, error) {
	if relative == "." {
		return "", errors.New("dependency closure path is the cache root")
	}
	if filepath.IsAbs(relative) {
		return "", errors.New("dependency closure path is absolute")
	}
	if relative == ".." {
		return "", errors.New("dependency closure path escapes cache root")
	}
	if strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("dependency closure path escapes cache root")
	}
	return filepath.ToSlash(relative), nil
}

// localExecutorSessionDependencySnapshot 在创建链接前以封存的私有 Go 闭包替换可变宿主缓存，证明缺失或漂移立即拒绝。
func localExecutorSessionDependencySnapshot(receipt LocalExecutorSessionReceipt, dependencies LocalExecutorDependencyInputs) (LocalExecutorDependencyInputs, func() error, error) {
	sealed, ok := receipt.(*localExecutorSessionReceipt)
	if !ok {
		return LocalExecutorDependencyInputs{}, nil, errors.New("local executor receipt has no private dependency snapshot proof")
	}
	cleanups := make([]func() error, 0, 2)
	bind := func(name, root string, assign func(string)) error {
		if root == "" {
			return nil
		}
		proof, err := sealed.localDependencyProof(name)
		if err != nil {
			return err
		}
		canonical, err := canonicalLocalSandboxPath(root, "local executor "+name+" dependency")
		if err != nil {
			return err
		}
		if proof.root != canonical {
			return fmt.Errorf("local executor %s dependency drifted from sealed receipt", name)
		}
		snapshot, cleanup, err := materializeLocalReceiptDependencySnapshot(proof)
		if err != nil {
			return err
		}
		assign(snapshot)
		cleanups = append(cleanups, cleanup)
		return nil
	}
	if err := bind("go", dependencies.GoModuleCache, func(snapshot string) { dependencies.GoModuleCache = snapshot }); err != nil {
		return LocalExecutorDependencyInputs{}, nil, err
	}
	if err := bind("frontend-embed", dependencies.FrontendEmbedRoot, func(snapshot string) { dependencies.FrontendEmbedRoot = snapshot }); err != nil {
		return LocalExecutorDependencyInputs{}, nil, errors.Join(err, runLocalDependencySnapshotCleanups(cleanups))
	}
	return dependencies, func() error { return runLocalDependencySnapshotCleanups(cleanups) }, nil
}

func runLocalDependencySnapshotCleanups(cleanups []func() error) error {
	var cleanupErr error
	for index := range slices.Backward(cleanups) {
		cleanupErr = errors.Join(cleanupErr, cleanups[index]())
	}
	return cleanupErr
}

func (receipt *localExecutorSessionReceipt) localDependencyProof(name string) (localExecutorDependencyProof, error) {
	if receipt == nil {
		return localExecutorDependencyProof{}, errors.New("local executor receipt is nil")
	}
	for _, proof := range receipt.dependencies {
		if proof.name == name {
			return proof, nil
		}
	}
	return localExecutorDependencyProof{}, fmt.Errorf("local executor receipt dependency proof %q is absent", name)
}

func materializeLocalReceiptDependencySnapshot(proof localExecutorDependencyProof) (string, func() error, error) {
	if err := reverifyLocalReceiptDependencyContent(proof); err != nil {
		return "", nil, err
	}
	parent, err := os.MkdirTemp("", "super-dolphin-local-dependency-")
	if err != nil {
		return "", nil, err
	}
	rawParent := parent
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return "", nil, errors.Join(fmt.Errorf("canonicalize local dependency snapshot root: %w", err), cleanupLocalReceiptDependencySnapshot(rawParent))
	}
	cleanup := func() error { return cleanupLocalReceiptDependencySnapshot(parent) }
	snapshot := filepath.Join(parent, proof.name)
	if err := copyLocalReceiptDependencySnapshot(proof, snapshot); err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	if err := sealLocalReceiptDependencySnapshot(snapshot); err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	return snapshot, cleanup, nil
}

func copyLocalReceiptDependencySnapshot(proof localExecutorDependencyProof, snapshot string) error {
	if err := os.Mkdir(snapshot, 0o700); err != nil {
		return err
	}
	for _, expected := range proof.contentFiles {
		if err := copyLocalReceiptDependencySnapshotFile(proof.root, snapshot, expected); err != nil {
			return err
		}
	}
	return nil
}

// copyLocalReceiptDependencySnapshotFile 仅复制摘要匹配的闭包文件到私有快照，路径、读取或摘要漂移立即返回。
func copyLocalReceiptDependencySnapshotFile(sourceRoot, snapshot string, expected localExecutorDependencyContentFile) error {
	source, err := localReceiptJoinRelativePath(sourceRoot, expected.path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != expected.mode {
		return fmt.Errorf("local dependency closure file %q metadata drifted while snapshotting", expected.path)
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if digestBytes(content) != expected.digest {
		return fmt.Errorf("local dependency closure file %q drifted while snapshotting", expected.path)
	}
	target, err := localReceiptJoinRelativePath(snapshot, expected.path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.WriteFile(target, content, os.FileMode(expected.mode))
}

// sealLocalReceiptDependencySnapshot 先封存文件再自底向上封存目录，遇到非普通条目或权限失败立即返回。
func sealLocalReceiptDependencySnapshot(snapshot string) error {
	directories := make([]string, 0)
	err := filepath.WalkDir(snapshot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return fmt.Errorf("local dependency snapshot contains forbidden entry %q", path)
		}
		return os.Chmod(path, 0o400)
	})
	if err != nil {
		return err
	}
	for index := range slices.Backward(directories) {
		if err := os.Chmod(directories[index], 0o500); err != nil {
			return err
		}
	}
	return nil
}

func cleanupLocalReceiptDependencySnapshot(parent string) error {
	var permissionErr error
	if err := filepath.WalkDir(parent, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		mode := os.FileMode(0o600)
		if entry.IsDir() {
			mode = 0o700
		}
		if err := os.Chmod(path, mode); err != nil {
			permissionErr = errors.Join(permissionErr, err)
		}
		return nil
	}); err != nil {
		permissionErr = errors.Join(permissionErr, err)
	}
	return errors.Join(permissionErr, os.RemoveAll(parent))
}

// verifyLocalReceiptFrontendDependency 验证前端离线缓存与锁文件，并冻结对应内容摘要。
func verifyLocalReceiptFrontendDependency(ctx context.Context, trustedGit TrustedGitBinary, repositoryRoot, tree string, dependencies LocalExecutorDependencyInputs) (localExecutorDependencyProof, error) {
	if err := validateLocalReceiptFrontendRoots(dependencies); err != nil {
		return localExecutorDependencyProof{}, err
	}
	packageJSON, err := gitTreeBlob(ctx, trustedGit, repositoryRoot, tree, "frontend-app/package.json")
	if err != nil {
		return localExecutorDependencyProof{}, err
	}
	lock, err := gitTreeBlob(ctx, trustedGit, repositoryRoot, tree, "frontend-app/package-lock.json")
	if err != nil {
		return localExecutorDependencyProof{}, err
	}
	verification, err := verifyFrontendLockOffline(ctx, dependencies.FrontendNPMCache, packageJSON, lock)
	if err != nil {
		return localExecutorDependencyProof{}, err
	}
	contentDigest, err := localReceiptFrontendContentDigest(dependencies)
	if err != nil {
		return localExecutorDependencyProof{}, err
	}
	return localExecutorDependencyProof{name: "frontend", root: dependencies.FrontendNodeModules, secondaryRoot: dependencies.FrontendViteCache, tertiaryRoot: dependencies.FrontendNPMCache, lockDigest: digestBytes(append(append([]byte{}, packageJSON...), lock...)), contentDigest: contentDigest, verification: verification, lockFiles: []localExecutorLockedFile{{path: "frontend-app/package.json", digest: digestBytes(packageJSON)}, {path: "frontend-app/package-lock.json", digest: digestBytes(lock)}}}, nil
}

func validateLocalReceiptFrontendRoots(dependencies LocalExecutorDependencyInputs) error {
	for name, root := range map[string]string{"frontend node_modules": dependencies.FrontendNodeModules, "frontend npm cache": dependencies.FrontendNPMCache, "frontend Vite cache": dependencies.FrontendViteCache} {
		if err := validateLocalDependencyRoot(root, name); err != nil {
			return err
		}
	}
	return nil
}

func localReceiptFrontendContentDigest(dependencies LocalExecutorDependencyInputs) (string, error) {
	nodeModulesDigest, err := contentDigestTree(dependencies.FrontendNodeModules)
	if err != nil {
		return "", err
	}
	viteDigest, err := contentDigestTree(dependencies.FrontendViteCache)
	if err != nil {
		return "", err
	}
	return digestCanonicalJSON(cicontract.LocalFrontendRuntimeContentDomain, []string{nodeModulesDigest, viteDigest})
}

func verifyLocalReceiptEmbedDependency(root string) (localExecutorDependencyProof, error) {
	if err := validateLocalDependencyRoot(root, "frontend embed root"); err != nil {
		return localExecutorDependencyProof{}, err
	}
	canonicalRoot, err := canonicalLocalSandboxPath(root, "local frontend embed dependency root")
	if err != nil {
		return localExecutorDependencyProof{}, err
	}
	contentFiles, contentDigest, err := localReceiptDependencyContentManifest(canonicalRoot, []string{canonicalRoot})
	if err != nil {
		return localExecutorDependencyProof{}, err
	}
	return localExecutorDependencyProof{name: "frontend-embed", root: canonicalRoot, contentDigest: contentDigest, contentFiles: contentFiles}, nil
}
