package skill

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestExecCommandRejectsShellMetacharacters(t *testing.T) {
	t.Parallel()

	svc := &service{}
	if _, err := svc.ExecCommand(context.Background(), "printf", []string{"a|b"}, "", nil); err == nil {
		t.Fatal("ExecCommand expected shell metacharacter validation error")
	}
}

func TestExecCommandRejectsWrappedDangerousCommand(t *testing.T) {
	t.Parallel()

	svc := &service{}
	if _, err := svc.ExecCommand(context.Background(), "env", []string{"SAFE=1", "rm", "-rf", "/"}, "", nil); err == nil {
		t.Fatal("ExecCommand expected wrapped dangerous command validation error")
	}
}

func TestExecCommandRejectsShellInterpreter(t *testing.T) {
	t.Parallel()

	svc := &service{}
	if _, err := svc.ExecCommand(context.Background(), "sh", []string{"-lc", "printf ok"}, "", nil); err == nil {
		t.Fatal("ExecCommand expected shell interpreter validation error")
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
	if out.CWD != root {
		t.Fatalf("ExecCommand cwd mismatch: got %q want %q", out.CWD, root)
	}
	if got := strings.TrimSpace(out.Stdout); got != root {
		t.Fatalf("ExecCommand stdout mismatch: got %q want %q", got, root)
	}
}

func TestExecCommandInjectsWhitelistedEnv(t *testing.T) {
	t.Setenv("TEST_E2E_SKILL_ENV", "allowed")
	t.Setenv("UNRELATED_SKILL_ENV", "blocked")

	svc := &service{}
	allowed, err := svc.ExecCommand(context.Background(), "printenv", []string{"TEST_E2E_SKILL_ENV"}, "", nil)
	if err != nil {
		t.Fatalf("ExecCommand allowed env returned error: %v", err)
	}
	if got := strings.TrimSpace(allowed.Stdout); got != "allowed" {
		t.Fatalf("allowed env mismatch: got %q", got)
	}
	blocked, err := svc.ExecCommand(context.Background(), "printenv", []string{"UNRELATED_SKILL_ENV"}, "", nil)
	if err != nil {
		t.Fatalf("ExecCommand blocked env returned error: %v", err)
	}
	if blocked.ExitCode == 0 || strings.TrimSpace(blocked.Stdout) != "" {
		t.Fatalf("blocked env leaked: exit=%d stdout=%q", blocked.ExitCode, blocked.Stdout)
	}
}

func TestExecCommandOverlaysAllowedEnv(t *testing.T) {
	t.Setenv("TEST_E2E_SKILL_ENV", "base")
	svc := &service{}

	allowed, err := svc.ExecCommand(context.Background(), "printenv", []string{"TEST_E2E_SKILL_ENV"}, "", map[string]string{
		"TEST_E2E_SKILL_ENV":  "override",
		"UNRELATED_SKILL_ENV": "blocked",
	})
	if err != nil {
		t.Fatalf("ExecCommand override env returned error: %v", err)
	}
	if got := strings.TrimSpace(allowed.Stdout); got != "override" {
		t.Fatalf("override env mismatch: got %q", got)
	}

	blocked, err := svc.ExecCommand(context.Background(), "printenv", []string{"UNRELATED_SKILL_ENV"}, "", map[string]string{
		"UNRELATED_SKILL_ENV": "blocked",
	})
	if err != nil {
		t.Fatalf("ExecCommand blocked overlay returned error: %v", err)
	}
	if blocked.ExitCode == 0 || strings.TrimSpace(blocked.Stdout) != "" {
		t.Fatalf("blocked overlay leaked: exit=%d stdout=%q", blocked.ExitCode, blocked.Stdout)
	}
}

func TestNewServiceConfiguresProjectRootAndHTTPTimeout(t *testing.T) {
	t.Parallel()

	impl, ok := NewService(" /tmp/project ").(*service)
	if !ok {
		t.Fatal("NewService type assertion failed")
	}
	if impl.projectRoot != "/tmp/project" {
		t.Fatalf("projectRoot mismatch: got %q", impl.projectRoot)
	}
	if got, want := impl.projectSkillsRoot, "/tmp/project/.agent/skills"; got != want {
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
