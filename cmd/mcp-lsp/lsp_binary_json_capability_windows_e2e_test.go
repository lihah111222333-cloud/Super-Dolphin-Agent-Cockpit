//go:build windows && e2e

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func realNodeJSONScriptPins(t *testing.T, root string) []string {
	t.Helper()
	for _, pin := range realNodeScriptPins(t, root) {
		if strings.HasPrefix(pin, "vscode-langservers-extracted@") {
			return []string{pin}
		}
	}
	t.Fatal("Windows bundle script has no vscode-langservers-extracted JSON pin")
	return nil
}

func realNodeFocusedJSONInstallRootForE2E(t *testing.T, npmBin, nodeDist string, pins []string) string {
	t.Helper()
	installDir, reused, err := realNodeReusableRawNPMInstallRoot()
	if err != nil {
		t.Fatalf("resolve reusable JSON raw npm install root: %v", err)
	}
	if !reused {
		installDir = t.TempDir()
		registerRealMCPTempRootCleanup(t, installDir)
		realNodeInstallFocusedJSON(t, npmBin, nodeDist, installDir, pins)
		t.Logf("JSON-focused npm cohort cold-installed in private directory %s with pins %v", installDir, pins)
	} else {
		t.Logf("reusing existing raw npm cohort for JSON without npm install or cleanup: %s", installDir)
	}
	packageJSON := filepath.Join(installDir, "node_modules", "vscode-langservers-extracted", "package.json")
	payload, err := os.ReadFile(packageJSON)
	if err != nil {
		t.Fatalf("read JSON language-server package metadata: %v", err)
	}
	var manifest struct {
		Version string            `json:"version"`
		Bin     map[string]string `json:"bin"`
	}
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatalf("decode JSON language-server package metadata: %v", err)
	}
	if manifest.Version == "" || len(manifest.Bin) == 0 {
		t.Fatalf("JSON language-server package metadata is incomplete: version=%q bin=%v", manifest.Version, manifest.Bin)
	}
	if !fileExists(filepath.Join(installDir, "node_modules", ".bin", "vscode-json-language-server.cmd")) {
		t.Fatalf("JSON language-server Windows shim is missing")
	}
	return installDir
}

func realNodeInstallFocusedJSON(t *testing.T, npmBin, nodeDist, installDir string, pins []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	args := append([]string{"install", "--ignore-scripts", "--prefix", installDir, "--save-exact"}, pins...)
	cmd := exec.CommandContext(ctx, npmBin, args...)
	cmd.Env = realNodeEnvironment(os.Environ(), nodeDist, installDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("focused JSON npm install failed: %v output=%s", err, output)
	}
}

// TestRealNodeFocusedJSONChildEnvironment locks the subprocess PATH contract:
// npm lifecycle scripts must resolve the exact cached Node runtime, not merely
// the npm.cmd launcher that started installation.
func TestRealNodeFocusedJSONChildEnvironment(t *testing.T) {
	if os.Getenv(realNodeJSONCapabilityWindowsE2EEnv) != "1" {
		t.Skipf("set %s=1 to run the cached Node child-environment contract", realNodeJSONCapabilityWindowsE2EEnv)
	}
	nodeDist := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_NODE_DIST"))
	if nodeDist == "" {
		t.Fatal("SUPER_DOLPHIN_NODE_DIST must identify the cached exact Node runtime")
	}
	cmd := exec.Command("cmd.exe", "/d", "/s", "/c", "node --version")
	cmd.Env = realNodeEnvironment(os.Environ(), nodeDist, t.TempDir())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cached Node child process cannot resolve node: %v output=%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "v"+realNodeVersion {
		t.Fatalf("cached Node child version=%q, want v%s", got, realNodeVersion)
	}
}

// TestRealNodeFocusedNPMPostinstallEnvironment probes the npm lifecycle itself,
// rather than only cmd.exe. It records booleans so diagnostics never expose
// managed absolute paths or the host environment.
func TestRealNodeFocusedNPMPostinstallEnvironment(t *testing.T) {
	if os.Getenv(realNodeJSONCapabilityWindowsE2EEnv) != "1" {
		t.Skipf("set %s=1 to run the cached npm lifecycle environment contract", realNodeJSONCapabilityWindowsE2EEnv)
	}
	nodeDist := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_NODE_DIST"))
	if nodeDist == "" {
		t.Fatal("SUPER_DOLPHIN_NODE_DIST must identify the cached exact Node runtime")
	}
	probeRoot := t.TempDir()
	probePackage := filepath.Join(probeRoot, "probe-package")
	installRoot := filepath.Join(probeRoot, "install")
	if err := os.MkdirAll(probePackage, 0o700); err != nil {
		t.Fatalf("create npm probe package: %v", err)
	}
	reportPath := filepath.Join(probeRoot, "report.json")
	packageJSON := `{"name":"super-dolphin-json-npm-probe","version":"1.0.0","scripts":{"postinstall":"node postinstall.js"}}` + "\n"
	postinstall := `const fs = require('fs');
const path = require('path');
const managed = process.env.SUPER_DOLPHIN_MANAGED_NODE_DIR || '';
const pathValue = process.env.PATH || process.env.Path || '';
const report = {
  npmExecPathPresent: Boolean(process.env.npm_execpath),
  npmNodeExecPathPresent: Boolean(process.env.npm_node_execpath),
  managedNodeInPath: pathValue.toLowerCase().split(path.delimiter).includes(managed.toLowerCase()),
  nodeVersion: process.version
};
fs.writeFileSync(process.env.SUPER_DOLPHIN_NPM_PROBE_REPORT, JSON.stringify(report));
`
	if err := os.WriteFile(filepath.Join(probePackage, "package.json"), []byte(packageJSON), 0o600); err != nil {
		t.Fatalf("write npm probe package metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(probePackage, "postinstall.js"), []byte(postinstall), 0o600); err != nil {
		t.Fatalf("write npm probe lifecycle: %v", err)
	}
	if err := os.MkdirAll(installRoot, 0o700); err != nil {
		t.Fatalf("create npm probe install root: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	npmBin := filepath.Join(nodeDist, "npm.cmd")
	cmd := exec.CommandContext(ctx, npmBin, "install", "--prefix", installRoot, probePackage)
	cmd.Env = realNodeEnvironment(append(os.Environ(),
		"SUPER_DOLPHIN_MANAGED_NODE_DIR="+nodeDist,
		"SUPER_DOLPHIN_NPM_PROBE_REPORT="+reportPath,
	), nodeDist, installRoot)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm lifecycle probe failed: %v output=%s", err, output)
	}
	payload, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read npm lifecycle probe report: %v", err)
	}
	var report struct {
		NPMExecPathPresent     bool   `json:"npmExecPathPresent"`
		NPMNodeExecPathPresent bool   `json:"npmNodeExecPathPresent"`
		ManagedNodeInPath      bool   `json:"managedNodeInPath"`
		NodeVersion            string `json:"nodeVersion"`
	}
	if err := json.Unmarshal(payload, &report); err != nil {
		t.Fatalf("decode npm lifecycle probe report: %v", err)
	}
	if !report.NPMExecPathPresent || !report.NPMNodeExecPathPresent || !report.ManagedNodeInPath {
		t.Fatalf("npm lifecycle environment contract failed: execpath=%v node_execpath=%v managed_node_in_path=%v version=%s", report.NPMExecPathPresent, report.NPMNodeExecPathPresent, report.ManagedNodeInPath, report.NodeVersion)
	}
	if report.NodeVersion != "v"+realNodeVersion {
		t.Fatalf("npm lifecycle used Node %q, want v%s", report.NodeVersion, realNodeVersion)
	}
}
