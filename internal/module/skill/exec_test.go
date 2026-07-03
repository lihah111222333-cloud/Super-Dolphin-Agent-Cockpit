package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecCommandRejectsShellMetacharacters(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	svc := &service{projectRoot: project}
	if _, err := svc.ExecCommand(context.Background(), "cat", []string{"a|b"}, project, nil); err == nil {
		t.Fatal("ExecCommand expected shell metacharacter validation error")
	}
}

func TestExecCommandRejectsWrappedDangerousCommand(t *testing.T) {
	t.Parallel()

	svc := &service{projectRoot: t.TempDir()}
	if _, err := svc.ExecCommand(context.Background(), "env", []string{"SAFE=1", "rm", "-rf", "/"}, "", nil); err == nil {
		t.Fatal("ExecCommand expected wrapped dangerous command validation error")
	}
}

func TestExecCommandRejectsShellInterpreter(t *testing.T) {
	t.Parallel()

	svc := &service{projectRoot: t.TempDir()}
	if _, err := svc.ExecCommand(context.Background(), "sh", []string{"-lc", "printf ok"}, "", nil); err == nil {
		t.Fatal("ExecCommand expected shell interpreter validation error")
	}
}

func TestExecCommandRejectsCodeExecutionRuntime(t *testing.T) {
	t.Parallel()

	svc := &service{projectRoot: t.TempDir()}
	if _, err := svc.ExecCommand(context.Background(), "node", []string{"-e", "console.log(1)"}, "", nil); err == nil {
		t.Fatal("ExecCommand expected code execution runtime validation error")
	}
}

func TestExecCommandRejectsCWDOutsideWorkspaceRoots(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	svc := &service{projectRoot: project}

	if result, err := svc.ExecCommand(context.Background(), "cat", []string{outside}, string(filepath.Separator), nil); err == nil {
		t.Fatalf("ExecCommand read outside workspace: stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestExecCommandRejectsAbsoluteArgOutsideWorkspaceRoots(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	svc := &service{projectRoot: project}

	if result, err := svc.ExecCommand(context.Background(), "cat", []string{outside}, project, nil); err == nil {
		t.Fatalf("ExecCommand accepted absolute outside arg: stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestExecCommandRejectsDotDotArgEscapingWorkspace(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	project := filepath.Join(parent, "repo")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(parent, "outside.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	svc := &service{projectRoot: project}

	if result, err := svc.ExecCommand(context.Background(), "cat", []string{"../outside.txt"}, project, nil); err == nil {
		t.Fatalf("ExecCommand accepted dot-dot escape: stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestExecCommandRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	link := filepath.Join(project, "secret-link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink outside file: %v", err)
	}
	svc := &service{projectRoot: project}

	if result, err := svc.ExecCommand(context.Background(), "cat", []string{"secret-link.txt"}, project, nil); err == nil {
		t.Fatalf("ExecCommand followed symlink escape: stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestExecCommandRejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	svc := &service{projectRoot: project}
	if _, err := svc.ExecCommand(context.Background(), "printf", []string{"ok"}, project, nil); err == nil {
		t.Fatal("ExecCommand accepted command outside the allowlist")
	}
}

func TestExecCommandRejectsEmbeddedReadAndSecondaryExecutionCommands(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "file.txt"), []byte("workspace"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
	tests := []struct {
		command string
		args    []string
	}{
		{command: "awk", args: []string{"BEGIN {print 1}"}},
		{command: "find", args: []string{"."}},
		{command: "sed", args: []string{"-n", "1p", "file.txt"}},
		{command: "less", args: []string{"--version"}},
		{command: "more", args: []string{"--version"}},
	}
	svc := &service{projectRoot: project}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			if result, err := svc.ExecCommand(context.Background(), tt.command, tt.args, project, nil); err == nil {
				t.Fatalf("ExecCommand accepted %s: stdout=%q stderr=%q", tt.command, result.Stdout, result.Stderr)
			}
		})
	}
}

func TestExecCommandRejectsMissingTrustedWorkspaceRoots(t *testing.T) {
	t.Parallel()

	svc := &service{}
	if result, err := svc.ExecCommand(context.Background(), "cat", []string{"SKILL.md"}, "", nil); err == nil {
		t.Fatalf("ExecCommand accepted missing workspace root: stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestExecCommandCatReadsWorkspaceFile(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("inside"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
	svc := &service{projectRoot: project}

	result, err := svc.ExecCommand(context.Background(), "cat", []string{"README.md"}, project, nil)
	if err != nil {
		t.Fatalf("ExecCommand returned error: %v", err)
	}
	if got := strings.TrimPrefix(result.Stdout, lspPreferenceHint); got != "inside" {
		t.Fatalf("ExecCommand stdout = %q, want file content", result.Stdout)
	}
	if !sameCleanPath(result.CWD, project) {
		t.Fatalf("ExecCommand cwd = %q, want %q", result.CWD, project)
	}
}

func TestLimitedBufferReportsFullWriteAfterTruncating(t *testing.T) {
	t.Parallel()

	buffer := &limitedBuffer{limit: 3}
	n, err := buffer.Write([]byte("abcde"))
	if err != nil {
		t.Fatalf("limitedBuffer.Write() error = %v", err)
	}
	if n != 5 {
		t.Fatalf("limitedBuffer.Write() n = %d, want 5", n)
	}
	if got := buffer.String(); got != "abc" {
		t.Fatalf("limitedBuffer.String() = %q, want capped content", got)
	}

	n, err = buffer.Write([]byte("fg"))
	if err != nil {
		t.Fatalf("limitedBuffer.Write() after cap error = %v", err)
	}
	if n != 2 {
		t.Fatalf("limitedBuffer.Write() after cap n = %d, want 2", n)
	}
	if got := buffer.String(); got != "abc" {
		t.Fatalf("limitedBuffer.String() after cap = %q, want unchanged cap", got)
	}
}

func TestExecCommandFallsBackToProjectRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	svc := &service{projectRoot: root}
	out, err := svc.ExecCommand(context.Background(), "pwd", nil, "", nil)
	if err != nil {
		t.Fatalf("ExecCommand returned error: %v", err)
	}
	if !sameCleanPath(out.CWD, root) {
		t.Fatalf("ExecCommand cwd mismatch: got %q want %q", out.CWD, root)
	}
	if got := strings.TrimSpace(out.Stdout); !sameCleanPath(got, root) {
		t.Fatalf("ExecCommand stdout mismatch: got %q want %q", got, root)
	}
}

func TestExecCommandRejectsEnvOverlay(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	svc := &service{projectRoot: project}
	if _, err := svc.ExecCommand(context.Background(), "pwd", nil, project, map[string]string{"LOG_LEVEL": "debug"}); err == nil {
		t.Fatal("ExecCommand accepted env overlay")
	}
}

func TestNewServiceConfiguresProjectRootAndHTTPTimeout(t *testing.T) {
	t.Parallel()

	project := filepath.Clean("/tmp/project")
	impl, ok := NewService(" /tmp/project ").(*service)
	if !ok {
		t.Fatal("NewService type assertion failed")
	}
	if impl.projectRoot != project {
		t.Fatalf("projectRoot mismatch: got %q", impl.projectRoot)
	}
	if got, want := impl.projectSkillsRoot, filepath.Join(project, ".agents", "skills"); got != want {
		t.Fatalf("projectSkillsRoot mismatch: got %q want %q", got, want)
	}
	if impl.http == nil || impl.http.Timeout != 15*time.Second {
		t.Fatalf("http timeout mismatch: %#v", impl.http)
	}
}

func TestNewServiceOmitsProjectSkillsRootWhenProjectRootEmpty(t *testing.T) {
	t.Parallel()

	impl, ok := NewService("   ").(*service)
	if !ok {
		t.Fatal("NewService type assertion failed")
	}
	if impl.projectRoot != "" {
		t.Fatalf("projectRoot mismatch: got %q", impl.projectRoot)
	}
	if impl.projectSkillsRoot != "" {
		t.Fatalf("projectSkillsRoot mismatch: got %q want empty", impl.projectSkillsRoot)
	}
}

func TestNewServiceIgnoresLegacySkillsRootEnvOverride(t *testing.T) {
	override := t.TempDir()
	t.Setenv("SKILLS_ROOT", "  "+override+"  ")

	impl, ok := NewService("").(*service)
	if !ok {
		t.Fatal("NewService type assertion failed")
	}
	if impl.root != "" {
		t.Fatalf("legacy root = %q, want empty", impl.root)
	}
}
