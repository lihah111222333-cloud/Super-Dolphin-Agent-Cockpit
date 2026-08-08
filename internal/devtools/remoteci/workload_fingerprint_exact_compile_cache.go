package remoteci

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
)

// remoteExactCompileRootKey 将同一 exact snapshot 内的包级编译根按目录和构建 profile 隔离。
// race profile 必须进入 key，避免复用仅适用于 race 的生产或测试源码。
type remoteExactCompileRootKey struct {
	directory string
	profile   string
}

// remoteExactCompileRootCacheEntry 保存不可变编译条目集合及诊断 Merkle 根。
// Merkle 根只用于 snapshot-local 缓存检查；selector identity 仍序列化原有扁平条目。
type remoteExactCompileRootCacheEntry struct {
	entries    []remoteGitTreeEntry
	merkleRoot string
	err        error
}

// exactCompileRootEntries 返回一个包全部适用测试、生产和 embed 编译输入。
// 计算在 snapshot 内串行化，避免并发 selector 重复执行昂贵的 AST/blob 闭包遍历。
func (snapshot *remoteGitTreeSnapshot) exactCompileRootEntries(
	directory string,
	profile remoteGoBuildProfile,
) ([]remoteGitTreeEntry, error) {
	if snapshot == nil {
		return nil, errors.New("remote exact compile root snapshot is required")
	}
	key := remoteExactCompileRootKey{directory: directory, profile: profile.cacheKey()}
	snapshot.exactCompileRootMu.Lock()
	defer snapshot.exactCompileRootMu.Unlock()
	if cached, ok := snapshot.loadExactCompileRootCacheEntry(key); ok {
		return cloneExactCompileRootEntries(cached)
	}
	snapshot.cacheMu.Lock()
	snapshot.exactCompileRootComputations++
	snapshot.cacheMu.Unlock()
	computed := snapshot.computeExactCompileRoot(directory, profile)
	snapshot.storeExactCompileRootCacheEntry(key, computed)
	return cloneExactCompileRootEntries(computed)
}

func (snapshot *remoteGitTreeSnapshot) loadExactCompileRootCacheEntry(key remoteExactCompileRootKey) (remoteExactCompileRootCacheEntry, bool) {
	if snapshot.exactCompileRootCache == nil {
		snapshot.exactCompileRootCache = make(map[remoteExactCompileRootKey]remoteExactCompileRootCacheEntry)
	}
	entry, ok := snapshot.exactCompileRootCache[key]
	return entry, ok
}

func (snapshot *remoteGitTreeSnapshot) storeExactCompileRootCacheEntry(key remoteExactCompileRootKey, entry remoteExactCompileRootCacheEntry) {
	snapshot.exactCompileRootCache[key] = entry
}

func cloneExactCompileRootEntries(cached remoteExactCompileRootCacheEntry) ([]remoteGitTreeEntry, error) {
	if cached.err != nil {
		return nil, cached.err
	}
	return append([]remoteGitTreeEntry(nil), cached.entries...), nil
}

// computeExactCompileRoot 执行一次包级测试与生产编译闭包扫描，并冻结条目及 Merkle 根。
func (snapshot *remoteGitTreeSnapshot) computeExactCompileRoot(directory string, profile remoteGoBuildProfile) remoteExactCompileRootCacheEntry {
	computed := remoteExactCompileRootCacheEntry{}
	files, _, fallback := snapshot.remoteGoTestDeclarations(directory, profile)
	if fallback {
		computed.err = errors.New("parse exact Go test compile inputs")
		return computed
	}
	selected := make(map[string]remoteGitTreeEntry)
	if err := snapshot.addGoExactTestProductionCompileEntries(directory, selected, profile); err != nil {
		computed.err = err
		return computed
	}
	if !snapshot.hasProductionGoPackage(directory, profile) && len(files) == 0 {
		computed.err = fmt.Errorf("remote worker exact Go test package directory %q has no linux/amd64 source files", directory)
		return computed
	}
	if err := snapshot.addExactCompileTestFiles(files, selected, profile); err != nil {
		computed.err = err
		return computed
	}
	computed.entries = sortedRemoteGitTreeEntries(selected)
	computed.merkleRoot = remoteExactCompileRootMerkle(computed.entries)
	return computed
}

// addExactCompileTestFiles 将全部适用同包测试文件加入编译根。
func (snapshot *remoteGitTreeSnapshot) addExactCompileTestFiles(files []remoteGoTestFile, selected map[string]remoteGitTreeEntry, profile remoteGoBuildProfile) error {
	for _, file := range files {
		if err := snapshot.addExactCompileTestFile(file, selected, profile); err != nil {
			return err
		}
	}
	return nil
}

// addExactCompileTestFile 仅加入测试 blob、embed 资源和本地生产编译闭包，
// 不解析测试声明的运行时观察；后者必须由 selector 自身的 goTestSources 负责。
func (snapshot *remoteGitTreeSnapshot) addExactCompileTestFile(
	file remoteGoTestFile,
	selected map[string]remoteGitTreeEntry,
	profile remoteGoBuildProfile,
) error {
	entry, ok := snapshot.byPath[file.path]
	if !ok {
		return fmt.Errorf("Go test compile input %q is absent from Git tree", file.path)
	}
	selected[file.path] = entry
	if err := snapshot.addGoEmbedEntries(path.Dir(file.path), file.source, selected); err != nil {
		return err
	}
	for _, importPath := range remoteGoTestImports(file.file) {
		if localDirectory, local := snapshot.resolveLocalGoImport(importPath); local {
			if err := snapshot.addProductionGoPackageEntriesWithAssets(localDirectory, selected, true, profile); err != nil {
				return err
			}
		}
	}
	return nil
}

// remoteExactCompileRootMerkle 对 canonical Git tree 条目计算确定性的二叉 Merkle 根。
// 该根不投影到 workload identity，调用方继续保留历史扁平摘要编码。
func remoteExactCompileRootMerkle(entries []remoteGitTreeEntry) string {
	if len(entries) == 0 {
		return ""
	}
	canonical := append([]remoteGitTreeEntry(nil), entries...)
	sort.Slice(canonical, func(left, right int) bool {
		return canonical[left].path < canonical[right].path
	})
	leaves := make([][sha256.Size]byte, len(canonical))
	for index, entry := range canonical {
		leaves[index] = sha256.Sum256([]byte(remoteExactCompileRootEntryLine(entry)))
	}
	for len(leaves) > 1 {
		leaves = remoteExactCompileRootMerkleLevel(leaves)
	}
	return "sha256:" + hex.EncodeToString(leaves[0][:])
}

func remoteExactCompileRootMerkleLevel(leaves [][sha256.Size]byte) [][sha256.Size]byte {
	next := make([][sha256.Size]byte, 0, (len(leaves)+1)/2)
	for index := 0; index < len(leaves); index += 2 {
		right := leaves[index]
		if index+1 < len(leaves) {
			right = leaves[index+1]
		}
		material := make([]byte, 0, 1+sha256.Size*2)
		material = append(material, 'n')
		material = append(material, leaves[index][:]...)
		material = append(material, right[:]...)
		next = append(next, sha256.Sum256(material))
	}
	return next
}

func remoteExactCompileRootEntryLine(entry remoteGitTreeEntry) string {
	return fmt.Sprintf("%s %s %s\t%s\n", entry.mode, entry.kind, entry.objectID, entry.path)
}
