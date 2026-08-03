package remoteci

import (
	"context"
	"fmt"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	remoteWorkloadInputSchemaVersion = 3
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
	repositoryRoot string
	tree           string
	entries        []remoteGitTreeEntry
	byPath         map[string]remoteGitTreeEntry
	goSources      map[string][]byte
	moduleMappings []remoteGoModuleMapping

	cacheMu                sync.Mutex
	productionClosureCache map[string]remoteProductionClosureCache
	goTestDeclarationCache map[string]remoteGoTestDeclarationCache
	goWorkloadSharedScript *remoteGitTreeEntry
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
	return snapshot.workerExecutionContractDigest(ctx)
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
