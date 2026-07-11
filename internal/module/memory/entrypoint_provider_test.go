package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	shared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/shared"
)

func newEntrypointProviderTestSetup(t *testing.T) (*MemoryEntrypointProvider, contract.BuildCtx, string) {
	t.Helper()
	root := t.TempDir()
	projectRoot := newTestGitProjectRoot(t)
	cfg := &Config{Enabled: true, RootDir: root, ProjectRoot: projectRoot}
	provider := NewEntrypointProvider(cfg, nil, nil)
	buildCtx := contract.BuildCtx{CWD: projectRoot, GitRoot: projectRoot}
	autoDir := provider.resolvedAutoMemPath(buildCtx)
	if strings.TrimSpace(autoDir) == "" {
		t.Fatalf("resolvedAutoMemPath() returned empty for cfg=%+v buildCtx=%+v", cfg, buildCtx)
	}
	if err := os.MkdirAll(autoDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", autoDir, err)
	}
	return provider, buildCtx, autoDir
}

func writeEntrypointMemory(t *testing.T, autoDir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(autoDir, "MEMORY.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func resolveStartEntrypoint(t *testing.T, provider *MemoryEntrypointProvider, buildCtx contract.BuildCtx) *string {
	t.Helper()
	out, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{},
		BuildCtx: buildCtx,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	return out
}

func requireEntrypointContent(t *testing.T, got string) {
	t.Helper()
	if !strings.HasPrefix(got, "## "+contract.DynamicSectionMemoryEntrypoint) {
		t.Fatalf("Resolve() prefix = %q, want section header for %q", firstLine(got), contract.DynamicSectionMemoryEntrypoint)
	}
	if !strings.Contains(got, "Contents of") || !strings.Contains(got, "source=auto") {
		t.Fatalf("Resolve() missing auto block header:\n%s", got)
	}
	if strings.Contains(got, "internal: refresh quarterly") {
		t.Fatalf("Resolve() leaked HTML comment:\n%s", got)
	}
	for _, banned := range []string{"---", "name: project notes", "type: project"} {
		if strings.Contains(got, banned) {
			t.Fatalf("Resolve() leaked frontmatter %q:\n%s", banned, got)
		}
	}
	if !strings.Contains(got, "[Architecture](architecture.md)") {
		t.Fatalf("Resolve() missing index entry:\n%s", got)
	}
}

func TestMemoryEntrypointProviderHappyPathStripsFrontmatterAndComments(t *testing.T) {
	t.Setenv(envHarnessKind, "")
	provider, buildCtx, autoDir := newEntrypointProviderTestSetup(t)
	body := strings.Join([]string{
		"---",
		"name: project notes",
		"description: durable index",
		"type: project",
		"---",
		"",
		"<!-- internal: refresh quarterly -->",
		"",
		"- [Architecture](architecture.md) — start here",
		"- [Runbook](runbook.md) — incident playbook",
	}, "\n")
	writeEntrypointMemory(t, autoDir, body)
	out := resolveStartEntrypoint(t, provider, buildCtx)
	if out == nil {
		t.Fatal("Resolve() = nil, want wrapped block")
	}
	requireEntrypointContent(t, *out)
}

func TestMemoryEntrypointProviderReturnsNilWhenIndexMissing(t *testing.T) {
	t.Setenv(envHarnessKind, "")
	provider, buildCtx, _ := newEntrypointProviderTestSetup(t)
	out, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{},
		BuildCtx: buildCtx,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if out != nil {
		t.Fatalf("Resolve() = %q, want nil for missing MEMORY.md", *out)
	}
}

func TestMemoryEntrypointProviderReturnsNilWhenIndexEmpty(t *testing.T) {
	t.Setenv(envHarnessKind, "")
	provider, buildCtx, autoDir := newEntrypointProviderTestSetup(t)
	if err := os.WriteFile(filepath.Join(autoDir, "MEMORY.md"), []byte("\n   \n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	out, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{},
		BuildCtx: buildCtx,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if out != nil {
		t.Fatalf("Resolve() = %q, want nil for whitespace-only MEMORY.md", *out)
	}
}

func TestMemoryEntrypointProviderSuppressedUnderOverlay(t *testing.T) {
	t.Setenv(envHarnessKind, "claude_code")
	provider, buildCtx, autoDir := newEntrypointProviderTestSetup(t)
	if err := os.WriteFile(filepath.Join(autoDir, "MEMORY.md"), []byte("- [doc](doc.md) — entry\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	out, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{},
		BuildCtx: buildCtx,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if out != nil {
		t.Fatalf("Resolve() = %q, want nil under claude_code overlay", *out)
	}
}

func TestMemoryEntrypointProviderSkipsTurnContext(t *testing.T) {
	t.Setenv(envHarnessKind, "")
	provider, buildCtx, autoDir := newEntrypointProviderTestSetup(t)
	if err := os.WriteFile(filepath.Join(autoDir, "MEMORY.md"), []byte("- [doc](doc.md) — entry\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	out, err := provider.Resolve(context.Background(), contract.SectionContext{
		Turn:     &contract.TurnInput{},
		BuildCtx: buildCtx,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if out != nil {
		t.Fatalf("Resolve() = %q, want nil for turn-only context", *out)
	}
}

func TestMemoryEntrypointProviderSectionName(t *testing.T) {
	provider := NewEntrypointProvider(&Config{Enabled: true}, nil, nil)
	if got := provider.SectionName(); got != contract.DynamicSectionMemoryEntrypoint {
		t.Fatalf("SectionName() = %q, want %q", got, contract.DynamicSectionMemoryEntrypoint)
	}
}

func TestCleanEntrypointContentEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "whitespace_only", in: "   \n  ", want: ""},
		{name: "bom_then_text", in: "\uFEFF- entry one", want: "- entry one"},
		{
			name: "frontmatter_then_body",
			in:   "---\nname: x\ntype: project\n---\n\n- entry one\n",
			want: "- entry one",
		},
		{
			name: "frontmatter_with_trailing_whitespace_on_fences",
			in:   "--- \nname: x\n--- \n\n- entry one\n",
			want: "- entry one",
		},
		{
			name: "html_comment_only_yields_empty",
			in:   "<!-- only a comment -->\n",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanEntrypointContent(tc.in)
			if got != tc.want {
				t.Fatalf("cleanEntrypointContent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

func TestMemoryEntrypointProviderHeaderUsesRelativeName(t *testing.T) {
	t.Setenv(envHarnessKind, "")
	provider, buildCtx, autoDir := newEntrypointProviderTestSetup(t)
	if err := os.WriteFile(filepath.Join(autoDir, "MEMORY.md"), []byte("- [doc](doc.md) — entry\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	out, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{},
		BuildCtx: buildCtx,
	})
	if err != nil || out == nil {
		t.Fatalf("Resolve() = (%v, %v), want non-nil", out, err)
	}
	got := *out
	if !strings.Contains(got, "Contents of MEMORY.md (source=auto):") {
		t.Fatalf("header missing relative MEMORY.md form:\n%s", got)
	}
	if strings.Contains(got, autoDir) {
		t.Fatalf("header leaked absolute auto-mem path %q:\n%s", autoDir, got)
	}
}

func TestSafeReadEntrypointRejectsSymlinkOutsideRoot(t *testing.T) {
	jail := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("- exfil\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}
	link := filepath.Join(jail, "MEMORY.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks not supported on this filesystem: %v", err)
	}
	raw, _, err := shared.SafeReadEntrypoint(jail, link)
	if !errors.Is(err, shared.ErrSafeReadContainment) {
		t.Fatalf("SafeReadEntrypoint() err = %v, want ErrSafeReadContainment; raw=%q", err, raw)
	}
}

func TestSafeReadEntrypointAcceptsInRoot(t *testing.T) {
	root := t.TempDir()
	index := filepath.Join(root, "MEMORY.md")
	if err := os.WriteFile(index, []byte("- entry\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	raw, _, err := shared.SafeReadEntrypoint(root, index)
	if err != nil {
		t.Fatalf("SafeReadEntrypoint() err = %v, want nil", err)
	}
	if !strings.Contains(string(raw), "- entry") {
		t.Fatalf("SafeReadEntrypoint() content = %q, want containing %q", raw, "- entry")
	}
}

func TestSafeReadEntrypointMissingRoot(t *testing.T) {
	_, _, err := shared.SafeReadEntrypoint(filepath.Join(t.TempDir(), "does-not-exist"), "/tmp/whatever/MEMORY.md")
	if !errors.Is(err, shared.ErrSafeReadNotFound) {
		t.Fatalf("SafeReadEntrypoint() err = %v, want ErrSafeReadNotFound", err)
	}
}

func TestMemoryEntrypointProviderRespectsInjectPromptEntrypoint(t *testing.T) {
	t.Setenv(envHarnessKind, "")
	provider, buildCtx, autoDir := newEntrypointProviderTestSetup(t)
	if err := os.WriteFile(filepath.Join(autoDir, "MEMORY.md"), []byte("- entry\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	// Disable via the underlying gate flag: SkipIndex causes
	// InjectMemoryIndex (and therefore InjectPromptEntrypoint) to be false.
	provider.cfg.SkipIndex = true
	out, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{},
		BuildCtx: buildCtx,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if out != nil {
		t.Fatalf("Resolve() = %q, want nil when InjectPromptEntrypoint is false", *out)
	}
}

func TestMemoryContextProviderResolveAlwaysReturnsNil(t *testing.T) {
	provider := mustNewContextProvider(t, &Config{Enabled: true, RootDir: t.TempDir(), ProjectRoot: newTestGitProjectRoot(t)})
	cases := []contract.SectionContext{
		{Start: &contract.StartInput{}},
		{Turn: &contract.TurnInput{ThreadID: "t1", UserText: "hi there"}},
		{},
	}
	for _, in := range cases {
		out, err := provider.Resolve(context.Background(), in)
		if err != nil {
			t.Fatalf("Resolve(%+v) error = %v", in, err)
		}
		if out != nil {
			t.Fatalf("Resolve(%+v) = %q, want nil after H1 turn-fallback removal", in, *out)
		}
	}
}
