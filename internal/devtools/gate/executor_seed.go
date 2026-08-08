package gate

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/module"
)

// RuntimeSeedSchemaVersion is the shared image-builder/executor manifest schema.
const RuntimeSeedSchemaVersion = 12

// RuntimeSeedManifest binds immutable runtime seeds to repository lock files.
type RuntimeSeedManifest struct {
	SchemaVersion         uint32 `json:"schema_version"`
	GoSumSHA256           string `json:"go_sum_sha256"`
	ModuleProxyLockSHA256 string `json:"module_proxy_lock_sha256"`
	ModuleProxyTreeSHA256 string `json:"module_proxy_tree_sha256"`
	GoModCacheTreeSHA256  string `json:"go_mod_cache_tree_sha256"`
	PackageLockSHA256     string `json:"package_lock_sha256"`
	NodeModulesTreeSHA256 string `json:"node_modules_tree_sha256"`
	NPMCacheTreeSHA256    string `json:"npm_cache_tree_sha256"`
	ViteCacheTreeSHA256   string `json:"vite_cache_tree_sha256"`
	RipgrepSHA256         string `json:"ripgrep_sha256"`
	SqruffSHA256          string `json:"sqruff_sha256"`
}

type executorPreparedRuntimeSeeds struct {
	runtimeSeedRoot       string
	runtimeSeedManifest   string
	manifest              RuntimeSeedManifest
	goRootsVerified       bool
	frontendRootsVerified bool
}

// installRuntimeSeeds 按执行边界取得已验证 manifest 后安装锁文件绑定的依赖种子。
func installRuntimeSeeds(config executorConfig, layout executorLayout, program ExecutorProgram) error {
	if !program.NeedsGoSeed && !program.NeedsFrontendSeed {
		return nil
	}
	manifest, prepared, err := runtimeSeedManifestForProgram(config, program)
	if err != nil {
		return err
	}
	if program.NeedsGoSeed {
		if err := validateGoRuntimeSeedLocks(layout, manifest); err != nil {
			return err
		}
		if !prepared.goRootsVerified {
			return errors.New("prepared runtime seeds do not cover Go dependency roots")
		}
	}
	if program.NeedsFrontendSeed {
		return installFrontendRuntimeSeed(config, layout, manifest, prepared.frontendRootsVerified)
	}
	return nil
}

// installFrontendRuntimeSeed 校验前端锁与固定镜像根，再为当前分片安装私有可写覆盖层。
func installFrontendRuntimeSeed(
	config executorConfig,
	layout executorLayout,
	manifest RuntimeSeedManifest,
	rootsVerified bool,
) error {
	if !rootsVerified {
		return errors.New("prepared runtime seeds do not cover frontend dependency roots")
	}
	boundFile := filepath.Join(layout.sourceCopy, "frontend-app", "package-lock.json")
	seedRoot := filepath.Join(config.runtimeSeedRoot, "frontend", "node_modules")
	targetRoot := filepath.Join(layout.sourceCopy, "frontend-app", "node_modules")
	if err := validateBoundRuntimeFile(boundFile, manifest.PackageLockSHA256); err != nil {
		return fmt.Errorf("install frontend runtime seed: digest bound source file: %w", err)
	}
	seedPath, err := trustedDirectory(seedRoot, false, -1)
	if err != nil {
		return fmt.Errorf("install frontend runtime seed: runtime seed directory: %w", err)
	}
	viteSeedRoot := filepath.Join(config.runtimeSeedRoot, "frontend", "vite-cache")
	if _, err := trustedDirectory(viteSeedRoot, false, -1); err != nil {
		return fmt.Errorf("install frontend runtime seed: Vite cache root: %w", err)
	}
	if err := installFrontendRuntimeOverlays(seedPath, viteSeedRoot, targetRoot, filepath.Join(layout.tmp, ".vite-temp")); err != nil {
		return err
	}
	if config.executionTiming != nil {
		config.executionTiming.viteCacheSeedHit = true
	}
	return nil
}

// runtimeSeedManifestForProgram 返回当前程序所需且按 plan 或 standalone 边界验证的 seed manifest。
func runtimeSeedManifestForProgram(
	config executorConfig,
	program ExecutorProgram,
) (RuntimeSeedManifest, executorPreparedRuntimeSeeds, error) {
	if config.preparedRuntimeSeeds == nil {
		return standaloneRuntimeSeedManifest(config, program)
	}
	prepared := *config.preparedRuntimeSeeds
	if err := validatePreparedRuntimeSeedManifest(config, program, prepared); err != nil {
		return RuntimeSeedManifest{}, executorPreparedRuntimeSeeds{}, err
	}
	return prepared.manifest, prepared, nil
}

// standaloneRuntimeSeedManifest 为非计划执行完整复验依赖树，禁止借用 ECI accepted proof。
func standaloneRuntimeSeedManifest(
	config executorConfig,
	program ExecutorProgram,
) (RuntimeSeedManifest, executorPreparedRuntimeSeeds, error) {
	prepared, err := prepareExecutorRuntimeSeeds(
		config.runtimeSeedRoot,
		config.runtimeSeedManifest,
		program.NeedsGoSeed,
		program.NeedsFrontendSeed,
	)
	if err != nil {
		return RuntimeSeedManifest{}, executorPreparedRuntimeSeeds{}, err
	}
	if prepared == nil {
		return RuntimeSeedManifest{}, executorPreparedRuntimeSeeds{}, errors.New("runtime seed preparation produced no result")
	}
	if program.NeedsGoSeed {
		if err := validateGoRuntimeSeedTrees(config.runtimeSeedRoot, prepared.manifest); err != nil {
			return RuntimeSeedManifest{}, executorPreparedRuntimeSeeds{}, err
		}
	}
	if program.NeedsFrontendSeed {
		if err := validateFrontendRuntimeSeedTrees(config.runtimeSeedRoot, prepared.manifest); err != nil {
			return RuntimeSeedManifest{}, executorPreparedRuntimeSeeds{}, err
		}
	}
	return prepared.manifest, *prepared, nil
}

// validatePreparedRuntimeSeedManifest 确认 plan proof 精确覆盖当前程序需要的固定 roots。
func validatePreparedRuntimeSeedManifest(
	config executorConfig,
	program ExecutorProgram,
	prepared executorPreparedRuntimeSeeds,
) error {
	if prepared.runtimeSeedRoot != config.runtimeSeedRoot || prepared.runtimeSeedManifest != config.runtimeSeedManifest {
		return errors.New("prepared runtime seed identity does not match executor config")
	}
	if program.NeedsGoSeed && !prepared.goRootsVerified {
		return errors.New("prepared runtime seeds do not cover Go dependencies")
	}
	if program.NeedsFrontendSeed && !prepared.frontendRootsVerified {
		return errors.New("prepared runtime seeds do not cover frontend dependencies")
	}
	return nil
}

// prepareExecutorRuntimeSeeds 每个分片只校验一次 accepted manifest、固定根与只读镜像挂载。
func prepareExecutorRuntimeSeeds(
	runtimeSeedRoot string,
	runtimeSeedManifest string,
	needsGoSeed bool,
	needsFrontendSeed bool,
) (*executorPreparedRuntimeSeeds, error) {
	if !needsGoSeed && !needsFrontendSeed {
		return nil, nil
	}
	manifest, err := LoadRuntimeSeedManifest(runtimeSeedManifest)
	if err != nil {
		return nil, err
	}
	if err := validateRuntimeSeedImageRoots(runtimeSeedRoot, needsGoSeed, needsFrontendSeed); err != nil {
		return nil, err
	}
	return &executorPreparedRuntimeSeeds{
		runtimeSeedRoot: runtimeSeedRoot, runtimeSeedManifest: runtimeSeedManifest, manifest: manifest,
		goRootsVerified: needsGoSeed, frontendRootsVerified: needsFrontendSeed,
	}, nil
}

// validateRuntimeSeedImageRoots 只校验 accepted 不可变镜像的固定根和只读挂载。
// 树摘要由镜像构建/内容验收生成并复验，normal shard 不重复扫描整棵依赖树。
func validateRuntimeSeedImageRoots(runtimeSeedRoot string, needsGoSeed bool, needsFrontendSeed bool) error {
	root, err := trustedDirectory(runtimeSeedRoot, false, -1)
	if err != nil {
		return fmt.Errorf("runtime seed root: %w", err)
	}
	if root == ExecutorRuntimeSeedRoot {
		if err := validateReadOnlyImagePath(root); err != nil {
			return fmt.Errorf("runtime seed image mount: %w", err)
		}
	}
	roots := make([]string, 0, 5)
	if needsGoSeed {
		roots = append(roots, filepath.Join(root, "go-proxy"), filepath.Join(root, "go-mod-cache"))
	}
	if needsFrontendSeed {
		roots = append(roots,
			filepath.Join(root, "frontend", "node_modules"),
			filepath.Join(root, "frontend", "npm-cache"),
			filepath.Join(root, "frontend", "vite-cache"),
		)
	}
	for _, seedRoot := range roots {
		if _, err := trustedDirectory(seedRoot, false, -1); err != nil {
			return fmt.Errorf("runtime seed image root %q: %w", seedRoot, err)
		}
	}
	return nil
}

// validateFrontendRuntimeSeedTrees 为非计划 standalone 执行保留完整树复验。
// ECI normal shard 总是携带 plan 级 prepared proof，不进入此慢路径。
func validateFrontendRuntimeSeedTrees(runtimeSeedRoot string, manifest RuntimeSeedManifest) error {
	for _, seed := range []struct {
		name     string
		path     string
		expected string
	}{
		{name: "node_modules", path: filepath.Join(runtimeSeedRoot, "frontend", "node_modules"), expected: manifest.NodeModulesTreeSHA256},
		{name: "npm cache", path: filepath.Join(runtimeSeedRoot, "frontend", "npm-cache"), expected: manifest.NPMCacheTreeSHA256},
		{name: "Vite cache", path: filepath.Join(runtimeSeedRoot, "frontend", "vite-cache"), expected: manifest.ViteCacheTreeSHA256},
	} {
		if err := validateRuntimeSeedTree(seed.path, seed.expected); err != nil {
			return fmt.Errorf("validate frontend %s seed: %w", seed.name, err)
		}
	}
	return nil
}

// installExecutorSeeds 组合锁文件绑定的运行时依赖与 Go embed 编译占位种子。
func installExecutorSeeds(config executorConfig, layout executorLayout, program ExecutorProgram) error {
	if err := installRuntimeSeeds(config, layout, program); err != nil {
		return err
	}
	if program.NeedsGoSeed {
		seedRoot, err := executorGoBuildCacheSeedRoot(config)
		if err != nil {
			return err
		}
		if config.goBuildCacheRoot == "" {
			if err := seedExecutorGoBuildCache(seedRoot, layout.goCache); err != nil {
				return err
			}
		}
		if err := bindSharedGoModuleCache(filepath.Join(config.runtimeSeedRoot, "go-mod-cache"), layout.goModCache); err != nil {
			return fmt.Errorf("bind shared Go module cache: %w", err)
		}
	}
	if !program.NeedsFrontendEmbedSeed {
		return nil
	}
	return installFrontendEmbedSeed(config, layout)
}

func executorGoBuildCacheSeedRoot(config executorConfig) (string, error) {
	if config.goBuildCacheSeedRoot != "" {
		return config.goBuildCacheSeedRoot, nil
	}
	return "", errors.New("Go build cache seed is not configured")
}

// validateSharedGoModuleCache 确认共享模块缓存和下载元数据均已由基线预热。
func validateSharedGoModuleCache(sharedRoot string) (string, error) {
	sharedPath, err := trustedDirectory(sharedRoot, false, -1)
	if err != nil {
		return "", fmt.Errorf("shared cache: %w", err)
	}
	downloadPath, err := trustedDirectory(filepath.Join(sharedPath, "cache", "download"), false, -1)
	if err != nil {
		return "", fmt.Errorf("shared download metadata: %w", err)
	}
	entries, err := os.ReadDir(downloadPath)
	if err != nil {
		return "", fmt.Errorf("read shared Go module download metadata: %w", err)
	}
	if len(entries) == 0 {
		return "", errors.New("shared Go module download metadata is empty")
	}
	return sharedPath, nil
}

// validateBoundRuntimeFile 校验候选快照中的运行时锁文件与镜像清单一致。
func validateBoundRuntimeFile(path string, expectedDigest string) error {
	digest, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if digest != expectedDigest {
		return errors.New("runtime seed source lock digest does not match snapshot")
	}
	return nil
}

// validateRuntimeSeedTree 校验镜像构建或 standalone 执行中的依赖树摘要。
func validateRuntimeSeedTree(path string, expectedDigest string) error {
	actualDigest, err := RuntimeSeedTreeDigest(path)
	if err != nil {
		return err
	}
	if actualDigest != expectedDigest {
		return fmt.Errorf("runtime seed tree digest %s does not match manifest %s", actualDigest, expectedDigest)
	}
	return nil
}

var errGoBuildCacheMiss = errors.New("Go build cache entry is missing")

func goBuildCachePath(root string, identity []byte, kind string) (string, error) {
	if len(identity) != goBuildCacheHashBytes || (kind != "a" && kind != "d") {
		return "", errors.New("Go build cache path identity is invalid")
	}
	encoded := hex.EncodeToString(identity)
	return filepath.Join(root, encoded[:2], encoded+"-"+kind), nil
}

// installFrontendEmbedSeed 注入与产品代码无关的 Go embed 编译占位种子，不替代前端构建门禁。
func installFrontendEmbedSeed(config executorConfig, layout executorLayout) error {
	seedRoot, err := trustedDirectory(config.frontendEmbedSeedRoot, false, -1)
	if err != nil {
		return fmt.Errorf("frontend embed seed directory: %w", err)
	}
	if err := requireRegularRuntimeSeedFile(filepath.Join(seedRoot, "index.html")); err != nil {
		return fmt.Errorf("frontend embed seed index.html: %w", err)
	}
	targetRoot := filepath.Join(layout.sourceCopy, "cmd", "agent-terminal", "web-dist")
	if _, err := os.Lstat(targetRoot); err == nil {
		return errors.New("frontend embed seed target already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect frontend embed seed target: %w", err)
	}
	if err := copyRuntimeSeed(seedRoot, targetRoot); err != nil {
		return fmt.Errorf("install frontend embed seed: %w", err)
	}
	return nil
}

// LoadRuntimeSeedManifest 从固定镜像路径严格解码种子清单。
func LoadRuntimeSeedManifest(path string) (RuntimeSeedManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return RuntimeSeedManifest{}, fmt.Errorf("open runtime seed manifest: %w", err)
	}
	defer file.Close()
	return DecodeRuntimeSeedManifest(io.LimitReader(file, 1<<20))
}

// DecodeRuntimeSeedManifest 拒绝未知字段、尾随 JSON 和非法摘要。
func DecodeRuntimeSeedManifest(reader io.Reader) (RuntimeSeedManifest, error) {
	if reader == nil {
		return RuntimeSeedManifest{}, errors.New("runtime seed manifest reader is required")
	}
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	var manifest RuntimeSeedManifest
	if err := decoder.Decode(&manifest); err != nil {
		return RuntimeSeedManifest{}, fmt.Errorf("decode runtime seed manifest: %w", err)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return RuntimeSeedManifest{}, err
	}
	if err := manifest.validateDecodedShape(); err != nil {
		return RuntimeSeedManifest{}, err
	}
	return manifest, nil
}

// EncodeRuntimeSeedManifest 输出镜像构建器与执行器共用的规范 JSON。
func EncodeRuntimeSeedManifest(writer io.Writer, manifest RuntimeSeedManifest) error {
	if writer == nil {
		return errors.New("runtime seed manifest writer is required")
	}
	if err := manifest.validateShape(); err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(manifest); err != nil {
		return fmt.Errorf("encode runtime seed manifest: %w", err)
	}
	return nil
}

// executeRuntimeSeedCommand 通过统一 worker 命令写入或复验运行时清单。
func executeRuntimeSeedCommand(args []string) error {
	if len(args) != 3 {
		return errors.New("usage: super-dolphin-gate worker runtime-seed <write|verify> <snapshot-root> <runtime-root>")
	}
	manifest, err := BuildRuntimeSeedManifest(args[1], args[2])
	if err != nil {
		return err
	}
	if _, err := RuntimeSeedTreeDigest(filepath.Join(args[2], "frontend", "node_modules")); err != nil {
		return err
	}
	if _, err := RuntimeSeedTreeDigest(filepath.Join(args[2], "frontend", "npm-cache")); err != nil {
		return err
	}
	switch args[0] {
	case "write":
		return writeRuntimeSeedManifest(args[2], manifest)
	case "verify":
		return verifyRuntimeSeedManifest(args[1], args[2], manifest)
	default:
		return errors.New("runtime seed action must be write or verify")
	}
}

func writeRuntimeSeedManifest(runtimeRoot string, manifest RuntimeSeedManifest) error {
	path := filepath.Join(runtimeRoot, "manifest.json")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	encodeErr := EncodeRuntimeSeedManifest(file, manifest)
	closeErr := file.Close()
	return errors.Join(encodeErr, closeErr)
}

func verifyRuntimeSeedManifest(snapshotRoot, runtimeRoot string, manifest RuntimeSeedManifest) error {
	tracked, err := LoadRuntimeSeedManifest(filepath.Join(runtimeRoot, "manifest.json"))
	if err != nil {
		return err
	}
	if tracked != manifest {
		return fmt.Errorf("runtime seed manifest does not match the immutable image seeds: fields=%s", strings.Join(runtimeSeedManifestDriftFields(tracked, manifest), ","))
	}
	return tracked.Validate(snapshotRoot, runtimeRoot)
}

func (manifest RuntimeSeedManifest) validateShape() error {
	if manifest.SchemaVersion != RuntimeSeedSchemaVersion {
		return fmt.Errorf("runtime seed schema = %d, want %d", manifest.SchemaVersion, RuntimeSeedSchemaVersion)
	}
	return manifest.validateDigestFields(true)
}

// validateDecodedShape 只接受当前镜像清单 schema，拒绝旧路径回流。
func (manifest RuntimeSeedManifest) validateDecodedShape() error {
	return manifest.validateShape()
}

func (manifest RuntimeSeedManifest) validateDigestFields(requireNPMCache bool) error {
	digests := map[string]string{
		"go_sum_sha256":            manifest.GoSumSHA256,
		"module_proxy_lock_sha256": manifest.ModuleProxyLockSHA256,
		"module_proxy_tree_sha256": manifest.ModuleProxyTreeSHA256,
		"go_mod_cache_tree_sha256": manifest.GoModCacheTreeSHA256,
		"package_lock_sha256":      manifest.PackageLockSHA256,
		"node_modules_tree_sha256": manifest.NodeModulesTreeSHA256,
		"vite_cache_tree_sha256":   manifest.ViteCacheTreeSHA256,
		"ripgrep_sha256":           manifest.RipgrepSHA256,
		"sqruff_sha256":            manifest.SqruffSHA256,
	}
	if requireNPMCache {
		digests["npm_cache_tree_sha256"] = manifest.NPMCacheTreeSHA256
	}
	for name, digest := range digests {
		if !validSHA256Digest(digest) {
			return fmt.Errorf("runtime seed manifest %s is invalid", name)
		}
	}
	return nil
}

// BuildRuntimeSeedManifest 从源码快照与运行时种子根构造共享清单。
func BuildRuntimeSeedManifest(snapshotRoot string, runtimeRoot string) (RuntimeSeedManifest, error) {
	snapshotPath, err := trustedDirectory(snapshotRoot, false, -1)
	if err != nil {
		return RuntimeSeedManifest{}, fmt.Errorf("source snapshot: %w", err)
	}
	runtimePath, err := trustedDirectory(runtimeRoot, false, -1)
	if err != nil {
		return RuntimeSeedManifest{}, fmt.Errorf("runtime seed root: %w", err)
	}
	goSum, err := fileSHA256(filepath.Join(snapshotPath, "go.sum"))
	if err != nil {
		return RuntimeSeedManifest{}, fmt.Errorf("digest go.sum: %w", err)
	}
	moduleProxyLock, moduleProxy, err := runtimeModuleProxyDigests(snapshotPath, runtimePath)
	if err != nil {
		return RuntimeSeedManifest{}, err
	}
	goModCache, err := RuntimeSeedTreeDigest(filepath.Join(runtimePath, "go-mod-cache"))
	if err != nil {
		return RuntimeSeedManifest{}, fmt.Errorf("digest Go module cache: %w", err)
	}
	packageLock, nodeModules, npmCache, viteCache, err := runtimeFrontendSeedDigests(snapshotPath, runtimePath)
	if err != nil {
		return RuntimeSeedManifest{}, err
	}
	ripgrep, err := fileSHA256(filepath.Join(runtimePath, "bin", "rg"))
	if err != nil {
		return RuntimeSeedManifest{}, fmt.Errorf("digest ripgrep: %w", err)
	}
	sqruff, err := fileSHA256(filepath.Join(runtimePath, "bin", "sqruff"))
	if err != nil {
		return RuntimeSeedManifest{}, fmt.Errorf("digest sqruff: %w", err)
	}
	return RuntimeSeedManifest{
		SchemaVersion: RuntimeSeedSchemaVersion, GoSumSHA256: goSum,
		ModuleProxyLockSHA256: moduleProxyLock, ModuleProxyTreeSHA256: moduleProxy,
		GoModCacheTreeSHA256: goModCache,
		PackageLockSHA256:    packageLock, NodeModulesTreeSHA256: nodeModules, NPMCacheTreeSHA256: npmCache, ViteCacheTreeSHA256: viteCache,
		RipgrepSHA256: ripgrep, SqruffSHA256: sqruff,
	}, nil
}

// runtimeFrontendSeedDigests 绑定前端锁文件和不可变安装树。
func runtimeFrontendSeedDigests(snapshotPath string, runtimePath string) (string, string, string, string, error) {
	packageLock, err := fileSHA256(filepath.Join(snapshotPath, "frontend-app", "package-lock.json"))
	if err != nil {
		return "", "", "", "", fmt.Errorf("digest package-lock.json: %w", err)
	}
	nodeModules, err := RuntimeSeedTreeDigest(filepath.Join(runtimePath, "frontend", "node_modules"))
	if err != nil {
		return "", "", "", "", err
	}
	npmCache, err := RuntimeSeedTreeDigest(filepath.Join(runtimePath, "frontend", "npm-cache"))
	if err != nil {
		return "", "", "", "", err
	}
	viteCache, err := RuntimeSeedTreeDigest(filepath.Join(runtimePath, "frontend", "vite-cache"))
	if err != nil {
		return "", "", "", "", err
	}
	return packageLock, nodeModules, npmCache, viteCache, nil
}

// runtimeModuleProxyDigests 验证完整 file proxy 后计算锁文件与种子树摘要。
func runtimeModuleProxyDigests(snapshotPath string, runtimePath string) (string, string, error) {
	proxyLockPath := filepath.Join(snapshotPath, "build", "gate", "runtime-proxy", "go.sum")
	proxyRoot := filepath.Join(runtimePath, "go-proxy")
	if err := validateLockedModuleProxy(proxyLockPath, proxyRoot); err != nil {
		return "", "", err
	}
	lockDigest, err := fileSHA256(proxyLockPath)
	if err != nil {
		return "", "", fmt.Errorf("digest runtime module proxy lock: %w", err)
	}
	treeDigest, err := RuntimeSeedTreeDigest(proxyRoot)
	if err != nil {
		return "", "", err
	}
	return lockDigest, treeDigest, nil
}

// validateLockedModuleProxy 验证专用 go.sum 中每个完整模块摘要都有可离线读取的 file proxy 条目。
func validateLockedModuleProxy(lockPath string, proxyRoot string) error {
	file, err := os.Open(lockPath)
	if err != nil {
		return fmt.Errorf("open runtime module proxy lock: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	locked := 0
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 || strings.HasSuffix(fields[1], "/go.mod") {
			continue
		}
		if err := validateLockedModuleProxyEntry(proxyRoot, fields[0], fields[1], fields[2]); err != nil {
			return err
		}
		locked++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read runtime module proxy lock: %w", err)
	}
	if locked == 0 {
		return errors.New("runtime module proxy lock has no complete module checksums")
	}
	return nil
}

// validateLockedModuleProxyEntry 验证单个锁定 module/version 的完整 file proxy 协议文件。
func validateLockedModuleProxyEntry(proxyRoot string, modulePath string, version string, checksum string) error {
	escapedPath, err := module.EscapePath(modulePath)
	if err != nil {
		return fmt.Errorf("escape runtime module proxy path %q: %w", modulePath, err)
	}
	escapedVersion, err := module.EscapeVersion(version)
	if err != nil {
		return fmt.Errorf("escape runtime module proxy version %q: %w", version, err)
	}
	versionRoot := filepath.Join(proxyRoot, filepath.FromSlash(escapedPath), "@v")
	for _, suffix := range []string{".info", ".mod", ".zip", ".ziphash"} {
		if err := requireRegularRuntimeSeedFile(filepath.Join(versionRoot, escapedVersion+suffix)); err != nil {
			return fmt.Errorf("runtime module proxy %s@%s%s: %w", modulePath, version, suffix, err)
		}
	}
	zipHash, err := os.ReadFile(filepath.Join(versionRoot, escapedVersion+".ziphash"))
	if err != nil || strings.TrimSpace(string(zipHash)) != checksum {
		return fmt.Errorf("runtime module proxy %s@%s zip checksum does not match lock", modulePath, version)
	}
	list, err := os.ReadFile(filepath.Join(versionRoot, "list"))
	if err != nil || !slices.Contains(slices.Collect(strings.SplitSeq(string(list), "\n")), version) {
		return fmt.Errorf("runtime module proxy %s@%s is absent from version list", modulePath, version)
	}
	return nil
}

// requireRegularRuntimeSeedFile 拒绝缺失文件、目录和符号链接。
func requireRegularRuntimeSeedFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("path is not a regular file")
	}
	return nil
}

// Validate 重算锁文件与种子树摘要，确认清单未漂移。
func (manifest RuntimeSeedManifest) Validate(snapshotRoot string, runtimeRoot string) error {
	if err := manifest.validateShape(); err != nil {
		return err
	}
	actual, err := BuildRuntimeSeedManifest(snapshotRoot, runtimeRoot)
	if err != nil {
		return err
	}
	if actual != manifest {
		return errors.New("runtime seed manifest does not match snapshot or seed content")
	}
	return nil
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("runtime seed manifest has trailing JSON")
		}
		return fmt.Errorf("decode runtime seed manifest trailer: %w", err)
	}
	return nil
}

func validSHA256Digest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", digest.Sum(nil)), nil
}

// RuntimeSeedTreeDigest 计算镜像构建器与执行器共享的规范目录树摘要。
func RuntimeSeedTreeDigest(root string) (string, error) {
	resolvedRoot, err := trustedDirectory(root, false, -1)
	if err != nil {
		return "", fmt.Errorf("runtime seed tree root: %w", err)
	}
	digest := sha256.New()
	if err := writeSeedRecord(digest, 'V', []byte("super-dolphin-runtime-seed-tree"), []byte("1")); err != nil {
		return "", fmt.Errorf("initialize runtime seed tree digest: %w", err)
	}
	err = filepath.WalkDir(resolvedRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(resolvedRoot, path)
		if err != nil || relative == "." {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		return hashSeedEntry(digest, resolvedRoot, relative, path, info)
	})
	if err != nil {
		return "", fmt.Errorf("digest runtime seed tree: %w", err)
	}
	return fmt.Sprintf("sha256:%x", digest.Sum(nil)), nil
}

// hashSeedEntry 把路径、类型、权限、内容或安全链接目标纳入树摘要。
func hashSeedEntry(digest hash.Hash, root string, relative string, path string, info fs.FileInfo) error {
	relative = filepath.ToSlash(relative)
	mode := make([]byte, 4)
	binary.BigEndian.PutUint32(mode, uint32(info.Mode().Perm()))
	switch {
	case info.IsDir():
		return writeSeedRecord(digest, 'D', []byte(relative), mode)
	case info.Mode().IsRegular():
		contentDigest, err := regularFileContentDigest(path, info.Size())
		if err != nil {
			return err
		}
		size := make([]byte, 8)
		binary.BigEndian.PutUint64(size, uint64(info.Size()))
		return writeSeedRecord(digest, 'F', []byte(relative), mode, size, contentDigest)
	case info.Mode()&os.ModeSymlink != 0:
		target, err := safeSeedSymlink(root, relative, path)
		if err != nil {
			return err
		}
		return writeSeedRecord(digest, 'L', []byte(relative), []byte(target))
	default:
		return fmt.Errorf("runtime seed entry %q has forbidden type", relative)
	}
}

// writeSeedRecord 用固定字段计数和长度前缀写入一个不可歧义的摘要记录。
func writeSeedRecord(digest hash.Hash, kind byte, fields ...[]byte) error {
	if digest == nil {
		return errors.New("runtime seed digest is required")
	}
	fieldCount := make([]byte, 4)
	binary.BigEndian.PutUint32(fieldCount, uint32(len(fields)))
	if _, err := digest.Write(append([]byte{kind}, fieldCount...)); err != nil {
		return err
	}
	for _, field := range fields {
		length := make([]byte, 8)
		binary.BigEndian.PutUint64(length, uint64(len(field)))
		if _, err := digest.Write(length); err != nil {
			return err
		}
		if _, err := digest.Write(field); err != nil {
			return err
		}
	}
	return nil
}

// regularFileContentDigest 计算独立内容摘要并拒绝哈希期间可见的大小漂移。
func regularFileContentDigest(path string, expectedSize int64) (_ []byte, retErr error) {
	if expectedSize < 0 {
		return nil, errors.New("runtime seed file size is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, file.Close()) }()
	digest := sha256.New()
	written, err := io.Copy(digest, file)
	if err != nil {
		return nil, err
	}
	if written != expectedSize {
		return nil, errors.New("runtime seed file size changed while hashing")
	}
	return digest.Sum(nil), nil
}

// safeSeedSymlink 仅允许最终解析结果仍位于种子根内的相对链接。
func safeSeedSymlink(root string, relative string, path string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil || filepath.IsAbs(target) {
		return "", errors.New("runtime seed symlink target is invalid")
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(filepath.FromSlash(relative)), target))
	if resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
		return "", errors.New("runtime seed symlink escapes seed root")
	}
	resolvedTarget, err := filepath.EvalSymlinks(filepath.Join(root, resolved))
	if err != nil {
		return "", errors.New("runtime seed symlink target is missing")
	}
	if resolvedTarget != root && !pathContains(root, resolvedTarget) {
		return "", errors.New("runtime seed symlink chain escapes seed root")
	}
	return target, nil
}

// copyRuntimeSeed 防覆盖复制已校验的种子，并在最后恢复目录权限。
func copyRuntimeSeed(sourceRoot string, targetRoot string) error {
	if err := os.Mkdir(targetRoot, 0o700); err != nil {
		return fmt.Errorf("create runtime seed target: %w", err)
	}
	directories := []copiedDirectory{{path: targetRoot, mode: 0o700}}
	copier := runtimeSeedCopier{sourceRoot: sourceRoot, targetRoot: targetRoot, directories: &directories}
	err := filepath.WalkDir(sourceRoot, copier.copy)
	if err != nil {
		return fmt.Errorf("copy runtime seed: %w", err)
	}
	for _, directory := range slices.Backward(directories) {
		if err := os.Chmod(directory.path, directory.mode); err != nil {
			return err
		}
	}
	return nil
}

type runtimeSeedCopier struct {
	sourceRoot  string
	targetRoot  string
	directories *[]copiedDirectory
}

func (copier runtimeSeedCopier) copy(sourcePath string, _ fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	relative, err := filepath.Rel(copier.sourceRoot, sourcePath)
	if err != nil || relative == "." {
		return err
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return err
	}
	return copier.copyEntry(sourcePath, relative, info)
}

// copyEntry 按条目类型复制目录、普通文件或已验证的内部链接。
func (copier runtimeSeedCopier) copyEntry(sourcePath string, relative string, info fs.FileInfo) error {
	targetPath := filepath.Join(copier.targetRoot, relative)
	switch {
	case info.IsDir():
		if err := os.Mkdir(targetPath, 0o700); err != nil {
			return err
		}
		*copier.directories = append(*copier.directories, copiedDirectory{path: targetPath, mode: info.Mode().Perm()})
		return nil
	case info.Mode().IsRegular():
		return copyRegularFile(sourcePath, targetPath, info.Mode().Perm())
	case info.Mode()&os.ModeSymlink != 0:
		target, err := safeSeedSymlink(copier.sourceRoot, filepath.ToSlash(relative), sourcePath)
		if err != nil {
			return err
		}
		return os.Symlink(target, targetPath)
	default:
		return errors.New("runtime seed contains a forbidden entry type")
	}
}
