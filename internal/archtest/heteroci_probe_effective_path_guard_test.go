package archtest

import (
	"fmt"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest/ssaload"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

const heterociEffectivePathPackage = "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"

type heterociEffectivePathGuard struct {
	function *ssa.Function
	local    *types.Package
}

type heterociEffectivePathBranch struct {
	block   *ssa.BasicBlock
	nonzero *ssa.BasicBlock
}

type heterociEffectivePathBoundary struct {
	headers map[*ssa.BasicBlock]bool
	latches map[*ssa.BasicBlock]bool
}

type heterociEffectivePathState struct {
	block  *ssa.BasicBlock
	stored bool
}

// TestHeterociEffectiveRoutingSnapshotPathGuard 证明生产实现的每条非零路径都会先写回有效快照。
func TestHeterociEffectiveRoutingSnapshotPathGuard(t *testing.T) {
	loaded, err := loadHeterociEffectiveProduction(t)
	if err != nil {
		t.Fatal(err)
	}
	if violations := loaded.violations(); len(violations) != 0 {
		t.Fatalf("effectiveRoutingSnapshot path violations: %s", strings.Join(violations, "; "))
	}
}

// TestHeterociEffectiveRoutingSnapshotPathGuardRejectsSyntheticBypass 固定绕过 store 的反例必须为 RED。
func TestHeterociEffectiveRoutingSnapshotPathGuardRejectsSyntheticBypass(t *testing.T) {
	loaded := loadHeterociEffectiveSynthetic(t, `package heteroci
	type ResourceCapacity struct { CPUMilli int64 }
	type NodeSnapshot struct { Available ResourceCapacity }
	type SnapshotSet struct { Nodes []NodeSnapshot }
	type nodeReservationKey struct{}

	func cloneRoutingSnapshotSet(source SnapshotSet) SnapshotSet { return source }
	func checkedSubtractReservation(available, reserved ResourceCapacity) (ResourceCapacity, error) {
		available.CPUMilli -= reserved.CPUMilli
		return available, nil
	}
	func effectiveRoutingSnapshot(source SnapshotSet, current map[nodeReservationKey]ResourceCapacity) (SnapshotSet, error) {
		effective := cloneRoutingSnapshotSet(source)
		for index := range effective.Nodes {
			node := &effective.Nodes[index]
			reserved := current[nodeReservationKey{}]
			if reserved == (ResourceCapacity{}) { continue }
			available, err := checkedSubtractReservation(node.Available, reserved)
			if err != nil { return SnapshotSet{}, err }
			if reserved.CPUMilli > 0 { return effective, nil }
			node.Available = available
		}
		return effective, nil
	}`)
	violations := strings.Join(loaded.violations(), "; ")
	if !strings.Contains(violations, "nil-error success return") {
		t.Fatalf("synthetic bypass violations = %q, want nil-error success return rejection", violations)
	}
}

func TestHeterociEffectiveRoutingSnapshotPathGuardRejectsPostStoreWrites(t *testing.T) {
	cases := []struct {
		name  string
		write string
	}{
		{name: "restore gross Available", write: "node.Available = source.Nodes[index].Available"},
		{name: "overwrite whole NodeSnapshot", write: "*node = source.Nodes[index]"},
		{name: "overwrite CPUMilli", write: "node.Available.CPUMilli = source.Nodes[index].Available.CPUMilli"},
		{name: "overwrite MemoryBytes", write: "node.Available.MemoryBytes = source.Nodes[index].Available.MemoryBytes"},
		{name: "overwrite MaxConcurrency", write: "node.Available.MaxConcurrency = source.Nodes[index].Available.MaxConcurrency"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			loaded := loadHeterociEffectiveSynthetic(t, heterociEffectivePathWriteFixture(testCase.write))
			violations := strings.Join(loaded.violations(), "; ")
			if !strings.Contains(violations, "post-store alias write") {
				t.Fatalf("synthetic post-store write violations = %q, want alias-write rejection", violations)
			}
		})
	}
}

func heterociEffectivePathWriteFixture(write string) string {
	return `package heteroci
	type ResourceCapacity struct { CPUMilli int64; MemoryBytes int64; MaxConcurrency int }
	type NodeSnapshot struct { Available ResourceCapacity }
	type SnapshotSet struct { Nodes []NodeSnapshot }
	type nodeReservationKey struct{}

	func cloneRoutingSnapshotSet(source SnapshotSet) SnapshotSet { return source }
	func checkedSubtractReservation(available, reserved ResourceCapacity) (ResourceCapacity, error) {
		available.CPUMilli -= reserved.CPUMilli
		return available, nil
	}
	func effectiveRoutingSnapshot(source SnapshotSet, current map[nodeReservationKey]ResourceCapacity) (SnapshotSet, error) {
		effective := cloneRoutingSnapshotSet(source)
		for index := range effective.Nodes {
			node := &effective.Nodes[index]
			reserved := current[nodeReservationKey{}]
			if reserved == (ResourceCapacity{}) { continue }
			available, err := checkedSubtractReservation(node.Available, reserved)
			if err != nil { return SnapshotSet{}, err }
			node.Available = available
			` + write + `
		}
		return effective, nil
	}`
}

func loadHeterociEffectiveProduction(t *testing.T) (*heterociEffectivePathGuard, error) {
	t.Helper()
	root, err := heterociEffectiveRepoRoot()
	if err != nil {
		return nil, err
	}
	loaded, err := ssaload.Load(ssaload.Options{
		RepoRoot: root,
		Patterns: []string{"./internal/devtools/heteroci"},
		Tests:    false,
		LoadMode: packages.LoadSyntax,
		Include:  func(pkg *packages.Package) bool { return pkg.PkgPath == heterociEffectivePathPackage },
	})
	if err != nil {
		return nil, fmt.Errorf("load effective snapshot production SSA: %w", err)
	}
	return newHeterociEffectivePathGuard(loaded)
}

func loadHeterociEffectiveSynthetic(t *testing.T, source string) *heterociEffectivePathGuard {
	t.Helper()
	root := t.TempDir()
	writeHeterociEffectiveFixture(t, filepath.Join(root, "go.mod"), "module synthetic\n\ngo 1.26\n")
	writeHeterociEffectiveFixture(t, filepath.Join(root, "heteroci", "fixture.go"), source)
	loaded, err := ssaload.Load(ssaload.Options{
		RepoRoot: root,
		Patterns: []string{"./heteroci"},
		Tests:    false,
		LoadMode: packages.LoadSyntax,
		Include:  func(pkg *packages.Package) bool { return pkg.PkgPath == "synthetic/heteroci" },
	})
	if err != nil {
		t.Fatalf("load synthetic effective snapshot SSA: %v", err)
	}
	guard, err := newHeterociEffectivePathGuard(loaded)
	if err != nil {
		t.Fatal(err)
	}
	return guard
}

func newHeterociEffectivePathGuard(loaded []*packages.Package) (*heterociEffectivePathGuard, error) {
	if len(loaded) != 1 {
		return nil, fmt.Errorf("effective snapshot SSA package count = %d, want 1", len(loaded))
	}
	_, built, err := ssaload.Build(loaded)
	if err != nil {
		return nil, fmt.Errorf("build effective snapshot SSA: %w", err)
	}
	if len(built) != 1 {
		return nil, fmt.Errorf("effective snapshot built package count = %d, want 1", len(built))
	}
	function := built[0].Func("effectiveRoutingSnapshot")
	if function == nil {
		return nil, fmt.Errorf("effectiveRoutingSnapshot SSA root is missing")
	}
	return &heterociEffectivePathGuard{function: function, local: loaded[0].Types}, nil
}

func heterociEffectiveRepoRoot() (string, error) {
	working, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get effective snapshot test cwd: %w", err)
	}
	for current := working; ; current = filepath.Dir(current) {
		if _, statErr := os.Stat(filepath.Join(current, "go.mod")); statErr == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("find effective snapshot repository root from %q", working)
		}
	}
}

func writeHeterociEffectiveFixture(t *testing.T, path, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create effective snapshot fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write effective snapshot fixture: %v", err)
	}
}

func (guard heterociEffectivePathGuard) violations() []string {
	store, branches, boundaries, err := guard.pathInputs()
	if err != nil {
		return []string{err.Error()}
	}
	return guard.scanPathBranches(store, branches, boundaries)
}

func (guard heterociEffectivePathGuard) pathInputs() (*ssa.Store, []heterociEffectivePathBranch, heterociEffectivePathBoundary, error) {
	if guard.function == nil || guard.local == nil {
		return nil, nil, heterociEffectivePathBoundary{}, fmt.Errorf("effectiveRoutingSnapshot SSA root is missing")
	}
	call, err := guard.checkedCall()
	if err != nil {
		return nil, nil, heterociEffectivePathBoundary{}, err
	}
	store, err := guard.checkedStore(call)
	if err != nil {
		return nil, nil, heterociEffectivePathBoundary{}, err
	}
	if !guard.storeFeedsSuccessfulReturns(store) {
		return nil, nil, heterociEffectivePathBoundary{}, fmt.Errorf("checked subtraction store does not feed every successful SnapshotSet return")
	}
	if len(call.Common().Args) != 2 {
		return nil, nil, heterociEffectivePathBoundary{}, fmt.Errorf("checkedSubtractReservation call must have available and reserved arguments")
	}
	branches := guard.nonzeroBranches(call.Common().Args[1], call.Block())
	if len(branches) == 0 {
		return nil, nil, heterociEffectivePathBoundary{}, fmt.Errorf("reserved zero guard has no non-zero branch reaching checked subtraction")
	}
	return store, branches, guard.loopBoundaries(), nil
}

func (guard heterociEffectivePathGuard) scanPathBranches(store *ssa.Store, branches []heterociEffectivePathBranch, boundaries heterociEffectivePathBoundary) []string {
	found := make(map[string]bool)
	for _, branch := range branches {
		for _, violation := range guard.walkNonzeroPath(branch, store, boundaries) {
			found[violation] = true
		}
	}
	result := make([]string, 0, len(found))
	for violation := range found {
		result = append(result, violation)
	}
	sort.Strings(result)
	return result
}

func (guard heterociEffectivePathGuard) checkedCall() (*ssa.Call, error) {
	var calls []*ssa.Call
	for _, block := range guard.function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && heterociEffectivePathNamed(call.Common().StaticCallee(), guard.local, "checkedSubtractReservation") {
				calls = append(calls, call)
			}
		}
	}
	if len(calls) != 1 {
		return nil, fmt.Errorf("effectiveRoutingSnapshot must call checkedSubtractReservation exactly once, got %d", len(calls))
	}
	return calls[0], nil
}

func (guard heterociEffectivePathGuard) checkedStore(call *ssa.Call) (*ssa.Store, error) {
	if call == nil || len(call.Common().Args) != 2 {
		return nil, fmt.Errorf("checkedSubtractReservation call has invalid arguments")
	}
	stores := guard.checkedStores(call)
	if len(stores) != 1 {
		return nil, fmt.Errorf("checked subtraction result must have exactly one NodeSnapshot.Available store, got %d", len(stores))
	}
	return stores[0], nil
}

func (guard heterociEffectivePathGuard) checkedStores(call *ssa.Call) []*ssa.Store {
	var stores []*ssa.Store
	for _, reference := range heterociEffectivePathReferences(call) {
		extract, ok := reference.(*ssa.Extract)
		if !ok || extract.Index != 0 {
			continue
		}
		for _, use := range heterociEffectivePathReferences(extract) {
			store, ok := use.(*ssa.Store)
			if ok && heterociEffectivePathAvailableField(store.Addr, guard.local) && heterociEffectivePathFlowsFrom(store.Val, extract, nil) {
				stores = append(stores, store)
			}
		}
	}
	return stores
}

func (guard heterociEffectivePathGuard) storeFeedsSuccessfulReturns(store *ssa.Store) bool {
	storeRoots := heterociEffectivePathMemoryRoots(store.Addr)
	seenSuccess := false
	for _, block := range guard.function.Blocks {
		for _, instruction := range block.Instrs {
			returned, ok := instruction.(*ssa.Return)
			if !ok || len(returned.Results) != 2 || !heterociEffectivePathNilError(returned.Results[1]) {
				continue
			}
			seenSuccess = true
			if !heterociEffectivePathSetsIntersect(storeRoots, heterociEffectivePathMemoryRoots(returned.Results[0])) {
				return false
			}
		}
	}
	return seenSuccess
}

func (guard heterociEffectivePathGuard) nonzeroBranches(reserved ssa.Value, callBlock *ssa.BasicBlock) []heterociEffectivePathBranch {
	var result []heterociEffectivePathBranch
	seen := make(map[*ssa.BasicBlock]bool)
	for _, block := range guard.function.Blocks {
		if len(block.Instrs) == 0 {
			continue
		}
		branch, ok := block.Instrs[len(block.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		nonzero, ok := heterociEffectivePathNonzeroSuccessor(branch, reserved)
		if ok && !seen[block] && heterociEffectivePathReaches(nonzero, callBlock) {
			seen[block] = true
			result = append(result, heterociEffectivePathBranch{block: block, nonzero: nonzero})
		}
	}
	return result
}

func heterociEffectivePathNonzeroSuccessor(branch *ssa.If, reserved ssa.Value) (*ssa.BasicBlock, bool) {
	comparison, ok := branch.Cond.(*ssa.BinOp)
	if !ok || comparison.Op != token.EQL && comparison.Op != token.NEQ || len(branch.Block().Succs) != 2 {
		return nil, false
	}
	other, ok := heterociEffectivePathComparisonOther(comparison, reserved)
	if !ok || !heterociEffectivePathZero(other) {
		return nil, false
	}
	nonzero := branch.Block().Succs[1]
	if comparison.Op == token.NEQ {
		nonzero = branch.Block().Succs[0]
	}
	return nonzero, true
}

func heterociEffectivePathComparisonOther(comparison *ssa.BinOp, reserved ssa.Value) (ssa.Value, bool) {
	if heterociEffectivePathEquivalent(comparison.X, reserved) {
		return comparison.Y, true
	}
	if heterociEffectivePathEquivalent(comparison.Y, reserved) {
		return comparison.X, true
	}
	return nil, false
}

func heterociEffectivePathEquivalent(left, right ssa.Value) bool {
	if left == right {
		return true
	}
	if heterociEffectivePathEquivalentThrough(left, right, make(map[ssa.Value]bool)) {
		return true
	}
	return heterociEffectivePathSetsIntersect(heterociEffectivePathMemoryRoots(left), heterociEffectivePathMemoryRoots(right))
}

func heterociEffectivePathEquivalentThrough(left, right ssa.Value, seen map[ssa.Value]bool) bool {
	if left == nil || right == nil || seen[left] {
		return false
	}
	seen[left] = true
	return heterociEffectivePathEquivalentFresh(left, right, seen)
}

func heterociEffectivePathEquivalentFresh(left, right ssa.Value, seen map[ssa.Value]bool) bool {
	switch value := left.(type) {
	case *ssa.Extract:
		return heterociEffectivePathEquivalentExtract(value, right, seen)
	case *ssa.ChangeType:
		return heterociEffectivePathEquivalentUnary(value.X, right, seen)
	case *ssa.Convert:
		return heterociEffectivePathEquivalentUnary(value.X, right, seen)
	case *ssa.ChangeInterface:
		return heterociEffectivePathEquivalentUnary(value.X, right, seen)
	case *ssa.MakeInterface:
		return heterociEffectivePathEquivalentUnary(value.X, right, seen)
	case *ssa.UnOp:
		return heterociEffectivePathEquivalentUnary(value.X, right, seen)
	case *ssa.Phi:
		return heterociEffectivePathEquivalentPhi(value, right, seen)
	}
	return false
}

func heterociEffectivePathEquivalentExtract(value *ssa.Extract, right ssa.Value, seen map[ssa.Value]bool) bool {
	other, ok := right.(*ssa.Extract)
	return ok && value.Index == other.Index && heterociEffectivePathEquivalentThrough(value.Tuple, other.Tuple, seen)
}

func heterociEffectivePathEquivalentUnary(value, right ssa.Value, seen map[ssa.Value]bool) bool {
	return heterociEffectivePathEquivalentThrough(value, right, seen)
}

func heterociEffectivePathEquivalentPhi(value *ssa.Phi, right ssa.Value, seen map[ssa.Value]bool) bool {
	for _, edge := range value.Edges {
		if heterociEffectivePathEquivalentThrough(edge, right, seen) {
			return true
		}
	}
	return false
}

func heterociEffectivePathZero(value ssa.Value) bool {
	constant, ok := value.(*ssa.Const)
	return ok && constant.Value == nil
}

func (guard heterociEffectivePathGuard) loopBoundaries() heterociEffectivePathBoundary {
	boundaries := heterociEffectivePathBoundary{headers: make(map[*ssa.BasicBlock]bool), latches: make(map[*ssa.BasicBlock]bool)}
	for _, block := range guard.function.Blocks {
		for _, successor := range block.Succs {
			if successor.Dominates(block) {
				boundaries.headers[successor] = true
				boundaries.latches[block] = true
			}
		}
	}
	return boundaries
}

func (guard heterociEffectivePathGuard) walkNonzeroPath(branch heterociEffectivePathBranch, store *ssa.Store, boundaries heterociEffectivePathBoundary) []string {
	queue := []heterociEffectivePathState{{block: branch.nonzero}}
	seen := make(map[heterociEffectivePathState]bool)
	found := make(map[string]bool)
	for len(queue) > 0 {
		state := queue[0]
		queue = queue[1:]
		if state.block == nil {
			continue
		}
		if seen[state] {
			guard.recordPathCycle(state, branch, found)
			continue
		}
		seen[state] = true
		stored, terminated := guard.inspectPathBlock(state, store, boundaries, branch, found)
		if terminated {
			continue
		}
		queue = guard.enqueuePathSuccessors(queue, state.block, stored, boundaries, branch, found)
	}
	result := make([]string, 0, len(found))
	for violation := range found {
		result = append(result, violation)
	}
	sort.Strings(result)
	return result
}

func (guard heterociEffectivePathGuard) recordPathCycle(state heterociEffectivePathState, branch heterociEffectivePathBranch, found map[string]bool) {
	if !state.stored {
		found[fmt.Sprintf("non-zero branch %d enters a cycle before checked subtraction store", branch.block.Index)] = true
	}
}

func (guard heterociEffectivePathGuard) inspectPathBlock(state heterociEffectivePathState, store *ssa.Store, boundaries heterociEffectivePathBoundary, branch heterociEffectivePathBranch, found map[string]bool) (bool, bool) {
	stored, terminated := guard.inspectPathInstructions(state, store, branch, found)
	if terminated {
		return stored, true
	}
	if !stored && boundaries.latches[state.block] {
		found[fmt.Sprintf("non-zero branch %d reaches loop latch before checked subtraction store", branch.block.Index)] = true
		return stored, true
	}
	if len(state.block.Succs) == 0 {
		if !stored {
			found[fmt.Sprintf("non-zero branch %d exits CFG before checked subtraction store", branch.block.Index)] = true
		}
		return stored, true
	}
	return stored, false
}

func (guard heterociEffectivePathGuard) inspectPathInstructions(state heterociEffectivePathState, store *ssa.Store, branch heterociEffectivePathBranch, found map[string]bool) (bool, bool) {
	stored := state.stored
	for _, instruction := range state.block.Instrs {
		stored = stored || instruction == store
		if guard.recordPostStoreWrite(stored, instruction, store, branch, found) {
			return stored, true
		}
		returned, ok := instruction.(*ssa.Return)
		if ok {
			guard.recordPathReturn(stored, returned, branch, found)
			return stored, true
		}
	}
	return stored, false
}

func (guard heterociEffectivePathGuard) recordPostStoreWrite(stored bool, instruction ssa.Instruction, store *ssa.Store, branch heterociEffectivePathBranch, found map[string]bool) bool {
	if !stored || instruction == store {
		return false
	}
	write, ok := instruction.(*ssa.Store)
	if !ok || !guard.postStoreAliasWrite(write, store) {
		return false
	}
	found[fmt.Sprintf("non-zero branch %d reaches post-store alias write before exit", branch.block.Index)] = true
	return true
}

func (guard heterociEffectivePathGuard) recordPathReturn(stored bool, returned *ssa.Return, branch heterociEffectivePathBranch, found map[string]bool) {
	if !stored && len(returned.Results) == 2 && heterociEffectivePathNilError(returned.Results[1]) {
		found[fmt.Sprintf("non-zero branch %d reaches nil-error success return before checked subtraction store", branch.block.Index)] = true
	}
}

func (guard heterociEffectivePathGuard) enqueuePathSuccessors(queue []heterociEffectivePathState, block *ssa.BasicBlock, stored bool, boundaries heterociEffectivePathBoundary, branch heterociEffectivePathBranch, found map[string]bool) []heterociEffectivePathState {
	for _, successor := range block.Succs {
		if !stored && boundaries.headers[successor] {
			found[fmt.Sprintf("non-zero branch %d reaches loop header before checked subtraction store", branch.block.Index)] = true
			continue
		}
		queue = append(queue, heterociEffectivePathState{block: successor, stored: stored})
	}
	return queue
}

func heterociEffectivePathNamed(function *ssa.Function, local *types.Package, name string) bool {
	if function == nil {
		return false
	}
	object, ok := function.Object().(*types.Func)
	return ok && object.Pkg() == local && object.Name() == name
}

func heterociEffectivePathAvailableField(value ssa.Value, local *types.Package) bool {
	field, ok := value.(*ssa.FieldAddr)
	if !ok {
		return false
	}
	named, ok := heterociEffectivePathDereference(field.X.Type()).(*types.Named)
	if !ok || named.Obj().Pkg() != local || named.Obj().Name() != "NodeSnapshot" {
		return false
	}
	structure, ok := named.Underlying().(*types.Struct)
	return ok && field.Field >= 0 && field.Field < structure.NumFields() && structure.Field(field.Field).Name() == "Available"
}

func (guard heterociEffectivePathGuard) postStoreAliasWrite(write, trusted *ssa.Store) bool {
	if write == nil || trusted == nil || !heterociEffectivePathSetsIntersect(heterociEffectivePathMemoryRoots(write.Addr), heterociEffectivePathMemoryRoots(trusted.Addr)) {
		return false
	}
	return guard.capacityAliasAddress(write.Addr)
}

func (guard heterociEffectivePathGuard) capacityAliasAddress(address ssa.Value) bool {
	field, ok := address.(*ssa.FieldAddr)
	if ok {
		return guard.capacityAliasField(field)
	}
	named, ok := heterociEffectivePathDereference(address.Type()).(*types.Named)
	return ok && named.Obj().Pkg() == guard.local && named.Obj().Name() == "NodeSnapshot"
}

func (guard heterociEffectivePathGuard) capacityAliasField(field *ssa.FieldAddr) bool {
	named, ok := heterociEffectivePathDereference(field.X.Type()).(*types.Named)
	if !ok || named.Obj().Pkg() != guard.local {
		return false
	}
	structure, ok := named.Underlying().(*types.Struct)
	if !ok || field.Field < 0 || field.Field >= structure.NumFields() {
		return false
	}
	fieldName := structure.Field(field.Field).Name()
	if named.Obj().Name() == "NodeSnapshot" {
		return fieldName == "Available"
	}
	return named.Obj().Name() == "ResourceCapacity" && heterociEffectivePathCapacityField(fieldName)
}

func heterociEffectivePathCapacityField(name string) bool {
	return name == "CPUMilli" || name == "MemoryBytes" || name == "MaxConcurrency"
}

func heterociEffectivePathDereference(typ types.Type) types.Type {
	for {
		pointer, ok := typ.(*types.Pointer)
		if !ok {
			return typ
		}
		typ = pointer.Elem()
	}
}

func heterociEffectivePathFlowsFrom(value, origin ssa.Value, seen map[ssa.Value]bool) bool {
	if value == origin {
		return true
	}
	if value == nil {
		return false
	}
	if seen == nil {
		seen = make(map[ssa.Value]bool)
	}
	if seen[value] {
		return false
	}
	seen[value] = true
	switch value := value.(type) {
	case *ssa.ChangeInterface:
		return heterociEffectivePathFlowUnary(value.X, origin, seen)
	case *ssa.ChangeType:
		return heterociEffectivePathFlowUnary(value.X, origin, seen)
	case *ssa.Convert:
		return heterociEffectivePathFlowUnary(value.X, origin, seen)
	case *ssa.MakeInterface:
		return heterociEffectivePathFlowUnary(value.X, origin, seen)
	case *ssa.Phi:
		return heterociEffectivePathFlowPhi(value, origin, seen)
	}
	return false
}

func heterociEffectivePathFlowUnary(value, origin ssa.Value, seen map[ssa.Value]bool) bool {
	return heterociEffectivePathFlowsFrom(value, origin, seen)
}

func heterociEffectivePathFlowPhi(value *ssa.Phi, origin ssa.Value, seen map[ssa.Value]bool) bool {
	for _, edge := range value.Edges {
		if heterociEffectivePathFlowsFrom(edge, origin, seen) {
			return true
		}
	}
	return false
}

func heterociEffectivePathNilError(value ssa.Value) bool {
	constant, ok := value.(*ssa.Const)
	return ok && constant.Value == nil
}

func heterociEffectivePathReferences(value ssa.Value) []ssa.Instruction {
	if value == nil || value.Referrers() == nil {
		return nil
	}
	return *value.Referrers()
}

func heterociEffectivePathMemoryRoots(value ssa.Value) map[ssa.Value]bool {
	result := make(map[ssa.Value]bool)
	heterociEffectivePathCollectMemoryRoots(value, result, make(map[ssa.Value]bool))
	return result
}

func heterociEffectivePathCollectMemoryRoots(value ssa.Value, roots, seen map[ssa.Value]bool) {
	if value == nil || seen[value] {
		return
	}
	seen[value] = true
	switch value := value.(type) {
	case *ssa.FieldAddr:
		heterociEffectivePathCollectMemoryUnary(value.X, roots, seen)
	case *ssa.IndexAddr:
		heterociEffectivePathCollectMemoryUnary(value.X, roots, seen)
	case *ssa.Slice:
		heterociEffectivePathCollectMemoryUnary(value.X, roots, seen)
	case *ssa.ChangeType:
		heterociEffectivePathCollectMemoryUnary(value.X, roots, seen)
	case *ssa.Convert:
		heterociEffectivePathCollectMemoryUnary(value.X, roots, seen)
	case *ssa.UnOp:
		heterociEffectivePathCollectMemoryUnary(value.X, roots, seen)
	case *ssa.Phi:
		heterociEffectivePathCollectMemoryPhi(value, roots, seen)
	default:
		roots[value] = true
	}
}

func heterociEffectivePathCollectMemoryUnary(value ssa.Value, roots, seen map[ssa.Value]bool) {
	heterociEffectivePathCollectMemoryRoots(value, roots, seen)
}

func heterociEffectivePathCollectMemoryPhi(value *ssa.Phi, roots, seen map[ssa.Value]bool) {
	for _, edge := range value.Edges {
		heterociEffectivePathCollectMemoryRoots(edge, roots, seen)
	}
}

func heterociEffectivePathSetsIntersect(left, right map[ssa.Value]bool) bool {
	for value := range left {
		if right[value] {
			return true
		}
	}
	return false
}

func heterociEffectivePathReaches(start, target *ssa.BasicBlock) bool {
	queue := []*ssa.BasicBlock{start}
	seen := make(map[*ssa.BasicBlock]bool)
	for len(queue) > 0 {
		block := queue[0]
		queue = queue[1:]
		if block == target {
			return true
		}
		if block == nil || seen[block] {
			continue
		}
		seen[block] = true
		queue = append(queue, block.Succs...)
	}
	return false
}
