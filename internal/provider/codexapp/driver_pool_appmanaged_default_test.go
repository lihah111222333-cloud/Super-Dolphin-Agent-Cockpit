package codexapp

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

func TestStartSessionMapsLegacyPackagedDefaultHomeToAppManagedRelayHome(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	superHome := filepath.Join(t.TempDir(), "Library", "Application Support", "Super Dolphin")
	userHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	workDir := t.TempDir()
	var gotHome string
	pool := NewServerPool(slog.Default(), func(_ context.Context, home, _ string) (SpawnedServer, error) {
		gotHome = home
		return nil, errors.New("stop after acquire")
	}, PoolConfig{SpawnBackoff: 1})
	defer pool.Close(context.Background())
	mirror := &recordingSkillMirrorReconciler{}
	d := &driver{logRuntime: testLoggerRuntime(t), approvals: testApprovalManager(), logger: slog.Default(), pool: pool, mirror: mirror}

	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID:       "agent-legacy-default",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
		Config: map[string]any{
			contract.CodexHomeKey:          "~/.super-dolphin/providers/codex",
			contract.CodexInstanceKeyKey:   "default",
			contract.CodexModelProviderKey: defaultBootstrapModelProvider,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "stop after acquire") {
		t.Fatalf("StartSession() error = %v, want acquire error after reconcile", err)
	}
	wantHome, err := filepath.EvalSymlinks(filepath.Join(superHome, "providers", "codex"))
	if err != nil {
		t.Fatalf("EvalSymlinks app-managed codex home: %v", err)
	}
	if gotHome != wantHome {
		t.Fatalf("pool codex home = %q, want app-managed relay home %q", gotHome, wantHome)
	}
	assertExplicitCodexMirrorTargets(t, mirror.targets, workDir, wantHome)
}
