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
	if err := DetectCycle(map[string][]string{}); err != nil {
		t.Fatalf("empty graph: err = %v, want nil", err)
	}
}

func TestDetectCycle_SingleNodeNoDeps(t *testing.T) {
	if err := DetectCycle(map[string][]string{"a": nil}); err != nil {
		t.Fatalf("single node: err = %v, want nil", err)
	}
}

func TestDetectCycle_LinearChain(t *testing.T) {
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

// --- v2 (R3 P2 #2) Tarjan SCC 拆环单测 ---

// 单环向后兼容：Components 应为一个，内容 = Nodes。
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

// 两个独立环应被拆为两个 Components。
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

// 复杂场景：健康链 + 两个环 + 环下游捞不出的节点。验证环被准确拆，且不把
// 「环下游节点」误当成环报出来。
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

// 单节点自环：Components 不该丢。
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
