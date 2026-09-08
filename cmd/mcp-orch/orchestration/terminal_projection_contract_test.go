package orchestration

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestTerminalProjectionKindCoverage 动态枚举contract允许类别和生产映射分支，拒绝新增遗漏或陈旧映射。
func TestTerminalProjectionKindCoverage(t *testing.T) {
	producer := terminalProjectionCases(t, "../../../internal/contract/terminal_outcome.go", "ProjectionKind")
	consumer := terminalProjectionCases(t, "terminal_projection_lifecycle.go", "projectionKind")
	if !reflect.DeepEqual(producer, consumer) {
		t.Fatalf("projection kinds differ: contract=%v lifecycle=%v", producer, consumer)
	}
	for kind := range producer {
		if _, err := terminalProjectionRuntimeState(kind); err != nil {
			t.Fatalf("registered projection %q rejected: %v", kind, err)
		}
	}
}

// terminalProjectionCases 从真实源码switch动态读取允许集合；无法枚举时立即失败。
func terminalProjectionCases(t *testing.T, path, tag string) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		stmt, ok := node.(*ast.SwitchStmt)
		if !ok || terminalProjectionSwitchTag(stmt.Tag) != tag {
			return true
		}
		collectTerminalProjectionCases(t, stmt, values)
		return false
	})
	if len(values) == 0 {
		t.Fatalf("no projection cases found in %s", path)
	}
	return values
}

// terminalProjectionSwitchTag 识别字段校验或纯映射函数中的switch目标。
func terminalProjectionSwitchTag(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.SelectorExpr:
		return value.Sel.Name
	case *ast.Ident:
		return value.Name
	default:
		return ""
	}
}

// collectTerminalProjectionCases 拒绝非字符串case，避免无法解析时静默缩小守卫范围。
func collectTerminalProjectionCases(t *testing.T, stmt *ast.SwitchStmt, values map[string]bool) {
	t.Helper()
	for _, entry := range stmt.Body.List {
		clause, ok := entry.(*ast.CaseClause)
		if !ok {
			t.Fatal("projection switch contains non-case statement")
		}
		for _, expr := range clause.List {
			literal, ok := expr.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				t.Fatalf("unhandled projection case %T", expr)
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			values[value] = true
		}
	}
}

type unknownProjectionPort struct {
	contract.TerminalOutcomeCommitPort
}

// GetPublicTerminalOutcome 模拟已配置端口违规返回未知类别，确保公开读端不静默改写failed。
func (unknownProjectionPort) GetPublicTerminalOutcome(context.Context, string) (contract.TerminalOutcomeCommit, error) {
	return contract.TerminalOutcomeCommit{ProjectionKind: "unknown_projection", Identity: contract.CanonicalTerminalIdentity{AgentID: "agent-1"}}, nil
}

// TestUnknownTerminalProjectionFailsBeforePublicMutation 验证公开读端、恢复与重放均拒绝未知类别。
func TestUnknownTerminalProjectionFailsBeforePublicMutation(t *testing.T) {
	svc, _ := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	svc.terminalOutcomes = unknownProjectionPort{svc.terminalOutcomes}
	ctx := context.Background()
	commit, err := svc.terminalOutcomes.GetPublicTerminalOutcome(ctx, agent.id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := terminalProjectionAlreadyApplied(agent, commit); err == nil {
		t.Fatal("replay accepted unknown projection")
	}
	if _, err := svc.GetReport(ctx, agent.id); err == nil {
		t.Fatal("GetReport accepted unknown projection")
	}
	if _, err := svc.GetState(ctx, agent.id); err == nil {
		t.Fatal("GetState accepted unknown projection")
	}
	if _, err := svc.Snapshot(ctx, agent.id); err == nil {
		t.Fatal("Snapshot accepted unknown projection")
	}
	assertUnknownProjectionRecoveryRejected(t, svc, agent)
	snapshot := AgentSnapshot{AgentID: agent.id, State: "turn_running", LastReport: "untouched"}
	before := snapshot
	if err := svc.overlayPublicTerminalOutcome(ctx, &snapshot); err == nil {
		t.Fatal("overlay accepted unknown projection")
	}
	if !reflect.DeepEqual(snapshot, before) {
		t.Fatal("unknown projection mutated public snapshot")
	}
}

// assertUnknownProjectionRecoveryRejected 验证恢复投影使用同一严格类别映射且不修改runtime。
func assertUnknownProjectionRecoveryRejected(t *testing.T, svc *service, agent *agentRuntime) {
	t.Helper()
	ctx := context.Background()
	head, err := svc.terminalHeadReader.LoadTerminalOutcomeCurrentHead(ctx, agent.id)
	if err != nil {
		t.Fatal(err)
	}
	before := agent.state
	if err := svc.projectRecoveryTerminalLocked(ctx, agent, head); err == nil {
		t.Fatal("recovery accepted unknown projection")
	}
	if agent.state != before {
		t.Fatal("unknown recovery projection changed runtime state")
	}
}
