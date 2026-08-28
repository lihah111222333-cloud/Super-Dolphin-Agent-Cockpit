package remotecicli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
)

type portableGoPlatform struct {
	name     string
	platform remoteportable.RepositoryWorkloadPlatform
	runtime  remoteportable.RepositoryWorkloadRuntime
}

func portableGoCatalogPlatforms() [2]portableGoPlatform {
	return [2]portableGoPlatform{
		{name: "linux-amd64", platform: remoteportable.RepositoryWorkloadPlatformLinuxAMD64, runtime: remoteportable.RepositoryWorkloadRuntimeUbuntuContainer},
		{name: "windows-amd64-wine", platform: remoteportable.RepositoryWorkloadPlatformWindowsAMD64, runtime: remoteportable.RepositoryWorkloadRuntimeUbuntuWineContainer},
	}
}

// expandDetectedRepositoryGoCatalog 从 exact tree 派生 execution workloads，不改写原始 tree blob。
func expandDetectedRepositoryGoCatalog(ctx context.Context, repository DetectedRepository) (DetectedRepository, error) {
	if err := validatePortableCatalogRepository(ctx, repository); err != nil {
		return DetectedRepository{}, err
	}
	seenUnitIDs, err := portableWorkloadUnitIDs(repository.Workloads)
	if err != nil {
		return DetectedRepository{}, err
	}
	reader, err := newPortableCatalogGitObjectResolver(repository.Root, repository.Manifest.CatalogLimits)
	if err != nil {
		return DetectedRepository{}, err
	}
	return expandGeneratedPortableCatalog(ctx, reader, repository, seenUnitIDs)
}

func newPortableCatalogGitObjectResolver(repositoryRoot string, limits remoteportable.PortableManifestCatalogLimits) (gitObjectResolver, error) {
	if err := limits.Validate(); err != nil {
		return gitObjectResolver{}, err
	}
	return gitObjectResolver{repositoryRoot: repositoryRoot, maxTreeOutputBytes: int(limits.MaxTreeOutputBytes), maxTreeEntries: limits.MaxTreeEntries}, nil
}

func validatePortableCatalogRepository(ctx context.Context, repository DetectedRepository) error {
	if ctx == nil || repository.Root == "" || repository.TreeSHA == "" {
		return errors.New("portable Go catalog repository identity is invalid")
	}
	if err := repository.Manifest.Validate(); err != nil {
		return fmt.Errorf("validate portable Go catalog manifest: %w", err)
	}
	return nil
}

func expandGeneratedPortableCatalog(ctx context.Context, reader remoteportable.ExactGitTreeReader, repository DetectedRepository, seenUnitIDs map[string]struct{}) (DetectedRepository, error) {
	cachedReader := newPortableCatalogCachedReader(reader)
	for _, target := range portableGoCatalogPlatforms() {
		workloads, err := portableGoTargetWorkloads(ctx, cachedReader, repository.TreeSHA, repository.Manifest.Modules, repository.Manifest.ExcludedNestedModules, repository.Manifest.CatalogPolicies, target)
		if err != nil {
			return DetectedRepository{}, closePortableCatalogReader(cachedReader, err)
		}
		if err := appendPortableGeneratedWorkloads(&repository.Workloads, seenUnitIDs, workloads); err != nil {
			return DetectedRepository{}, closePortableCatalogReader(cachedReader, err)
		}
	}
	if err := cachedReader.Close(); err != nil {
		return DetectedRepository{}, err
	}
	slices.SortFunc(repository.Workloads, func(left, right remoteportable.PortableManifestWorkload) int {
		return strings.Compare(left.UnitID, right.UnitID)
	})
	return repository, nil
}

// portableCatalogCachedReader 在同一次 catalog 绑定内复用 exact-tree listing 与 Git batch 进程，不跨请求保存状态。
type portableCatalogCachedReader struct {
	reader    remoteportable.ExactGitTreeReader
	entries   map[string][]remoteportable.GitTreeEntry
	regular   map[string]map[string]struct{}
	batch     portableCatalogBlobBatch
	batchTree string
}

type portableCatalogBatchBlobReader interface {
	newListedBlobBatch(context.Context, string) (portableCatalogBlobBatch, error)
}

type portableCatalogBlobBatch interface {
	ReadBlob(context.Context, string, string) ([]byte, error)
	Close() error
}

// newPortableCatalogCachedReader 创建仅限当前请求的 exact-tree 读缓存。
func newPortableCatalogCachedReader(reader remoteportable.ExactGitTreeReader) *portableCatalogCachedReader {
	return &portableCatalogCachedReader{
		reader: reader, entries: make(map[string][]remoteportable.GitTreeEntry), regular: make(map[string]map[string]struct{}),
	}
}

// ListTree 返回独立的 exact-tree listing 副本，避免两个平台重复启动 Git 进程。
func (reader *portableCatalogCachedReader) ListTree(ctx context.Context, tree string) ([]remoteportable.GitTreeEntry, error) {
	if err := portableCatalogCacheContext(ctx); err != nil {
		return nil, err
	}
	if entries, ok := reader.entries[tree]; ok {
		return slices.Clone(entries), nil
	}
	entries, err := reader.reader.ListTree(ctx, tree)
	if err != nil {
		return nil, err
	}
	reader.entries[tree] = slices.Clone(entries)
	reader.regular[tree] = portableCatalogRegularPaths(entries)
	return slices.Clone(entries), nil
}

func portableCatalogRegularPaths(entries []remoteportable.GitTreeEntry) map[string]struct{} {
	regular := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.Kind == "blob" && (entry.Mode == "100644" || entry.Mode == "100755") {
			regular[entry.Path] = struct{}{}
		}
	}
	return regular
}

// ReadBlob 只读取 listing 已证明的 regular blob，并让两个平台共享一个有界 Git batch 进程。
func (reader *portableCatalogCachedReader) ReadBlob(ctx context.Context, tree, filePath string) ([]byte, error) {
	if err := portableCatalogCacheContext(ctx); err != nil {
		return nil, err
	}
	return reader.readUpstreamBlob(ctx, tree, filePath)
}

func (reader *portableCatalogCachedReader) readUpstreamBlob(ctx context.Context, tree, filePath string) ([]byte, error) {
	if regular, listed := reader.regular[tree]; listed {
		if _, ok := regular[filePath]; !ok {
			return nil, errors.New("portable catalog blob is not a listed regular file")
		}
		if batchReader, ok := reader.reader.(portableCatalogBatchBlobReader); ok {
			if reader.batch == nil {
				batch, err := batchReader.newListedBlobBatch(ctx, tree)
				if err != nil {
					return nil, err
				}
				reader.batch = batch
				reader.batchTree = tree
			}
			if tree != reader.batchTree {
				return nil, errors.New("portable catalog batch tree changed")
			}
			return reader.batch.ReadBlob(ctx, tree, filePath)
		}
	}
	return reader.reader.ReadBlob(ctx, tree, filePath)
}

// Close 回收 request-local Git batch，并核对仓库根身份未漂移。
func (reader *portableCatalogCachedReader) Close() error {
	if reader == nil || reader.batch == nil {
		return nil
	}
	err := reader.batch.Close()
	reader.batch = nil
	reader.batchTree = ""
	return err
}

func closePortableCatalogReader(reader *portableCatalogCachedReader, cause error) error {
	closeErr := reader.Close()
	if closeErr == nil {
		return cause
	}
	return errors.Join(cause, closeErr)
}

func portableCatalogCacheContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("portable catalog cache context is required")
	}
	return ctx.Err()
}

// portableGoTargetWorkloads 为一个平台读取 manifest policy 并从 exact tree 生成 package workload。
func portableGoTargetWorkloads(ctx context.Context, reader remoteportable.ExactGitTreeReader, tree string, modules []remoteportable.RepositoryWorkloadModule, excludedNestedModules []string, policies []remoteportable.PortableManifestCatalogPolicy, target portableGoPlatform) ([]remoteportable.PortableManifestWorkload, error) {
	policy, err := portableGoCatalogPolicy(policies, target)
	if err != nil {
		return nil, err
	}
	inventory, err := remoteportable.BuildPortableGoWorkloadInventory(ctx, reader, tree, string(target.platform), modules, excludedNestedModules)
	if err != nil {
		return nil, fmt.Errorf("build %s portable Go inventory: %w", target.name, err)
	}
	workloads, err := portableGoPackageWorkloads(modules, inventory.GoPackages, target, policy)
	if err != nil {
		return nil, err
	}
	return workloads, nil
}

// portableWorkloadUnitIDs 校验并索引手工 workload 的稳定 UnitID。
func portableWorkloadUnitIDs(workloads []remoteportable.PortableManifestWorkload) (map[string]struct{}, error) {
	seen := make(map[string]struct{}, len(workloads))
	for _, workload := range workloads {
		if _, duplicate := seen[workload.UnitID]; duplicate {
			return nil, fmt.Errorf("portable workload unit %q is duplicated", workload.UnitID)
		}
		seen[workload.UnitID] = struct{}{}
	}
	return seen, nil
}

// appendPortableGeneratedWorkloads 追加 exact-tree 目录并拒绝与手工 workload 冲突。
func appendPortableGeneratedWorkloads(workloads *[]remoteportable.PortableManifestWorkload, seen map[string]struct{}, generated []remoteportable.PortableManifestWorkload) error {
	for _, workload := range generated {
		if _, duplicate := seen[workload.UnitID]; duplicate {
			return fmt.Errorf("portable workload unit %q is duplicated", workload.UnitID)
		}
		seen[workload.UnitID] = struct{}{}
		*workloads = append(*workloads, workload)
	}
	return nil
}

// portableGoCatalogPolicy 读取目标平台的显式资源策略并校验 runtime 配对。
func portableGoCatalogPolicy(policies []remoteportable.PortableManifestCatalogPolicy, target portableGoPlatform) (remoteportable.PortableManifestCatalogPolicy, error) {
	for _, policy := range policies {
		if policy.Platform == string(target.platform) {
			if policy.Runtime != string(target.runtime) {
				return remoteportable.PortableManifestCatalogPolicy{}, fmt.Errorf("portable Go catalog policy runtime for %s is invalid", target.name)
			}
			return policy, nil
		}
	}
	return remoteportable.PortableManifestCatalogPolicy{}, fmt.Errorf("portable Go catalog policy for %s is missing", target.name)
}

func portableGoPackageWorkloads(modules []remoteportable.RepositoryWorkloadModule, packages []string, target portableGoPlatform, policy remoteportable.PortableManifestCatalogPolicy) ([]remoteportable.PortableManifestWorkload, error) {
	workloads := make([]remoteportable.PortableManifestWorkload, 0, len(packages))
	for _, packageTarget := range packages {
		moduleRoot, relativePackage, err := portablePackageModule(modules, packageTarget)
		if err != nil {
			return nil, err
		}
		unitID := portablePackageWorkloadID(target.name, policy.ExecutionPolicy, packageTarget)
		workload := remoteportable.PortableManifestWorkload{
			UnitID: unitID, CommandDigest: digestBytes([]byte(unitID)), TargetPlatform: string(target.platform), RuntimeCapability: string(target.runtime),
			CPUMilli: policy.CPUMilli, MemoryBytes: policy.MemoryBytes, HistoricalEstimateMS: policy.HistoricalEstimateMS,
			Kind: remoteportable.RepositoryWorkloadKindGoPackage, ModuleRoot: moduleRoot, Package: relativePackage, ExecutionPolicy: policy.ExecutionPolicy,
		}
		executionDigest, err := remoteportable.DerivePortableWorkloadExecutionDigest(workload)
		if err != nil {
			return nil, fmt.Errorf("derive portable workload execution digest: %w", err)
		}
		workload.ExecutionDigest = executionDigest
		workloads = append(workloads, workload)
	}
	return workloads, nil
}

// portablePackageModule 将 exact-tree package target 解析为声明模块根与模块内包路径。
func portablePackageModule(modules []remoteportable.RepositoryWorkloadModule, packageTarget string) (string, string, error) {
	if moduleRoot, relative, ok := strings.Cut(packageTarget, "\x00"); ok {
		return moduleRoot, relative, nil
	}
	for _, module := range slices.Backward(modules) {
		root := module.Root
		if root == "." {
			continue
		}
		prefix := "./" + root
		if packageTarget == prefix {
			return root, ".", nil
		}
		if relative, ok := strings.CutPrefix(packageTarget, prefix+"/"); ok {
			return root, "./" + relative, nil
		}
	}
	if slices.ContainsFunc(modules, func(module remoteportable.RepositoryWorkloadModule) bool { return module.Root == "." }) {
		return ".", packageTarget, nil
	}
	return "", "", fmt.Errorf("package %q is outside declared modules", packageTarget)
}

func portablePackageWorkloadID(platform, policy, packageTarget string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(path.Clean(packageTarget)))
	return "auto-go-package/" + platform + "/" + policy + "/" + encoded
}
