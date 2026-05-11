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
	// indeg[v] = 「指向 v」的边数 = 「依赖 v」的节点数（注意：依赖方向
	// 在我们这里是 v --depends_on--> u, 等价于 边 v→u, 那么 v 的「出
	// 度」是 len(adjacency[v]); 而要做 Kahn 拓扑，需要 入度 = 谁依赖
	// 我。所以 in_degree(u) = sum_v 1[u in adjacency[v]]）。
	//
	// 等价说法：建图 v→u 表示 v depends on u（即 u 必须先 done v 才能
	// 跑），拓扑顺序就是 u 在 v 之前。Kahn 先弹「无人依赖的、可以最
	// 早跑的」即出度为 0 的源 ＝ 入度（被依赖数）为 0 取错。
	//
	// 重写：换个更直观的方向 —— 把 depends_on 当成 「prereq → node」
	// 的边。indeg[node] = len(adjacency[node])（即「依赖了多少个」）。
	// 然后弹 indeg=0 的节点（无未满足依赖的根节点），把它从图里删
	// 除：等价于把所有「以它为依赖」的节点的 indeg 减 1。
	indeg := make(map[string]int, len(adjacency))
	// reverse[u] = 列表，包含所有「依赖了 u」的下游节点 v。Kahn 弹根
	// 时用它把 indeg[v] -= 1。
	reverse := make(map[string][]string, len(adjacency))
	for node, deps := range adjacency {
		// 防御：node 必须在 indeg 中有 key（即使 len(deps)==0 也要 0
		// 入度）。
		if _, seen := indeg[node]; !seen {
			indeg[node] = 0
		}
		for _, dep := range deps {
			if _, known := adjacency[dep]; !known {
				// 外部 / 未知依赖：上游 add_node 已保证不存在；
				// 这里防御性跳过，不影响环判定（外部节点不在子图）。
				continue
			}
			indeg[node]++
			reverse[dep] = append(reverse[dep], node)
		}
	}
	// queue：当前 indeg=0 的节点集合（拓扑可立即出栈）。
	// 为了让错误消息 deterministic，先按字典序排好序再 enqueue。
	queue := make([]string, 0, len(indeg))
	for node, d := range indeg {
		if d == 0 {
			queue = append(queue, node)
		}
	}
	sort.Strings(queue)
	processed := 0
	for len(queue) > 0 {
		head := queue[0]
		queue = queue[1:]
		processed++
		// pop head：所有依赖 head 的下游节点 indeg -= 1。
		// 收集这一轮新弹出的 indeg=0 节点再次按字典序追加，让 K 个
		// 同时弹出的节点输出确定。
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
	if processed == len(indeg) {
		return nil
	}
	// 剩余 indeg>0 的就是环参与节点。按字典序排好返回。
	remaining := make([]string, 0, len(indeg)-processed)
	for node, d := range indeg {
		if d > 0 {
			remaining = append(remaining, node)
		}
	}
	sort.Strings(remaining)
	return &CycleError{Nodes: remaining}
}
