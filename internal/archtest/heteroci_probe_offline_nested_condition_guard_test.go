package archtest

import (
	"go/ast"
	"strings"
	"testing"
)

func (guard *heterociProbeSSAGuard) offlineConditionPath(function *ast.FuncDecl, branch *ast.IfStmt) []ast.Expr {
	if function == nil || branch == nil {
		return nil
	}
	path, ok := offlineConditionPathStmt(function.Body, branch, nil)
	if !ok {
		return nil
	}
	return path
}

func offlineConditionPathStmt(statement ast.Stmt, branch *ast.IfStmt, path []ast.Expr) ([]ast.Expr, bool) {
	if current, ok := statement.(*ast.IfStmt); ok {
		path = append(path, current.Cond)
		if current == branch {
			return path, true
		}
		return offlineConditionPathStmt(current.Body, branch, path)
	}
	if block, ok := statement.(*ast.BlockStmt); ok {
		return offlineConditionPathBlock(block, branch, path)
	}
	return nil, false
}

func offlineConditionPathBlock(block *ast.BlockStmt, branch *ast.IfStmt, path []ast.Expr) ([]ast.Expr, bool) {
	for _, statement := range block.List {
		if found, ok := offlineConditionPathStmt(statement, branch, path); ok {
			return found, true
		}
	}
	return nil, false
}

func TestHeterociProbeOfflineNestedConditionPositive(t *testing.T) {
	source := heterociProbeOfflineNestedConditionSource()
	guard, _ := newHeterociProbeSyntheticGuard(t, heterociProbeOfflinePreamble()+source, "")
	if violations := guard.offlineConditionViolations(); len(violations) != 0 {
		t.Fatalf("nested offline condition violations: %s", strings.Join(violations, "; "))
	}
}

func heterociProbeOfflineNestedConditionSource() string {
	return `
		func acquireProbeNode(cancel context.Context, config NodeConfig, wire ProbeTransport, request []byte) (NodeSnapshot, error) {
			body, raw := wire.Probe(cancel, "", request)
			bodyNil := isNilProbeValue(body)
			ctxErr := cancel.Err()
			var state probeExternalErrorSnapshot
			if raw != nil { state = snapshotProbeExternalError(raw) }
			if ctxErr == nil && bodyNil { if state.onlyUnavailable { return offlineProbeSnapshot(config), nil } }
			return NodeSnapshot{}, nil
		}`
}

func TestHeterociProbeOfflineConditionEscapes(t *testing.T) {
	for _, fixture := range heterociProbeOfflineEscapeFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			guard, _ := newHeterociProbeSyntheticGuard(t, heterociProbeOfflinePreamble()+fixture.source, "")
			if violations := guard.offlineConditionViolations(); len(violations) == 0 {
				t.Fatal("offline condition escape was accepted")
			}
		})
	}
}

func heterociProbeOfflineEscapeFixtures() []heterociProbeOfflineFixture {
	return []heterociProbeOfflineFixture{
		{name: "any interface address escapes", source: `
			func consumeProbeAny(any) {}
			func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
				body, err := transport.Probe(ctx, "", request); bodyNil := isNilProbeValue(body); ctxErr := ctx.Err(); var transportErr probeExternalErrorSnapshot
				if err != nil { transportErr = snapshotProbeExternalError(err) }; consumeProbeAny(any(&transportErr))
				if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }; return NodeSnapshot{}, nil
			}`},
		{name: "struct holder stores address", source: `
			type probeHolder struct { Snapshot *probeExternalErrorSnapshot }; func consumeProbeHolder(probeHolder) {}
			func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
				body, err := transport.Probe(ctx, "", request); bodyNil := isNilProbeValue(body); ctxErr := ctx.Err(); var transportErr probeExternalErrorSnapshot
				if err != nil { transportErr = snapshotProbeExternalError(err) }; holder := probeHolder{Snapshot: &transportErr}; consumeProbeHolder(holder)
				if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }; return NodeSnapshot{}, nil
			}`},
		{name: "pointer struct holder stores address", source: `
			type probeHolder struct { Snapshot *probeExternalErrorSnapshot }; func consumeProbeHolderPointer(*probeHolder) {}
			func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
				body, err := transport.Probe(ctx, "", request); bodyNil := isNilProbeValue(body); ctxErr := ctx.Err(); var transportErr probeExternalErrorSnapshot
				if err != nil { transportErr = snapshotProbeExternalError(err) }; holder := &probeHolder{Snapshot: &transportErr}; consumeProbeHolderPointer(holder)
				if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }; return NodeSnapshot{}, nil
			}`},
		{name: "slice holder stores address", source: `
			func consumeProbeSlice([]*probeExternalErrorSnapshot) {}
			func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
				body, err := transport.Probe(ctx, "", request); bodyNil := isNilProbeValue(body); ctxErr := ctx.Err(); var transportErr probeExternalErrorSnapshot
				if err != nil { transportErr = snapshotProbeExternalError(err) }; holder := []*probeExternalErrorSnapshot{&transportErr}; consumeProbeSlice(holder)
				if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }; return NodeSnapshot{}, nil
			}`},
		{name: "map holder stores address", source: `
			func consumeProbeMap(map[string]*probeExternalErrorSnapshot) {}
			func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
				body, err := transport.Probe(ctx, "", request); bodyNil := isNilProbeValue(body); ctxErr := ctx.Err(); var transportErr probeExternalErrorSnapshot
				if err != nil { transportErr = snapshotProbeExternalError(err) }; holder := map[string]*probeExternalErrorSnapshot{"snapshot": &transportErr}; consumeProbeMap(holder)
				if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }; return NodeSnapshot{}, nil
			}`},
		{name: "go helper address escapes", source: `
			func consumeProbeGo(*probeExternalErrorSnapshot, chan struct{}) {}
			func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
				body, err := transport.Probe(ctx, "", request); bodyNil := isNilProbeValue(body); ctxErr := ctx.Err(); var transportErr probeExternalErrorSnapshot; ready := make(chan struct{})
				if err != nil { transportErr = snapshotProbeExternalError(err) }; go consumeProbeGo(&transportErr, ready); <-ready
				if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }; return NodeSnapshot{}, nil
			}`},
		{name: "defer helper address escapes", source: `
			func consumeProbeDefer(*probeExternalErrorSnapshot) {}
			func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
				body, err := transport.Probe(ctx, "", request); bodyNil := isNilProbeValue(body); ctxErr := ctx.Err(); var transportErr probeExternalErrorSnapshot
				if err != nil { transportErr = snapshotProbeExternalError(err) }; defer consumeProbeDefer(&transportErr)
				if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }; return NodeSnapshot{}, nil
			}`},
		{name: "field address escapes to helper", source: `
			func consumeProbeField(*bool) {}
			func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
				body, err := transport.Probe(ctx, "", request); bodyNil := isNilProbeValue(body); ctxErr := ctx.Err(); var transportErr probeExternalErrorSnapshot
				if err != nil { transportErr = snapshotProbeExternalError(err) }; consumeProbeField(&transportErr.onlyUnavailable)
				if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }; return NodeSnapshot{}, nil
			}`},
		{name: "whole snapshot address escapes to helper", source: `
			func consumeProbeSnapshot(*probeExternalErrorSnapshot) {}
			func acquireProbeNode(ctx context.Context, config NodeConfig, transport ProbeTransport, request []byte) (NodeSnapshot, error) {
				body, err := transport.Probe(ctx, "", request); bodyNil := isNilProbeValue(body); ctxErr := ctx.Err(); var transportErr probeExternalErrorSnapshot
				if err != nil { transportErr = snapshotProbeExternalError(err) }; consumeProbeSnapshot(&transportErr)
				if bodyNil && ctxErr == nil && transportErr.onlyUnavailable { return offlineProbeSnapshot(config), nil }; return NodeSnapshot{}, nil
			}`},
	}
}
