package remoteci

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// remoteExactGoTestInputDigests 仅在父包缓存未命中后显式展开其逐测试输入身份。
func remoteExactGoTestInputDigests(
	ctx context.Context,
	repositoryRoot string,
	tree string,
	parentWorkloads []gate.Workload,
) (map[string]string, error) {
	snapshot, err := loadRemoteGitTreeSnapshot(ctx, repositoryRoot, tree)
	if err != nil {
		return nil, err
	}
	return snapshot.remoteExactGoTestInputDigests(ctx, parentWorkloads)
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

func (snapshot *remoteGitTreeSnapshot) remoteExactGoTestInputDigests(
	ctx context.Context,
	parentWorkloads []gate.Workload,
) (map[string]string, error) {
	digests := make(map[string]string)
	for _, workload := range parentWorkloads {
		if err := snapshot.addExactGoTestDigests(ctx, workload, digests); err != nil {
			return nil, fmt.Errorf("fingerprint Go test children for %q: %w", workload.ID, err)
		}
	}
	return digests, nil
}

// addExactGoTestDigests publishes child identities alongside their package parent.
// The resume projection must never substitute the broader parent digest for a test run.
// 它只发布可唯一解析的 Go 测试子 workload。
func (snapshot *remoteGitTreeSnapshot) addExactGoTestDigests(ctx context.Context, workload gate.Workload, digests map[string]string) error {
	parent, kind, target, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil || !targeted || kind != gate.WorkloadTargetGoPackage {
		return err
	}
	if err := snapshot.prepareGoSources(ctx); err != nil {
		return err
	}
	directory, err := remoteGoPackageDirectory(target)
	if err != nil {
		return err
	}
	_, declarations, fallback := snapshot.remoteGoTestDeclarations(directory)
	if fallback {
		return errors.New("parse Go test declarations for exact fingerprint")
	}
	for name, declaration := range declarations {
		if !remoteExactGoTestDeclaration(name, declaration) {
			continue
		}
		child, err := gate.NewGoTestWorkload(parent, target, name, 1)
		if err != nil {
			continue
		}
		digest, err := snapshot.goExactTestInputDigest(ctx, gate.GoTestTarget{Package: target, Name: name})
		if err != nil {
			return err
		}
		digests[child.ID] = digest
	}
	return nil
}

// remoteExactGoTestDeclaration 只接受唯一声明的标准 Go 测试入口。
func remoteExactGoTestDeclaration(name string, declarations []remoteGoTestDeclaration) bool {
	return len(declarations) == 1 && remoteGoTestName(name)
}

func remoteGoTestName(name string) bool {
	return strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Fuzz") || strings.HasPrefix(name, "Example")
}
