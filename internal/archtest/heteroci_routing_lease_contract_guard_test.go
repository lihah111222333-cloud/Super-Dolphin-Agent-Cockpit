package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// TestHeterociRoutingSchemaVersionsFreezeV2 prevents a legacy routing wire
// identity from being accepted as a current local routing decision or lease.
func TestHeterociRoutingSchemaVersionsFreezeV2(t *testing.T) {
	if got, want := heteroci.RoutingDecisionSchemaVersion, "heteroci-routing-decision/v2"; got != want {
		t.Fatalf("RoutingDecisionSchemaVersion = %q, want %q", got, want)
	}
	if got, want := heteroci.RoutingLeaseSchemaVersion, "heteroci-routing-lease/v2"; got != want {
		t.Fatalf("RoutingLeaseSchemaVersion = %q, want %q", got, want)
	}
}

// TestHeterociRoutingWireLayoutsAreExact keeps the two public wire structs
// field-for-field compatible with their frozen ordered schemas.
func TestHeterociRoutingWireLayoutsAreExact(t *testing.T) {
	assertHeterociRoutingStructLayout(t, reflect.TypeFor[heteroci.RouteAssignment](), []heterociRoutingFieldSpec{
		{name: "UnitID", typ: reflect.TypeFor[string](), tag: `json:"unit_id"`},
		{name: "Executor", typ: reflect.TypeFor[heteroci.ExecutorKind](), tag: `json:"executor"`},
		{name: "NodeID", typ: reflect.TypeFor[string](), tag: `json:"node_id"`},
		{name: "Endpoint", typ: reflect.TypeFor[string](), tag: `json:"endpoint"`},
		{name: "ProbeIdentity", typ: reflect.TypeFor[string](), tag: `json:"probe_identity"`},
		{name: "ProbeBootID", typ: reflect.TypeFor[string](), tag: `json:"probe_boot_id"`},
		{name: "ECITier", typ: reflect.TypeFor[heteroci.ECIResourceTier](), tag: `json:"eci_tier"`},
	})
	assertHeterociRoutingStructLayout(t, reflect.TypeFor[heteroci.RoutingLease](), []heterociRoutingFieldSpec{
		{name: "SchemaVersion", typ: reflect.TypeFor[string](), tag: `json:"schema_version"`},
		{name: "Authority", typ: reflect.TypeFor[string](), tag: `json:"authority"`},
		{name: "RunID", typ: reflect.TypeFor[string](), tag: `json:"run_id"`},
		{name: "DecisionDigest", typ: reflect.TypeFor[string](), tag: `json:"decision_digest"`},
		{name: "UnitID", typ: reflect.TypeFor[string](), tag: `json:"unit_id"`},
		{name: "Executor", typ: reflect.TypeFor[heteroci.ExecutorKind](), tag: `json:"executor"`},
		{name: "NodeID", typ: reflect.TypeFor[string](), tag: `json:"node_id"`},
		{name: "Endpoint", typ: reflect.TypeFor[string](), tag: `json:"endpoint"`},
		{name: "ProbeIdentity", typ: reflect.TypeFor[string](), tag: `json:"probe_identity"`},
		{name: "ProbeBootID", typ: reflect.TypeFor[string](), tag: `json:"probe_boot_id"`},
		{name: "ECITier", typ: reflect.TypeFor[heteroci.ECIResourceTier](), tag: `json:"eci_tier"`},
		{name: "State", typ: reflect.TypeFor[heteroci.RoutingLeaseState](), tag: `json:"state"`},
		{name: "ReservedAtUnixMilli", typ: reflect.TypeFor[int64](), tag: `json:"reserved_at_unix_milli"`},
		{name: "StartedAtUnixMilli", typ: reflect.TypeFor[int64](), tag: `json:"started_at_unix_milli"`},
		{name: "FinishedAtUnixMilli", typ: reflect.TypeFor[int64](), tag: `json:"finished_at_unix_milli"`},
	})
	assertHeterociRoutingStructLayout(t, reflect.TypeFor[heteroci.RoutingDecision](), []heterociRoutingFieldSpec{
		{name: "SchemaVersion", typ: reflect.TypeFor[string](), tag: `json:"schema_version"`},
		{name: "Authority", typ: reflect.TypeFor[string](), tag: `json:"authority"`},
		{name: "SnapshotCapturedAtUnixMilli", typ: reflect.TypeFor[int64](), tag: `json:"snapshot_captured_at_unix_milli"`},
		{name: "PackingPlanDigest", typ: reflect.TypeFor[string](), tag: `json:"packing_plan_digest"`},
		{name: "Assignments", typ: reflect.TypeFor[[]heteroci.RouteAssignment](), tag: `json:"assignments"`},
		{name: "DecisionDigest", typ: reflect.TypeFor[string](), tag: `json:"decision_digest"`},
	})
}

type heterociRoutingFieldSpec struct {
	name string
	typ  reflect.Type
	tag  string
}

func assertHeterociRoutingStructLayout(t *testing.T, got reflect.Type, want []heterociRoutingFieldSpec) {
	t.Helper()
	if got.Kind() != reflect.Struct {
		t.Fatalf("%s kind = %s, want struct", got, got.Kind())
	}
	if got.NumField() != len(want) {
		t.Fatalf("%s field count = %d, want exact %d", got.Name(), got.NumField(), len(want))
	}
	for index, expected := range want {
		field := got.Field(index)
		if field.Name != expected.name || field.Type != expected.typ || string(field.Tag) != expected.tag {
			t.Fatalf("%s field %d = %s %s %q, want %s %s %q", got.Name(), index, field.Name, field.Type, field.Tag, expected.name, expected.typ, expected.tag)
		}
	}
}

// TestHeterociRoutingLocalIdentityChainGuardsFourIdentities exercises the
// production chain from a live NodeSnapshot through assignment digest,
// planned lease reservation, and an exact Start binding.
func TestHeterociRoutingLocalIdentityChainGuardsFourIdentities(t *testing.T) {
	fixture := newHeterociRoutingLeaseFixture(t, false)
	assignment := assertHeterociRoutingDecision(t, fixture, heteroci.ExecutorKindLocalNode)
	assertHeterociRoutingDecisionDigest(t, fixture)
	assertHeterociLocalAssignmentIdentities(t, fixture, assignment)
	reserveAndStartHeterociRoutingLease(t, fixture, assignment, "r9b-local-run")
}

// TestHeterociRoutingECIIdentityIsExplicitlyEmpty guards the ECI branch from
// leaking a local node identity into the cloud overflow lease.
func TestHeterociRoutingECIIdentityIsExplicitlyEmpty(t *testing.T) {
	fixture := newHeterociRoutingLeaseFixture(t, true)
	assignment := assertHeterociRoutingDecision(t, fixture, heteroci.ExecutorKindAliyunECI)
	if assignment.NodeID != "" || assignment.Endpoint != "" || assignment.ProbeIdentity != "" || assignment.ProbeBootID != "" {
		t.Fatalf("ECI assignment carries local identity: %#v", assignment)
	}
	if assignment.ECITier == "" {
		t.Fatal("ECI assignment must carry a fixed resource tier")
	}

	book := heteroci.NewRoutingLeaseBook()
	leases, err := book.Reserve("r9b-eci-run", fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.heartbeatTTL)
	if err != nil {
		t.Fatalf("ECI RoutingLeaseBook.Reserve() = %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("ECI planned lease count = %d, want 1", len(leases))
	}
	assertHeterociRoutingLeaseBinding(t, leases[0], assignment, heteroci.LeaseStatePlanned)
	started, err := book.Start("r9b-eci-run", assignment.UnitID, fixture.snapshot, fixture.now.Add(time.Second))
	if err != nil {
		t.Fatalf("ECI RoutingLeaseBook.Start() = %v", err)
	}
	assertHeterociRoutingLeaseBinding(t, started, assignment, heteroci.LeaseStateStarted)
}

type heterociRoutingLeaseFixture struct {
	manifest     heteroci.Manifest
	snapshot     heteroci.SnapshotSet
	units        []heteroci.ExecutionUnit
	plan         heteroci.PackingPlan
	decision     heteroci.RoutingDecision
	now          time.Time
	heartbeatTTL time.Duration
}

func assertHeterociRoutingDecisionDigest(t *testing.T, fixture heterociRoutingLeaseFixture) {
	t.Helper()
	canonicalDigest, err := fixture.decision.CanonicalDigest()
	if err != nil {
		t.Fatalf("RoutingDecision.CanonicalDigest() = %v", err)
	}
	if fixture.decision.DecisionDigest != canonicalDigest {
		t.Fatalf("decision digest = %q, want canonical %q", fixture.decision.DecisionDigest, canonicalDigest)
	}
}

func assertHeterociLocalAssignmentIdentities(t *testing.T, fixture heterociRoutingLeaseFixture, assignment heteroci.RouteAssignment) {
	t.Helper()
	node := fixture.snapshot.Nodes[0]
	assertHeterociStringEqual(t, "local node_id", assignment.NodeID, node.NodeID)
	assertHeterociStringEqual(t, "local endpoint", assignment.Endpoint, node.Endpoint)
	assertHeterociStringEqual(t, "local probe_identity", assignment.ProbeIdentity, node.ProbeIdentity)
	assertHeterociStringEqual(t, "local probe_boot_id", assignment.ProbeBootID, node.ProbeBootID)
	if assignment.ECITier != "" {
		t.Fatalf("local assignment carries ECI tier %q", assignment.ECITier)
	}
}

func reserveAndStartHeterociRoutingLease(t *testing.T, fixture heterociRoutingLeaseFixture, assignment heteroci.RouteAssignment, runID string) {
	t.Helper()
	book := heteroci.NewRoutingLeaseBook()
	leases, err := book.Reserve(runID, fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.heartbeatTTL)
	if err != nil {
		t.Fatalf("RoutingLeaseBook.Reserve() = %v", err)
	}
	if len(leases) != 1 {
		t.Fatalf("planned lease count = %d, want 1", len(leases))
	}
	assertHeterociRoutingLeaseBinding(t, leases[0], assignment, heteroci.LeaseStatePlanned)
	started, err := book.Start(runID, assignment.UnitID, fixture.snapshot, fixture.now.Add(time.Second))
	if err != nil {
		t.Fatalf("RoutingLeaseBook.Start() = %v", err)
	}
	assertHeterociRoutingLeaseBinding(t, started, assignment, heteroci.LeaseStateStarted)
}

func newHeterociRoutingLeaseFixture(t *testing.T, eci bool) heterociRoutingLeaseFixture {
	t.Helper()
	now := time.UnixMilli(1_700_000_000_000)
	const nodeID = "r9b-local-node"
	const endpoint = "https://r9b-local-node.invalid/runner"
	const probeIdentity = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const imageDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	manifest := heteroci.Manifest{
		SchemaVersion: heteroci.ManifestSchemaVersion,
		Authority:     heteroci.AuthorityLocalNonAuthoritative,
		CloudOverflow: heteroci.CloudOverflowConfig{Enabled: true, Provider: heteroci.CloudProviderAliyunECI, Platform: heteroci.PlatformLinuxAMD64},
		Nodes: []heteroci.NodeConfig{{
			NodeID: nodeID, Endpoint: endpoint, ProbeIdentity: probeIdentity, Platform: heteroci.PlatformLinuxAMD64,
			Capabilities: []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeUbuntuContainer, Image: "ubuntu:22.04", ImageDigest: imageDigest}},
		}},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("routing fixture manifest.Validate() = %v", err)
	}
	snapshot := heteroci.SnapshotSet{
		SchemaVersion:       heteroci.SnapshotSchemaVersion,
		Authority:           heteroci.AuthorityLocalNonAuthoritative,
		OwnerEpoch:          heteroci.RoutingOwnerEpoch(1),
		CapacityBasis:       heteroci.SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations,
		CapturedAtUnixMilli: now.UnixMilli(),
		Nodes: []heteroci.NodeSnapshot{{
			NodeID: nodeID, Endpoint: endpoint, Platform: heteroci.PlatformLinuxAMD64,
			ProbeIdentity: probeIdentity, ProbeBootID: "r9b-boot-1",
			ActiveCapabilities: []heteroci.ActiveCapabilitySnapshot{{Runtime: heteroci.RuntimeUbuntuContainer, ImageDigest: imageDigest}},
			Status:             heteroci.NodeStatusLive, HeartbeatUnixMilli: now.UnixMilli(),
			Total:     heteroci.ResourceCapacity{CPUMilli: 2_000, MemoryBytes: 4 * 1024 * 1024 * 1024, MaxConcurrency: 1},
			Available: heteroci.ResourceCapacity{CPUMilli: 2_000, MemoryBytes: 4 * 1024 * 1024 * 1024, MaxConcurrency: 1},
		}},
	}
	if eci {
		snapshot.Nodes[0].Available = heteroci.ResourceCapacity{}
	}
	units := []heteroci.ExecutionUnit{{
		UnitID: "r9b-unit", TargetPlatform: heteroci.PlatformLinuxAMD64, RequiredRuntime: heteroci.RuntimeUbuntuContainer,
		EstimatedDurationMS: 100, CPUMilli: 500, MemoryBytes: 256 * 1024 * 1024,
	}}
	const heartbeatTTL = 5 * time.Minute
	plan, decision, err := heteroci.PlanRoutingDecision(manifest, snapshot, units, now, heartbeatTTL)
	if err != nil {
		t.Fatalf("PlanRoutingDecision(eci=%t) = %v", eci, err)
	}
	return heterociRoutingLeaseFixture{manifest: manifest, snapshot: snapshot, units: units, plan: plan, decision: decision, now: now, heartbeatTTL: heartbeatTTL}
}

func assertHeterociRoutingDecision(t *testing.T, fixture heterociRoutingLeaseFixture, executor heteroci.ExecutorKind) heteroci.RouteAssignment {
	t.Helper()
	if err := fixture.decision.ValidateAgainst(fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.heartbeatTTL); err != nil {
		t.Fatalf("RoutingDecision.ValidateAgainst() = %v", err)
	}
	if len(fixture.decision.Assignments) != 1 || fixture.decision.Assignments[0].Executor != executor {
		t.Fatalf("routing decision assignments = %#v, want one %q assignment", fixture.decision.Assignments, executor)
	}
	return fixture.decision.Assignments[0]
}

func assertHeterociRoutingLeaseBinding(t *testing.T, lease heteroci.RoutingLease, assignment heteroci.RouteAssignment, state heteroci.RoutingLeaseState) {
	t.Helper()
	assertHeterociRoutingLeaseEnvelope(t, lease, state)
	assertHeterociRoutingLeaseIdentity(t, lease, assignment)
}

func assertHeterociRoutingLeaseEnvelope(t *testing.T, lease heteroci.RoutingLease, state heteroci.RoutingLeaseState) {
	t.Helper()
	assertHeterociStringEqual(t, "lease schema", lease.SchemaVersion, heteroci.RoutingLeaseSchemaVersion)
	assertHeterociStringEqual(t, "lease authority", lease.Authority, heteroci.AuthorityLocalNonAuthoritative)
	assertHeterociStringEqual(t, "lease state", string(lease.State), string(state))
}

func assertHeterociRoutingLeaseIdentity(t *testing.T, lease heteroci.RoutingLease, assignment heteroci.RouteAssignment) {
	t.Helper()
	assertHeterociStringEqual(t, "lease unit_id", lease.UnitID, assignment.UnitID)
	assertHeterociStringEqual(t, "lease executor", string(lease.Executor), string(assignment.Executor))
	assertHeterociStringEqual(t, "lease node_id", lease.NodeID, assignment.NodeID)
	assertHeterociStringEqual(t, "lease endpoint", lease.Endpoint, assignment.Endpoint)
	assertHeterociStringEqual(t, "lease probe_identity", lease.ProbeIdentity, assignment.ProbeIdentity)
	assertHeterociStringEqual(t, "lease probe_boot_id", lease.ProbeBootID, assignment.ProbeBootID)
	assertHeterociStringEqual(t, "lease ECI tier", string(lease.ECITier), string(assignment.ECITier))
}

func assertHeterociStringEqual(t *testing.T, label, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", label, got, want)
	}
}

// TestHeterociRoutingStartBoundaryUsesSnapshot prevents a caller-supplied
// assignment from bypassing the stored decision and fresh snapshot checks.
func TestHeterociRoutingStartBoundaryUsesSnapshot(t *testing.T) {
	root := findRepoRoot(t)
	directory := filepath.Join(root, "internal", "devtools", "heteroci")
	sources := heterociRoutingProductionSources(t, directory)
	assertHeterociRoutingStartBoundary(t, sources)
}

type heterociRoutingProductionFunction struct {
	source heterociRoutingProductionSource
	decl   *ast.FuncDecl
}

func assertHeterociRoutingStartBoundary(t *testing.T, sources []heterociRoutingProductionSource) {
	t.Helper()
	starts := heterociRoutingStartFunctions(sources)
	if len(starts) != 1 {
		t.Fatalf("heteroci production RoutingLeaseBook.Start methods = %d, want exactly one", len(starts))
	}
	start := starts[0].decl
	snapshotParams, routeAssignmentParams := heterociRoutingStartParameterKinds(start)
	if snapshotParams != 1 {
		t.Fatalf("RoutingLeaseBook.Start SnapshotSet parameters = %d, want exactly one", snapshotParams)
	}
	if routeAssignmentParams != 0 {
		t.Fatalf("RoutingLeaseBook.Start accepts RouteAssignment parameter")
	}
	functions := heterociRoutingProductionFunctions(sources)
	hasSnapshotValidation, hasStoredDecision := heterociRoutingStartPathGuards(start, functions)
	if !hasSnapshotValidation {
		t.Fatal("RoutingLeaseBook.Start path does not validate a SnapshotSet against the current manifest/time")
	}
	if !hasStoredDecision {
		t.Fatal("RoutingLeaseBook.Start path does not bind the lease from the stored decision")
	}
}

func heterociRoutingStartFunctions(sources []heterociRoutingProductionSource) []heterociRoutingProductionFunction {
	functions := make([]heterociRoutingProductionFunction, 0, 1)
	for _, source := range sources {
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != "Start" || !heterociRoutingReceiverIsLeaseBook(function) {
				continue
			}
			functions = append(functions, heterociRoutingProductionFunction{source: source, decl: function})
		}
	}
	return functions
}

func heterociRoutingReceiverIsLeaseBook(function *ast.FuncDecl) bool {
	if function.Recv == nil || len(function.Recv.List) != 1 {
		return false
	}
	receiver := function.Recv.List[0].Type
	if pointer, ok := receiver.(*ast.StarExpr); ok {
		receiver = pointer.X
	}
	return heterociRoutingIsType(receiver, "RoutingLeaseBook")
}

func heterociRoutingStartParameterKinds(function *ast.FuncDecl) (int, int) {
	snapshotParams := 0
	routeAssignmentParams := 0
	if function.Type.Params == nil {
		return snapshotParams, routeAssignmentParams
	}
	for _, parameter := range function.Type.Params.List {
		if heterociRoutingIsType(parameter.Type, "SnapshotSet") {
			snapshotParams += len(parameter.Names)
			if len(parameter.Names) == 0 {
				snapshotParams++
			}
		}
		if heterociRoutingTypeContains(parameter.Type, "RouteAssignment") {
			routeAssignmentParams += len(parameter.Names)
			if len(parameter.Names) == 0 {
				routeAssignmentParams++
			}
		}
	}
	return snapshotParams, routeAssignmentParams
}

func heterociRoutingTypeContains(expression ast.Expr, want string) bool {
	switch expression := expression.(type) {
	case *ast.StarExpr:
		return heterociRoutingTypeContains(expression.X, want)
	case *ast.ArrayType:
		return heterociRoutingTypeContains(expression.Elt, want)
	default:
		return heterociRoutingIsType(expression, want)
	}
}

func heterociRoutingProductionFunctions(sources []heterociRoutingProductionSource) map[string][]heterociRoutingProductionFunction {
	functions := make(map[string][]heterociRoutingProductionFunction)
	for _, source := range sources {
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			name := function.Name.Name
			functions[name] = append(functions[name], heterociRoutingProductionFunction{source: source, decl: function})
		}
	}
	return functions
}

func heterociRoutingStartPathGuards(start *ast.FuncDecl, functions map[string][]heterociRoutingProductionFunction) (bool, bool) {
	queue := []*ast.FuncDecl{start}
	seen := map[*ast.FuncDecl]bool{}
	hasSnapshotValidation := false
	hasStoredDecision := false
	for len(queue) != 0 {
		function := queue[0]
		queue = queue[1:]
		if seen[function] {
			continue
		}
		seen[function] = true
		hasSnapshotValidation = hasSnapshotValidation || heterociRoutingFunctionValidatesSnapshot(function)
		hasStoredDecision = hasStoredDecision || heterociRoutingFunctionUsesStoredDecision(function)
		queue = append(queue, heterociRoutingCalledFunctions(function, functions)...)
	}
	return hasSnapshotValidation, hasStoredDecision
}

func heterociRoutingCalledFunctions(function *ast.FuncDecl, functions map[string][]heterociRoutingProductionFunction) []*ast.FuncDecl {
	called := make([]*ast.FuncDecl, 0)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		name := ident.Name
		for _, candidate := range functions[name] {
			called = append(called, candidate.decl)
		}
		return true
	})
	return called
}

func heterociRoutingFunctionValidatesSnapshot(function *ast.FuncDecl) bool {
	snapshotParams := heterociRoutingSnapshotParameterNames(function)
	if len(snapshotParams) == 0 {
		return false
	}
	validates := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "ValidateAgainst" {
			return true
		}
		base, ok := selector.X.(*ast.Ident)
		validates = ok && snapshotParams[base.Name]
		return !validates
	})
	return validates
}

func heterociRoutingSnapshotParameterNames(function *ast.FuncDecl) map[string]bool {
	names := make(map[string]bool)
	if function.Recv != nil {
		for _, receiver := range function.Recv.List {
			if !heterociRoutingIsType(receiver.Type, "SnapshotSet") {
				continue
			}
			for _, name := range receiver.Names {
				names[name.Name] = true
			}
		}
	}
	if function.Type.Params == nil {
		return names
	}
	for _, parameter := range function.Type.Params.List {
		if !heterociRoutingIsType(parameter.Type, "SnapshotSet") {
			continue
		}
		for _, name := range parameter.Names {
			names[name.Name] = true
		}
	}
	return names
}

func heterociRoutingFunctionUsesStoredDecision(function *ast.FuncDecl) bool {
	usesDecision := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Assignments" {
			return true
		}
		decision, ok := selector.X.(*ast.SelectorExpr)
		usesDecision = ok && decision.Sel.Name == "decision"
		return !usesDecision
	})
	return usesDecision
}

// TestHeterociRoutingHasSingleBuilderAndNoFallback locks the production
// construction and call boundaries for the single routing decision chain.
func TestHeterociRoutingHasSingleBuilderAndNoFallback(t *testing.T) {
	root := findRepoRoot(t)
	directory := filepath.Join(root, "internal", "devtools", "heteroci")
	sources := heterociRoutingProductionSources(t, directory)
	assertHeterociRoutingDecisionBuilder(t, sources)
	assertHeterociRoutingCallBoundary(t, sources, "BuildRoutingDecision", "router.go", "PlanRoutingDecision")
	assertHeterociRoutingCallBoundary(t, sources, "PlanRoutingDecision", "routing_lease_book.go", "planAndReserveLocked")
	assertHeterociRoutingConstructionBoundary(t, sources, "RouteAssignment", map[string]bool{
		"router.go:routingLocalAssignments": true,
		"router.go:routeUnassignedUnit":     true,
	})
	assertHeterociRoutingConstructionBoundary(t, sources, "RoutingLease", map[string]bool{
		"routing_lease_book.go:buildPlannedRoutingLeases": true,
	})
}

func heterociRoutingProductionGoPaths(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read heteroci production directory: %v", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		paths = append(paths, filepath.Join(directory, entry.Name()))
	}
	return paths
}

type heterociRoutingProductionSource struct {
	path    string
	file    *ast.File
	fileSet *token.FileSet
}

type heterociRoutingProductionSite struct {
	path     string
	function string
	line     int
	schema   string
}

func heterociRoutingProductionSources(t *testing.T, directory string) []heterociRoutingProductionSource {
	t.Helper()
	paths := heterociRoutingProductionGoPaths(t, directory)
	sources := make([]heterociRoutingProductionSource, 0, len(paths))
	for _, path := range paths {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read heteroci source %s: %v", filepath.Base(path), err)
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, source, 0)
		if err != nil {
			t.Fatalf("parse heteroci source %s: %v", filepath.Base(path), err)
		}
		sources = append(sources, heterociRoutingProductionSource{path: path, file: file, fileSet: fileSet})
	}
	return sources
}

func assertHeterociRoutingDecisionBuilder(t *testing.T, sources []heterociRoutingProductionSource) {
	t.Helper()
	sites := make([]heterociRoutingProductionSite, 0)
	for _, source := range sources {
		sites = append(sites, heterociRoutingDecisionSites(source)...)
	}
	if len(sites) != 1 {
		t.Fatalf("heteroci production non-empty RoutingDecision constructions = %s, want exactly one", heterociRoutingProductionSiteSummary(sites))
	}
	site := sites[0]
	if filepath.Base(site.path) != "router.go" || site.function != "BuildRoutingDecision" || site.schema != "RoutingDecisionSchemaVersion" {
		t.Fatalf("routing decision construction = %s, want router.go:BuildRoutingDecision with RoutingDecisionSchemaVersion", heterociRoutingProductionSiteSummary(sites))
	}
}

func heterociRoutingDecisionSites(source heterociRoutingProductionSource) []heterociRoutingProductionSite {
	sites := make([]heterociRoutingProductionSite, 0)
	for _, declaration := range source.file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		variables := heterociRoutingDecisionVariables(function)
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.CompositeLit:
				if heterociRoutingIsType(node.Type, "RoutingDecision") && len(node.Elts) != 0 {
					sites = append(sites, heterociRoutingProductionSite{path: source.path, function: function.Name.Name, line: source.fileSet.Position(node.Pos()).Line, schema: heterociRoutingSchemaValue(node)})
				}
			case *ast.AssignStmt:
				if schema, ok := heterociRoutingSchemaAssignment(node, variables); ok {
					sites = append(sites, heterociRoutingProductionSite{path: source.path, function: function.Name.Name, line: source.fileSet.Position(node.Pos()).Line, schema: schema})
				}
			}
			return true
		})
	}
	return sites
}

func heterociRoutingDecisionVariables(function *ast.FuncDecl) map[string]bool {
	variables := make(map[string]bool)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.ValueSpec:
			for index, name := range node.Names {
				if heterociRoutingValueSpecIsDecision(node, index) {
					variables[name.Name] = true
				}
			}
		case *ast.AssignStmt:
			for index, left := range node.Lhs {
				ident, ok := left.(*ast.Ident)
				if ok && index < len(node.Rhs) && heterociRoutingIsDecisionComposite(node.Rhs[index]) {
					variables[ident.Name] = true
				}
			}
		}
		return true
	})
	return variables
}

func heterociRoutingValueSpecIsDecision(spec *ast.ValueSpec, index int) bool {
	if heterociRoutingIsType(spec.Type, "RoutingDecision") {
		return true
	}
	return index < len(spec.Values) && heterociRoutingIsDecisionComposite(spec.Values[index])
}

func heterociRoutingIsDecisionComposite(expression ast.Expr) bool {
	literal, ok := expression.(*ast.CompositeLit)
	return ok && heterociRoutingIsType(literal.Type, "RoutingDecision")
}

func heterociRoutingSchemaAssignment(statement *ast.AssignStmt, variables map[string]bool) (string, bool) {
	for index, left := range statement.Lhs {
		selector, ok := left.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "SchemaVersion" {
			continue
		}
		base, ok := selector.X.(*ast.Ident)
		if !ok || !variables[base.Name] || index >= len(statement.Rhs) {
			continue
		}
		return heterociRoutingExprName(statement.Rhs[index]), true
	}
	return "", false
}

func heterociRoutingSchemaValue(literal *ast.CompositeLit) string {
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := keyValue.Key.(*ast.Ident)
		if ok && key.Name == "SchemaVersion" {
			return heterociRoutingExprName(keyValue.Value)
		}
	}
	return ""
}

func heterociRoutingExprName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		return expression.Sel.Name
	case *ast.BasicLit:
		return expression.Value
	default:
		return ""
	}
}

func heterociRoutingIsType(expression ast.Expr, want string) bool {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name == want
	case *ast.SelectorExpr:
		return expression.Sel.Name == want
	default:
		return false
	}
}

func heterociRoutingProductionSiteSummary(sites []heterociRoutingProductionSite) string {
	if len(sites) == 0 {
		return "<none>"
	}
	parts := make([]string, 0, len(sites))
	for _, site := range sites {
		parts = append(parts, filepath.Base(site.path)+":"+site.function+":"+site.schema)
	}
	return strings.Join(parts, ", ")
}

func assertHeterociRoutingCallBoundary(t *testing.T, sources []heterociRoutingProductionSource, callee, wantFile, wantFunction string) {
	t.Helper()
	sites := make([]heterociRoutingProductionSite, 0)
	for _, source := range sources {
		sites = append(sites, heterociRoutingCallSites(source, callee)...)
	}
	if len(sites) != 1 {
		t.Fatalf("heteroci production %s call sites = %s, want exactly one", callee, heterociRoutingProductionSiteSummary(sites))
	}
	site := sites[0]
	if filepath.Base(site.path) != wantFile || site.function != wantFunction {
		t.Fatalf("heteroci production %s call site = %s, want %s:%s", callee, heterociRoutingProductionSiteSummary(sites), wantFile, wantFunction)
	}
}

func heterociRoutingCallSites(source heterociRoutingProductionSource, callee string) []heterociRoutingProductionSite {
	sites := make([]heterociRoutingProductionSite, 0)
	for _, declaration := range source.file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if ok && heterociRoutingExprName(call.Fun) == callee {
				sites = append(sites, heterociRoutingProductionSite{path: source.path, function: function.Name.Name, line: source.fileSet.Position(call.Pos()).Line})
			}
			return true
		})
	}
	return sites
}

func assertHeterociRoutingConstructionBoundary(t *testing.T, sources []heterociRoutingProductionSource, typeName string, allowed map[string]bool) {
	t.Helper()
	sites := make([]heterociRoutingProductionSite, 0)
	for _, source := range sources {
		sites = append(sites, heterociRoutingConstructionSites(source, typeName)...)
	}
	for _, site := range sites {
		key := filepath.Base(site.path) + ":" + site.function
		if !allowed[key] {
			t.Errorf("heteroci production %s construction bypasses its owner at %s:%d", typeName, key, site.line)
		}
	}
}

func heterociRoutingConstructionSites(source heterociRoutingProductionSource, typeName string) []heterociRoutingProductionSite {
	sites := make([]heterociRoutingProductionSite, 0)
	for _, declaration := range source.file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if ok && len(literal.Elts) != 0 && heterociRoutingIsType(literal.Type, typeName) {
				sites = append(sites, heterociRoutingProductionSite{path: source.path, function: function.Name.Name, line: source.fileSet.Position(literal.Pos()).Line})
			}
			return true
		})
	}
	return sites
}

// TestHeterociRoutingV1IsStrictlyRejected verifies no compatibility read path
// accepts either retired v1 schema after the v2 decision/lease chain exists.
func TestHeterociRoutingV1IsStrictlyRejected(t *testing.T) {
	fixture := newHeterociRoutingLeaseFixture(t, false)
	legacyDecision := fixture.decision
	legacyDecision.SchemaVersion = "heteroci-routing-decision/v1"
	if err := legacyDecision.ValidateAgainst(fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.heartbeatTTL); err == nil {
		t.Fatal("RoutingDecision.ValidateAgainst accepted retired v1 schema")
	}
	book := heteroci.NewRoutingLeaseBook()
	if _, err := book.Reserve("r9b-legacy-decision", legacyDecision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.heartbeatTTL); err == nil {
		t.Fatal("RoutingLeaseBook.Reserve accepted a retired v1 decision")
	}
	leases, err := book.Reserve("r9b-valid-lease", fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.heartbeatTTL)
	if err != nil {
		t.Fatalf("reserve valid v2 lease: %v", err)
	}
	legacyLease := leases[0]
	legacyLease.SchemaVersion = "heteroci-routing-lease/v1"
	if err := legacyLease.ValidateAgainst(fixture.decision, fixture.now.Add(time.Second)); err == nil {
		t.Fatal("RoutingLease.ValidateAgainst accepted retired v1 schema")
	}
}
