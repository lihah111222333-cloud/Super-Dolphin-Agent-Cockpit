package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
)

func TestMcpOrchSidecarRuntimeRequiresParentContract(t *testing.T) {
	_, err := runtimeenv.ResolveSidecarRuntimeContract(runtimeenv.SidecarRuntimeInput{
		ExecutablePath: "/Applications/Super Dolphin.app/Contents/Resources/bin/mcp-orch",
		Env:            map[string]string{},
	})
	if err == nil {
		t.Fatal("ResolveSidecarRuntimeContract() error = nil, want parent contract failure")
	}
	if !strings.Contains(err.Error(), "parent launch contract missing") {
		t.Fatalf("error = %q, want parent launch contract missing", err.Error())
	}
}

func TestMcpOrchSidecarRuntimeConsumesPackagedParentContract(t *testing.T) {
	resources := "/Applications/Super Dolphin.app/Contents/Resources"
	contract, err := runtimeenv.ResolveSidecarRuntimeContract(runtimeenv.SidecarRuntimeInput{
		ExecutablePath: "/Applications/Super Dolphin.app/Contents/Resources/bin/mcp-orch",
		Env: map[string]string{
			"SUPER_DOLPHIN_RUNTIME_MODE":          "packaged",
			"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR": resources,
		},
	})
	if err != nil {
		t.Fatalf("ResolveSidecarRuntimeContract() error = %v", err)
	}
	if contract.Mode != "packaged" || contract.ResourcesDir != filepath.Clean(resources) {
		t.Fatalf("contract = %#v, want packaged parent resources", contract)
	}
}

func TestMcpOrchSidecarRuntimeDevParentIgnoresResidualPackagedEnv(t *testing.T) {
	resources := "/work/repo"
	contract, err := runtimeenv.ResolveSidecarRuntimeContract(runtimeenv.SidecarRuntimeInput{
		ExecutablePath: "/Applications/Super Dolphin.app/Contents/Resources/bin/mcp-orch",
		Env: map[string]string{
			"SUPER_DOLPHIN_RUNTIME_MODE":          "dev",
			"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR": resources,
			"PROJECT_ROOT":                        "/Applications/Super Dolphin.app/Contents/Resources",
			"SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX": "1",
		},
	})
	if err != nil {
		t.Fatalf("ResolveSidecarRuntimeContract() error = %v", err)
	}
	if contract.Mode != "dev" || contract.ResourcesDir != filepath.Clean(resources) {
		t.Fatalf("contract = %#v, want inherited dev resources", contract)
	}
}
