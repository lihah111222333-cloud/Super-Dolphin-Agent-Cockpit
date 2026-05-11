package nodeexec

import (
	"errors"
	"reflect"
	"testing"
)

// DetectCycle 单测覆盖矩阵：
//   - happy path: 空图 / 单节点无依赖 / DAG 链 / DAG 树
//   - 环: 自环 / 二节点对环 / 三节点链环
//   - 外部依赖: depends_on 引用了 adjacency 外的节点 → 视为外部、不入图

func TestDetectCycle_Empty(t *testing.T) {
	t.Parallel()
	if err := DetectCycle(map[string][]string{}); err != nil {
		t.Fatalf("empty graph: err = %v, want nil", err)
	}
}

// TestDetectCycle_NilAdjacency 防御性 nil map 不该 panic（R3 P3 #5）。
// 调用点上游可能因 0-item ops 生 nil map，走同「空图」路径。
func TestDetectCycle_NilAdjacency(t *testing.T) {
	t.Parallel()
	var adj map[string][]string // nil
	if err := DetectCycle(adj); err != nil {
		t.Fatalf("nil adjacency: err = %v, want nil", err)
	}
}

func TestDetectCycle_SingleNodeNoDeps(t *testing.T) {
	t.Parallel()
	if err := DetectCycle(map[string][]string{"a": nil}); err != nil {
		t.Fatalf("single node: err = %v, want nil", err)
	}
}

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

// BenchmarkDetectCycle_1000Nodes 超大图床型 benchmark（R3 P3 #5）。
// 构造 1000 节点线性链 + 末节点依赖首节点闭环，跡实 Kahn O(V+E)
// 在千节点量级上的常数。运行：go test -bench=BenchmarkDetectCycle .
//
// 环检测路径（1 ring）与无环路径（1000-node DAG）都临伍，看出「无环
// 提前返 nil」还是「有环 出 newCycleError」的中位数表现。
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
			cycleAdj[nodes[i]] = []string{nodes[N-1]} // 闭环后凃
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
