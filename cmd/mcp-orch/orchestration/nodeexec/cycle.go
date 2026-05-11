package nodeexec

import (
	"errors"
	"fmt"
	"sort"
)

// 环检测 —— ApplyOps add_node / update_node 在写库前的前置校验（蓝图 v2 §5）。
//
// 算法：Kahn 拓扑排序。先建邻接表 + 入度表，反复挑入度为 0 的节点出栈；若遍历
// 终了仍有节点未挑出，说明剩余节点构成至少一个环。
//
// 选 Kahn 而非 DFS：Kahn 主循环直接产出参与环的节点集合，便于错误消息列出
// "n1 -> n2 -> n3" 类型路径；DFS 需要额外栈追踪。两者复杂度都是 O(V+E)，
// 节点规模 << 千级，常数差异忽略。
//
// Cycle detection used by ApplyOps add_node / update_node before persistence
// (blueprint v2 §5). Implemented via Kahn topological sort: O(V+E), surfaces
// the offending node set so the error message can name names.

// ErrDAGCyclic 是拓扑排序发现环时返回的 sentinel 错误。
// errors.Is(err, ErrDAGCyclic) 可命中。
//
// ErrDAGCyclic is returned when DetectCycle finds a cycle. Matchable via
// errors.Is(err, ErrDAGCyclic).
var ErrDAGCyclic = errors.New("dag: cycle detected")

// CycleError 是 ErrDAGCyclic 的富错误包装：携带至少一个参与环的 node_key
// 集合（按字典序排序，便于 deterministic 测试断言）。
type CycleError struct {
	Nodes []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("%s: nodes %v form a cycle", ErrDAGCyclic.Error(), e.Nodes)
}

func (e *CycleError) Unwrap() error { return ErrDAGCyclic }

// DetectCycle 接受一张以 node_key 为索引的依赖图（adjacency = 当前节点 ←
// 所依赖的上游节点），返回 nil 表示无环，否则返回 *CycleError 包装 ErrDAGCyclic。
//
// 输入约定：
//   - adjacency 的 key 是 DAG 全集节点 key
//   - adjacency[k] 是 node k 的 depends_on 列表
//   - 依赖中引用的、但 adjacency 没有 key 的「外部 / 未知节点」被忽略
//     （上游校验已经保证 depends_on 都指向 adjacency 内节点）
//
// DetectCycle returns nil for an acyclic graph; otherwise it returns a
// *CycleError wrapping ErrDAGCyclic. The input map is read-only: adjacency[k]
// lists the upstream dependencies of node k. Unknown deps (referenced but not
// keyed in adjacency) are skipped on the assumption upstream validation has
// already rejected them.
func DetectCycle(adjacency map[string][]string) error {
	indeg, reverse := buildCycleDetectionIndex(adjacency)
	processed := kahnDrain(indeg, reverse)
	if processed == len(indeg) {
		return nil
	}
	return newCycleError(indeg)
}

// buildCycleDetectionIndex 把 adjacency 编译成两个表：
//   - indeg[node] = node 依赖的有效上游数（外部依赖被剔除）
//   - reverse[u] = 所有把 u 列在 depends_on 里的下游节点
//
// 拓扑方向解释：把 depends_on 看作 prereq→node 的有向边，Kahn 弹 indeg=0 的
// 节点 = 当前可执行（无未满足依赖）的根节点。
func buildCycleDetectionIndex(adjacency map[string][]string) (map[string]int, map[string][]string) {
	indeg := make(map[string]int, len(adjacency))
	reverse := make(map[string][]string, len(adjacency))
	for node, deps := range adjacency {
		if _, seen := indeg[node]; !seen {
			indeg[node] = 0
		}
		for _, dep := range deps {
			if _, known := adjacency[dep]; !known {
				// 外部 / 未知依赖：上游 add_node 已保证不存在；防御性跳过。
				continue
			}
			indeg[node]++
			reverse[dep] = append(reverse[dep], node)
		}
	}
	return indeg, reverse
}

// kahnDrain 跑 Kahn 主循环并返回成功出栈的节点数。indeg 会被原地修改（消耗）。
// 用确定的字典序入队，让错误消息 deterministic。
func kahnDrain(indeg map[string]int, reverse map[string][]string) int {
	queue := initialZeroIndegQueue(indeg)
	processed := 0
	for len(queue) > 0 {
		head := queue[0]
		queue = queue[1:]
		processed++
		nextBatch := make([]string, 0)
		for _, dn := range reverse[head] {
			indeg[dn]--
			if indeg[dn] == 0 {
				nextBatch = append(nextBatch, dn)
			}
		}
		sort.Strings(nextBatch)
		queue = append(queue, nextBatch...)
	}
	return processed
}

// initialZeroIndegQueue 拿出所有 indeg==0 的根节点、按字典序排序后入队。
func initialZeroIndegQueue(indeg map[string]int) []string {
	queue := make([]string, 0, len(indeg))
	for node, d := range indeg {
		if d == 0 {
			queue = append(queue, node)
		}
	}
	sort.Strings(queue)
	return queue
}

// newCycleError 从未拓扑出的 indeg map 取剩余节点构造 CycleError。
func newCycleError(indeg map[string]int) *CycleError {
	remaining := make([]string, 0)
	for node, d := range indeg {
		if d > 0 {
			remaining = append(remaining, node)
		}
	}
	sort.Strings(remaining)
	return &CycleError{Nodes: remaining}
}
