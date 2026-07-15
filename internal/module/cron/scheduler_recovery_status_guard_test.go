package cron

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestRecoverDanglingRunsContinuesValidRunAndAggregatesUnknownStatus(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	submitter := &programmableSubmitter{}
	s := newTestScheduler(t, store, submitter)
	pageCalls := 0
	store.listUnresolvedPageFn = func(_ context.Context, _ int32, cursor string) ([]RunRecord, error) {
		pageCalls++
		if cursor == "" {
			return []RunRecord{
				{ID: "run-unknown", JobID: "job-unknown", Status: "future_state"},
				{ID: "run-valid", JobID: "job-valid", TurnID: "turn-valid", Status: statusRunning},
			}, nil
		}
		return nil, nil
	}
	store.getJobFn = func(_ context.Context, id string) (JobRecord, error) {
		return JobRecord{ID: id, ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "tok", LeaseExpiresAt: s.now().Add(time.Hour)}, nil
	}
	finalizeCalls := 0
	store.finalizeRecoveredRunFn = func(context.Context, FinalizeRecoveredRunParams) error {
		finalizeCalls++
		return nil
	}
	observed := 0
	submitter.observeFn = func(_ context.Context, turnID string) error {
		if turnID == "turn-valid" {
			observed++
		}
		return nil
	}
	err := s.RecoverDanglingRuns(context.Background())
	assertErrorContainsAll(t, err, "run-unknown", "job-unknown", "future_state")
	if observed != 1 {
		t.Fatalf("valid recovery observed=%d, want 1", observed)
	}
	if pageCalls != 2 {
		t.Fatalf("recovery pageCalls=%d, want 2", pageCalls)
	}
	if finalizeCalls != 0 {
		t.Fatalf("unknown status finalized recovery=%d times, want 0", finalizeCalls)
	}
	if len(store.casCalls) != 0 {
		t.Fatalf("unknown status wrote CAS recovery transition: %+v", store.casCalls)
	}
}

func assertErrorContainsAll(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want contextual recovery error")
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want containing %q", err, want)
		}
	}
}

func TestRecoverySwitchCoversProducedRunStatuses(t *testing.T) {
	dir := cronPackageDir(t)
	produced := parseProducedRunStatusNames(t, filepath.Join(dir, "contract.go"))
	consumed := parseRecoverySwitchStatusNames(t, filepath.Join(dir, "scheduler_recovery.go"))
	exemptions := map[string]string{
		"statusPending":     "pending runs have not entered submission and are excluded from unresolved recovery",
		"statusFinished":    "finished is terminal and excluded from unresolved recovery",
		"statusFailed":      "failed is terminal and excluded from unresolved recovery",
		"statusObserveLost": "observe_lost is terminal and excluded from unresolved recovery",
	}

	missing := missingRecoveryStatuses(produced, consumed, exemptions)
	stale := append(staleRecoveryStatuses(produced, consumed), staleRecoveryExemptions(produced, exemptions)...)
	overlap := overlappingRecoveryStatuses(consumed, exemptions)
	sort.Strings(missing)
	sort.Strings(stale)
	sort.Strings(overlap)
	if len(missing) > 0 || len(stale) > 0 || len(overlap) > 0 {
		t.Fatalf("recovery status coverage drift: missing=%v stale=%v overlap=%v", missing, stale, overlap)
	}
}

func missingRecoveryStatuses(produced, consumed map[string]struct{}, exemptions map[string]string) []string {
	var missing []string
	for name := range produced {
		_, handled := consumed[name]
		reason, exempt := exemptions[name]
		if !handled && (!exempt || strings.TrimSpace(reason) == "") {
			missing = append(missing, name)
		}
	}
	return missing
}

func staleRecoveryStatuses(produced, consumed map[string]struct{}) []string {
	var stale []string
	for name := range consumed {
		if _, ok := produced[name]; !ok {
			stale = append(stale, name)
		}
	}
	return stale
}

func staleRecoveryExemptions(produced map[string]struct{}, exemptions map[string]string) []string {
	var stale []string
	for name, reason := range exemptions {
		if _, ok := produced[name]; !ok || strings.TrimSpace(reason) == "" {
			stale = append(stale, name)
		}
	}
	return stale
}

func overlappingRecoveryStatuses(consumed map[string]struct{}, exemptions map[string]string) []string {
	var overlap []string
	for name := range consumed {
		if _, exempt := exemptions[name]; exempt {
			overlap = append(overlap, name)
		}
	}
	return overlap
}

func cronPackageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve cron package directory")
	}
	return filepath.Dir(file)
}

func parseProducedRunStatusNames(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	file := parseCronSource(t, path)
	statuses := make(map[string]struct{})
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range value.Names {
				if strings.HasPrefix(name.Name, "status") {
					statuses[name.Name] = struct{}{}
				}
			}
		}
	}
	if len(statuses) == 0 {
		t.Fatalf("no produced run statuses found in %s", path)
	}
	return statuses
}

func parseRecoverySwitchStatusNames(t *testing.T, path string) map[string]struct{} {
	t.Helper()
	file := parseCronSource(t, path)
	statuses := make(map[string]struct{})
	found := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "recoverDanglingRun" {
			continue
		}
		found = true
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				if ident, ok := expr.(*ast.Ident); ok && strings.HasPrefix(ident.Name, "status") {
					statuses[ident.Name] = struct{}{}
				}
			}
			return true
		})
	}
	if !found || len(statuses) == 0 {
		t.Fatalf("recoverDanglingRun status switch not found in %s", path)
	}
	return statuses
}

func parseCronSource(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if file == nil {
		t.Fatalf("parse %s returned nil AST", path)
	}
	return file
}
