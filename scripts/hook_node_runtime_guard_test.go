package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookNodeRuntimeUsesExplicitRuntime(t *testing.T) {
	runtimeBin := writeHookNodeRuntime(t, filepath.Join(t.TempDir(), "runtime", "bin"), "explicit-node")
	out, err := runHookNodeRuntime(t, runtimeBin, map[string]string{
		"SUPER_DOLPHIN_HOOK_NODE_BIN": runtimeBin,
	})
	if err != nil {
		t.Fatalf("configure explicit runtime: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "SUPER_DOLPHIN_HOOK_NODE_BIN", "explicit-node")
}

func TestHookNodeRuntimeDiscoversCodexBundledRuntime(t *testing.T) {
	dependencies := filepath.Join(t.TempDir(), "codex-primary-runtime", "dependencies")
	overrideBin := filepath.Join(dependencies, "bin", "override")
	if err := os.MkdirAll(overrideBin, 0o755); err != nil {
		t.Fatalf("mkdir override bin: %v", err)
	}
	runtimeBin := writeHookNodeRuntime(t, filepath.Join(dependencies, "node", "bin"), "bundled-node")

	out, err := runHookNodeRuntime(t, overrideBin, nil)
	if err != nil {
		t.Fatalf("discover Codex runtime: %v\n%s", err, out)
	}
	assertOutputContainsAll(t, out, "Codex bundled runtime", runtimeBin, "bundled-node")
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

func writeHookNodeRuntime(t *testing.T, binDir, version string) string {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime bin: %v", err)
	}
	for name, body := range map[string]string{
		"node": "#!/usr/bin/env bash\nif [ \"${1:-}\" = \"-e\" ]; then exit 0; fi\nprintf '%s\\n' '" + version + "'\n",
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
	cmd := exec.Command("bash", "-c", "source \"$1\" && configure_hook_node_runtime && node --version", "hook-runtime-test", script)
	cmd.Dir = "../scripts"
	env := append([]string{}, os.Environ()...)
	env = append(env, "PATH="+pathValue+":"+os.Getenv("PATH"))
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
