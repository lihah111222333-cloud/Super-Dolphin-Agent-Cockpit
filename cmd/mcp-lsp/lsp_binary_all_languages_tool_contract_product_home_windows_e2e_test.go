//go:build windows && e2e

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

type allLanguageToolContractBinaryPackage struct {
	binary   string
	bundle   string
	manifest string
	nodePath string
}

// prepareAllLanguageToolContractBinaryPackage 将本次新编译的 binary 安装到
// 独立 product-home 的 bin\LSP，且每次使用唯一文件名，避免覆盖任何既有交付物。
func prepareAllLanguageToolContractBinaryPackage(t *testing.T, binary, productHome, fakeBundle, fakeBinDir string) allLanguageToolContractBinaryPackage {
	t.Helper()
	installRoot := filepath.Join(productHome, "bin", "LSP")
	if err := os.MkdirAll(installRoot, 0o700); err != nil {
		t.Fatalf("create isolated Windows all-language package bin: %v", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(installRoot, 0o700); err != nil {
		t.Fatalf("restrict isolated Windows all-language package bin: %v", err)
	}

	source, err := os.ReadFile(binary)
	if err != nil {
		t.Fatalf("read newly compiled mcp-lsp binary: %v", err)
	}
	installed, err := os.CreateTemp(installRoot, "mcp-lsp-windows-"+runtime.GOARCH+"-e2e-*.exe")
	if err != nil {
		t.Fatalf("create unique Windows all-language mcp-lsp package binary: %v", err)
	}
	installedPath := installed.Name()
	if _, err := installed.Write(source); err != nil {
		_ = installed.Close()
		t.Fatalf("write unique Windows all-language mcp-lsp package binary: %v", err)
	}
	if err := installed.Chmod(0o700); err != nil {
		_ = installed.Close()
		t.Fatalf("restrict unique Windows all-language mcp-lsp package binary: %v", err)
	}
	if err := installed.Close(); err != nil {
		t.Fatalf("close unique Windows all-language mcp-lsp package binary: %v", err)
	}

	bundle := filepath.Join(installRoot, "lsp")
	copyAllLanguageToolContractBundle(t, fakeBundle, bundle)
	if err := securefs.RestrictPrivateOwnerOnly(bundle, 0o700); err != nil {
		t.Fatalf("restrict isolated Windows all-language LSP bundle: %v", err)
	}
	manifestPayload, err := os.ReadFile(filepath.Join(bundle, "manifest.json"))
	if err != nil {
		t.Fatalf("read copied Windows all-language fake manifest: %v", err)
	}
	manifest := filepath.Join(bundle, "lsp-manifest.json")
	if err := os.WriteFile(manifest, manifestPayload, 0o600); err != nil {
		t.Fatalf("write copied Windows all-language LSP manifest: %v", err)
	}
	nodePath := allLanguageToolContractNodePath(t, bundle, fakeBinDir)
	return allLanguageToolContractBinaryPackage{
		binary:   installedPath,
		bundle:   bundle,
		manifest: manifest,
		nodePath: nodePath,
	}
}

func copyAllLanguageToolContractBundle(t *testing.T, source, target string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("fake bundle entry is not a regular file: %s", relative)
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		return os.WriteFile(destination, payload, 0o700)
	}); err != nil {
		t.Fatalf("copy Windows all-language fake bundle into product package: %v", err)
	}
}

func startAllLanguageToolContractBinaryForTest(t *testing.T, ctx context.Context, binary, root, fakeBinDir string, extraEnv []string) *mcpLSPBinaryClient {
	t.Helper()
	extraEnv = append(extraEnv, "MCP_LSP_IDLE_TIMEOUT=15m")
	client := startWindowsGoplsMCPBinaryForTest(t, ctx, binary, root, fakeBinDir, extraEnv)
	t.Cleanup(func() { cleanupFakeWindowsGoplsBundleProcesses(t, binary) })
	return client
}

// allLanguageToolContractWorkDir 在 Windows 上把子进程 workspace 放进仓库可写缓存根，
// 避免宿主系统临时目录对 CreateFile/EvalSymlinks 施加不一致的令牌权限。
func allLanguageToolContractWorkDir(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(repoRootForMcpLSPBinaryTest(t), ".build-cache", "lsp-e2e-workspaces")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatalf("create Windows all-language workspace parent: %v", err)
	}
	root, err := os.MkdirTemp(parent, "all-language-")
	if err != nil {
		t.Fatalf("create Windows all-language workspace: %v", err)
	}
	relative, relErr := filepath.Rel(filepath.Clean(parent), filepath.Clean(root))
	if relErr != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) {
		t.Fatalf("validate Windows all-language workspace %q within %q: %v", root, parent, relErr)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove Windows all-language workspace %s: %v", fmt.Sprintf("<workspace:%s>", filepath.Base(root)), err)
		}
	})
	return root
}

// writeAllLanguageToolContractBundle 在独立 product root 下构造 fixture，
// 其 cache/lsp-assets 子树与生产锁定路径保持一致。
func writeAllLanguageToolContractBundle(t *testing.T, fakeBinDir string) string {
	t.Helper()
	bundleDir := t.TempDir()
	bundleDir = writeFakeAllLanguagesProtocolBundle(t, fakeBinDir, bundleDir)
	writeAllLanguageToolContractASTGrepCompanion(t, bundleDir, fakeBinDir)
	writeAllLanguageToolContractMarkdownRuntime(t, bundleDir, fakeBinDir)
	return bundleDir
}

// allLanguageToolContractNodePath 将 fake Node 放入 Markdown client 解析的产品 ready 树，
// 生产 resolver 仍保持只读并要求精确命中该路径。
func allLanguageToolContractNodePath(t *testing.T, bundleDir, fakeBinDir string) string {
	t.Helper()
	productRoot := filepath.Clean(bundleDir)
	nodeRuntime, err := lspinstaller.NewWindowsNodeRuntime(productRoot, nil)
	if err != nil {
		t.Fatalf("create Windows all-language fake Node runtime: %v", err)
	}
	paths, err := nodeRuntime.ExpectedPaths()
	if err != nil {
		t.Fatalf("resolve Windows all-language fake Node runtime paths: %v", err)
	}
	fakeNodePath := filepath.Join(fakeBinDir, allLanguageToolContractExecutableName("node"))
	payload, err := os.ReadFile(fakeNodePath)
	if err != nil {
		t.Fatalf("read Windows all-language fake Node runtime: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.NodePath), 0o700); err != nil {
		t.Fatalf("create Windows all-language fake Node ready directory: %v", err)
	}
	if err := os.WriteFile(paths.NodePath, payload, 0o700); err != nil {
		t.Fatalf("write Windows all-language fake Node runtime: %v", err)
	}
	return paths.NodePath
}

// writeAllLanguageToolContractMarkdownRuntime 将 fake Markdown server 与 markdown-it
// 放入锁定 npm cohort，使 42-ID 矩阵走真实 Markdown runtime 路径但只使用测试字节。
func writeAllLanguageToolContractMarkdownRuntime(t *testing.T, bundleDir, fakeBinDir string) {
	t.Helper()
	productRoot := filepath.Clean(bundleDir)
	nodeRuntime, err := lspinstaller.NewWindowsNodeRuntime(productRoot, nil)
	if err != nil {
		t.Fatalf("create Windows Markdown fake Node runtime: %v", err)
	}
	paths, err := nodeRuntime.ExpectedPaths()
	if err != nil {
		t.Fatalf("resolve Windows Markdown fake Node runtime paths: %v", err)
	}
	serverName := "vscode-markdown-language-server"
	sourcePath := filepath.Join(fakeBinDir, allLanguageToolContractExecutableName(serverName))
	payload, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read Windows Markdown fake server: %v", err)
	}
	// bundleDir 是独立产品 root；锁定 npm cohort 位于其 cache/lsp-assets 下，
	// 因而 manifest 仍在 bundle 内，Markdown module root 也保持生产 .bin 布局。
	serverPath := filepath.Join(paths.BinDir, allLanguageToolContractExecutableName(serverName))
	if err := os.MkdirAll(filepath.Dir(serverPath), 0o700); err != nil {
		t.Fatalf("create Windows Markdown fake npm cohort: %v", err)
	}
	if err := os.WriteFile(serverPath, payload, 0o700); err != nil {
		t.Fatalf("write Windows Markdown fake server: %v", err)
	}
	packageDir := filepath.Join(paths.Prefix, "node_modules", "markdown-it")
	if err := os.MkdirAll(packageDir, 0o700); err != nil {
		t.Fatalf("create Windows Markdown fake markdown-it package: %v", err)
	}
	packageJSON := fmt.Sprintf("{\"version\":%q}\n", runtimeMarkdownItInstallVersion)
	if err := os.WriteFile(filepath.Join(packageDir, "package.json"), []byte(packageJSON), 0o600); err != nil {
		t.Fatalf("write Windows Markdown fake markdown-it metadata: %v", err)
	}
	relativePath, err := filepath.Rel(bundleDir, serverPath)
	if err != nil || relativePath == "." || filepath.IsAbs(relativePath) {
		t.Fatalf("resolve Windows Markdown fake server manifest path: %v", err)
	}
	digest := sha256.Sum256(payload)
	manifestPath := filepath.Join(bundleDir, "manifest.json")
	manifestPayload, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read Windows Markdown fake bundle manifest: %v", err)
	}
	var manifest struct {
		SchemaVersion int                        `json:"schema_version"`
		BundlePath    string                     `json:"bundle_path"`
		Profile       string                     `json:"profile"`
		Servers       map[string]json.RawMessage `json:"servers"`
	}
	if err := json.Unmarshal(manifestPayload, &manifest); err != nil {
		t.Fatalf("decode Windows Markdown fake bundle manifest: %v", err)
	}
	if manifest.Servers == nil {
		t.Fatal("Windows Markdown fake bundle manifest has no servers")
	}
	entry, err := json.Marshal(map[string]any{
		"path":      filepath.ToSlash(relativePath),
		"version":   "v24.12.0",
		"sha256":    hex.EncodeToString(digest[:]),
		"languages": []string{"markdown"},
	})
	if err != nil {
		t.Fatalf("marshal Windows Markdown fake manifest entry: %v", err)
	}
	manifest.Servers[serverName] = entry
	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal Windows Markdown fake bundle manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, append(updated, '\n'), 0o644); err != nil {
		t.Fatalf("write Windows Markdown fake bundle manifest: %v", err)
	}
}

// writeAllLanguageToolContractASTGrepCompanion 将 fake sg 放进已校验的 bundle manifest，
// 让 Windows 代表性 E2E 通过生产 runtime 的显式 companion 路径运行。
func writeAllLanguageToolContractASTGrepCompanion(t *testing.T, bundleDir, fakeBinDir string) {
	t.Helper()
	executableName := allLanguageToolContractExecutableName("sg")
	sourcePath := filepath.Join(fakeBinDir, executableName)
	payload, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read fake ast-grep companion %s: %v", sourcePath, err)
	}
	relativePath := filepath.Join("bin", executableName)
	companionPath := filepath.Join(bundleDir, relativePath)
	if err := os.WriteFile(companionPath, payload, 0o700); err != nil {
		t.Fatalf("write fake ast-grep companion %s: %v", companionPath, err)
	}
	digest := sha256.Sum256(payload)
	manifestPath := filepath.Join(bundleDir, "manifest.json")
	manifestPayload, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read fake ast-grep companion manifest: %v", err)
	}
	var manifest struct {
		SchemaVersion int                        `json:"schema_version"`
		BundlePath    string                     `json:"bundle_path"`
		Profile       string                     `json:"profile"`
		Servers       map[string]json.RawMessage `json:"servers"`
	}
	if err := json.Unmarshal(manifestPayload, &manifest); err != nil {
		t.Fatalf("decode fake ast-grep companion manifest: %v", err)
	}
	if manifest.Servers == nil {
		t.Fatal("fake ast-grep companion manifest has no servers")
	}
	companion, err := json.Marshal(map[string]any{
		"path":      filepath.ToSlash(relativePath),
		"version":   astGrepInstallVersion,
		"sha256":    hex.EncodeToString(digest[:]),
		"languages": []string{runtimeASTGrepLanguageID},
	})
	if err != nil {
		t.Fatalf("marshal fake ast-grep companion manifest entry: %v", err)
	}
	manifest.Servers[runtimeASTGrepLanguageID] = companion
	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal fake ast-grep companion manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, append(updated, '\n'), 0o644); err != nil {
		t.Fatalf("write fake ast-grep companion manifest: %v", err)
	}
}

// prepareAllLanguageToolContractProductHome 将 Windows 产品根与源码 work_dir 隔离。
// 严格的受保护 DACL 仍然保留，但不再作为子目录干扰父 work_dir 的规范化。
func prepareAllLanguageToolContractProductHome(t *testing.T, _ string) string {
	t.Helper()
	productHome := filepath.Join(t.TempDir(), ".super-dolphin")
	if err := os.MkdirAll(productHome, 0o700); err != nil {
		t.Fatalf("create isolated Windows all-language product home: %v", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(productHome, 0o700); err != nil {
		t.Fatalf("restrict isolated Windows all-language product home: %v", err)
	}
	return productHome
}
