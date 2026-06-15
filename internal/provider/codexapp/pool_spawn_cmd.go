package codexapp

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

// PoolSpawnArgs drives BuildPoolSpawnCmd. Home is injected as CODEX_HOME,
// ExtraArgs are appended after `codex app-server`, and ParentEnv keeps the
// builder unit-testable with synthetic parent environments.
type PoolSpawnArgs struct {
	Home      string
	ExtraArgs []string
	ParentEnv []string
}

const (
	codexBinaryName       = "codex"
	codexAppServerCommand = "app-server"
	codexAppServerListen  = "--listen"
)

func buildCodexAppServerArgs(listenURL string, configArgs []string) []string {
	return append(append([]string{codexBinaryName}, configArgs...), codexAppServerCommand, codexAppServerListen, listenURL)
}

func localSpawnAppServerArgs() []string {
	return buildCodexAppServerArgs(localSpawnListenURL(), localSpawnNativeLSPFailClosedArgs())
}

func localSpawnNativeLSPFailClosedArgs() []string {
	return poolSpawnNativeLSPConfigOverrideArgs([]string{
		"mcp_servers.lsp.command=" + tomlString(codexLocalMCPCommand("mcp-lsp")),
		"mcp_servers.lsp.type=" + tomlString("stdio"),
		"mcp_servers.lsp.env.GO_AGENT_LSP_ROOTS=" + tomlString("[]"),
	})
}

func codexLocalMCPCommand(name string) string {
	if dir := strings.TrimSpace(resolveCodexLocalMCPBinaryDir()); dir != "" {
		return filepath.Join(dir, strings.TrimSpace(name))
	}
	return strings.TrimSpace(name)
}
func resolveCodexLocalMCPBinaryDir() string { return providershared.ResolveBinaryDir("", nil) }

func isCodexAppServerListenArgs(args []string) bool {
	for i := 0; i < len(args)-1; i++ {
		if commandLeaf(args[i]) == codexAppServerCommand && args[i+1] == codexAppServerListen {
			return true
		}
	}
	return false
}

func commandLeaf(arg string) string {
	if idx := strings.LastIndexAny(arg, `/\`); idx >= 0 {
		return arg[idx+1:]
	}
	return arg
}

// BuildPoolSpawnCmd assembles a *exec.Cmd that the ServerPool spawner
// Start()s. The recipe combines three plan-mandated pieces:
//
//  1. Shell wrapper for ulimit. macOS GUI-launched processes inherit
//     launchd's 256 fd soft limit, which starves batch agent scenarios
//     (100 agents × 2 MCP servers each). Running under sh -c 'ulimit
//     -n 1048576 ...; exec codex ...' guarantees the child starts with
//     a high limit regardless of launch context (.app / terminal /
//     launchd).
//  2. Env allowlist via buildAllowlistedSpawnEnv — strips everything
//     outside the closed allowlist (PATH / HOME / USER / ...) and
//     injects CODEX_HOME=<Home>. Overrides win over parent values so
//     a stale CODEX_HOME in the parent can't bleed through.
//  3. Setpgid so the child and its descendants land in a new process
//     group — orphan sweepers can then kill the whole tree by
//     negative pgid when the parent exits uncleanly.
//
// The function only builds the *exec.Cmd; starting + stderr pumping +
// listen URL parsing + transport registration remain the caller's
// responsibility. Keeping the function pure makes it straightforward
// to unit-test the env / argv / SysProcAttr shape without actually
// spawning a child.
// BuildPoolSpawnCmd 构建poolspawncmd。
func BuildPoolSpawnCmd(ctx context.Context, args PoolSpawnArgs) (*exec.Cmd, error) {
	home := strings.TrimSpace(args.Home)
	if home == "" {
		return nil, fmt.Errorf("codexapp: BuildPoolSpawnCmd requires a codex home")
	}
	workDir, err := poolSpawnNormalizedWorkDir(ctx)
	if err != nil {
		return nil, err
	}
	// Do not use exec.CommandContext: startup-timeout ctx cancels after listen
	// URL discovery, while process lifetime belongs to transport.shutdownTransport.
	// The ctx here only supports pre-cancel checks and I/O-level waits.
	extraArgs := append(poolSpawnNativeLSPConfigArgs(ctx, workDir), args.ExtraArgs...)
	argv := buildCodexAppServerArgs(localSpawnListenURL(), extraArgs)
	cmd := wrapWithFDLimit(argv)
	if workDir != "" {
		cmd.Dir = workDir
	}
	parent := args.ParentEnv
	if parent == nil {
		parent = os.Environ()
	}
	cmd.Env = buildPoolSpawnEnv(parent, home, workDir)
	setCodexProcessAttrs(cmd)
	return cmd, nil
}

// poolSpawnNativeLSPConfigArgs 处理poolspawnnativeLSP配置args。
func poolSpawnNativeLSPConfigArgs(ctx context.Context, workDir string) []string {
	roots := poolSpawnWorkspaceRoots(ctx)
	if len(roots) == 0 && strings.TrimSpace(workDir) != "" {
		roots = []string{strings.TrimSpace(workDir)}
	}
	binaryDir := strings.TrimSpace(poolSpawnMCPBinaryDir(ctx))
	if len(roots) == 0 {
		if binaryDir == "" {
			return nil
		}
		return poolSpawnNativeLSPConfigOverrideArgs([]string{
			"mcp_servers.lsp.command=" + tomlString(filepath.Join(binaryDir, "mcp-lsp")),
			"mcp_servers.lsp.type=" + tomlString("stdio"),
			"mcp_servers.lsp.env.GO_AGENT_LSP_ROOTS=" + tomlString("[]"),
		})
	}
	primary := roots[0]
	rawRoots, err := json.Marshal(roots)
	if err != nil {
		return nil
	}
	overrides := []string{
		"mcp_servers.lsp.type=" + tomlString("stdio"),
		"mcp_servers.lsp.cwd=" + tomlString(primary),
		"mcp_servers.lsp.env.GO_AGENT_LSP_ROOT=" + tomlString(primary),
		"mcp_servers.lsp.env.GO_AGENT_LSP_ROOTS=" + tomlString(string(rawRoots)),
		"mcp_servers.lsp.env." + providershared.SuperDolphinHomeEnv + "=" + tomlString(os.Getenv(providershared.SuperDolphinHomeEnv)),
	}
	if binaryDir != "" {
		overrides = append([]string{
			"mcp_servers.lsp.command=" + tomlString(filepath.Join(binaryDir, "mcp-lsp")),
		}, overrides...)
	}
	return poolSpawnNativeLSPConfigOverrideArgs(overrides)
}

func poolSpawnNativeLSPConfigOverrideArgs(overrides []string) []string {
	args := make([]string, 0, len(overrides)*2)
	for _, override := range overrides {
		args = append(args, "-c", override)
	}
	return args
}

func tomlString(value string) string {
	return strconv.Quote(strings.TrimSpace(value))
}

// extractCodexWheel 提取codexwheel。
func extractCodexWheel(wheelPath, targetDir string) error {
	reader, err := zip.OpenReader(wheelPath)
	if err != nil {
		return fmt.Errorf("open Codex wheel: %w", err)
	}
	defer func() { _ = reader.Close() }()
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return fmt.Errorf("create Codex extract dir: %w", err)
	}
	var total int64
	for _, file := range reader.File {
		if !strings.HasPrefix(file.Name, "codex_cli_bin/") {
			continue
		}
		if err := extractCodexZipFile(file, targetDir, &total); err != nil {
			return err
		}
	}
	return nil
}

func extractCodexZipFile(file *zip.File, targetDir string, total *int64) error {
	target, err := codexArchiveEntryTarget(file.Name, targetDir, "wheel")
	if err != nil {
		return err
	}
	if file.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	written, err := writeCodexZipEntry(file, target)
	if err != nil {
		return err
	}
	return addCodexExtractedBytes(total, written)
}

func codexArchiveEntryTarget(name, targetDir, label string) (string, error) {
	cleanName := filepath.Clean(name)
	if cleanName == "." || strings.HasPrefix(cleanName, ".."+string(filepath.Separator)) || filepath.IsAbs(cleanName) {
		return "", fmt.Errorf("Codex %s contains unsafe path %q", label, name)
	}
	target := filepath.Join(targetDir, cleanName)
	if !strings.HasPrefix(target, filepath.Clean(targetDir)+string(filepath.Separator)) {
		return "", fmt.Errorf("Codex %s path escapes target dir: %q", label, name)
	}
	return target, nil
}

func writeCodexZipEntry(file *zip.File, target string) (int64, error) {
	if file.UncompressedSize64 > uint64(codexInstall.maxFileBytes) {
		return 0, fmt.Errorf("Codex wheel entry %q exceeds %d bytes", file.Name, codexInstall.maxFileBytes)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, fmt.Errorf("create Codex wheel entry dir: %w", err)
	}
	src, err := file.Open()
	if err != nil {
		return 0, fmt.Errorf("open Codex wheel entry %q: %w", file.Name, err)
	}
	defer func() { _ = src.Close() }()
	mode := file.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	return writeCodexArchiveEntry(src, target, mode, "wheel", file.Name)
}

func extractCodexTarGz(archivePath, targetDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open Codex tar.gz: %w", err)
	}
	defer func() { _ = file.Close() }()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open Codex gzip stream: %w", err)
	}
	defer func() { _ = gzipReader.Close() }()
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return fmt.Errorf("create Codex extract dir: %w", err)
	}
	return extractCodexTarStream(tar.NewReader(gzipReader), targetDir)
}

// extractCodexTarStream 提取codextar流。
func extractCodexTarStream(reader *tar.Reader, targetDir string) error {
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read Codex tar entry: %w", err)
		}
		written, err := extractCodexTarEntry(reader, header, targetDir)
		if err != nil {
			return err
		}
		if err := addCodexExtractedBytes(&total, written); err != nil {
			return err
		}
	}
}

func extractCodexTarEntry(reader *tar.Reader, header *tar.Header, targetDir string) (int64, error) {
	target, err := codexArchiveEntryTarget(header.Name, targetDir, "tar.gz")
	if err != nil {
		return 0, err
	}
	switch header.Typeflag {
	case tar.TypeDir:
		return 0, os.MkdirAll(target, 0o755)
	case tar.TypeReg, tar.TypeRegA:
		return writeCodexTarEntry(reader, header, target)
	default:
		return 0, fmt.Errorf("Codex tar.gz contains unsupported entry %q", header.Name)
	}
}

func writeCodexTarEntry(reader *tar.Reader, header *tar.Header, target string) (int64, error) {
	if header.Size > codexInstall.maxFileBytes {
		return 0, fmt.Errorf("Codex tar.gz entry %q exceeds %d bytes", header.Name, codexInstall.maxFileBytes)
	}
	mode := fs.FileMode(header.Mode).Perm()
	if mode == 0 {
		mode = 0o644
	}
	return writeCodexArchiveEntry(reader, target, mode, "tar entry", header.Name)
}

func writeCodexArchiveEntry(src io.Reader, target string, mode fs.FileMode, label, name string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, fmt.Errorf("create Codex %s dir: %w", label, err)
	}
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return 0, fmt.Errorf("create Codex %s %q: %w", label, name, err)
	}
	written, copyErr := copyCodexArchiveFile(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return 0, fmt.Errorf("write Codex %s %q: %w", label, name, copyErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("close Codex %s %q: %w", label, name, closeErr)
	}
	return written, nil
}

func copyCodexArchiveFile(dst io.Writer, src io.Reader) (int64, error) {
	written, err := io.Copy(dst, io.LimitReader(src, codexInstall.maxFileBytes+1))
	if err != nil {
		return written, err
	}
	if written > codexInstall.maxFileBytes {
		return written, fmt.Errorf("Codex archive entry exceeds %d bytes", codexInstall.maxFileBytes)
	}
	return written, nil
}

func addCodexExtractedBytes(total *int64, written int64) error {
	if total == nil {
		return nil
	}
	*total += written
	if *total > codexInstall.maxTotalBytes {
		return fmt.Errorf("Codex archive extraction exceeds %d bytes", codexInstall.maxTotalBytes)
	}
	return nil
}

// ensureCodexInstallLayout 确保codex安装layout。
func ensureCodexInstallLayout(targetDir string) error {
	expected := filepath.Join(targetDir, "codex_cli_bin", "bin", codexExecutableFileName())
	if _, err := os.Stat(expected); err == nil {
		if err := chmodCodexInstallHelpers(targetDir); err != nil {
			return err
		}
		if isExecutable(expected) {
			return nil
		}
	}
	found, err := findExtractedCodexExecutable(targetDir)
	if err != nil {
		return err
	}
	if found == "" {
		return fmt.Errorf("downloaded Codex asset did not contain executable codex binary")
	}
	if err := installCodexWrapper(found, expected); err != nil {
		return err
	}
	return chmodCodexInstallHelpers(targetDir)
}

// chmodCodexInstallHelpers 处理chmodcodex安装helpers。
func chmodCodexInstallHelpers(targetDir string) error {
	for _, path := range []string{
		filepath.Join(targetDir, "codex_cli_bin", "bin", codexExecutableFileName()),
		filepath.Join(targetDir, "codex_cli_bin", "codex-path", codexExecutableFileNameFor("rg")),
	} {
		if _, err := os.Stat(path); err == nil {
			if err := os.Chmod(path, 0o755); err != nil {
				return fmt.Errorf("chmod Codex helper %s: %w", path, err)
			}
		}
	}
	return nil
}

// findExtractedCodexExecutable 查找extractedcodex可执行文件。
func findExtractedCodexExecutable(root string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || found != "" {
			return walkErr
		}
		if entry.IsDir() || filepath.Base(path) != codexExecutableFileName() {
			return nil
		}
		if isExecutable(path) {
			found = path
		}
		return nil
	})
	return found, err
}

func installCodexWrapper(actual, expected string) error {
	if err := os.MkdirAll(filepath.Dir(expected), 0o755); err != nil {
		return fmt.Errorf("create Codex wrapper dir: %w", err)
	}
	if runtime.GOOS == "windows" {
		return copyCodexExecutable(actual, expected)
	}
	rel, err := filepath.Rel(filepath.Dir(expected), actual)
	if err != nil {
		return fmt.Errorf("resolve Codex wrapper target: %w", err)
	}
	body := strings.Join([]string{
		"#!/bin/sh",
		`case "$0" in */*) script_dir=${0%/*} ;; *) script_dir=. ;; esac`,
		`exec "$script_dir"/` + shellQuote(filepath.ToSlash(rel)) + ` "$@"`,
		"",
	}, "\n")
	return os.WriteFile(expected, []byte(body), 0o755)
}

func copyCodexExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open Codex executable: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("create Codex executable copy: %w", err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	return errors.Join(copyErr, closeErr)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func compareCodexInstallNames(a, b string) int {
	aParts, aOK := codexInstallVersionParts(a)
	bParts, bOK := codexInstallVersionParts(b)
	if aOK && bOK {
		return compareIntParts(aParts, bParts)
	}
	if aOK {
		return 1
	}
	if bOK {
		return -1
	}
	return strings.Compare(a, b)
}

func codexInstallVersionParts(name string) ([]int, bool) {
	version := strings.TrimPrefix(strings.TrimPrefix(name, "rust-v"), "v")
	parts := strings.Split(version, ".")
	if len(parts) == 0 || parts[0] == name {
		return nil, false
	}
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		out = append(out, value)
	}
	return out, true
}

// compareIntParts 处理compareintparts。
func compareIntParts(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			return av - bv
		}
	}
	return 0
}

// downloadCodexAsset 处理downloadcodexasset。
func downloadCodexAsset(ctx context.Context, rawURL, checksum, target string) error {
	if strings.TrimSpace(rawURL) == "" {
		return errors.New("Codex release asset download URL is empty")
	}
	if err := validateCodexAssetDownloadURL(rawURL); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("build Codex asset request: %w", err)
	}
	req.Header.Set("User-Agent", "Super-Dolphin")
	resp, err := codexHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("download Codex release asset: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := validateCodexDownloadResponse(resp); err != nil {
		return err
	}
	return writeCodexDownloadBody(resp.Body, checksum, target)
}

// validateCodexAssetDownloadURL 校验codexassetdownloadURL。
func validateCodexAssetDownloadURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid Codex release asset URL %q", rawURL)
	}
	if isOfficialCodexAssetURL(parsed) {
		return nil
	}
	if !trustedCodexReleaseMirror() {
		return fmt.Errorf("untrusted Codex release asset URL %q; use %s only for explicitly trusted mirrors", rawURL, codexTrustedReleaseMirrorEnv)
	}
	return validateTrustedCodexMirrorURL(parsed, "Codex release asset URL")
}

func isOfficialCodexAssetURL(parsed *url.URL) bool {
	return parsed.Scheme == "https" &&
		strings.EqualFold(parsed.Hostname(), "github.com") &&
		strings.HasPrefix(parsed.EscapedPath(), "/openai/codex/releases/download/")
}

func validateCodexDownloadResponse(resp *http.Response) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download Codex release asset: unexpected HTTP status %s", resp.Status)
	}
	if resp.ContentLength > maxCodexDownloadBytes {
		return fmt.Errorf("Codex release asset is too large: %d bytes", resp.ContentLength)
	}
	return nil
}

// writeCodexDownloadBody 写入codexdownload正文。
func writeCodexDownloadBody(body io.Reader, checksum, target string) error {
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create Codex wheel file: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(body, maxCodexDownloadBytes+1))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(target)
		return fmt.Errorf("write Codex wheel file: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(target)
		return fmt.Errorf("close Codex wheel file: %w", closeErr)
	}
	if written > maxCodexDownloadBytes {
		_ = os.Remove(target)
		return fmt.Errorf("Codex release asset exceeded %d bytes", maxCodexDownloadBytes)
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != checksum {
		_ = os.Remove(target)
		return fmt.Errorf("Codex release asset checksum mismatch: got %s want %s", got, checksum)
	}
	return nil
}
