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
	productionClosureCache        map[string]remoteProductionClosureCache
	goTestDeclarationCache        map[string]remoteGoTestDeclarationCache
	goWorkloadSharedScript        *remoteGitTreeEntry
	goPackageInputDigestCache     map[remoteGoPackageInputDigestKey]string
	goPackageInputEntriesCache    map[remoteGoPackageInputDigestKey][]remoteGitTreeEntry
	goEmbedResolutionCache        map[remoteGoEmbedResolutionKey]remoteGoEmbedResolutionCache
	workerExecutionDigestCache    string
	closureCaptureCalls           uint64
	goEmbedResolutionComputations uint64
	goEmbedResolutionCacheHits    uint64
	closureCapture                *remoteInputClosureCapture
	closureCaptureMu              sync.Mutex
	closureCaptureStateMu         sync.RWMutex
}

// remoteInputClosureCapture 收集一次 workload 指纹实际写入摘要的 Git tree 条目。
// 它只用于迁移候选的保守负路径，不改变生产指纹本身。
type remoteInputClosureCapture struct {
	mu      sync.Mutex
	entries []remoteGitTreeEntry
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

// workloadInputDigestWithClosure 在同一次 exact-tree 指纹计算中返回摘要实际
// 观察的条目集合，避免 Prepare 完成后迁移 resolver 再次解析 AST/blob。
func (snapshot *remoteGitTreeSnapshot) workloadInputDigestWithClosure(
	ctx context.Context,
	workload gate.Workload,
) (string, []remoteGitTreeEntry, error) {
	if snapshot == nil {
		return "", nil, fmt.Errorf("remote workload fingerprint snapshot is required")
	}
	snapshot.cacheMu.Lock()
	snapshot.closureCaptureCalls++
	snapshot.cacheMu.Unlock()
	if remoteWorkloadClosureNeedsGoSources(workload) {
		if err := snapshot.prepareGoSources(ctx); err != nil {
			return "", nil, err
		}
	}
	snapshot.closureCaptureMu.Lock()
	defer snapshot.closureCaptureMu.Unlock()
	capture := &remoteInputClosureCapture{}
	snapshot.closureCaptureStateMu.Lock()
	snapshot.closureCapture = capture
	snapshot.closureCaptureStateMu.Unlock()
	defer func() {
		snapshot.closureCaptureStateMu.Lock()
		snapshot.closureCapture = nil
		snapshot.closureCaptureStateMu.Unlock()
	}()
	digest, err := snapshot.workloadInputDigest(ctx, workload)
	if err != nil {
		return "", nil, err
	}
	capture.mu.Lock()
	entries := capture.entries
	capture.mu.Unlock()
	if len(entries) == 0 {
		return "", nil, fmt.Errorf("remote workload %q observed input closure is empty", workload.ID)
	}
	return digest, entries, nil
}

func (snapshot *remoteGitTreeSnapshot) closureCaptureCount() uint64 {
	if snapshot == nil {
		return 0
	}
	snapshot.cacheMu.Lock()
	defer snapshot.cacheMu.Unlock()
	return snapshot.closureCaptureCalls
}

// workloadInputClosureEntries 返回当前 fingerprint 算法实际观察的条目集合。
// 捕获窗口由独立互斥锁串行化，并复用 snapshot 已建立的解析缓存；捕获结束
// 后立即清空状态，不改变正常指纹结果。
func (snapshot *remoteGitTreeSnapshot) workloadInputClosureEntries(ctx context.Context, workload gate.Workload) ([]remoteGitTreeEntry, error) {
	_, closure, err := snapshot.workloadInputDigestWithClosure(ctx, workload)
	return closure, err
}

// remoteWorkloadClosureNeedsGoSources 判断迁移 closure 捕获是否需要预加载 Go 源码。
func remoteWorkloadClosureNeedsGoSources(workload gate.Workload) bool {
	_, targetKind, target, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil || !targeted {
		return false
	}
	switch targetKind {
	case gate.WorkloadTargetGoPackage, gate.WorkloadTargetGoTest, gate.WorkloadTargetGoBenchmark:
		return true
	case gate.WorkloadTargetGoGuard:
		return target != gate.GoGuardTargetCanonical && target != gate.GoGuardTargetSource && target != gate.GoGuardTargetSourceRawGoTest && target != gate.GoGuardTargetAIMaintenanceUnit && target != gate.GoGuardTargetAIMaintenanceGate
	default:
		return false
	}
}

// captureInputClosure records the final digest input selected by the current algorithm.
func (snapshot *remoteGitTreeSnapshot) captureInputClosure(entries []remoteGitTreeEntry) {
	snapshot.closureCaptureStateMu.RLock()
	capture := snapshot.closureCapture
	snapshot.closureCaptureStateMu.RUnlock()
	if capture == nil {
		return
	}
	capture.mu.Lock()
	capture.entries = entries
	capture.mu.Unlock()
}
