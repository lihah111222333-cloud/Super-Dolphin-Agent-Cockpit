//go:build e2e

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

func TestMcpLSPCompiledSidecarsConcurrentOwnerIsolation_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("owner isolation E2E is not a short test")
	}
	root := t.TempDir()
	productRoot := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_HOME"))
	if productRoot == "" {
		productRoot = filepath.Join(t.TempDir(), "shared-product-home")
		if err := os.MkdirAll(productRoot, 0o700); err != nil {
			t.Fatalf("create shared product home: %v", err)
		}
		if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
			t.Fatalf("protect shared product home: %v", err)
		}
	}
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	binary := buildMcpLSPBinaryForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	commonEnv := []string{
		"SUPER_DOLPHIN_HOME=" + productRoot,
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=" + repoRoot,
	}
	aEnv := append(append([]string(nil), commonEnv...), "SUPER_DOLPHIN_SIDECAR_OWNER_ID=e2e-owner-a")
	bEnv := append(append([]string(nil), commonEnv...), "SUPER_DOLPHIN_SIDECAR_OWNER_ID=e2e-owner-b")
	a := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), aEnv)
	b := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), bEnv)

	initParams := map[string]any{"protocolVersion": "2024-11-05"}
	if got := a.call(t, "initialize", initParams); got.Error != nil {
		t.Fatalf("owner a initialize error: %v; stderr=%s", got.Error, a.stderrString())
	}
	if got := b.call(t, "initialize", initParams); got.Error != nil {
		t.Fatalf("owner b initialize error: %v; stderr=%s", got.Error, b.stderrString())
	}
	for owner, client := range map[string]*mcpLSPBinaryClient{"e2e-owner-a": a, "e2e-owner-b": b} {
		result := client.call(t, "tools/list", map[string]any{})
		if result.Result.IsError {
			t.Fatalf("%s tools/list failed: %#v; stderr=%s", owner, result.Result, client.stderrString())
		}
	}
	a.close(t)
	b.close(t)

	aState := filepath.Join(productRoot, "runtime-state", "sidecars", "e2e-owner-a")
	bState := filepath.Join(productRoot, "runtime-state", "sidecars", "e2e-owner-b")
	if filepath.Clean(aState) == filepath.Clean(bState) {
		t.Fatal("owner runtime state paths overlap")
	}
	for owner, state := range map[string]string{"e2e-owner-a": aState, "e2e-owner-b": bState} {
		if _, err := os.Stat(filepath.Join(state, "log", binaryName)); err != nil {
			t.Fatalf("%s owner log state missing at %s: %v", owner, state, err)
		}
	}
}
