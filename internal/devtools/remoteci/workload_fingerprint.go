package remoteci

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	remoteWorkloadInputSchemaVersion = 4
	remoteGitTreeListingMaxBytes     = 64 << 20
	remoteGitBlobMaxBytes            = 16 << 20
	remoteGitSourceTotalMaxBytes     = 256 << 20
)

type remoteGitTreeEntry struct {
	mode     string
	kind     string
	objectID string
	path     string
}

type remoteGitTreeSnapshot struct {
	repositoryRoot  string
	tree            string
	entries         []remoteGitTreeEntry
	byPath          map[string]remoteGitTreeEntry
	goSources       map[string][]byte
	frontendSources map[string][]byte
	moduleMappings  []remoteGoModuleMapping
	goSourcesMu     sync.Mutex

	cacheMu                       sync.Mutex
	exactCompileRootMu            sync.Mutex
	productionIndexMu             sync.Mutex
	productionClosureCache        map[string]remoteProductionClosureCache
	goTestDeclarationCache        map[string]remoteGoTestDeclarationCache
	exactCompileRootCache         map[remoteExactCompileRootKey]remoteExactCompileRootCacheEntry
	productionIndexCache          map[string]remoteGoProductionIndexCacheEntry
	goWorkloadSharedScript        *remoteGitTreeEntry
	goPackageInputDigestCache     map[remoteGoPackageInputDigestKey]string
	goEmbedResolutionCache        map[remoteGoEmbedResolutionKey]remoteGoEmbedResolutionCache
	workerExecutionDigestCache    string
	goEmbedResolutionComputations uint64
	goEmbedResolutionCacheHits    uint64
	exactCompileRootComputations  uint64
	productionIndexComputations   uint64
}

type remoteGoPackageInputDigestKey struct {
	target string
	race   bool
}

// remoteGoEmbedResolutionKey 将 go:embed 解析绑定到当前 snapshot 中的包目录和源码内容身份。
// snapshot-local cache 不跨 tree/run 共享，内容摘要避免同一目录中的可变 source 切换复用旧结果。
type remoteGoEmbedResolutionKey struct {
	directory      string
	sourceIdentity [sha256.Size]byte
}

type remoteGoEmbedResolutionCache struct {
	entries []remoteGitTreeEntry
	err     error
}

type remoteProductionClosureCache struct {
	entries []remoteGitTreeEntry
	err     error
}

type remoteGoTestDeclarationCache struct {
	files        []remoteGoTestFile
	declarations map[string][]remoteGoTestDeclaration
	fallback     bool
}

type remoteGoModuleMapping struct {
	importPath string
	directory  string
}

// remoteWorkloadInputDigests 为每个测试或门禁计算与批次、分片和提交身份无关的生产输入摘要。
func remoteWorkloadInputDigests(
	ctx context.Context,
	repositoryRoot string,
	tree string,
	workloads []gate.Workload,
) (map[string]string, error) {
	snapshot, err := loadRemoteGitTreeSnapshot(ctx, repositoryRoot, tree)
	if err != nil {
		return nil, err
	}
	return snapshot.remoteWorkloadInputDigests(ctx, workloads)
}

// ResolveWorkerExecutionDigest 只摘要受控的 linux/amd64 Worker 执行契约。
func ResolveWorkerExecutionDigest(ctx context.Context, repositoryRoot string, tree string) (string, error) {
	snapshot, err := loadRemoteGitTreeSnapshot(ctx, repositoryRoot, tree)
	if err != nil {
		return "", err
	}
	return snapshot.workerExecutionDigest(ctx)
}

func (snapshot *remoteGitTreeSnapshot) workerExecutionDigest(ctx context.Context) (string, error) {
	snapshot.cacheMu.Lock()
	if snapshot.workerExecutionDigestCache != "" {
		digest := snapshot.workerExecutionDigestCache
		snapshot.cacheMu.Unlock()
		return digest, nil
	}
	snapshot.cacheMu.Unlock()
	digest, err := snapshot.workerExecutionContractDigest(ctx)
	if err != nil {
		return "", err
	}
	snapshot.cacheMu.Lock()
	if snapshot.workerExecutionDigestCache == "" {
		snapshot.workerExecutionDigestCache = digest
	} else {
		digest = snapshot.workerExecutionDigestCache
	}
	snapshot.cacheMu.Unlock()
	return digest, nil
}

func (snapshot *remoteGitTreeSnapshot) remoteWorkloadInputDigests(
	ctx context.Context,
	workloads []gate.Workload,
) (map[string]string, error) {
	digests := make(map[string]string, len(workloads))
	for _, workload := range workloads {
		digest, err := snapshot.workloadInputDigest(ctx, workload)
		if err != nil {
			return nil, fmt.Errorf("fingerprint workload %q: %w", workload.ID, err)
		}
		digests[workload.ID] = digest
	}
	return digests, nil
}
