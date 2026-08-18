//go:build windows && e2e

package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

type fakeProtocolBundleLayout struct {
	binary       string
	bundleDir    string
	manifestPath string
	extraEnv     []string
}

func writeFakeProtocolBundle(t *testing.T, binary, fakeServersBinDir, serverName, languageID string) fakeProtocolBundleLayout {
	t.Helper()
	if serverName == "gopls" {
		return writeFakeWindowsGoplsProtocolBundle(t, binary, languageID)
	}
	productRoot := t.TempDir()
	bundleDir := filepath.Join(productRoot, "cache", "lsp-assets")
	bundleBinDir := filepath.Join(bundleDir, "bin")
	if err := os.MkdirAll(bundleBinDir, 0o755); err != nil {
		t.Fatalf("create fake %s Windows protocol bundle bin dir: %v", languageID, err)
	}
	bundleServerName := serverName + ".cmd"
	if serverName == "vscode-markdown-language-server" {
		bundleServerName = serverName + ".exe"
	}
	testBinary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("resolve fake %s test binary: %v", serverName, err)
	}
	nodePath := filepath.Join(bundleDir, "bin", bundleServerName)
	serverPath := nodePath
	serverManifestPath := filepath.ToSlash(filepath.Join("bin", bundleServerName))
	if serverName == "vscode-markdown-language-server" {
		nodeRuntime, err := lspinstaller.NewWindowsNodeRuntime(productRoot, nil)
		if err != nil {
			t.Fatalf("create fake Windows Markdown Node runtime: %v", err)
		}
		paths, err := nodeRuntime.ExpectedPaths()
		if err != nil {
			t.Fatalf("resolve fake Windows Markdown Node runtime paths: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(paths.NodePath), 0o700); err != nil {
			t.Fatalf("create fake Windows Markdown Node runtime ready directory: %v", err)
		}
		writeFakeWindowsMultilangGoplsExecutable(t, paths.NodePath, serverName)
		nodePath = paths.NodePath
		if err := os.MkdirAll(paths.BinDir, 0o700); err != nil {
			t.Fatalf("create fake Windows Markdown npm cohort bin directory: %v", err)
		}
		serverPath = filepath.Join(paths.BinDir, bundleServerName)
		writeFakeWindowsMultilangGoplsExecutable(t, serverPath, serverName)
		serverManifestPath, err = filepath.Rel(bundleDir, serverPath)
		if err != nil {
			t.Fatalf("resolve fake Windows Markdown server manifest path: %v", err)
		}
		serverManifestPath = filepath.ToSlash(serverManifestPath)
		markdownPackageDir := filepath.Join(paths.Prefix, "node_modules", "markdown-it")
		if err := os.MkdirAll(markdownPackageDir, 0o700); err != nil {
			t.Fatalf("create fake Windows Markdown npm module directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(markdownPackageDir, "package.json"), []byte(fmt.Sprintf("{\"version\":%q}\n", runtimeMarkdownItInstallVersion)), 0o600); err != nil {
			t.Fatalf("write fake Windows Markdown npm module metadata: %v", err)
		}
	}
	if serverName != "vscode-markdown-language-server" {
		fakeServer := []byte(fmt.Sprintf("@echo off\r\nif /I \"%%~1\"==\"--version\" (\r\n  echo v24.12.0\r\n  exit /b 0\r\n)\r\nset \"MCP_LSP_FAKE_MULTILANG_DIAGNOSTICS=1\"\r\nset \"MCP_LSP_FAKE_MULTILANG_SERVER=%s\"\r\n\"%s\" -test.run=TestFakeMultilangDiagnosticsLangserverHelper -- %%*\r\n", serverName, testBinary))
		if err := os.MkdirAll(filepath.Dir(serverPath), 0o700); err != nil {
			t.Fatalf("create fake %s Windows protocol server directory: %v", serverName, err)
		}
		if err := os.WriteFile(serverPath, fakeServer, 0o700); err != nil {
			t.Fatalf("write fake %s Windows protocol bundle server: %v", serverName, err)
		}
	}
	digest := sha256.Sum256(mustReadFileForFakeWindowsBundle(t, serverPath))
	manifest := fmt.Appendf(nil, "{\n  \"servers\": {\n    %q: {\"path\": %q, \"sha256\": %q, \"languages\": [%q]}\n  }\n}\n",
		serverName, serverManifestPath, fmt.Sprintf("%x", digest[:]), languageID)
	if err := os.WriteFile(filepath.Join(bundleDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatalf("write fake %s Windows protocol bundle manifest: %v", languageID, err)
	}
	return fakeProtocolBundleLayout{
		binary:       binary,
		bundleDir:    bundleDir,
		manifestPath: filepath.Join(bundleDir, "manifest.json"),
		extraEnv:     []string{"SUPER_DOLPHIN_WINDOWS_NODE_PATH=" + nodePath},
	}
}

func writeFakeWindowsGoplsProtocolBundle(t *testing.T, binary, languageID string) fakeProtocolBundleLayout {
	t.Helper()
	productRoot := t.TempDir()
	installDir := filepath.Join(productRoot, "bin", "LSP")
	bundleDir := filepath.Join(installDir, "lsp")
	bundleBinDir := filepath.Join(bundleDir, "bin")
	if err := os.MkdirAll(bundleBinDir, 0o755); err != nil {
		t.Fatalf("create fake %s Windows gopls package: %v", languageID, err)
	}
	binaryOverride := filepath.Join(installDir, "mcp-lsp.exe")
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatalf("read mcp-lsp binary for fake %s Windows package: %v", languageID, err)
	}
	if err := os.WriteFile(binaryOverride, payload, 0o700); err != nil {
		t.Fatalf("install mcp-lsp binary for fake %s Windows package: %v", languageID, err)
	}
	goplsPath := filepath.Join(bundleBinDir, "gopls.exe")
	writeFakeWindowsMultilangGoplsExecutable(t, goplsPath, "gopls")
	digest := sha256.Sum256(mustReadFileForFakeWindowsBundle(t, goplsPath))
	manifest := fmt.Appendf(nil, "{\n  \"schema_version\": 1,\n  \"bundle_path\": \"lsp\",\n  \"profile\": \"standard\",\n  \"servers\": {\n    \"gopls\": {\"path\": \"bin/gopls.exe\", \"version\": \"v24.12.0\", \"sha256\": \"%x\", \"languages\": [%q]}\n  }\n}\n", digest[:], languageID)
	manifestPath := filepath.Join(bundleDir, "lsp-manifest.json")
	if err := os.WriteFile(manifestPath, manifest, 0o600); err != nil {
		t.Fatalf("write fake %s Windows gopls manifest: %v", languageID, err)
	}
	return fakeProtocolBundleLayout{
		binary:       binaryOverride,
		bundleDir:    bundleDir,
		manifestPath: manifestPath,
		extraEnv: []string{
			"SUPER_DOLPHIN_WINDOWS_NODE_PATH=" + goplsPath,
			"MCP_LSP_IDLE_TIMEOUT=1s",
		},
	}
}

func mustReadFileForFakeWindowsBundle(t *testing.T, path string) []byte {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fake Windows gopls executable: %v", err)
	}
	return payload
}

func writeFakeWindowsMultilangGoplsExecutable(t *testing.T, target, serverName string) {
	t.Helper()
	host, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("resolve fake Windows %s test host: %v", serverName, err)
	}
	serverNameExpression := fmt.Sprintf("%q", serverName)
	if serverName == "" {
		serverNameExpression = "fakeMultilangServerNameFromExecutable()"
	}
	source := fmt.Sprintf(`package main

import (
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
)

func fakeMultilangServerNameFromExecutable() string {
    executable, err := os.Executable()
    if err != nil {
        panic(err)
    }
    base := filepath.Base(executable)
    name := strings.TrimSuffix(base, filepath.Ext(base))
    if name == "" || name == "." || name == ".." {
        panic("invalid fake multilang server executable name")
    }
    return name
}

func main() {
    if len(os.Args) > 1 && os.Args[1] == "--version" {
        fmt.Println("v24.12.0")
        return
    }
    serverName := %s
    args := append([]string{"-test.run=TestFakeMultilangDiagnosticsLangserverHelper", "--"}, os.Args[1:]...)
    command := exec.Command(%q, args...)
    command.Stdin = os.Stdin
    command.Stdout = os.Stdout
    command.Stderr = os.Stderr
    command.Env = append(os.Environ(), "MCP_LSP_FAKE_MULTILANG_DIAGNOSTICS=1", "MCP_LSP_FAKE_MULTILANG_SERVER="+serverName)
    if err := command.Run(); err != nil {
        os.Exit(1)
    }
}
`, serverNameExpression, host)
	sourcePath := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatalf("write fake Windows %s source: %v", serverName, err)
	}
	buildWindowsTestExecutable(t, target, sourcePath)
}

func startFakeProtocolBundleClientForTest(t *testing.T, ctx context.Context, bundle fakeProtocolBundleLayout, root, fakeServersBinDir string, extraEnv []string, serverName string) *mcpLSPBinaryClient {
	t.Helper()
	if serverName == "gopls" {
		client := startWindowsGoplsMCPBinaryForTest(t, ctx, bundle.binary, root, fakeServersBinDir, extraEnv)
		t.Cleanup(func() { killFakeWindowsBundleProcesses(t, filepath.Dir(bundle.binary)) })
		return client
	}
	return startMcpLSPBinaryForTestWithEnv(t, ctx, bundle.binary, root, fakeServersBinDir, extraEnv)
}

func killFakeWindowsBundleProcesses(t *testing.T, installDir string) {
	t.Helper()
	quotedRoot := strings.ReplaceAll(filepath.Clean(installDir), "'", "''")
	script := "$root='" + quotedRoot + "'; Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.ToLower().StartsWith($root.ToLower()) } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }"
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Logf("cleanup fake Windows bundle processes under %s: %v; output=%s", installDir, err, output)
	}
}
