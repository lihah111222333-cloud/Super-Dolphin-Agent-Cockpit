package codexapp

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

func TestStartSessionDefaultCodexHomeDoesNotRedirectPersonalMirror(t *testing.T) {
	t.Setenv(poolRoutingEnvVar, "1")
	superHome := filepath.Join(t.TempDir(), "sd-home")
	userHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv(providershared.SuperDolphinHomeEnv, superHome)
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	mustCanonicalCodexHome(t, userHome)
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
		AgentID:       "agent-default-codex-home",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
		Config: map[string]any{
			contract.CodexHomeKey:          "~/.codex",
			contract.CodexInstanceKeyKey:   "default",
			contract.CodexModelProviderKey: "openai",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "stop after acquire") {
		t.Fatalf("StartSession() error = %v, want acquire error after reconcile", err)
	}
	wantHome, err := filepath.EvalSymlinks(filepath.Join(userHome, ".codex"))
	if err != nil {
		t.Fatalf("EvalSymlinks default codex home: %v", err)
	}
	if gotHome != wantHome {
		t.Fatalf("pool codex home = %q, want %q", gotHome, wantHome)
	}
	assertCodexMirrorTargets(t, mirror.targets, workDir, userHome)
	if _, err := os.Stat(filepath.Join(userHome, ".codex", "skills")); !os.IsNotExist(err) {
		t.Fatalf("default codex skills root stat = %v, want not created", err)
	}
}
