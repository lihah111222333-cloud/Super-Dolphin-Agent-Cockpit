package codemapindex

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type invalidCodemapSemanticCase struct {
	name       string
	body       string
	prepare    func(t *testing.T, root string)
	wantErrSub string
}

func TestGeneratedAtForModeUsesInjectedClock(t *testing.T) {
	if got := generatedAtForMode(false, filepath.Join(t.TempDir(), "missing-index.json"), func() time.Time {
		return time.Date(2026, time.August, 1, 0, 30, 0, 0, time.FixedZone("UTC+14", 14*60*60))
	}); got != "2026-07-31" {
		t.Fatalf("generatedAtForMode() = %q", got)
	}
}

func TestBuildIndexFailsWhenCodemapDirMissing(t *testing.T) {
	root := t.TempDir()
	srcDir := filepath.Join(root, "internal", "example")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "example.go"), []byte("package example\n"), 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	_, _, err := buildIndex(root, filepath.Join(root, "docs", "doc", "codemap"), "2026-05-20")
	if err == nil || !strings.Contains(err.Error(), "scan codemap markdown") {
		t.Fatalf("buildIndex() error = %v, want codemap markdown scan error", err)
	}
}

// TestBuildIndexRejectsInvalidCodemapSemantics 锁定编号卷中的路径、行号、生命周期和计数语义。
func TestBuildIndexRejectsInvalidCodemapSemantics(t *testing.T) {
	for _, test := range invalidCodemapSemanticCases() {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			codemapDir := filepath.Join(root, "docs", "doc", "codemap")
			if err := os.MkdirAll(codemapDir, 0o755); err != nil {
				t.Fatalf("mkdir codemap dir: %v", err)
			}
			prepareCodemapSemanticPolicyFixture(t, root)
			writeCodemapFixtureFile(t, root, "docs/doc/codemap/01-fixture.md", test.body)
			if test.prepare != nil {
				test.prepare(t, root)
			}

			_, _, err := buildIndex(root, codemapDir, "2026-07-27")
			if err == nil || !strings.Contains(err.Error(), test.wantErrSub) {
				t.Fatalf("buildIndex() error = %v, want substring %q", err, test.wantErrSub)
			}
		})
	}
}

// TestBuildIndexAcceptsValidCodemapSemantics 锁定真实扩展名、围栏、多锚点与生产计数的 GREEN 基线。
func TestBuildIndexAcceptsValidCodemapSemantics(t *testing.T) {
	root := t.TempDir()
	codemapDir := filepath.Join(root, "docs", "doc", "codemap")
	prepareCodemapSemanticPolicyFixture(t, root)
	writeCodemapFixtureFile(t, root, "internal/example/example.jsx", "one\ntwo\n")
	writeCodemapFixtureFile(t, root, "internal/example/example.tsx", "one\ntwo\n")
	writeCodemapFixtureFile(t, root, "internal/example/query.sql.go", "one\ntwo\n")
	writeCodemapFixtureFile(t, root, "internal/example/example.go", "package example\n\nconst PackageSymbol = 1\n\nfunc Symbol() {}\n")
	writeCodemapFixtureFile(t, root, "internal/wiring/module.go", "package wiring\n\nvar Module = fx.Module(\"wiring\", first.Module, second.Module)\n")
	writeCodemapFixtureFile(t, root, "docs/doc/codemap/linked.md", "# Valid heading\n")
	writeCodemapFixtureFile(t, root, "Makefile", "test:\n")
	writeCodemapFixtureFile(t, root, "sqlc.yaml", "version: \"2\"\nsql: []\n")
	writeCodemapFixtureFile(t, root, "docs/doc/codemap/01-fixture.md", strings.Join([]string{
		"# Map",
		"",
		"`internal/example/example.jsx:1/2`",
		"`internal/example/example.tsx:1-2`",
		"`internal/example/query.sql.go:1-2`",
		"`internal/example/example.go.Symbol` and `internal/example.PackageSymbol` are symbol-qualified paths.",
		"`internal/example/*.go` validates its literal owner before accepting the glob.",
		"`internal/example/**/*.go` also matches Go files directly under the owner.",
		"`Makefile:1` and `sqlc.yaml:2` are repository-root navigation targets.",
		"`internal/example/retired.go` is an explicitly absent generated artifact.",
		"[heading](linked.md#valid-heading)",
		"<!-- codemap-absent path=\"internal/example/retired.go\" -->",
		"<!-- codemap-absent path=\"internal/example.RetiredSymbol\" -->",
		"<!-- codemap-count path=\"internal/example\" kind=\"go-files\" -->",
		"<!-- codemap-count path=\"internal/wiring/module.go\" kind=\"fx-module-refs\" -->",
		"```text",
		"`internal/example/intentionally-missing.go`",
		"```",
		"",
	}, "\n"))
	writeCodemapFixtureFile(t, root, "docs/doc/codemap/13-archtest-boundaries.md", strings.Join([]string{
		"# Generated archtest map",
		"",
		"`internal/tool/{lsp,ida}` is validated by the archtest-map owner.",
		"`internal/owner-generated/**/*.go` is also owned by archtest-map.",
		"",
	}, "\n"))

	index, _, err := buildIndex(root, codemapDir, "2026-07-27")
	if err != nil {
		t.Fatalf("buildIndex() error = %v, want valid semantics", err)
	}
	wantCounts := []CodemapCount{
		{Path: "internal/example", Kind: "go-files", Value: 2},
		{Path: "internal/wiring/module.go", Kind: "fx-module-refs", Value: 2},
	}
	if len(index.Counts) != len(wantCounts) {
		t.Fatalf("buildIndex() counts = %#v, want %#v", index.Counts, wantCounts)
	}
	for countIndex, want := range wantCounts {
		if index.Counts[countIndex] != want {
			t.Fatalf("buildIndex() counts[%d] = %#v, want %#v", countIndex, index.Counts[countIndex], want)
		}
	}
}

func invalidCodemapSemanticCases() []invalidCodemapSemanticCase {
	cases := invalidRepositorySemanticCases()
	return append(cases, invalidMarkdownSemanticCases()...)
}

func invalidRepositorySemanticCases() []invalidCodemapSemanticCase {
	return []invalidCodemapSemanticCase{
		{
			name:       "missing repository path",
			body:       "# Map\n\n`internal/example/missing.go`\n",
			wantErrSub: "missing repository path",
		},
		{
			name: "missing bare child path",
			body: "# Map\n\n`internal/example/missing-child`\n",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				writeCodemapFixtureFile(t, root, "internal/example/example.go", "package example\n")
			},
			wantErrSub: "missing repository path: internal/example/missing-child",
		},
		{
			name: "missing package symbol",
			body: "# Map\n\n`internal/example.DoesNotExist`\n",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				writeCodemapFixtureFile(t, root, "internal/example/example.go", "package example\n")
			},
			wantErrSub: "missing repository symbol",
		},
		{
			name:       "dead path with wildcard suffix",
			body:       "# Map\n\n`internal/missing.go*`\n",
			wantErrSub: "missing repository pattern: internal/missing.go*",
		},
		{
			name: "declared absent path exists",
			body: "# Map\n\n<!-- codemap-absent path=\"internal/example/example.go\" -->\n",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				writeCodemapFixtureFile(t, root, "internal/example/example.go", "package example\n")
			},
			wantErrSub: "codemap absence violated",
		},
		{
			name: "declared absent symbol exists",
			body: "# Map\n\n<!-- codemap-absent path=\"internal/example.Present\" -->\n",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				writeCodemapFixtureFile(t, root, "internal/example/example.go", "package example\n\nconst Present = 1\n")
			},
			wantErrSub: "codemap absence violated",
		},
		{
			name: "line anchor out of range",
			body: "# Map\n\n`internal/example/example.go:9`\n",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				writeCodemapFixtureFile(t, root, "internal/example/example.go", "package example\n")
			},
			wantErrSub: "line anchor",
		},
		{
			name:       "repository path escape",
			body:       "# Map\n\n<!-- codemap-count path=\"internal/../../outside\" kind=\"go-files\" -->\n",
			wantErrSub: "path escapes repository",
		},
	}
}

func invalidMarkdownSemanticCases() []invalidCodemapSemanticCase {
	return []invalidCodemapSemanticCase{
		{
			name:       "missing markdown link",
			body:       "# Map\n\n[missing](missing.md)\n",
			wantErrSub: "missing markdown link",
		},
		{
			name: "historical document authority",
			body: "# Map\n\nAuthoritative: [old plan](../../plans/old.md)\n",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				writeCodemapFixtureFile(t, root, "docs/plans/old.md", "# old\n")
			},
			wantErrSub: "historical document",
		},
		{
			name: "unsupported count kind",
			body: "# Map\n\n<!-- codemap-count path=\"internal/example\" kind=\"unknown-files\" -->\n",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				writeCodemapFixtureFile(t, root, "internal/example/example.go", "package example\n")
			},
			wantErrSub: "unsupported kind",
		},
		{
			name:       "unclosed fenced block",
			body:       "# Map\n\n```text\nhidden\n",
			wantErrSub: "unclosed Markdown fence",
		},
		{
			name:       "malformed count declaration",
			body:       "# Map\n\n<!-- codemap-count path=\"internal/example\" kind=\"go-files\" expected=\"1\" -->\n",
			wantErrSub: "malformed codemap-count",
		},
		{
			name:       "malformed absence declaration",
			body:       "# Map\n\n<!-- codemap-absent value=\"internal/example\" -->\n",
			wantErrSub: "malformed codemap-absent",
		},
		{
			name: "missing markdown fragment",
			body: "# Map\n\n[bad](linked.md#missing)\n",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				writeCodemapFixtureFile(t, root, "docs/doc/codemap/linked.md", "# Present\n")
			},
			wantErrSub: "missing markdown fragment",
		},
	}
}

func prepareCodemapSemanticPolicyFixture(t *testing.T, root string) {
	t.Helper()
	policy, err := os.ReadFile(filepath.Join(codemapGeneratorRepoRoot(t), "scripts", "codemap_policy.txt"))
	if err != nil {
		t.Fatalf("read codemap policy fixture: %v", err)
	}
	writeCodemapFixtureFile(t, root, "scripts/codemap_policy.txt", string(policy))
	shards := []string{"app-ui.tsv", "docs-agent.tsv", "modules.tsv", "orchestration.tsv", "other.tsv", "platform-provider.tsv", "remote-ci.tsv", "store-sql.tsv"}
	var manifest strings.Builder
	manifest.WriteString("{\"index_files\":{\"shards\":[")
	for index, shard := range shards {
		if index > 0 {
			manifest.WriteByte(',')
		}
		manifest.WriteString("{\"file\":\"docs/doc/codemap/project-map/index/")
		manifest.WriteString(shard)
		manifest.WriteString("\"}")
		writeCodemapFixtureFile(t, root, "docs/doc/codemap/project-map/index/"+shard, "path\tmodule\tdomain\ttype\tsize_bytes\tpurpose\tsearch_keys\n")
	}
	manifest.WriteString("]}}\n")
	writeCodemapFixtureFile(t, root, "docs/doc/codemap/project-map/AI_PROJECT_MANIFEST.json", manifest.String())
}

func codemapGeneratorRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve codemap generator test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func writeCodemapFixtureFile(t *testing.T, root, relativePath, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", relativePath, err)
	}
}
