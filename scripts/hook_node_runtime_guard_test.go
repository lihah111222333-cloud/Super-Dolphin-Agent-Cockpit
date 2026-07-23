package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookNodeRuntimeUsesExplicitRuntime(t *testing.T) {
	runtimeBin := writeHookNodeRuntime(t, filepath.Join(t.TempDir(), "runtime", "bin"), "v24.13.0")
	out, err := runHookNodeRuntime(t, runtimeBin, map[string]string{
		"SUPER_DOLPHIN_HOOK_NODE_BIN": runtimeBin,
	})
	if err != nil {
		t.Fatalf("configure explicit runtime: %v\n%s", err, out)
	}
	canonicalNode, err := filepath.EvalSymlinks(filepath.Join(runtimeBin, "node"))
	if err != nil {
		t.Fatalf("resolve canonical node: %v", err)
	}
	assertOutputContainsAll(
		t,
		out,
		"SUPER_DOLPHIN_HOOK_NODE_BIN",
		"v24.13.0",
		"CANONICAL_EXEC="+canonicalNode,
		"CANONICAL_VERSION=v24.13.0",
	)
}

func TestHookNodeRuntimeDiscoversCodexBundledRuntime(t *testing.T) {
	dependencies := filepath.Join(t.TempDir(), "codex-primary-runtime", "dependencies")
	overrideBin := filepath.Join(dependencies, "bin", "override")
	if err := os.MkdirAll(overrideBin, 0o755); err != nil {
		t.Fatalf("mkdir override bin: %v", err)
	}
	runtimeBin := writeHookNodeRuntime(t, filepath.Join(dependencies, "node", "bin"), "v24.13.0")

	out, err := runHookNodeRuntime(t, overrideBin, nil)
	if err != nil {
		t.Fatalf("discover Codex runtime: %v\n%s", err, out)
	}
	canonicalNode, err := filepath.EvalSymlinks(filepath.Join(runtimeBin, "node"))
	if err != nil {
		t.Fatalf("resolve bundled canonical node: %v", err)
	}
	assertOutputContainsAll(
		t,
		out,
		"Codex bundled runtime",
		runtimeBin,
		"v24.13.0",
		"CANONICAL_EXEC="+canonicalNode,
		"CANONICAL_VERSION=v24.13.0",
	)
}

func TestHookNodeRuntimeRejectsInvalidExplicitRuntime(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	out, err := runHookNodeRuntime(t, os.Getenv("PATH"), map[string]string{
		"SUPER_DOLPHIN_HOOK_NODE_BIN": missing,
	})
	if err == nil {
		t.Fatalf("invalid explicit runtime succeeded\n%s", out)
	}
	assertOutputContainsAll(t, out, "git hook Node 不可执行", filepath.Join(missing, "node"))
}

func TestHookNodeRuntimeRejectsNativeFallback(t *testing.T) {
	out, err := runHookNodeRuntime(t, t.TempDir(), nil)
	if err == nil {
		t.Fatalf("native fallback succeeded\n%s", out)
	}
	assertOutputContainsAll(t, out, "缺少受管 Node.js", "SUPER_DOLPHIN_HOOK_NODE_BIN")
}

func writeHookNodeRuntime(t *testing.T, binDir, version string) string {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime bin: %v", err)
	}
	canonicalBin, err := filepath.EvalSymlinks(binDir)
	if err != nil {
		t.Fatalf("resolve fake runtime bin: %v", err)
	}
	nodePath := filepath.Join(canonicalBin, "node")
	nodeBody := fmt.Sprintf(`#!/usr/bin/env bash
if [ "${1:-}" = "-e" ]; then exit 0; fi
if [ "${1:-}" = "-p" ]; then
  case "${2:-}" in
    *realpathSync*) printf '%%s\n' '%s' ;;
    *process.version*) printf '%%s\n' '%s' ;;
    *) exit 2 ;;
  esac
  exit 0
fi
printf '%%s\n' '%s'
`, nodePath, version, version)
	for name, body := range map[string]string{
		"node": nodeBody,
		"npm":  "#!/usr/bin/env bash\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	return binDir
}

func runHookNodeRuntime(t *testing.T, pathValue string, extra map[string]string) (string, error) {
	t.Helper()
	script := filepath.Join("configure_hook_node_runtime.sh")
	cmd := exec.Command(
		"bash",
		"-c",
		"source \"$1\" && configure_hook_node_runtime && node --version && printf 'CANONICAL_EXEC=%s\\nCANONICAL_VERSION=%s\\n' \"$SUPER_DOLPHIN_CANONICAL_NODE_EXEC_PATH\" \"$SUPER_DOLPHIN_CANONICAL_NODE_VERSION\"",
		"hook-runtime-test",
		script,
	)
	cmd.Dir = "../scripts"
	env := append([]string{}, os.Environ()...)
	env = append(env, "PATH="+pathValue+":/usr/bin:/bin:/usr/sbin:/sbin")
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
