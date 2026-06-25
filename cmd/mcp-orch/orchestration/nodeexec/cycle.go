package nodeexec

import (
	"errors"
	"fmt"
	"sort"
)

// 环检测 —— ApplyOps add_node / update_node 在写库前的前置校验（蓝图 v2 §5）。
//
// 算法演变：
//   v1 (F4.1 骨架期)：Kahn 拓扑。未拓扑出的节点一股脑捞进 CycleError.Nodes，
//     多个独立环 ·会被混在一起无法区分。
//   v2 (R3 P2 #2 修复)：Kahn 预检 → Tarjan SCC 拆簇。Kahn 负责快路判无环（这
//     是绝大多数 happy path）；仅在存在环时才跑一次 Tarjan 把剩余节点按强
//     联通分量拆为独立环 components，让错误消息能区分「{a,b} + {c,d} 是两个环」
//     与「{a,b,c,d} 是一个包含 4 个节点的环」。
//
// 复杂度都是 O(V+E)，节点规模 << 千级。
//
// Cycle detection used by ApplyOps add_node / update_node before persistence
// (blueprint v2 §5). v1 (F4.1) used Kahn only and lumped all unprocessed nodes
// into one CycleError.Nodes slice. v2 (R3 P2 #2) keeps Kahn as the fast-path
// acyclic check and runs Tarjan SCC on the residual subgraph so multiple
// independent cycles ({a,b} + {c,d}) surface as separate Components instead of
// being merged into a single blob.

// ErrDAGCyclic 是拓扑排序发现环时返回的 sentinel 错误。
// errors.Is(err, ErrDAGCyclic) 可命中。
//
// ErrDAGCyclic is returned when DetectCycle finds a cycle. Matchable via
// errors.Is(err, ErrDAGCyclic).
var ErrDAGCyclic = errors.New("dag: cycle detected")

// CycleError 是 ErrDAGCyclic 的富错误包装。
//
// 字段语义：
//   - Nodes：所有参与环的节点平铺列表，字典序排序。向后兼容字段——v1 只有这一
//     项，不应丢。
//   - Components：按 Tarjan SCC 拆出的独立环列表。每个 Component 是一个环的
//     node_key 字典序排序。Components 本身也按 component[0] 字典序排序让
//     错误消息 deterministic。
//
// Nodes preserves the v1 contract (flat sorted list of every node involved in
// any cycle); Components carries the v2 SCC partitioning so callers can tell
// {a,b} + {c,d} apart from {a,b,c,d}.
type CycleError struct {
	Nodes      []string
	Components [][]string
}

// Error 返回错误文本。
func (e *CycleError) Error() string {
	if len(e.Components) <= 1 {
		return fmt.Sprintf("%s: nodes %v form a cycle", ErrDAGCyclic.Error(), e.Nodes)
	}
	return fmt.Sprintf("%s: %d independent cycles %v", ErrDAGCyclic.Error(), len(e.Components), e.Components)
}

// Unwrap 返回底层错误。
func (e *CycleError) Unwrap() error { return ErrDAGCyclic }

// DetectCycle 接受一张以 node_key 为索引的依赖图（adjacency = 当前节点 ←
// 所依赖的上游节点），返回 nil 表示无环，否则返回 *CycleError 包装 ErrDAGCyclic。
//
// 复杂度：worst-case O(V+E)。V = 节点数，E = 边总数（所有 depends_on 边之和）。
// Kahn 主循环每个节点只出一次队、每条边只减一次 indeg；buildCycleDetectionIndex
// 与ampling 同复杂度。千节点量级实测常数 < 1ms，不需为内存 / GC 做额外优化。
//
// 输入约定：
//   - adjacency 的 key 是 DAG 全集节点 key
//   - adjacency[k] 是 node k 的 depends_on 列表
//   - 依赖中引用的、但 adjacency 没有 key 的「外部 / 未知节点」被忽略
//     （上游校验已经保证 depends_on 都指向 adjacency 内节点）
//   - nil map 与空 map 语义一致：两者都是空图，直接返 nil。设计判断：
//     上游调用点（planOpsBatch / mergeAdjacency）可能汇 0 项 ops 生出 nil map，
//     拒绝则需走额外分支；连同语义会让调用点代码干净。
//
// DetectCycle returns nil for an acyclic graph; otherwise it returns a
// *CycleError wrapping ErrDAGCyclic. The input map is read-only: adjacency[k]
// lists the upstream dependencies of node k. Unknown deps (referenced but not
// keyed in adjacency) are skipped on the assumption upstream validation has
// already rejected them. nil map is treated identically to an empty map (no
// nodes, no cycles).
func DetectCycle(adjacency map[string][]string) error {
	if len(adjacency) == 0 {
		return nil
	}
	indeg, reverse := buildCycleDetectionIndex(adjacency)
	processed := kahnDrain(indeg, reverse)
	if processed == len(indeg) {
		return nil
	}
	return newCycleError(adjacency, indeg)
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

// newCycleError 从未拓扑出的 indeg map 提取残留子图，对该子图跑 Tarjan SCC 拆出
// 独立环。返回的 CycleError 同时填 Nodes（平铺字典序，向后兼容）与 Components
// （SCC 各环独立列出，字典序二重排序）。
func newCycleError(adjacency map[string][]string, indeg map[string]int) *CycleError {
	residual := residualSubgraph(adjacency, indeg)
	components := tarjanSCCCycleComponents(residual)
	flat := flattenComponents(components)
	return &CycleError{Nodes: flat, Components: components}
}

// residualSubgraph 提取未拓扑出的节点子集、只保留子集内部边。那些依赖指向已
// 拓扑出节点的边被丢弃（它们不参与环，存在仅因为下游节点在环中捞不出。环
// 详情只取决于子图内边）。
func residualSubgraph(adjacency map[string][]string, indeg map[string]int) map[string][]string {
	// 环外节点 indeg 会被 Kahn 递减到 0、出栈；环内节点 indeg 不能被减为 0（环内上游节点
	// 未被拓扑出）。所以 indeg > 0 等价于「节点在 residual 子图中」。
	residualSet := make(map[string]struct{})
	for node, d := range indeg {
		if d > 0 {
			residualSet[node] = struct{}{}
		}
	}
	residual := make(map[string][]string, len(residualSet))
	for node := range residualSet {
		for _, dep := range adjacency[node] {
			if _, in := residualSet[dep]; in {
				residual[node] = append(residual[node], dep)
			}
		}
		if _, has := residual[node]; !has {
			residual[node] = nil // 保证 node 出现在 residual key 集中
		}
	}
	return residual
}

// tarjanSCCCycleComponents 在 residual 上跑 Tarjan SCC，返回「仅含环」的 SCC
// （大小 >= 2 或「大小 == 1 且节点自环」）。每个 Component 节点列字典序
// 排序；所有 Components 二重按第一个节点字典序排序，让输出 deterministic。
//
// Algorithm: Tarjan 1972. indices/lowlink 维一份。本代码不递归（环处 DFS 栈手动
// 维护）避免深突 stack overflow。节点规模 << 千级，递归也 OK，为简洁本代码走
// 递归。未来若节点规模增长远超算需重估。
func tarjanSCCCycleComponents(graph map[string][]string) [][]string {
	t := newTarjanState(graph)
	nodes := sortedKeys(graph)
	for _, n := range nodes {
		if _, seen := t.indices[n]; !seen {
			t.strongConnect(n)
		}
	}
	sort.Slice(t.components, func(i, j int) bool {
		return t.components[i][0] < t.components[j][0]
	})
	return t.components
}

// tarjanState 被 Tarjan SCC 主函数与 strongConnect 递归共享。拆出独立类避免闭
// 包需要 capture 大量字段，同时使状态变迁可追。
type tarjanState struct {
	graph      map[string][]string
	index      int
	stack      []string
	onStack    map[string]bool
	indices    map[string]int
	lowlink    map[string]int
	components [][]string
}

func newTarjanState(graph map[string][]string) *tarjanState {
	return &tarjanState{
		graph:   graph,
		onStack: make(map[string]bool, len(graph)),
		indices: make(map[string]int, len(graph)),
		lowlink: make(map[string]int, len(graph)),
	}
}

// strongConnect 是 Tarjan SCC 的递归核心：为节点 v 赋 index/lowlink，递归处理邻居，弹出 SCC。
func (t *tarjanState) strongConnect(v string) {
	t.indices[v] = t.index
	t.lowlink[v] = t.index
	t.index++
	t.stack = append(t.stack, v)
	t.onStack[v] = true

	// 按字典序递归走邻居，让同同输入产出顺序一致。
	neighbors := append([]string(nil), t.graph[v]...)
	sort.Strings(neighbors)
	for _, w := range neighbors {
		if _, seen := t.indices[w]; !seen {
			t.strongConnect(w)
			if t.lowlink[w] < t.lowlink[v] {
				t.lowlink[v] = t.lowlink[w]
			}
		} else if t.onStack[w] {
			if t.indices[w] < t.lowlink[v] {
				t.lowlink[v] = t.indices[w]
			}
		}
	}

	if t.lowlink[v] == t.indices[v] {
		t.popSCC(v)
	}
}

// popSCC 从栈弹出一个 SCC，过滤出参与环的 SCC（len >= 2 或单个节点自环），
// node_key 字典序排序后附到 components。
func (t *tarjanState) popSCC(root string) {
	var scc []string
	for {
		n := len(t.stack) - 1
		w := t.stack[n]
		t.stack = t.stack[:n]
		t.onStack[w] = false
		scc = append(scc, w)
		if w == root {
			break
		}
	}
	if !isCycleSCC(scc, t.graph) {
		return
	}
	sort.Strings(scc)
	t.components = append(t.components, scc)
}

// isCycleSCC 判 SCC 是否参与环。多节点 SCC 必环；单节点 SCC 仅在节点自身有自
// 环边时才计环。该过滤避免把「没有环但被 residual 误拉进来」的孤点报为环。
func isCycleSCC(scc []string, graph map[string][]string) bool {
	if len(scc) > 1 {
		return true
	}
	node := scc[0]
	for _, dep := range graph[node] {
		if dep == node {
			return true
		}
	}
	return false
}

// flattenComponents 把 Components 变为平铺的字典序 node_key 列表（向后兼容 v1 语义）。
func flattenComponents(components [][]string) []string {
	n := 0
	for _, c := range components {
		n += len(c)
	}
	flat := make([]string, 0, n)
	for _, c := range components {
		flat = append(flat, c...)
	}
	sort.Strings(flat)
	return flat
}

// sortedKeys 拿 map 的 key 字典序返回，让 Tarjan 递归顺序 deterministic。
func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
