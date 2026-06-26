package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExecCommandHelperProcess(t *testing.T) {
	if os.Getenv("TEST_E2E_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	switch args[1] {
	case "cwd":
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(2)
		}
		fmt.Fprint(os.Stdout, cwd)
	case "env":
		if len(args) < 3 {
			os.Exit(2)
		}
		value := os.Getenv(args[2])
		fmt.Fprint(os.Stdout, value)
		if value == "" {
			os.Exit(1)
		}
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

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

func TestExecCommandRejectsCodeExecutionRuntime(t *testing.T) {
	t.Parallel()

	svc := &service{}
	if _, err := svc.ExecCommand(context.Background(), "node", []string{"-e", "console.log(1)"}, "", nil); err == nil {
		t.Fatal("ExecCommand expected code execution runtime validation error")
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
	command, args, env := execTestHelperCommand(t, "cwd")
	out, err := svc.ExecCommand(context.Background(), command, args, "", env)
	if err != nil {
		t.Fatalf("ExecCommand returned error: %v", err)
	}
	if out.CWD != root {
		t.Fatalf("ExecCommand cwd mismatch: got %q want %q", out.CWD, root)
	}
	if got := normalizePWDOutput(strings.TrimSpace(out.Stdout)); !sameCleanPath(got, root) {
		t.Fatalf("ExecCommand stdout mismatch: got %q want %q", got, root)
	}
}

func normalizePWDOutput(output string) string {
	if runtime.GOOS == "windows" && strings.HasPrefix(output, "/tmp/") {
		return filepath.Join(os.TempDir(), filepath.FromSlash(strings.TrimPrefix(output, "/tmp/")))
	}
	if runtime.GOOS == "windows" && len(output) >= 3 && output[0] == '/' && output[2] == '/' {
		return strings.ToUpper(output[1:2]) + `:\` + strings.ReplaceAll(output[3:], "/", `\`)
	}
	return output
}

func execTestHelperCommand(t *testing.T, mode string, extra ...string) (string, []string, map[string]string) {
	t.Helper()
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test helper executable: %v", err)
	}
	args := append([]string{"-test.run=TestExecCommandHelperProcess", "--", mode}, extra...)
	return command, args, map[string]string{"TEST_E2E_HELPER_PROCESS": "1"}
}

func TestBuildExecEnvDropsSensitiveProviderEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "parent-secret")
	t.Setenv("ANTHROPIC_API_KEY", "parent-secret")
	t.Setenv("CODEX_HOME", "parent-home")
	t.Setenv("MCP_TOKEN", "parent-secret")
	t.Setenv("TEST_E2E_SKILL_ENV", "allowed")

	env := buildExecEnv("", map[string]string{
		"OPENAI_API_KEY":     "overlay-secret",
		"TEST_E2E_SKILL_ENV": "overlay",
	})
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "CODEX_HOME", "MCP_TOKEN"} {
		if value := execTestEnvValue(env, key); value != "" {
			t.Fatalf("%s leaked into exec env as %q", key, value)
		}
	}
	if got := execTestEnvValue(env, "TEST_E2E_SKILL_ENV"); got != "overlay" {
		t.Fatalf("TEST_E2E_SKILL_ENV = %q, want overlay", got)
	}
}

func TestExecCommandInjectsWhitelistedEnv(t *testing.T) {
	t.Setenv("TEST_E2E_SKILL_ENV", "allowed")
	t.Setenv("UNRELATED_SKILL_ENV", "blocked")

	svc := &service{}
	command, args, env := execTestHelperCommand(t, "env", "TEST_E2E_SKILL_ENV")
	allowed, err := svc.ExecCommand(context.Background(), command, args, "", env)
	if err != nil {
		t.Fatalf("ExecCommand allowed env returned error: %v", err)
	}
	if got := strings.TrimSpace(allowed.Stdout); got != "allowed" {
		t.Fatalf("allowed env mismatch: got %q", got)
	}
	command, args, env = execTestHelperCommand(t, "env", "UNRELATED_SKILL_ENV")
	blocked, err := svc.ExecCommand(context.Background(), command, args, "", env)
	if err != nil {
		t.Fatalf("ExecCommand blocked env returned error: %v", err)
	}
	if blocked.ExitCode == 0 || strings.TrimSpace(blocked.Stdout) != "" {
		t.Fatalf("blocked env leaked: exit=%d stdout=%q", blocked.ExitCode, blocked.Stdout)
	}
}

func execTestEnvValue(env []string, key string) string {
	want := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, want) {
			return strings.TrimPrefix(entry, want)
		}
	}
	return ""
}

func TestExecCommandOverlaysAllowedEnv(t *testing.T) {
	t.Setenv("TEST_E2E_SKILL_ENV", "base")
	svc := &service{}

	command, args, env := execTestHelperCommand(t, "env", "TEST_E2E_SKILL_ENV")
	env["TEST_E2E_SKILL_ENV"] = "override"
	env["UNRELATED_SKILL_ENV"] = "blocked"
	allowed, err := svc.ExecCommand(context.Background(), command, args, "", env)
	if err != nil {
		t.Fatalf("ExecCommand override env returned error: %v", err)
	}
	if got := strings.TrimSpace(allowed.Stdout); got != "override" {
		t.Fatalf("override env mismatch: got %q", got)
	}

	command, args, env = execTestHelperCommand(t, "env", "UNRELATED_SKILL_ENV")
	env["UNRELATED_SKILL_ENV"] = "blocked"
	blocked, err := svc.ExecCommand(context.Background(), command, args, "", env)
	if err != nil {
		t.Fatalf("ExecCommand blocked overlay returned error: %v", err)
	}
	if blocked.ExitCode == 0 || strings.TrimSpace(blocked.Stdout) != "" {
		t.Fatalf("blocked overlay leaked: exit=%d stdout=%q", blocked.ExitCode, blocked.Stdout)
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
	if got, want := impl.projectSkillsRoot, filepath.Join(project, ".agent", "skills"); got != want {
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
