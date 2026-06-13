package claudecli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestStartSessionManifestIncludesAdditionalWorkspaceRoots(t *testing.T) {
	next := newBufferedTransport(t, "thread-1")
	var launched dto.MCPManifest
	d := newTestDriverWithLaunch(t, &recordingMirrorReconciler{}, func(_, _, _, _ string, _ cliLaunchConfig, manifest dto.MCPManifest, _ string) (*transport, func(), error) {
		launched = manifest
		return next.tr, nil, nil
	})

	workDir := t.TempDir()
	extraDir := t.TempDir()
	sess, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "claude",
		AgentID:  "agent-1",
		CWD:      workDir,
		Config: map[string]any{
			"additionalWorkingDirectories": []string{extraDir},
		},
		StartAssembly: contract.StartAssembly{BaseInstructions: "base"},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	if s, ok := sess.(*session); ok {
		defer func() {
			if err := s.Close(context.Background()); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		}()
		defer next.finish()
	}

	assertManifestLSPRoots(t, launched, []string{workDir, extraDir})
}

func TestResumeSessionManifestIncludesAdditionalWorkspaceRoots(t *testing.T) {
	next := newBufferedTransport(t, "thread-1")
	var launched dto.MCPManifest
	d := newTestDriverWithLaunch(t, &recordingMirrorReconciler{}, func(_, _, _, _ string, _ cliLaunchConfig, manifest dto.MCPManifest, _ string) (*transport, func(), error) {
		launched = manifest
		return next.tr, nil, nil
	})

	workDir := t.TempDir()
	extraDir := t.TempDir()
	binaryDir := filepath.Join(t.TempDir(), "super-agent-bin")
	sess, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{
		Provider:         "claude",
		AgentID:          "agent-1",
		ThreadID:         "thread-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              workDir,
		Config: map[string]any{
			"additionalWorkingDirectories": []string{extraDir},
			"autoApprove":                  []string{"lsp_workspace_info"},
			"binary_dir":                   binaryDir,
			"env":                          map[string]any{"CUSTOM_ENV": "value"},
		},
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if s, ok := sess.(*session); ok {
		defer func() {
			if err := s.Close(context.Background()); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		}()
		defer next.finish()
	}

	assertManifestLSPRoots(t, launched, []string{workDir, extraDir})
	lsp := requireManifestBinary(t, launched, "lsp")
	if len(lsp.Command) == 0 || lsp.Command[0] != filepath.Join(binaryDir, "mcp-lsp") {
		t.Fatalf("lsp command = %#v, want binary_dir override", lsp.Command)
	}
	if got := lsp.Env["CUSTOM_ENV"]; got != "value" {
		t.Fatalf("lsp CUSTOM_ENV = %q, want value; env=%#v", got, lsp.Env)
	}
	if !containsString(lsp.AutoApprove, "lsp_workspace_info") {
		t.Fatalf("lsp AutoApprove = %#v, want lsp_workspace_info", lsp.AutoApprove)
	}
}

func assertManifestLSPRoots(t *testing.T, manifest dto.MCPManifest, want []string) {
	t.Helper()
	lsp := requireManifestBinary(t, manifest, "lsp")
	var roots []string
	if err := json.Unmarshal([]byte(lsp.Env["GO_AGENT_LSP_ROOTS"]), &roots); err != nil {
		t.Fatalf("decode GO_AGENT_LSP_ROOTS %q: %v", lsp.Env["GO_AGENT_LSP_ROOTS"], err)
	}
	if len(roots) != len(want) {
		t.Fatalf("GO_AGENT_LSP_ROOTS = %#v, want %#v", roots, want)
	}
	for i, root := range want {
		if roots[i] != root {
			t.Fatalf("GO_AGENT_LSP_ROOTS[%d] = %q, want %q; all roots %#v", i, roots[i], root, roots)
		}
	}
}

func requireManifestBinary(t *testing.T, manifest dto.MCPManifest, name string) dto.MCPBinary {
	t.Helper()
	for _, bin := range manifest.Binaries {
		if bin.Name == name {
			return bin
		}
	}
	t.Fatalf("manifest missing binary %q: %#v", name, manifest.Binaries)
	return dto.MCPBinary{}
}
