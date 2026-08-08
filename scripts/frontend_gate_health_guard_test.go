package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

var (
	frontendNPMRunPattern      = regexp.MustCompile(`(?:npm|\$\(NPM\))\s+run\s+([A-Za-z0-9:_-]+)`)
	frontendMakeTargetPattern  = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]*):(?:\s+(.*))?$`)
	frontendMakeCommandPattern = regexp.MustCompile(`^\s*@?(?:\$\(MAKE\)|make)\s+([A-Za-z0-9][A-Za-z0-9_.-]*)`)
)

// TestFrontendGateTopologyIsAcyclic 验证 npm、Make 与 AI runner 的已知跨层调用边不存在死亡目标或调用环。
func TestFrontendGateTopologyIsAcyclic(t *testing.T) {
	graph := frontendGateTopology(t)
	if cycle := firstFrontendGateCycle(graph); len(cycle) > 0 {
		t.Fatalf("frontend gate invocation cycle: %s", strings.Join(cycle, " -> "))
	}
}

// TestFrontendGateCycleDetection 证明直接环、间接环和死亡目标都会被健康检查拒绝。
func TestFrontendGateCycleDetection(t *testing.T) {
	tests := []struct {
		name  string
		graph map[string][]string
		want  bool
	}{
		{name: "acyclic", graph: map[string][]string{"a": {"b"}, "b": nil}},
		{name: "direct cycle", graph: map[string][]string{"a": {"a"}}, want: true},
		{name: "indirect cycle", graph: map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"a"}}, want: true},
		{name: "missing target", graph: map[string][]string{"a": {"missing"}}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := len(firstFrontendGateCycle(test.graph)) > 0
			if got != test.want {
				t.Fatalf("cycle or missing target detected = %v, want %v", got, test.want)
			}
		})
	}
}

// TestFrontendBuildSyncHasUniqueOwner 验证前端构建同步只由 npm build 触发一次。
func TestFrontendBuildSyncHasUniqueOwner(t *testing.T) {
	owners, err := frontendBuildSyncOwners(
		readRepoFile(t, "../frontend-app/package.json"),
		readRepoFile(t, "../Makefile"),
	)
	if err != nil {
		t.Fatalf("collect frontend build sync owners: %v", err)
	}
	want := []string{"npm:build"}
	if !slices.Equal(owners, want) {
		t.Fatalf("frontend build sync owners = %v, want unique owner %v", owners, want)
	}
}

// TestFrontendBuildSyncOwnerDetection 证明跨 npm/Make 和同一 owner 内的重复同步都会被计数。
func TestFrontendBuildSyncOwnerDetection(t *testing.T) {
	packageJSON := `{"scripts":{"build":"vite build --configLoader runner && node scripts/sync-frontend-dist.mjs && node scripts/sync-frontend-dist.mjs"}}`
	makefile := "frontend-app-build:\n\tnode frontend-app/scripts/sync-frontend-dist.mjs\n"
	owners, err := frontendBuildSyncOwners(packageJSON, makefile)
	if err != nil {
		t.Fatalf("collect synthetic frontend build sync owners: %v", err)
	}
	want := []string{"make:frontend-app-build", "npm:build", "npm:build"}
	if !slices.Equal(owners, want) {
		t.Fatalf("duplicate frontend build sync owners = %v, want %v", owners, want)
	}
}

func frontendBuildSyncOwners(packageJSON, makefile string) ([]string, error) {
	const syncScript = "scripts/sync-frontend-dist.mjs"
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(packageJSON), &manifest); err != nil {
		return nil, fmt.Errorf("decode frontend-app/package.json: %w", err)
	}
	owners := make([]string, 0, 1)
	for name, command := range manifest.Scripts {
		for range strings.Count(command, syncScript) {
			owners = append(owners, "npm:"+name)
		}
	}
	var current string
	for line := range strings.SplitSeq(makefile, "\n") {
		if match := frontendMakeTargetPattern.FindStringSubmatch(line); match != nil {
			current = match[1]
			continue
		}
		if current == "" || !strings.HasPrefix(line, "\t") {
			continue
		}
		for range strings.Count(line, syncScript) {
			owners = append(owners, "make:"+current)
		}
	}
	sort.Strings(owners)
	return owners, nil
}

func frontendGateTopology(t *testing.T) map[string][]string {
	t.Helper()
	graph := frontendPackageScriptGraph(t)
	frontendAddMakeGraph(t, graph)
	frontendAddAIRunnerEdges(t, graph)
	for node := range graph {
		sort.Strings(graph[node])
	}
	return graph
}

func frontendPackageScriptGraph(t *testing.T) map[string][]string {
	t.Helper()
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "../frontend-app/package.json")), &manifest); err != nil {
		t.Fatalf("decode frontend-app/package.json: %v", err)
	}
	if len(manifest.Scripts) == 0 {
		t.Fatal("frontend-app/package.json scripts must not be empty")
	}
	graph := make(map[string][]string, len(manifest.Scripts))
	for name := range manifest.Scripts {
		graph["npm:"+name] = nil
	}
	for name, command := range manifest.Scripts {
		for _, match := range frontendNPMRunPattern.FindAllStringSubmatch(command, -1) {
			target := "npm:" + match[1]
			if _, ok := graph[target]; !ok {
				t.Fatalf("npm script %q references unknown script %q", name, match[1])
			}
			frontendAddUniqueEdge(graph, "npm:"+name, target)
		}
	}
	return graph
}

func frontendAddMakeGraph(t *testing.T, graph map[string][]string) {
	t.Helper()
	lines := strings.Split(readRepoFile(t, "../Makefile"), "\n")
	targets := frontendDeclareMakeTargets(lines, graph)
	var current string
	for _, line := range lines {
		current = frontendAddMakeLine(t, graph, targets, current, line)
	}
}

func frontendDeclareMakeTargets(lines []string, graph map[string][]string) map[string]bool {
	targets := map[string]bool{}
	for _, line := range lines {
		if match := frontendMakeTargetPattern.FindStringSubmatch(line); match != nil {
			targets[match[1]] = true
			graph["make:"+match[1]] = graph["make:"+match[1]]
		}
	}
	return targets
}

func frontendAddMakeLine(t *testing.T, graph map[string][]string, targets map[string]bool, current, line string) string {
	if match := frontendMakeTargetPattern.FindStringSubmatch(line); match != nil {
		frontendAddMakeDependencies(graph, targets, match[1], match[2])
		return match[1]
	}
	if current == "" || !strings.HasPrefix(line, "\t") {
		return current
	}
	frontendAddMakeNPMEdges(t, graph, current, line)
	if match := frontendMakeCommandPattern.FindStringSubmatch(line); match != nil && targets[match[1]] {
		frontendAddUniqueEdge(graph, "make:"+current, "make:"+match[1])
	}
	return current
}

func frontendAddMakeDependencies(graph map[string][]string, targets map[string]bool, current, value string) {
	for dependency := range strings.FieldsSeq(value) {
		if targets[dependency] {
			frontendAddUniqueEdge(graph, "make:"+current, "make:"+dependency)
		}
	}
}

func frontendAddMakeNPMEdges(t *testing.T, graph map[string][]string, current, line string) {
	for _, match := range frontendNPMRunPattern.FindAllStringSubmatch(line, -1) {
		target := "npm:" + match[1]
		if _, ok := graph[target]; !ok {
			t.Fatalf("Make target %q references unknown npm script %q", current, match[1])
		}
		frontendAddUniqueEdge(graph, "make:"+current, target)
	}
}

func frontendAddAIRunnerEdges(t *testing.T, graph map[string][]string) {
	t.Helper()
	staticGuard := frontendStaticGuardOwner(t, readRepoFile(t, "ai_maintenance/gate_execution.go"))
	edges := map[string]string{
		"ai:frontend:static-guards":       staticGuard,
		"ai:frontend:lint":                "npm:lint",
		"ai:frontend:typecheck-contracts": "npm:typecheck:contracts",
		"ai:frontend:embed-verify":        "npm:verify:embed:isolated",
		"ai:frontend:performance-verify":  "npm:performance:verify",
	}
	for from, to := range edges {
		graph[from] = graph[from]
		frontendAddUniqueEdge(graph, from, to)
	}
}

func frontendStaticGuardOwner(t *testing.T, source string) string {
	t.Helper()
	pattern := regexp.MustCompile(`(?s)func frontendStaticGuardCommand\(\).*?return "frontend-app", "npm", \[\]string\{"run", "([^"]+)"\}`)
	match := pattern.FindStringSubmatch(source)
	if len(match) != 2 {
		t.Fatal("cannot derive frontend static guard owner from production command")
	}
	return "npm:" + match[1]
}

// TestFrontendStaticGuardOwnerComesFromProductionCommand 防止健康图再次漂移到旧的 architecture 子门禁。
func TestFrontendStaticGuardOwnerComesFromProductionCommand(t *testing.T) {
	graph := frontendGateTopology(t)
	want := []string{"npm:guard:critical-skip"}
	if !slices.Equal(graph["ai:frontend:static-guards"], want) {
		t.Fatalf("AI frontend static guard owner = %v, want %v", graph["ai:frontend:static-guards"], want)
	}
}

// TestFrontendTurnContractHasSingleOwner 防止 AI 计划先经静态门禁再重复执行 turn-contract。
func TestFrontendTurnContractHasSingleOwner(t *testing.T) {
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "../frontend-app/package.json")), &manifest); err != nil {
		t.Fatalf("decode frontend-app/package.json: %v", err)
	}
	if count := strings.Count(manifest.Scripts["guard:critical-skip"], "npm run guard:turn-contract"); count != 0 {
		t.Fatalf("guard:critical-skip turn-contract owners = %d, want 0 because AI has turncontract:verify", count)
	}
	if count := strings.Count(manifest.Scripts["test:hook:preflight"], "npm run guard:turn-contract"); count != 1 {
		t.Fatalf("preflight turn-contract owners = %d, want 1", count)
	}
}

func frontendAddUniqueEdge(graph map[string][]string, from, to string) {
	if slices.Contains(graph[from], to) {
		return
	}
	graph[from] = append(graph[from], to)
}

const (
	frontendUnvisited = iota
	frontendVisiting
	frontendVisited
)

type frontendGateWalk struct {
	graph map[string][]string
	state map[string]int
	stack []string
}

func (walk *frontendGateWalk) visit(node string) []string {
	switch walk.state[node] {
	case frontendVisiting:
		return frontendCycleFromStack(walk.stack, node)
	case frontendVisited:
		return nil
	}
	walk.state[node] = frontendVisiting
	walk.stack = append(walk.stack, node)
	for _, next := range walk.graph[node] {
		if _, ok := walk.graph[next]; !ok {
			return []string{fmt.Sprintf("%s (missing)", next)}
		}
		if cycle := walk.visit(next); len(cycle) > 0 {
			return cycle
		}
	}
	walk.stack = walk.stack[:len(walk.stack)-1]
	walk.state[node] = frontendVisited
	return nil
}

func frontendCycleFromStack(stack []string, node string) []string {
	for index, candidate := range stack {
		if candidate == node {
			return append(append([]string{}, stack[index:]...), node)
		}
	}
	return []string{node, node}
}

func firstFrontendGateCycle(graph map[string][]string) []string {
	walk := frontendGateWalk{graph: graph, state: map[string]int{}}
	nodes := make([]string, 0, len(graph))
	for node := range graph {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		if cycle := walk.visit(node); len(cycle) > 0 {
			return cycle
		}
	}
	return nil
}
