package remoteci

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

var errRemoteWorkloadInputUnavailable = errors.New("remote workload input is unavailable in source tree")

const (
	remoteWorkloadInputSchemaVersion = cicontract.WorkloadInputFingerprintSchemaVersion
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
	repositoryRoot           string
	tree                     string
	candidateObjectAuthority gate.CandidateObjectAuthority
	entries                  []remoteGitTreeEntry
	byPath                   map[string]remoteGitTreeEntry
	goSources                map[string][]byte
	frontendSources          map[string][]byte
	moduleMappings           []remoteGoModuleMapping
	goSourcesMu              sync.Mutex
	gitBlobMu                sync.Mutex
	gitBlobContents          map[string][]byte

	cacheMu                       sync.Mutex
	exactCompileRootMu            sync.Mutex
	productionIndexMu             sync.Mutex
	productionImportsMu           sync.Mutex
	productionRuntimeMu           sync.Mutex
	productionClosureCache        map[string]remoteProductionClosureCache
	goTestDeclarationCache        map[string]remoteGoTestDeclarationCache
	exactCompileRootCache         map[remoteExactCompileRootKey]remoteExactCompileRootCacheEntry
	productionIndexCache          map[string]remoteGoProductionIndexCacheEntry
	productionImportsCache        map[string]remoteGoProductionImportsCacheEntry
	productionRuntimeCache        map[string]remoteGoProductionRuntimeCacheEntry
	goWorkloadSharedScript        *remoteGitTreeEntry
	goPackageInputDigestCache     map[remoteGoPackageInputDigestKey]string
	goEmbedResolutionCache        map[remoteGoEmbedResolutionKey]remoteGoEmbedResolutionCache
	remoteObservedAliasCache      map[string]remoteGoObservedAliasCacheEntry
	workerExecutionDigestCache    string
	goEmbedResolutionComputations uint64
	goEmbedResolutionCacheHits    uint64
	exactCompileRootComputations  uint64
	productionIndexComputations   uint64
	productionImportsComputations uint64
	productionRuntimeComputations uint64
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

// rememberRemoteGitBlob 缓存精确 Git blob 内容供运行时符号链接解析，缓存只属于当前 snapshot。
func (snapshot *remoteGitTreeSnapshot) rememberRemoteGitBlob(objectID string, data []byte) {
	if snapshot == nil || objectID == "" {
		return
	}
	copyData := append([]byte(nil), data...)
	snapshot.gitBlobMu.Lock()
	defer snapshot.gitBlobMu.Unlock()
	if snapshot.gitBlobContents == nil {
		snapshot.gitBlobContents = make(map[string][]byte)
	}
	snapshot.gitBlobContents[objectID] = copyData
}

// remoteGitBlob 返回精确 Git blob 内容的防御性副本。
func (snapshot *remoteGitTreeSnapshot) remoteGitBlob(objectID string) ([]byte, bool) {
	if snapshot == nil || objectID == "" {
		return nil, false
	}
	snapshot.gitBlobMu.Lock()
	defer snapshot.gitBlobMu.Unlock()
	data, ok := snapshot.gitBlobContents[objectID]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), data...), true
}

// addRemoteGitObservedPath 按运行时观察种类收敛候选树输入，并解析 Git 符号链接闭包。
func (snapshot *remoteGitTreeSnapshot) addRemoteGitObservedPath(kind, filePath string, selected map[string]remoteGitTreeEntry) error {
	switch kind {
	case "tree":
		return snapshot.addRemoteGitObservedTreeEntries(filePath, selected)
	case "glob":
		return snapshot.addRemoteGitGlobEntries(filePath, selected)
	default:
		return snapshot.addRemoteGitPathEntry(filePath, selected)
	}
}

// addRemoteGitObservedTreeEntries 解析目录观察指向的符号链接，并选择目标目录闭包。
func (snapshot *remoteGitTreeSnapshot) addRemoteGitObservedTreeEntries(filePath string, selected map[string]remoteGitTreeEntry) error {
	entry, ok := snapshot.byPath[filePath]
	if ok && entry.mode == "120000" {
		resolved, err := snapshot.addRemoteGitSymlinkPath(filePath, selected, make(map[string]struct{}))
		if err != nil {
			return err
		}
		snapshot.addRemoteGitDirectoryEntries(resolved, selected)
		return nil
	}
	snapshot.addRemoteGitDirectoryEntries(filePath, selected)
	return nil
}

// addRemoteGitPathEntry 选择普通观察路径，或解析其 Git 符号链接目标。
func (snapshot *remoteGitTreeSnapshot) addRemoteGitPathEntry(filePath string, selected map[string]remoteGitTreeEntry) error {
	entry, ok := snapshot.byPath[filePath]
	if !ok {
		return nil
	}
	if entry.mode == "120000" {
		_, err := snapshot.addRemoteGitSymlinkPath(filePath, selected, make(map[string]struct{}))
		return err
	}
	selected[filePath] = entry
	return nil
}

// addRemoteGitSymlinkPath 递归解析已跟踪符号链接，拒绝缺失、越树和循环目标。
func (snapshot *remoteGitTreeSnapshot) addRemoteGitSymlinkPath(filePath string, selected map[string]remoteGitTreeEntry, visiting map[string]struct{}) (string, error) {
	entry, ok := snapshot.byPath[filePath]
	if !ok {
		return "", fmt.Errorf("tracked Git symlink target %q is missing", filePath)
	}
	selected[filePath] = entry
	if entry.mode != "120000" {
		return filePath, nil
	}
	if _, cycle := visiting[filePath]; cycle {
		return "", fmt.Errorf("tracked Git symlink cycle includes %q", filePath)
	}
	visiting[filePath] = struct{}{}
	defer delete(visiting, filePath)
	resolved, err := snapshot.remoteGitSymlinkTargetPath(filePath, entry)
	if err != nil {
		return "", err
	}
	targetEntry, targetExists := snapshot.byPath[resolved]
	if !targetExists {
		if snapshot.hasRemoteGitDirectory(resolved) {
			snapshot.addRemoteGitDirectoryEntries(resolved, selected)
			return resolved, nil
		}
		return "", fmt.Errorf("tracked Git symlink %q target %q is missing", filePath, resolved)
	}
	if targetEntry.mode == "120000" {
		return snapshot.addRemoteGitSymlinkPath(resolved, selected, visiting)
	}
	selected[resolved] = targetEntry
	return resolved, nil
}

// remoteGitSymlinkTargetPath 读取符号链接 blob，并把目标校验为候选树内规范路径。
func (snapshot *remoteGitTreeSnapshot) remoteGitSymlinkTargetPath(filePath string, entry remoteGitTreeEntry) (string, error) {
	targetBytes, ok := snapshot.remoteGitBlob(entry.objectID)
	if !ok {
		return "", fmt.Errorf("tracked Git symlink %q target blob is unavailable", filePath)
	}
	target := string(targetBytes)
	if target == "" || strings.ContainsAny(target, "\x00\r\n") {
		return "", fmt.Errorf("tracked Git symlink %q target is not a single path", filePath)
	}
	resolved, ok := remoteGoTestRelativePath(path.Dir(filePath), target)
	if !ok {
		return "", fmt.Errorf("tracked Git symlink %q target %q escapes canonical worker source", filePath, target)
	}
	return resolved, nil
}

// hasRemoteGitDirectory 判断候选树中是否存在给定目录或其下属路径。
func (snapshot *remoteGitTreeSnapshot) hasRemoteGitDirectory(directory string) bool {
	for _, entry := range snapshot.entries {
		if entry.path == directory || strings.HasPrefix(entry.path, directory+"/") {
			return true
		}
	}
	return false
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
