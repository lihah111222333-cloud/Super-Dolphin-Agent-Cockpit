package remoteci

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const remoteWorkloadInputAlgorithmDomain = "remote-workload-input-algorithm/v1"

// remoteWorkloadInputAlgorithmRequiredPaths 返回 input producer 的跨包静态闭包；
// workload_fingerprint 前缀下的其他生产文件由动态路径规则一并纳入。
func remoteWorkloadInputAlgorithmRequiredPaths() []string {
	return []string{
		"go.mod",
		"go.sum",
		"internal/devtools/cicontract/contract.go",
		"internal/devtools/gate/compile_group.go",
		"internal/devtools/gate/executor.go",
		"internal/devtools/gate/executor_compile_group.go",
		"internal/devtools/gate/executor_mapping.go",
		"internal/devtools/gate/local_executor_receipt_git.go",
		"internal/devtools/gate/workload_pass_source_replay.go",
		"internal/devtools/gate/registry.go",
		"internal/devtools/gate/workload_model.go",
		"internal/devtools/gate/workload_targets.go",
		"internal/devtools/gateprivate/trusted_git.go",
		"internal/devtools/godistribution/go-distribution.lock",
		"internal/devtools/godistribution/go_distribution.go",
		"internal/devtools/remoteci/workload_fingerprint.go",
		"internal/devtools/remoteci/workload_fingerprint_digests.go",
		"internal/devtools/remoteci/workload_fingerprint_go_tests.go",
	}
}

// workloadInputAlgorithmDigest 只摘要 workload input producer 的生产闭包。
// 缺少任一 anchor 时返回 unsupported，由调用方继续做完整来源树重算。
func (snapshot *remoteGitTreeSnapshot) workloadInputAlgorithmDigest() (string, bool, error) {
	if snapshot == nil {
		return "", false, nil
	}
	requiredPaths := remoteWorkloadInputAlgorithmRequiredPaths()
	selected := make([]remoteGitTreeEntry, 0)
	seen := make(map[string]struct{})
	for _, entry := range snapshot.entries {
		if !remoteWorkloadInputAlgorithmEntry(entry.path, requiredPaths) {
			continue
		}
		if entry.mode == "" || entry.kind == "" || entry.objectID == "" {
			return "", false, fmt.Errorf("remote workload input algorithm entry %q is incomplete", entry.path)
		}
		selected = append(selected, entry)
		seen[entry.path] = struct{}{}
	}
	for _, required := range requiredPaths {
		if _, ok := seen[required]; !ok {
			return "", false, nil
		}
	}
	sort.Slice(selected, func(left, right int) bool { return selected[left].path < selected[right].path })
	hasher := sha256.New()
	fmt.Fprintln(hasher, remoteWorkloadInputAlgorithmDomain)
	for _, entry := range selected {
		fmt.Fprintf(hasher, "%s %s %s\t%s\n", entry.mode, entry.kind, entry.objectID, entry.path)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), true, nil
}

func remoteWorkloadInputAlgorithmEntry(filePath string, requiredPaths []string) bool {
	if strings.HasPrefix(filePath, "internal/devtools/remoteci/workload_fingerprint") && strings.HasSuffix(filePath, ".go") && !strings.HasSuffix(filePath, "_test.go") {
		return true
	}
	return slices.Contains(requiredPaths, filePath)
}

// remoteReplayTreeDistance 计算两个 immutable tree 的变更 entry 数；只用于
// best-first 排序，所有低优先级候选仍会在前序候选未命中时继续验证。
func remoteReplayTreeDistance(source, target *remoteGitTreeSnapshot) int {
	if source == nil || target == nil {
		return int(^uint(0) >> 1)
	}
	distance := 0
	for filePath, targetEntry := range target.byPath {
		sourceEntry, ok := source.byPath[filePath]
		if !ok || sourceEntry.mode != targetEntry.mode || sourceEntry.kind != targetEntry.kind || sourceEntry.objectID != targetEntry.objectID {
			distance++
		}
	}
	for filePath := range source.byPath {
		if _, ok := target.byPath[filePath]; !ok {
			distance++
		}
	}
	return distance
}
