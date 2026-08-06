package archtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMeasureFileMetrics_Sample(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	path := filepath.Join(root, "internal/archtest/testdata/metrics_sample.gotxt")

	m := MeasureFileMetrics(path)

	// Size checks
	require.NotZero(t, m.Lines, "Lines should be > 0")
	require.NotZero(t, m.MaxFuncLen, "MaxFuncLen should be > 0 (complexFunc has multiple lines)")

	// Complexity
	require.GreaterOrEqual(t, m.MaxNesting, 3, "MaxNesting: got %d, want >= 3 (deepNesting has nesting 4)", m.MaxNesting)
	require.GreaterOrEqual(t, m.MaxComplexity, 5, "MaxComplexity: got %d, want >= 5 (complexFunc is complex)", m.MaxComplexity)
	require.GreaterOrEqual(t, m.MaxParams, 8, "MaxParams: got %d, want >= 8 (manyParams has 8)", m.MaxParams)
	require.GreaterOrEqual(t, m.MaxReturns, 4, "MaxReturns: got %d, want >= 4 (manyReturns has 5)", m.MaxReturns)

	// Quality
	require.GreaterOrEqual(t, m.GlobalVars, 1, "GlobalVars: got %d, want >= 1 (globalCounter)", m.GlobalVars)
	require.GreaterOrEqual(t, m.PanicCount, 1, "PanicCount: got %d, want >= 1 (panicFunc)", m.PanicCount)
	require.GreaterOrEqual(t, m.NakedReturns, 1, "NakedReturns: got %d, want >= 1 (nakedReturnFunc)", m.NakedReturns)
	require.GreaterOrEqual(t, m.EmptyFuncs, 1, "EmptyFuncs: got %d, want >= 1 (emptyFunc)", m.EmptyFuncs)
	require.GreaterOrEqual(t, m.TodoCount, 2, "TodoCount: got %d, want >= 2 (two TODO comments)", m.TodoCount)
	require.GreaterOrEqual(t, m.MaxStructFields, 16, "MaxStructFields: got %d, want >= 16 (BigStruct)", m.MaxStructFields)
}

func TestMeasureFileMetrics_NotFound(t *testing.T) {
	t.Parallel()
	m := MeasureFileMetrics("/nonexistent/file.go")
	if m.Lines != 0 {
		t.Errorf("expected zero metrics for missing file, got lines=%d", m.Lines)
	}
}

func TestCountGlobalVarsV3_Exemptions(t *testing.T) {
	t.Parallel()
	// 通过 testdata 文件测试：globalCounter 应被计数，其他全局变量模式应被豁免。
	// 本测试通过 MeasureFileMetrics 间接验证。
	root := repoRootForGuardTests(t)
	path := filepath.Join(root, "internal/archtest/testdata/metrics_sample.gotxt")
	m := MeasureFileMetrics(path)

	// metrics_sample.gotxt 只有一个非豁免全局变量 (globalCounter)
	if m.GlobalVars != 1 {
		t.Errorf("GlobalVars: got %d, want 1 (only globalCounter should be counted)", m.GlobalVars)
	}
}

func TestCountGlobalVarsV3RejectsMutableCompositeInitializers(t *testing.T) {
	t.Parallel()
	for _, tt := range mutableGlobalVarCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := measureGlobalVarsFromSource(t, tt.source); got != tt.want {
				t.Fatalf("GlobalVars = %d, want %d", got, tt.want)
			}
		})
	}
}

type globalVarMetricCase struct {
	name   string
	source string
	want   int
}

func mutableGlobalVarCases() []globalVarMetricCase {
	return []globalVarMetricCase{
		{
			name: "value struct with mutex and map",
			source: `package sample
import "sync"
type state struct {
	mu sync.Mutex
	values map[string]int
}
var shared = state{mu: sync.Mutex{}, values: map[string]int{"seed": 1}}
`,
			want: 1,
		},
		{
			name: "pointer struct with mutex and map",
			source: `package sample
import "sync"
type state struct {
	mu sync.Mutex
	values map[string]int
}
var shared = &state{mu: sync.Mutex{}, values: map[string]int{"seed": 1}}
`,
			want: 1,
		},
		{
			name: "anonymous struct with mutex and map",
			source: `package sample
import "sync"
var shared = struct {
	mu sync.Mutex
	values map[string]int
}{values: map[string]int{"seed": 1}}
`,
			want: 1,
		},
		{
			name: "zero value struct with mutex and map",
			source: `package sample
import "sync"
type state struct {
	mu sync.Mutex
	values map[string]int
}
var shared = state{}
`,
			want: 1,
		},
		{name: "map", source: "package sample\nvar lookup = map[string]int{\"seed\": 1}\n", want: 1},
		{name: "slice", source: "package sample\nvar values = []int{1, 2}\n", want: 1},
		{name: "channel make", source: "package sample\nvar updates = make(chan int)\n", want: 1},
		{name: "pointer", source: "package sample\nvar descriptor = &struct{ Name string }{Name: \"sample\"}\n", want: 1},
		{name: "function", source: "package sample\nvar hook = func() {}\n", want: 1},
		{name: "sync zero value", source: "package sample\nimport \"sync\"\nvar lock sync.Mutex\n", want: 1},
		{name: "atomic zero value", source: "package sample\nimport \"sync/atomic\"\nvar counter atomic.Int64\n", want: 1},
		{name: "sync composite", source: "package sample\nimport \"sync\"\nvar lock = sync.Mutex{}\n", want: 1},
		{name: "atomic composite", source: "package sample\nimport \"sync/atomic\"\nvar counter = atomic.Int64{}\n", want: 1},
		{name: "unknown constructor", source: "package sample\nvar cache = NewCache()\n", want: 1},
		{name: "unresolved composite", source: "package sample\nvar cache = external.Cache{}\n", want: 1},
		{name: "unproven embed selector", source: "package sample\nvar files embed.FS\n", want: 1},
		{
			name:   "multi name multi rhs",
			source: "package sample\nvar lookup, state = [1]int{1}, map[string]int{\"seed\": 1}\n",
			want:   1,
		},
		{
			name:   "multi name shared expression",
			source: "package sample\nvar left, right = NewPair()\n",
			want:   2,
		},
	}
}

func TestCountGlobalVarsV3PreservesProvenImmutableInitializers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{name: "constant array", source: "package sample\nconst width = 2\nvar values = [width]string{\"a\", \"b\"}\n"},
		{
			name: "scalar descriptor",
			source: `package sample
type descriptor struct {
	name string
	enabled bool
}
var current = descriptor{name: "sample", enabled: true}
`,
		},
		{
			name: "readonly array lookup",
			source: `package sample
type entry struct {
	key string
	value int
}
var lookup = [...]entry{{key: "a", value: 1}, {key: "b", value: 2}}
`,
		},
		{name: "paired immutable rhs", source: "package sample\nvar left, right = [1]int{1}, [1]int{2}\n"},
		{name: "regexp", source: "package sample\nimport \"regexp\"\nvar pattern = regexp.MustCompile(\"sample\")\n"},
		{name: "fx module", source: "package sample\nimport \"go.uber.org/fx\"\nvar Module = fx.Module(\"sample\")\n"},
		{name: "aliased embed filesystem", source: "package sample\nimport embedded \"embed\"\nvar files embedded.FS\n"},
		{
			name: "interface assertion",
			source: `package sample
type runner interface{ Run() }
type implementation struct{}
func (*implementation) Run() {}
var _ runner = (*implementation)(nil)
`,
		},
		{
			name: "formal ignore",
			source: `package sample
// archguard:ignore global_vars -- test fixture explicitly owns mutable process state
var shared = map[string]int{"seed": 1}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := measureGlobalVarsFromSource(t, tt.source); got != 0 {
				t.Fatalf("GlobalVars = %d, want 0", got)
			}
		})
	}
}

func measureGlobalVarsFromSource(t *testing.T, source string) int {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.go")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write global variable fixture: %v", err)
	}
	return MeasureFileMetrics(path).GlobalVars
}

func TestCheckAllAllowsLargePackageLineTotals(t *testing.T) {
	t.Parallel()

	const retiredPackageLineLimit = 10000
	const filesInPackage = 20
	linesPerFile := MaxFileLines - 50
	if filesInPackage*linesPerFile <= retiredPackageLineLimit {
		t.Fatalf("fixture lines = %d, want above retired package line limit %d", filesInPackage*linesPerFile, retiredPackageLineLimit)
	}

	root := t.TempDir()
	pkgDir := filepath.Join(root, "pkg", "sample")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir package fixture: %v", err)
	}
	for fileIndex := range filesInPackage {
		var body strings.Builder
		body.WriteString("package sample\n\n")
		for lineIndex := range linesPerFile {
			fmt.Fprintf(&body, "var Value%dLine%d = %d\n", fileIndex, lineIndex, lineIndex)
		}
		path := filepath.Join(pkgDir, fmt.Sprintf("sample%d.go", fileIndex))
		if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
			t.Fatalf("write package fixture: %v", err)
		}
	}

	violations := CheckAll(CheckOptions{
		RepoRoot:  root,
		ScanRoots: []string{"pkg"},
		SkipDirs:  DefaultSkipDirs(),
	})
	if len(violations) != 0 {
		t.Fatalf("CheckAll() violations = %v, want none for package line totals", violations)
	}
}
