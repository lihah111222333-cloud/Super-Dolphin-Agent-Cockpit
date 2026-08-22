package heterociowner

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

func newPostgreSQLBindingMutationFixture(t *testing.T) (*PostgreSQLOwner, *postgresqlOwnerTestState, heteroci.ExecutorBindingMutation) {
	t.Helper()
	database, state := openPostgreSQLOwnerTestDB(t)
	now := time.UnixMilli(durableRoutingNowUnixMilli)
	owner, err := NewPostgreSQLOwnerWithClock(context.Background(), database, validOwnerScope(), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewPostgreSQLOwnerWithClock: %v", err)
	}
	fence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	session, err := heteroci.NewTopologySession(fence)
	if err != nil {
		t.Fatalf("NewTopologySession: %v", err)
	}
	request := postgresqlPlanRequestFixture(t, fence, now)
	planned, err := owner.PlanAndReserve(context.Background(), session, request)
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	plan := postgreSQLBindingPlanFixture(t, request, planned)
	return owner, state, heteroci.ExecutorBindingMutation{Fence: planned.Fence, Plan: plan}
}
func postgreSQLBindingPlanFixture(t *testing.T, request heteroci.TopologyPlanRequest, planned heteroci.TopologyReservedPlan) heteroci.ExecutorBindingPlan {
	t.Helper()
	topologyManifestDigest, err := heteroci.DeriveTopologyManifestDigest(request.Manifest)
	if err != nil {
		t.Fatalf("DeriveTopologyManifestDigest: %v", err)
	}
	planDigest, err := heteroci.DeriveTopologyReservedPlanDigest(planned)
	if err != nil {
		t.Fatalf("DeriveTopologyReservedPlanDigest: %v", err)
	}
	bindings := make([]heteroci.ExecutorBinding, 0, len(planned.Plan.NodeShards))
	for _, shard := range planned.Plan.NodeShards {
		batchIDs := make([]string, 0, len(shard.Waves))
		unitIDs := make([]string, 0)
		for _, wave := range shard.Waves {
			batchID, err := heteroci.DeriveExecutorBindingBatchID(string(request.RunIdentity.DurableRunID), shard.NodeID, wave.Wave, wave.UnitIDs)
			if err != nil {
				t.Fatalf("DeriveExecutorBindingBatchID: %v", err)
			}
			batchIDs = append(batchIDs, batchID)
			unitIDs = append(unitIDs, wave.UnitIDs...)
		}
		sort.Strings(batchIDs)
		sort.Strings(unitIDs)
		node, ok := findBindingNodeTest(planned.EffectiveSnapshot, shard.NodeID)
		if !ok {
			t.Fatalf("node facts missing for %s", shard.NodeID)
		}
		shardDigest, _ := heteroci.DeriveExecutorBindingValueDigest("heteroci-workflow-executor-shard/v1", []heteroci.NodeShard{shard})
		batchDigest, _ := heteroci.DeriveExecutorBindingValueDigest("heteroci-workflow-executor-batch-set/v1", batchIDs)
		unitDigest, _ := heteroci.DeriveExecutorBindingValueDigest("heteroci-workflow-executor-unit-set/v1", unitIDs)
		nodeDigest, _ := heteroci.DeriveExecutorBindingValueDigest("heteroci-workflow-executor-node-facts/v1", node)
		nodeIDDigest, _ := heteroci.DeriveExecutorBindingNodeIDDigest(shard.NodeID, heteroci.ExecutorKindLocalNode)
		bindings = append(bindings, heteroci.ExecutorBinding{NodeID: shard.NodeID, NodeIDDigest: nodeIDDigest, ExecutorKind: heteroci.ExecutorKindLocalNode, BatchIDs: batchIDs, ShardDigest: shardDigest, BatchSetDigest: batchDigest, UnitSetDigest: unitDigest, NodeFactsDigest: nodeDigest})
	}
	plan := heteroci.ExecutorBindingPlan{SchemaVersion: "remote-ci-executor-binding/v1", ExternalOperationID: request.ExternalOperationID, DurableRunID: request.RunIdentity.DurableRunID, RequestDigest: request.RequestDigest, ManifestDigest: request.ManifestDigest, TopologyManifestDigest: topologyManifestDigest, PlanDigest: planDigest, AuthorityGeneration: planned.Fence.OwnerEpoch, Bindings: bindings}
	plan.BindingDigest, err = heteroci.DeriveExecutorBindingAggregateDigest(plan)
	if err != nil {
		t.Fatalf("DeriveExecutorBindingAggregateDigest: %v", err)
	}
	return plan
}
func findBindingNodeTest(snapshot heteroci.SnapshotSet, nodeID string) (heteroci.NodeSnapshot, bool) {
	for _, node := range snapshot.Nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return heteroci.NodeSnapshot{}, false
}
func TestPostgreSQLOwnerBindExecutorsExactReplayAndFence(t *testing.T) {
	owner, state, mutation := newPostgreSQLBindingMutationFixture(t)
	first, err := owner.BindExecutors(context.Background(), mutation)
	if err != nil {
		t.Fatalf("BindExecutors: %v", err)
	}
	replayed, err := owner.BindExecutors(context.Background(), mutation)
	if err != nil {
		t.Fatalf("BindExecutors replay: %v", err)
	}
	if !reflect.DeepEqual(first, replayed) || first.Fence.Revision != mutation.Fence.Revision+1 {
		t.Fatalf("binding replay/fence = %#v / %#v", first, replayed)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if got := fmt.Sprint(state.data.operations[string(mutation.Plan.ExternalOperationID)][3]); got != string(heterociExecutorBindingKindForTest) {
		t.Fatalf("binding journal kind = %q", got)
	}
}

const heterociExecutorBindingKindForTest = "executor_bind/v1"

func TestPostgreSQLOwnerBindExecutorsCommitUncertaintyReadbackAndConflict(t *testing.T) {
	owner, state, mutation := newPostgreSQLBindingMutationFixture(t)
	state.mu.Lock()
	state.commitAppliedError = errors.New("network dropped after binding commit")
	state.mu.Unlock()
	first, err := owner.BindExecutors(context.Background(), mutation)
	if err != nil {
		t.Fatalf("BindExecutors ambiguous commit: %v", err)
	}
	if first.Plan.BindingDigest != mutation.Plan.BindingDigest {
		t.Fatalf("ambiguous commit binding digest = %q", first.Plan.BindingDigest)
	}
	conflict := mutation
	conflict.Plan.ManifestDigest = "sha256:" + strings.Repeat("8", 64)
	if _, err := owner.BindExecutors(context.Background(), conflict); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("binding conflict error = %v, want ErrOperationConflict", err)
	}
}
func TestPostgreSQLOwnerBindExecutorsRejectsPlanJournalTamperAndNodeFacts(t *testing.T) {
	owner, state, mutation := newPostgreSQLBindingMutationFixture(t)
	planOperationID := mutationPlanOperationIDForBindingTest(t, mutation.Plan.DurableRunID)
	state.mu.Lock()
	operation := state.data.operations[string(planOperationID)]
	operation[6] = []byte("tampered")
	state.data.operations[string(planOperationID)] = operation
	state.mu.Unlock()
	if _, err := owner.BindExecutors(context.Background(), mutation); err == nil {
		t.Fatal("BindExecutors accepted tampered persisted plan journal")
	}
	owner, _, mutation = newPostgreSQLBindingMutationFixture(t)
	mutation.Plan.Bindings[0].NodeFactsDigest = "sha256:" + strings.Repeat("7", 64)
	if _, err := owner.BindExecutors(context.Background(), mutation); err == nil {
		t.Fatal("BindExecutors accepted tampered node facts digest")
	}
}
func TestPostgreSQLOwnerBindExecutorsRejectsRequestAuditTamper(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*heteroci.ExecutorBindingPlan)
	}{
		{name: "external operation", mutate: func(plan *heteroci.ExecutorBindingPlan) {
			plan.ExternalOperationID = heteroci.OperationID(strings.Repeat("d", 64))
		}},
		{name: "request digest", mutate: func(plan *heteroci.ExecutorBindingPlan) { plan.RequestDigest = "sha256:" + strings.Repeat("8", 64) }},
		{name: "portable manifest digest", mutate: func(plan *heteroci.ExecutorBindingPlan) { plan.ManifestDigest = "sha256:" + strings.Repeat("8", 64) }},
		{name: "topology manifest digest", mutate: func(plan *heteroci.ExecutorBindingPlan) {
			plan.TopologyManifestDigest = "sha256:" + strings.Repeat("8", 64)
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			owner, state, mutation := newPostgreSQLBindingMutationFixture(t)
			before := mutation.Fence.Revision
			test.mutate(&mutation.Plan)
			if _, err := owner.BindExecutors(context.Background(), mutation); err == nil {
				t.Fatal("BindExecutors accepted tampered plan request audit")
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if got := state.data.head[2]; fmt.Sprint(got) != fmt.Sprint(before) {
				t.Fatalf("binding fence advanced after audit tamper: got=%v want=%v", got, before)
			}
		})
	}
}

func mutationPlanOperationIDForBindingTest(t *testing.T, runID heteroci.TopologyRunID) heteroci.OperationID {
	t.Helper()
	operationID, err := heteroci.DeriveTopologyOperationID(runID, heteroci.TopologyOperationPhasePlan, "")
	if err != nil {
		t.Fatalf("DeriveTopologyOperationID: %v", err)
	}
	return operationID
}

type postgreSQLBindingSQLCall struct {
	text string
	args int
}

func TestPostgreSQLBindingSQLPlaceholdersAreContiguous(t *testing.T) {
	t.Helper()
	const sourcePath = "postgresql_owner_binding.go"
	parsed, err := parser.ParseFile(token.NewFileSet(), sourcePath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", sourcePath, err)
	}
	calls, err := collectPostgreSQLBindingSQLCalls(parsed)
	if err != nil {
		t.Fatalf("collect binding SQL: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("binding SQL ExecContext call count = %d, want 3", len(calls))
	}
	for _, call := range calls {
		assertPostgreSQLBindingSQLShape(t, call.text, call.args)
	}
}

func collectPostgreSQLBindingSQLCalls(parsed *ast.File) ([]postgreSQLBindingSQLCall, error) {
	calls := make([]postgreSQLBindingSQLCall, 0, 3)
	var parseErr error
	ast.Inspect(parsed, func(node ast.Node) bool {
		call, matched, err := parsePostgreSQLBindingSQLCall(node)
		if err != nil {
			parseErr = err
			return false
		}
		if matched {
			calls = append(calls, call)
		}
		return true
	})
	return calls, parseErr
}

func parsePostgreSQLBindingSQLCall(node ast.Node) (postgreSQLBindingSQLCall, bool, error) {
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return postgreSQLBindingSQLCall{}, false, nil
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "ExecContext" || len(call.Args) < 2 {
		return postgreSQLBindingSQLCall{}, false, nil
	}
	literal, ok := call.Args[1].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return postgreSQLBindingSQLCall{}, false, nil
	}
	sqlText, err := strconv.Unquote(literal.Value)
	if err != nil {
		return postgreSQLBindingSQLCall{}, false, err
	}
	return postgreSQLBindingSQLCall{text: sqlText, args: len(call.Args) - 2}, true, nil
}

func assertPostgreSQLBindingSQLShape(t *testing.T, sqlText string, argCount int) {
	t.Helper()
	if strings.Contains(sqlText, "VALUES (,") || strings.Contains(sqlText, "=  WHERE") {
		t.Fatalf("malformed PostgreSQL binding SQL: %q", sqlText)
	}
	matches := regexp.MustCompile(`\$([0-9]+)`).FindAllStringSubmatch(sqlText, -1)
	if len(matches) == 0 || len(matches) != argCount {
		t.Fatalf("SQL placeholder/argument count mismatch: placeholders=%d args=%d SQL=%q", len(matches), argCount, sqlText)
	}
	seen, max, err := postgreSQLBindingPlaceholderSet(matches)
	if err != nil {
		t.Fatal(err)
	}
	if max != argCount || len(seen) != argCount {
		t.Fatalf("SQL placeholders are not contiguous: max=%d args=%d seen=%v SQL=%q", max, argCount, seen, sqlText)
	}
	for index := 1; index <= max; index++ {
		if _, ok := seen[index]; !ok {
			t.Fatalf("SQL placeholder $%d is missing: %q", index, sqlText)
		}
	}
}

func postgreSQLBindingPlaceholderSet(matches [][]string) (map[int]struct{}, int, error) {
	seen := make(map[int]struct{}, len(matches))
	max := 0
	for _, match := range matches {
		index, err := strconv.Atoi(match[1])
		if err != nil || index <= 0 {
			return nil, 0, fmt.Errorf("invalid SQL placeholder %q", match[0])
		}
		seen[index] = struct{}{}
		if index > max {
			max = index
		}
	}
	return seen, max, nil
}
