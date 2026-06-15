package archtest

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestParseArchGuardIgnoreMetrics(t *testing.T) {
	t.Parallel()

	got := parseArchGuardIgnoreMetrics("// archguard:ignore panic_count,global_vars -- justified")
	want := []string{"panic_count", "global_vars"}
	if len(got) != len(want) {
		t.Fatalf("len(parse) = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parse[%d] = %q, want %q; all=%#v", i, got[i], want[i], got)
		}
	}

	if got := parseArchGuardIgnoreMetrics("// ordinary comment"); got != nil {
		t.Fatalf("ordinary comment parsed as %#v, want nil", got)
	}
}

func TestMeasureFileMetrics_ArchGuardIgnoreComments(t *testing.T) {
	t.Parallel()

	const src = `package sample

import "sync"

// archguard:ignore global_vars -- test fixture cache
var cache sync.Map

func rethrow() (value string) {
	defer func() {
		if r := recover(); r != nil {
			// archguard:ignore panic_count -- rethrow preserves caller panic semantics
			panic(r)
		}
	}()
	value = "ok"
	// archguard:ignore naked_returns -- named result documents the fixture
	return
}
`
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	m := measureFileMetricsFromAST(SplitLines([]byte(src)), fset, node)
	if m.GlobalVars != 0 {
		t.Fatalf("GlobalVars = %d, want 0", m.GlobalVars)
	}
	if m.PanicCount != 0 {
		t.Fatalf("PanicCount = %d, want 0", m.PanicCount)
	}
	if m.NakedReturns != 0 {
		t.Fatalf("NakedReturns = %d, want 0", m.NakedReturns)
	}
}
