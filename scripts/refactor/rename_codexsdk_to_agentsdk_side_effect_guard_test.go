package main

/* ROLLBACK_SKIP_START

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameScriptSideEffectGuard_ProcessRenameFileSequenceAndWriteReport(t *testing.T) {
	t.Parallel()

	runRenameHarnessGoTest(t, "rename_codexsdk_to_agentsdk_side_effect_harness_test.go", `package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProcessRenameFileApplySequenceAndWriteBack(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "demo.go")
	original := "package demo\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatalf("write demo.go: %v", err)
	}

	var events []string
	reports := []fileReport{}
	total := 0
	collect := func(path string, src []byte) ([]edit, []replacement, error) {
		events = append(events, "collect:"+filepath.Base(path)+":"+string(src))
		return []edit{{
			Start:  8,
			End:    12,
			OldLit: "demo",
			NewLit: "renamed",
			Line:   1,
		}}, []replacement{{
			Old:  "demo",
			New:  "renamed",
			Line: 1,
		}}, nil
	}
	apply := func(src []byte, edits []edit) []byte {
		events = append(events, fmt.Sprintf("apply:%s:%d", string(src), len(edits)))
		return applyEdits(src, edits)
	}

	if err := processRenameFile(root, target, true, collect, apply, &reports, &total); err != nil {
		t.Fatalf("processRenameFile() error = %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	if got := string(data); got != "package renamed\n" {
		t.Fatalf("updated file = %q, want %q", got, "package renamed\n")
	}

	wantEvents := []string{
		"collect:demo.go:package demo\n",
		"apply:package demo\n:1",
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("side effect sequence = %#v, want %#v", events, wantEvents)
	}

	wantReports := []fileReport{{
		File:  "demo.go",
		Count: 1,
		Replacements: []replacement{{
			Old:  "demo",
			New:  "renamed",
			Line: 1,
		}},
	}}
	if !reflect.DeepEqual(reports, wantReports) {
		t.Fatalf("reports = %#v, want %#v", reports, wantReports)
	}
	if total != 1 {
		t.Fatalf("total replacements = %d, want 1", total)
	}
}

func TestWriteReportMarshalsIndentedJSONAndCreatesParentDir(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "nested", "out", "report.json")
	rep := report{
		Mode:              "apply",
		Root:              "/repo",
		OldImportRoot:     oldImportRoot,
		NewImportRoot:     newImportRoot,
		TotalFiles:        1,
		TotalReplacements: 2,
		Files: []fileReport{{
			File:  "demo.go",
			Count: 2,
			Replacements: []replacement{{
				Old:  oldImportRoot,
				New:  newImportRoot,
				Line: 3,
			}},
		}},
		GeneratedAt: "2026-03-19T00:00:00Z",
	}

	if err := writeReport(reportPath, rep); err != nil {
		t.Fatalf("writeReport() error = %v", err)
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatalf("report must end with newline, got %q", string(data))
	}
	if !strings.Contains(string(data), "\n  \"mode\": \"apply\",") {
		t.Fatalf("report must stay indented JSON, got %q", string(data))
	}

	var got report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if !reflect.DeepEqual(got, rep) {
		t.Fatalf("report payload = %#v, want %#v", got, rep)
	}
}
`)
}

func TestRenameScriptSideEffectGuard_PlanLifecycleAndRewriteContracts(t *testing.T) {
	t.Parallel()

	runRenameHarnessGoTest(t, "rename_codexsdk_to_agentsdk_plan_side_effect_harness_test.go", `package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildRenamePlanAndRecordRenamePlanPreserveRelSrcAndMode(t *testing.T) {
	root := t.TempDir()
	nestedDir := filepath.Join(root, "nested")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}
	target := filepath.Join(nestedDir, "demo.go")
	original := "package demo\n\nimport _ \"" + oldImportRoot + "\"\n"
	if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
		t.Fatalf("write demo.go: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat demo.go: %v", err)
	}

	plan, ok, err := buildRenamePlan(root, target, collectEdits)
	if err != nil || !ok {
		t.Fatalf("buildRenamePlan() = (%#v, %v, %v), want executable plan", plan, ok, err)
	}
	if plan.Rel != "nested/demo.go" {
		t.Fatalf("plan.Rel = %q, want nested/demo.go", plan.Rel)
	}
	if string(plan.Src) != original {
		t.Fatalf("plan.Src = %q, want %q", string(plan.Src), original)
	}
	if plan.FileMode != info.Mode().Perm() {
		t.Fatalf("plan.FileMode = %o, want %o", plan.FileMode, info.Mode().Perm())
	}
	if len(plan.Edits) != 1 || len(plan.Replacements) != 1 {
		t.Fatalf("plan edits/replacements = %d/%d, want 1/1", len(plan.Edits), len(plan.Replacements))
	}

	reports := []fileReport{}
	total := 0
	recordRenamePlan(plan, &reports, &total)
	wantReports := []fileReport{{
		File:         "nested/demo.go",
		Count:        1,
		Replacements: plan.Replacements,
	}}
	if !reflect.DeepEqual(reports, wantReports) {
		t.Fatalf("reports = %#v, want %#v", reports, wantReports)
	}
	if total != 1 {
		t.Fatalf("total replacements = %d, want 1", total)
	}
}

func TestCollectEditsAndRewriteImportPathContracts(t *testing.T) {
	src := []byte("package demo\n\nimport (\n\t_ \"" + oldImportRoot + "/service/runtime\"\n\t_ \"fmt\"\n\t_ \"" + oldImportRoot + "\"\n)\n")

	edits, reports, err := collectEdits("demo.go", src)
	if err != nil {
		t.Fatalf("collectEdits() error = %v", err)
	}
	if len(edits) != 2 || len(reports) != 2 {
		t.Fatalf("collectEdits() counts = %d edits, %d reports, want 2/2", len(edits), len(reports))
	}
	if edits[0].Start <= edits[1].Start {
		t.Fatalf("edits must stay reverse-sorted by start offset: %#v", edits)
	}
	if reports[0].New != newImportRoot+"/service/runtime" || reports[1].New != newImportRoot {
		t.Fatalf("replacement targets = %#v, want agentsdk roots", reports)
	}

	updated := string(applyEdits(src, edits))
	if strings.Contains(updated, oldImportRoot) {
		t.Fatalf("applyEdits() should remove old import root, got %q", updated)
	}
	if !strings.Contains(updated, newImportRoot+"/service/runtime") || !strings.Contains(updated, newImportRoot+"\"") {
		t.Fatalf("applyEdits() should inject new import roots, got %q", updated)
	}

	if got, ok := rewriteImportPath(oldImportRoot); !ok || got != newImportRoot {
		t.Fatalf("rewriteImportPath(root) = (%q, %v), want (%q, true)", got, ok, newImportRoot)
	}
	if got, ok := rewriteImportPath(oldImportRoot + "/service/runtime"); !ok || got != newImportRoot+"/service/runtime" {
		t.Fatalf("rewriteImportPath(nested) = (%q, %v), want nested agentsdk path", got, ok)
	}
	if got, ok := rewriteImportPath(modulePkgRoot + "codexsdk_extra"); ok || got != "" {
		t.Fatalf("rewriteImportPath(partial) = (%q, %v), want no rewrite", got, ok)
	}
}

func TestApplyRenamePlansRollsBackEarlierFilesOnLaterWriteFailure(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.go")
	original := "package demo\n\nimport _ \"" + oldImportRoot + "\"\n"
	if err := os.WriteFile(first, []byte(original), 0o640); err != nil {
		t.Fatalf("write first.go: %v", err)
	}
	firstPlan, ok, err := buildRenamePlan(root, first, collectEdits)
	if err != nil || !ok {
		t.Fatalf("buildRenamePlan(first) = (%#v, %v, %v), want executable plan", firstPlan, ok, err)
	}

	second := filepath.Join(root, "second.go")
	if err := os.Mkdir(second, 0o755); err != nil {
		t.Fatalf("mkdir second.go dir: %v", err)
	}
	secondPlan := renamePlan{
		Path:     second,
		Rel:      "second.go",
		Src:      []byte("package demo\n"),
		Edits:    []edit{{Start: 0, End: 0, NewLit: "package demo\n", Line: 1}},
		FileMode: 0o644,
	}

	err = applyRenamePlans([]renamePlan{firstPlan, secondPlan}, applyEdits)
	if err == nil {
		t.Fatal("applyRenamePlans() should fail on second.go write")
	}
	if !strings.Contains(err.Error(), "write second.go:") {
		t.Fatalf("applyRenamePlans() err = %q, want second.go write failure", err.Error())
	}
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read first.go after rollback: %v", err)
	}
	if string(data) != original {
		t.Fatalf("rollback must restore first.go, got %q want %q", string(data), original)
	}
}
`)
}

func runRenameHarnessGoTest(t *testing.T, testFileName, testSource string) {
	t.Helper()

	harnessDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(harnessDir, "go.mod"), []byte("module renameharness\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write harness go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(harnessDir, "rename_codexsdk_to_agentsdk.go"), []byte(strippedRenameScriptSource(t)), 0o644); err != nil {
		t.Fatalf("write harness script: %v", err)
	}
	if err := os.WriteFile(filepath.Join(harnessDir, testFileName), []byte(testSource), 0o644); err != nil {
		t.Fatalf("write harness test: %v", err)
	}

	cmd := exec.Command("go", "test", "-count=1", "-v")
	cmd.Dir = harnessDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("harness test command failed: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
}

func strippedRenameScriptSource(t *testing.T) string {
	t.Helper()

	src := readRenameScriptSourceFile(t, "rename_codexsdk_to_agentsdk.go")
	src = strings.TrimPrefix(src, "//go:build ignore\n\n")
	return src
}

ROLLBACK_SKIP_END */
