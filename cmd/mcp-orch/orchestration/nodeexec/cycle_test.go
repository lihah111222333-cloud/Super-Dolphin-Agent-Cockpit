package nodeexec

import (
	"errors"
	"reflect"
	"testing"
)

// TestDetectCycle_Empty 覆盖空图输入，确保 DAG 校验把无节点视为可执行。
func TestDetectCycle_Empty(t *testing.T) {
	t.Parallel()
	if err := DetectCycle(map[string][]string{}); err != nil {
		t.Fatalf("empty graph: err = %v, want nil", err)
	}
}

// TestDetectCycle_NilAdjacency 确认 nil adjacency 不会 panic。
// 调用点上游可能因 0-item ops 生 nil map，走同「空图」路径。
func TestDetectCycle_NilAdjacency(t *testing.T) {
	t.Parallel()
	var adj map[string][]string // nil
	if err := DetectCycle(adj); err != nil {
		t.Fatalf("nil adjacency: err = %v, want nil", err)
	}
}

// TestDetectCycle_SingleNodeNoDeps 覆盖单节点无依赖图，避免孤立节点被误判成环。
func TestDetectCycle_SingleNodeNoDeps(t *testing.T) {
	t.Parallel()
	if err := DetectCycle(map[string][]string{"a": nil}); err != nil {
		t.Fatalf("single node: err = %v, want nil", err)
	}
}

// TestDetectCycle_LinearChain 覆盖线性依赖链，确保入度递减能完整拓扑排序。
func TestDetectCycle_LinearChain(t *testing.T) {
	t.Parallel()
	// a -> b -> c -> d (b depends on a, c depends on b, d depends on c)
	adj := map[string][]string{
		"a": nil,
		"b": {"a"},
		"c": {"b"},
		"d": {"c"},
	}
	if err := DetectCycle(adj); err != nil {
		t.Fatalf("linear chain: err = %v, want nil", err)
	}
}

// TestDetectCycle_Diamond 覆盖菱形依赖，避免共享上游造成重复入度或误报环。
func TestDetectCycle_Diamond(t *testing.T) {
	t.Parallel()
	// a -> {b,c} -> d
	adj := map[string][]string{
		"a": nil,
		"b": {"a"},
		"c": {"a"},
		"d": {"b", "c"},
	}
	if err := DetectCycle(adj); err != nil {
		t.Fatalf("diamond: err = %v, want nil", err)
	}
}

// TestDetectCycle_SelfLoop 确认单节点自环返回可识别的 CycleError。
func TestDetectCycle_SelfLoop(t *testing.T) {
	t.Parallel()
	adj := map[string][]string{
		"a": {"a"},
	}
	err := DetectCycle(adj)
	if err == nil {
		t.Fatal("self loop: want cycle error, got nil")
	}
	if !errors.Is(err, ErrDAGCyclic) {
		t.Fatalf("self loop: err = %v, want errors.Is ErrDAGCyclic", err)
	}
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("self loop: err should be *CycleError, got %T", err)
	}
	if !reflect.DeepEqual(ce.Nodes, []string{"a"}) {
		t.Fatalf("self loop: nodes = %v, want [a]", ce.Nodes)
	}
}

// TestDetectCycle_TwoNodePair 覆盖两个节点互相依赖的最小闭环。
func TestDetectCycle_TwoNodePair(t *testing.T) {
	t.Parallel()
	// a depends b, b depends a
	adj := map[string][]string{
		"a": {"b"},
		"b": {"a"},
	}
	err := DetectCycle(adj)
	if err == nil {
		t.Fatal("two-node cycle: want error, got nil")
	}
	if !errors.Is(err, ErrDAGCyclic) {
		t.Fatalf("two-node cycle: err = %v, want errors.Is ErrDAGCyclic", err)
	}
	var ce *CycleError
	_ = errors.As(err, &ce)
	if !reflect.DeepEqual(ce.Nodes, []string{"a", "b"}) {
		t.Fatalf("two-node cycle: nodes = %v, want [a b]", ce.Nodes)
	}
}

// TestDetectCycle_ThreeNodeLoop 覆盖三节点链式闭环，锁住返回节点的稳定顺序。
func TestDetectCycle_ThreeNodeLoop(t *testing.T) {
	t.Parallel()
	// a -> b -> c -> a (each depends on prev)
	adj := map[string][]string{
		"a": {"c"},
		"b": {"a"},
		"c": {"b"},
	}
	err := DetectCycle(adj)
	if err == nil {
		t.Fatal("three-node cycle: want error, got nil")
	}
	if !errors.Is(err, ErrDAGCyclic) {
		t.Fatalf("three-node cycle: err = %v, want errors.Is ErrDAGCyclic", err)
	}
	var ce *CycleError
	_ = errors.As(err, &ce)
	if !reflect.DeepEqual(ce.Nodes, []string{"a", "b", "c"}) {
		t.Fatalf("three-node cycle: nodes = %v, want [a b c]", ce.Nodes)
	}
}

// TestDetectCycle_CycleAdjacentToDAGPart 确认健康 DAG 片段不会污染相邻闭环结果。
func TestDetectCycle_CycleAdjacentToDAGPart(t *testing.T) {
	t.Parallel()
	// 链 a -> b 健康，但 c <-> d 互依，应只把 c d 列出来。
	adj := map[string][]string{
		"a": nil,
		"b": {"a"},
		"c": {"d"},
		"d": {"c"},
	}
	err := DetectCycle(adj)
	if err == nil {
		t.Fatal("mixed graph: want error, got nil")
	}
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("mixed graph: err = %T, want *CycleError", err)
	}
	if !reflect.DeepEqual(ce.Nodes, []string{"c", "d"}) {
		t.Fatalf("mixed graph: nodes = %v, want [c d]", ce.Nodes)
	}
}

// TestDetectCycle_ExternalDepIgnored 确认 adjacency 外的依赖被当作外部节点处理。
func TestDetectCycle_ExternalDepIgnored(t *testing.T) {
	t.Parallel()
	// "b" 依赖 "external"，external 不在 adjacency 中 → 应视为已存在外部
	// 节点，不破坏环检测。这里 a→b 无环。
	adj := map[string][]string{
		"a": nil,
		"b": {"a", "external"},
	}
	if err := DetectCycle(adj); err != nil {
		t.Fatalf("external dep: err = %v, want nil", err)
	}
}

// BenchmarkDetectCycle_1000Nodes 覆盖千节点线性链和闭环的基准路径。
// 它观察 Kahn O(V+E) 在较大 DAG 上的常数成本；运行：go test -bench=BenchmarkDetectCycle .
//
// 环检测路径（1 ring）与无环路径（1000-node DAG）都覆盖，便于比较正常返回
// 与构造 CycleError 时的中位数表现。
func BenchmarkDetectCycle_1000Nodes(b *testing.B) {
	const N = 1000
	nodes := make([]string, N)
	for i := 0; i < N; i++ {
		nodes[i] = "n" + itoaCycle(i)
	}
	dagAdj := make(map[string][]string, N)
	cycleAdj := make(map[string][]string, N)
	for i := 0; i < N; i++ {
		if i == 0 {
			dagAdj[nodes[i]] = nil
			cycleAdj[nodes[i]] = []string{nodes[N-1]} // 让首节点依赖尾节点，形成闭环。
		} else {
			dagAdj[nodes[i]] = []string{nodes[i-1]}
			cycleAdj[nodes[i]] = []string{nodes[i-1]}
		}
	}
	b.ResetTimer()
	b.Run("acyclic_chain", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if err := DetectCycle(dagAdj); err != nil {
				b.Fatalf("unexpected err on acyclic: %v", err)
			}
		}
	})
	b.Run("cyclic_ring", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if err := DetectCycle(cycleAdj); err == nil {
				b.Fatalf("expected cycle err on ring, got nil")
			}
		}
	})
}

// itoaCycle 是 benchmark 小 helper，避免拉 strconv 包。只需递减除 10。
func itoaCycle(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// ----- Tarjan SCC 拆环测试 -----

// TestDetectCycle_SingleCycle_ComponentsHasOneEntry 确认单环只生成一个 Component。
// Nodes 字段仍保持与 Component 内容一致，兼容只读 Nodes 的调用方。
func TestDetectCycle_SingleCycle_ComponentsHasOneEntry(t *testing.T) {
	adj := map[string][]string{
		"a": {"b"},
		"b": {"a"},
	}
	err := DetectCycle(adj)
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("single cycle: err = %T, want *CycleError", err)
	}
	if len(ce.Components) != 1 {
		t.Fatalf("single cycle: components = %v, want len 1", ce.Components)
	}
	if !reflect.DeepEqual(ce.Components[0], []string{"a", "b"}) {
		t.Fatalf("single cycle: components[0] = %v, want [a b]", ce.Components[0])
	}
	if !reflect.DeepEqual(ce.Nodes, []string{"a", "b"}) {
		t.Fatalf("single cycle: nodes = %v, want [a b]", ce.Nodes)
	}
}

// TestDetectCycle_TwoIndependentCycles_TwoComponents 确认两个互不相连的环会拆成两个 Component。
func TestDetectCycle_TwoIndependentCycles_TwoComponents(t *testing.T) {
	// {a,b} 环 加 {c,d} 环、互不干涉。
	adj := map[string][]string{
		"a": {"b"},
		"b": {"a"},
		"c": {"d"},
		"d": {"c"},
	}
	err := DetectCycle(adj)
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("two cycles: err = %T, want *CycleError", err)
	}
	if len(ce.Components) != 2 {
		t.Fatalf("two cycles: components = %v, want len 2", ce.Components)
	}
	// 按第一个节点字典序排列。
	if !reflect.DeepEqual(ce.Components[0], []string{"a", "b"}) {
		t.Fatalf("two cycles: components[0] = %v, want [a b]", ce.Components[0])
	}
	if !reflect.DeepEqual(ce.Components[1], []string{"c", "d"}) {
		t.Fatalf("two cycles: components[1] = %v, want [c d]", ce.Components[1])
	}
	// Nodes 仍保平铺字典序（向后兼容）。
	if !reflect.DeepEqual(ce.Nodes, []string{"a", "b", "c", "d"}) {
		t.Fatalf("two cycles: nodes = %v, want [a b c d]", ce.Nodes)
	}
}

// TestDetectCycle_HealthyChainPlusTwoCycles 覆盖健康链、两个环和环下游节点的混合图。
// 环下游节点无法完成拓扑排序，但不属于 SCC，不能被误报进 Components。
func TestDetectCycle_HealthyChainPlusTwoCycles(t *testing.T) {
	// a→b 健康链；{c,d} 环 1；{e,f,g} 环 2（e→f→g→e）。
	// h 依赖 c（环外下游，c 捞不出 h 也拓扑不出、但 h 本身不在环中）。
	adj := map[string][]string{
		"a": nil,
		"b": {"a"},
		"c": {"d"},
		"d": {"c"},
		"e": {"g"},
		"f": {"e"},
		"g": {"f"},
		"h": {"c"},
	}
	err := DetectCycle(adj)
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("complex: err = %T, want *CycleError", err)
	}
	// Tarjan SCC 只该报 {c,d} 与 {e,f,g}；h 是环下游但不与任何环同在一个 SCC。
	if len(ce.Components) != 2 {
		t.Fatalf("complex: components = %v, want len 2", ce.Components)
	}
	if !reflect.DeepEqual(ce.Components[0], []string{"c", "d"}) {
		t.Fatalf("complex: components[0] = %v, want [c d]", ce.Components[0])
	}
	if !reflect.DeepEqual(ce.Components[1], []string{"e", "f", "g"}) {
		t.Fatalf("complex: components[1] = %v, want [e f g]", ce.Components[1])
	}
	// 环下游节点 h 不能出现在任何 Component 里。
	for _, c := range ce.Components {
		for _, n := range c {
			if n == "h" {
				t.Fatalf("complex: h 不在任何环中，不该出现在 Components。got %v", ce.Components)
			}
		}
	}
}

// TestDetectCycle_SelfLoop_ComponentsCarryNode 确认单节点自环也会写入 Components。
func TestDetectCycle_SelfLoop_ComponentsCarryNode(t *testing.T) {
	adj := map[string][]string{
		"x": {"x"},
	}
	err := DetectCycle(adj)
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("self loop v2: err = %T, want *CycleError", err)
	}
	if len(ce.Components) != 1 {
		t.Fatalf("self loop v2: components = %v, want len 1", ce.Components)
	}
	if !reflect.DeepEqual(ce.Components[0], []string{"x"}) {
		t.Fatalf("self loop v2: components[0] = %v, want [x]", ce.Components[0])
	}
}
