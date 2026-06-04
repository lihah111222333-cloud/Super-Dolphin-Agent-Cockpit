package app

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestNewUIDesktopScriptValidatesLocalPostgresURLPorts(t *testing.T) {
	harness := newUIDesktopPostgresHarness(t)
	successScript := harness + `
set -euo pipefail
postgres_is_local_database_url "$DATABASE_URL" "DATABASE_URL"
printf '%s:%s\n' "$POSTGRES_URL_HOST" "$POSTGRES_URL_PORT"
`

	successCases := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "explicit legal localhost port",
			url:  "postgres://super_dolphin@localhost:15432/super_dolphin?sslmode=disable",
			want: "localhost:15432\n",
		},
		{
			name: "missing local port defaults to 5432",
			url:  "postgres://super_dolphin@127.0.0.1/super_dolphin",
			want: "127.0.0.1:5432\n",
		},
	}
	for _, tc := range successCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, exitCode := runBashSnippet(t, successScript, []string{"DATABASE_URL=" + tc.url})
			if exitCode != 0 {
				t.Fatalf("expected success, exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
			}
			if stdout != tc.want {
				t.Fatalf("stdout = %q, want %q", stdout, tc.want)
			}
		})
	}

	failureScript := harness + `
set -euo pipefail
postgres_is_local_database_url "$DATABASE_URL" "DATABASE_URL"
`
	failureCases := []struct {
		name    string
		url     string
		wantErr string
	}{
		{
			name:    "non numeric port",
			url:     "postgres://super_dolphin@localhost:not-a-port/super_dolphin",
			wantErr: "not-a-port",
		},
		{
			name:    "out of range port",
			url:     "postgres://super_dolphin@localhost:70000/super_dolphin",
			wantErr: "70000",
		},
		{
			name:    "port with space",
			url:     "postgres://super_dolphin@localhost:5432 -o/super_dolphin",
			wantErr: "5432 -o",
		},
		{
			name:    "port with query fragment",
			url:     "postgres://super_dolphin@localhost:5432?sslmode=disable",
			wantErr: "5432?sslmode=disable",
		},
	}
	for _, tc := range failureCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, exitCode := runBashSnippet(t, failureScript, []string{"DATABASE_URL=" + tc.url})
			if exitCode == 0 {
				t.Fatalf("expected failure, stdout=%q stderr=%q", stdout, stderr)
			}
			if !strings.Contains(stderr, "DATABASE_URL") || !strings.Contains(stderr, tc.wantErr) {
				t.Fatalf("stderr = %q, want DATABASE_URL and %q", stderr, tc.wantErr)
			}
		})
	}
}

func TestNewUIDesktopScriptValidatesPostgresConnectionStringLocalPort(t *testing.T) {
	harness := newUIDesktopPostgresHarness(t)
	script := harness + `
set -euo pipefail
resolve_postgres_bin_dir() { printf '/tmp/postgres/bin\n'; }
configure_postgres_library_path() { :; }
resolve_postgres_share_dir() { printf '/tmp/postgres/share\n'; }
configure_dev_postgres_runtime
`

	stdout, stderr, exitCode := runBashSnippet(t, script, []string{
		"POSTGRES_CONNECTION_STRING=postgres://super_dolphin@127.0.0.1:abc/super_dolphin",
		"SUPER_DOLPHIN_LOCAL_POSTGRES_PORT=55433",
	})
	if exitCode == 0 {
		t.Fatalf("expected failure, stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stderr, "POSTGRES_CONNECTION_STRING") || !strings.Contains(stderr, "abc") {
		t.Fatalf("stderr = %q, want POSTGRES_CONNECTION_STRING and abc", stderr)
	}
}

func TestNewUIDesktopScriptValidatesLocalPostgresPortBeforeDefaultDatabaseURL(t *testing.T) {
	harness := newUIDesktopPostgresHarness(t)
	script := harness + `
set -euo pipefail
resolve_postgres_bin_dir() { printf '/tmp/postgres/bin\n'; }
configure_postgres_library_path() { :; }
resolve_postgres_share_dir() { printf '/tmp/postgres/share\n'; }
SUPER_DOLPHIN_LOCAL_POSTGRES_PORT="${SUPER_DOLPHIN_LOCAL_POSTGRES_PORT:-55433}"
validate_postgres_port "SUPER_DOLPHIN_LOCAL_POSTGRES_PORT" "$SUPER_DOLPHIN_LOCAL_POSTGRES_PORT"
configure_dev_postgres_runtime
printf '%s\n' "$DATABASE_URL"
`

	successCases := []struct {
		name string
		env  []string
		want string
	}{
		{
			name: "default override value is valid",
			want: "  → local PostgreSQL enabled for dev runtime\npostgres://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable\n",
		},
		{
			name: "explicit legal override value is valid",
			env:  []string{"SUPER_DOLPHIN_LOCAL_POSTGRES_PORT=15433"},
			want: "  → local PostgreSQL enabled for dev runtime\npostgres://super_dolphin@127.0.0.1:15433/super_dolphin?sslmode=disable\n",
		},
	}
	for _, tc := range successCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, exitCode := runBashSnippet(t, script, tc.env)
			if exitCode != 0 {
				t.Fatalf("expected success, exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
			}
			if stdout != tc.want {
				t.Fatalf("stdout = %q, want %q", stdout, tc.want)
			}
		})
	}

	failureCases := []struct {
		name string
		env  string
		want string
	}{
		{name: "non numeric override", env: "SUPER_DOLPHIN_LOCAL_POSTGRES_PORT=abc", want: "abc"},
		{name: "out of range override", env: "SUPER_DOLPHIN_LOCAL_POSTGRES_PORT=70000", want: "70000"},
		{name: "override with argument fragment", env: "SUPER_DOLPHIN_LOCAL_POSTGRES_PORT=55433 -o", want: "55433 -o"},
	}
	for _, tc := range failureCases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, exitCode := runBashSnippet(t, script, []string{tc.env})
			if exitCode == 0 {
				t.Fatalf("expected failure, stdout=%q stderr=%q", stdout, stderr)
			}
			if !strings.Contains(stderr, "SUPER_DOLPHIN_LOCAL_POSTGRES_PORT") || !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr = %q, want SUPER_DOLPHIN_LOCAL_POSTGRES_PORT and %q", stderr, tc.want)
			}
		})
	}
}

func newUIDesktopPostgresHarness(t *testing.T) string {
	t.Helper()
	text := readRootScript(t, "../../run-new-ui-desktop.sh")
	functions := []string{
		"validate_postgres_port",
		"postgres_is_local_database_url",
		"configure_dev_postgres_runtime",
	}
	var harness strings.Builder
	for _, name := range functions {
		harness.WriteString(extractBashFunction(t, text, name))
		harness.WriteByte('\n')
	}
	return harness.String()
}

func extractBashFunction(t *testing.T, text, name string) string {
	t.Helper()
	marker := "\n" + name + "() {"
	start := strings.Index(text, marker)
	if start >= 0 {
		start++
	} else if strings.HasPrefix(text, name+"() {") {
		start = 0
	} else {
		t.Fatalf("missing bash function %s", name)
	}
	rest := text[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("missing end of bash function %s", name)
	}
	return rest[:end+3]
}

func runBashSnippet(t *testing.T, script string, extraEnv []string) (string, string, int) {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = newUIDesktopScriptTestEnv(extraEnv)
	var stdout strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), stderr.String(), exitErr.ExitCode()
	}
	t.Fatalf("run bash snippet: %v", err)
	return "", "", 1
}

func newUIDesktopScriptTestEnv(extraEnv []string) []string {
	blocked := map[string]struct{}{
		"DATABASE_URL":                      {},
		"POSTGRES_CONNECTION_STRING":        {},
		"SUPER_DOLPHIN_LOCAL_POSTGRES_PORT": {},
		"SUPER_DOLPHIN_PROCESS_ROLE":        {},
		"SUPER_DOLPHIN_POSTGRES_BIN_DIR":    {},
		"SUPER_DOLPHIN_POSTGRES_SHARE_DIR":  {},
	}
	env := make([]string, 0, len(os.Environ())+len(extraEnv))
	for _, entry := range os.Environ() {
		key := strings.SplitN(entry, "=", 2)[0]
		if _, skip := blocked[key]; skip {
			continue
		}
		env = append(env, entry)
	}
	return append(env, extraEnv...)
}
