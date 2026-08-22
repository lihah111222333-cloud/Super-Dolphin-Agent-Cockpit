//go:build e2e

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPBinaryGrepFindsCodeGuardPlaceholderInRelativeDirectoryWithTrustedScope(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	root := filepath.Join(t.TempDir(), ".worktrees", "p16-batch1-selfguard-ci")
	codeGuardDir := filepath.Join(root, "backend", "cmd", "code_guard")
	query := "待实现的规则名占位符"
	writeLSPBinaryFixture(t,
		filepath.Join(codeGuardDir, "timeauthority_rules.go"),
		codeGuardPlaceholderFixture("// pattern/no-bare-time-now 是 T13 待实现的规则名占位符（INV-037）。"),
	)
	writeLSPBinaryFixture(t,
		filepath.Join(codeGuardDir, "kill_switch_rules.go"),
		codeGuardPlaceholderFixture("// pattern/kill-switch-isolation 是 TN 待实现的规则名占位符（INV-040）。"),
	)
	root = canonicalToolTestRoot(t, root)
	for _, want := range []string{"backend/cmd/code_guard/timeauthority_rules.go", "backend/cmd/code_guard/kill_switch_rules.go"} {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(want)))
		if err != nil || !strings.Contains(string(contents), query) {
			t.Fatalf("native fixture search %s = %q, err=%v; want %q", want, contents, err, query)
		}
	}
}

type codeGuardGrepFileRows struct {
	Cols []string `json:"cols"`
	Rows [][]any  `json:"rows"`
}

func codeGuardGrepRowsForFile(
	t *testing.T,
	root string,
	payload lspBinaryGrepResponse,
	wantRel string,
) codeGuardGrepFileRows {
	t.Helper()
	for path, rows := range payload.Data {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relative grep path %q: %v", path, err)
		}
		if filepath.ToSlash(rel) != wantRel {
			continue
		}
		return codeGuardGrepFileRows{Rows: rows.Rows}
	}
	t.Fatalf("grep payload missing %s; content files=%#v", wantRel, payload.Data)
	return codeGuardGrepFileRows{}
}

func codeGuardGrepRowText(t *testing.T, row []any) string {
	t.Helper()
	if len(row) < 3 {
		t.Fatalf("grep row has %d cells, want at least 3: %#v", len(row), row)
	}
	text, ok := row[2].(string)
	if !ok {
		t.Fatalf("grep row text cell = %T %[1]v, want string", row[2])
	}
	return text
}

func codeGuardPlaceholderFixture(line13 string) string {
	return strings.Repeat("// fixture padding\n", 12) + line13 + "\n"
}
