//go:build windows

package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	// WindowsGoSQLSVersion 是 Windows 生产 SQLS 源码的固定版本。
	WindowsGoSQLSVersion = "0.2.48"
	// WindowsGoSQLSModuleZipURL 是 SQLS v0.2.48 的 Go module proxy 固定地址。
	WindowsGoSQLSModuleZipURL = "https://proxy.golang.org/github.com/sqls-server/sqls/@v/v0.2.48.zip"
	// WindowsGoSQLSModuleZipSize 是 SQLS module zip 的锁定字节数，下载完成后必须精确匹配。
	WindowsGoSQLSModuleZipSize int64 = 1844570
	// WindowsGoSQLSModuleZipSHA256 是 SQLS module zip 的固定 SHA-256。
	WindowsGoSQLSModuleZipSHA256 = "2CEF077F0432DD264E4B8A85348F887DBF508C601B4B0CC7BDB27B4E566DB1F2"
	// WindowsGoSQLSOraclePrePatchSHA256 是 oracle.go 的上游源锚点摘要。
	WindowsGoSQLSOraclePrePatchSHA256 = "FAB4EBFB2062A14F6FCC646E947E83B7E199980D5C3A6AB39096F97633A51D8E"
	// WindowsGoSQLSWorkerPrePatchSHA256 是 worker.go 的上游源锚点摘要。
	WindowsGoSQLSWorkerPrePatchSHA256 = "C0DBF5498AB4391234830454D6DFEE675B35BEC5F2335A8BF8EA596A5C922F81"
	// WindowsGoSQLSOraclePostPatchSHA256 是插入 cgo build tag 后的 oracle.go 摘要。
	WindowsGoSQLSOraclePostPatchSHA256 = "D1BDB3BCA6F7DAE696ABB52B611D350E31581EC4F6C86A7045D2FD5FE85E1367"
	// WindowsGoSQLSWorkerPostPatchSHA256 是 sync.Once 补丁并 gofmt 后的 worker.go 摘要。
	WindowsGoSQLSWorkerPostPatchSHA256 = "B19ADE0A292BF3962B54C48268A7CC773BD41D93C19FD05A4D45A02F7932B053"
	// WindowsGoSQLSBinaryName 是 Windows SQLS 生产二进制的真实文件名。
	WindowsGoSQLSBinaryName = "sqls.exe"
)

const (
	windowsGoSQLSSourceRoot = "github.com/sqls-server/sqls@v0.2.48"
	windowsGoSQLSOraclePath = windowsGoSQLSSourceRoot + "/internal/database/oracle.go"
	windowsGoSQLSWorkerPath = windowsGoSQLSSourceRoot + "/internal/database/worker.go"
	windowsGoSQLSModulePath = windowsGoSQLSSourceRoot + "/go.mod"
	windowsGoSQLSGoPath     = "go/bin/go.exe"
	windowsGoSQLSOutputPath = "bin/sqls.exe"
)

// ErrWindowsGoSQLSBinaryInvalid 表示构建产物不是目标 NativeArch 的合法 Windows PE。
var ErrWindowsGoSQLSBinaryInvalid = errors.New("Windows Go SQLS executable is invalid")

// installWindowsGoSQLS 使用锁定的 Go 1.26.5 和 SQLS module 源码构建 Windows 原生 SQLS。
// NativeArch 由调用方传入，ProcessArch 不参与构建选择，也不允许跨架构回退。
func installWindowsGoSQLS(ctx context.Context, entry WindowsRuntimeDependencyCatalogEntry, architecture, stage string, payloads map[string]string, runner WindowsRuntimeDependencyCommandRunner) error {
	if runner == nil {
		runner = windowsGoSQLSCommandRunner(stage)
	}
	if err := validateWindowsGoSQLSModulePayload(entry, architecture, payloads); err != nil {
		return err
	}
	sourceRoot, err := locateWindowsRuntimeDependencyPath(stage, windowsGoSQLSModulePath)
	if err != nil {
		return fmt.Errorf("locate SQLS v%s source module: %w", WindowsGoSQLSVersion, err)
	}
	sourceRoot = filepath.Dir(sourceRoot)
	if err := validateWindowsInstallerPathWithinRoot(stage, sourceRoot, false); err != nil {
		return fmt.Errorf("validate SQLS source root: %w", err)
	}
	goRoot, err := windowsGoSQLSBuildRoot(stage, architecture)
	if err != nil {
		return fmt.Errorf("locate staged full Go 1.26.5 SDK: %w", err)
	}
	goPath := filepath.Join(goRoot, "bin", "go.exe")
	if err := windowsGoSQLSPatchSource(sourceRoot); err != nil {
		return err
	}
	outputPath := filepath.Join(stage, filepath.FromSlash(windowsGoSQLSOutputPath))
	if err := ensureDirectoryNoSymlink(filepath.Dir(outputPath)); err != nil {
		return fmt.Errorf("create SQLS output directory: %w", err)
	}
	buildCacheRoot, err := windowsGoSQLSBuildCacheRoot(stage, architecture)
	if err != nil {
		return err
	}
	if err := ensureDirectoryNoSymlink(buildCacheRoot); err != nil {
		return fmt.Errorf("create SQLS isolated Go module cache: %w", err)
	}
	args, err := windowsGoSQLSBuildArgs(entry, outputPath)
	if err != nil {
		return err
	}
	env, err := windowsGoSQLSBuildEnvironment(stage, architecture)
	if err != nil {
		return err
	}
	if err := runner(ctx, goPath, sourceRoot, args, env); err != nil {
		return fmt.Errorf("build SQLS v%s for Windows %s: %w", WindowsGoSQLSVersion, architecture, err)
	}
	if err := ValidateWindowsGoSQLSExecutable(outputPath, architecture); err != nil {
		return err
	}
	compilerBackup := filepath.Join(stage, ".sqls-go.exe")
	if err := copyWindowsGoSQLSFile(goPath, compilerBackup); err != nil {
		return fmt.Errorf("preserve product-owned Go compiler: %w", err)
	}
	if err := pruneWindowsGoSQLSBuildInputs(stage, sourceRoot); err != nil {
		return err
	}
	if err := ensureDirectoryNoSymlink(filepath.Join(stage, "go", "bin")); err != nil {
		return fmt.Errorf("publish product-owned Go compiler directory: %w", err)
	}
	if err := os.Rename(compilerBackup, filepath.Join(stage, "go", "bin", "go.exe")); err != nil {
		return fmt.Errorf("publish product-owned Go compiler: %w", err)
	}
	// SQLS 通过 os.UserConfigDir 解析配置；必须把 APPDATA 固定到当前产品 cohort，
	// 不能依赖用户环境中的系统 AppData，也不能让配置写出产品根目录。
	if err := ensureDirectoryNoSymlink(filepath.Join(stage, "config")); err != nil {
		return fmt.Errorf("create SQLS product-owned config directory: %w", err)
	}
	return nil
}

func copyWindowsGoSQLSFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func windowsGoSQLSCommandRunner(stage string) WindowsRuntimeDependencyCommandRunner {
	return func(ctx context.Context, executable, workingDir string, args, env []string) error {
		command := exec.CommandContext(ctx, executable, args...)
		command.Dir = workingDir
		command.Env = runtimeDependencyCommandEnvironment(env)
		output, err := command.CombinedOutput()
		redacted := strings.ReplaceAll(string(output), stage, "<stage>")
		logPath := filepath.Join(filepath.Dir(stage), filepath.Base(stage)+".sqls-build.log")
		if writeErr := os.WriteFile(logPath, []byte(redacted), 0o600); writeErr != nil && err == nil {
			return fmt.Errorf("write SQLS build diagnostic log: %w", writeErr)
		}
		if err == nil {
			_ = os.Remove(logPath)
			return nil
		}
		tail := redacted
		lines := strings.Split(tail, "\n")
		if len(lines) > 200 {
			lines = lines[len(lines)-200:]
		}
		tail = strings.TrimSpace(strings.Join(lines, "\n"))
		return fmt.Errorf("SQLS build failed executable=%s goos=%s goarch=%s cgo=%s module=%s package=%s output_tail=%s: %w",
			securefs.RedactPath(executable), envValue(env, "GOOS"), envValue(env, "GOARCH"), envValue(env, "CGO_ENABLED"),
			filepath.ToSlash(filepath.Base(workingDir)), filepath.ToSlash(filepath.Base(args[len(args)-1])), tail,
			newProcessFailureError("runtime-dependency-command", "runtime", joinProcessFailureCause(ctx.Err(), err), output, len(args), 0))
	}
}

func envValue(env []string, key string) string {
	for _, value := range env {
		if strings.HasPrefix(value, key+"=") {
			return strings.TrimPrefix(value, key+"=")
		}
	}
	return "<unset>"
}

// pruneWindowsGoSQLSBuildInputs 删除仅用于构建的 Go SDK 与解压源码，避免运行时 ready 树携带近 GB 的工具链。
func pruneWindowsGoSQLSBuildInputs(stage, sourceRoot string) error {
	cleanupRoot := stage + ".build-inputs"
	if err := ensureDirectoryNoSymlink(cleanupRoot); err != nil {
		return fmt.Errorf("create deferred SQLS build cleanup root: %w", err)
	}
	for index, target := range []string{sourceRoot, filepath.Join(stage, "go")} {
		if _, err := os.Lstat(target); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return fmt.Errorf("inspect SQLS build input %s: %w", securefs.RedactPath(target), securefs.WrapErrorForPath(err, target))
		}
		deferredTarget := filepath.Join(cleanupRoot, fmt.Sprintf("input-%d", index))
		if err := renameWindowsInstallerPathChecked(filepath.Dir(stage), target, deferredTarget); err != nil {
			return fmt.Errorf("quarantine SQLS build input %s: %w", securefs.RedactPath(target), securefs.WrapErrorForPath(err, target))
		}
	}
	launchWindowsGoSQLSDeferredCleanup(filepath.Dir(stage), cleanupRoot)
	return nil
}

const windowsGoSQLSDeferredCleanupTimeout = 30 * time.Second

// launchWindowsGoSQLSDeferredCleanup removes quarantined build inputs after the
// ready binary has passed validation. Rename is the publication barrier; a
// slow/locked recursive delete must never delay that barrier. Failures are
// retained beside the quarantine root for the next provisioning attempt to
// observe rather than being silently discarded.
func launchWindowsGoSQLSDeferredCleanup(root, cleanupRoot string) {
	go func() {
		done := make(chan error, 1)
		go func() { done <- removeWindowsInstallerAllChecked(root, cleanupRoot) }()
		timer := time.NewTimer(windowsGoSQLSDeferredCleanupTimeout)
		defer timer.Stop()
		select {
		case err := <-done:
			if err == nil || os.IsNotExist(err) {
				return
			}
			writeWindowsGoSQLSCleanupFailure(cleanupRoot, err)
		case <-timer.C:
			writeWindowsGoSQLSCleanupFailure(cleanupRoot, context.DeadlineExceeded)
		}
	}()
}

func writeWindowsGoSQLSCleanupFailure(cleanupRoot string, err error) {
	if err == nil {
		return
	}
	marker := cleanupRoot + ".error"
	_ = os.WriteFile(marker, []byte(err.Error()+"\n"), 0o600)
}

// windowsGoSQLSBuildCacheRoot 返回按版本/NativeArch 隔离的 product-owned Go module cache。
func windowsGoSQLSBuildCacheRoot(stage, architecture string) (string, error) {
	normalized, err := NormalizeWindowsArchitectureAlias(architecture)
	if err != nil {
		return "", fmt.Errorf("select SQLS build cache architecture %q: %w", architecture, err)
	}
	return filepath.Join(filepath.Dir(stage), ".go-sqls-build-cache", cacheSegment(WindowsGoSQLSVersion+"-"+normalized)), nil
}

func validateWindowsGoSQLSModulePayload(entry WindowsRuntimeDependencyCatalogEntry, architecture string, payloads map[string]string) error {
	var sourceAsset *WindowsRuntimeDependencyAsset
	for index := range entry.AssetsByArchitecture[architecture] {
		asset := &entry.AssetsByArchitecture[architecture][index]
		if asset.Component == "sqls-source" {
			sourceAsset = asset
			break
		}
	}
	if sourceAsset == nil {
		return fmt.Errorf("SQLS catalog %s has no source asset", architecture)
	}
	if sourceAsset.URL != WindowsGoSQLSModuleZipURL || !strings.EqualFold(sourceAsset.Checksum, WindowsGoSQLSModuleZipSHA256) {
		return fmt.Errorf("SQLS source asset pin drifted: URL=%q SHA256=%q", sourceAsset.URL, sourceAsset.Checksum)
	}
	payload, ok := payloads["sqls-source"]
	if !ok || strings.TrimSpace(payload) == "" {
		return errors.New("SQLS source payload is missing")
	}
	info, err := os.Lstat(payload)
	if err != nil {
		return fmt.Errorf("inspect SQLS source payload: %w", err)
	}
	if isUnsafeAssetFile(info) || !info.Mode().IsRegular() {
		return fmt.Errorf("SQLS source payload is not a regular file: %q", payload)
	}
	if info.Size() != WindowsGoSQLSModuleZipSize {
		return fmt.Errorf("SQLS source payload size=%d want=%d", info.Size(), WindowsGoSQLSModuleZipSize)
	}
	actual, err := windowsGoSQLSFileSHA256(payload)
	if err != nil {
		return fmt.Errorf("hash SQLS source payload: %w", err)
	}
	if !strings.EqualFold(actual, WindowsGoSQLSModuleZipSHA256) {
		return fmt.Errorf("SQLS source payload SHA256=%s want=%s", actual, strings.ToLower(WindowsGoSQLSModuleZipSHA256))
	}
	return nil
}

func windowsGoSQLSPatchSource(sourceRoot string) error {
	oraclePath := filepath.Join(sourceRoot, "internal", "database", "oracle.go")
	workerPath := filepath.Join(sourceRoot, "internal", "database", "worker.go")
	oracle, err := windowsGoSQLSPatchFileBytes(oraclePath, WindowsGoSQLSOraclePrePatchSHA256, WindowsGoSQLSOraclePostPatchSHA256)
	if err != nil {
		return fmt.Errorf("patch locked SQLS oracle.go: %w", err)
	}
	if !bytes.HasPrefix(oracle, []byte("//go:build cgo\n\npackage database\n")) {
		oracle = append([]byte("//go:build cgo\n\n"), oracle...)
	}
	if err := windowsGoSQLSWritePatchedFile(sourceRoot, oraclePath, oracle, WindowsGoSQLSOraclePostPatchSHA256); err != nil {
		return fmt.Errorf("publish SQLS oracle.go cgo guard: %w", err)
	}

	worker, err := windowsGoSQLSPatchFileBytes(workerPath, WindowsGoSQLSWorkerPrePatchSHA256, WindowsGoSQLSWorkerPostPatchSHA256)
	if err != nil {
		return fmt.Errorf("patch locked SQLS worker.go: %w", err)
	}
	if !bytes.Contains(worker, []byte("stopOnce sync.Once")) {
		oldFields := []byte("\tdone   chan struct{}\n\tupdate chan struct{}\n\tlock   sync.Mutex\n")
		newFields := []byte("\tdone     chan struct{}\n\tupdate   chan struct{}\n\tlock     sync.Mutex\n\tstopOnce sync.Once\n")
		if bytes.Count(worker, oldFields) != 1 {
			return errors.New("SQLS worker.go field patch anchor is missing or ambiguous")
		}
		worker = bytes.Replace(worker, oldFields, newFields, 1)
	}
	oldStop := []byte("func (w *Worker) Stop() {\n\tclose(w.done)\n}\n")
	newStop := []byte("func (w *Worker) Stop() {\n\tw.stopOnce.Do(func() {\n\t\tclose(w.done)\n\t})\n}\n")
	if bytes.Count(worker, oldStop) != 1 && !bytes.Contains(worker, newStop) {
		return errors.New("SQLS worker.go Stop patch anchor is missing or ambiguous")
	}
	worker = bytes.Replace(worker, oldStop, newStop, 1)
	if err := windowsGoSQLSWritePatchedFile(sourceRoot, workerPath, worker, WindowsGoSQLSWorkerPostPatchSHA256); err != nil {
		return fmt.Errorf("publish SQLS worker.go sync.Once guard: %w", err)
	}
	return nil
}

func windowsGoSQLSPatchFileBytes(path, preHash, postHash string) ([]byte, error) {
	displayPath := securefs.RedactPath(path)
	if err := validateWindowsInstallerExistingFile(path); err != nil {
		return nil, fmt.Errorf("inspect SQLS source %s: %w", displayPath, securefs.WrapErrorForPath(err, path))
	}
	input, err := openWindowsInstallerInput(path)
	if err != nil {
		return nil, fmt.Errorf("open SQLS source %s: %w", displayPath, securefs.WrapErrorForPath(err, path))
	}
	data, readErr := io.ReadAll(input)
	closeErr := input.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read SQLS source %s: %w", displayPath, securefs.WrapErrorForPath(readErr, path))
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close SQLS source %s: %w", displayPath, securefs.WrapErrorForPath(closeErr, path))
	}
	hash := sha256.Sum256(data)
	actual := hex.EncodeToString(hash[:])
	if strings.EqualFold(actual, postHash) {
		return data, nil
	}
	if !strings.EqualFold(actual, preHash) {
		return nil, fmt.Errorf("SQLS source %s hash=%s want pre=%s or post=%s", displayPath, actual, preHash, postHash)
	}
	return data, nil
}

func windowsGoSQLSFileSHA256(path string) (string, error) {
	displayPath := securefs.RedactPath(path)
	if err := validateWindowsInstallerExistingFile(path); err != nil {
		return "", fmt.Errorf("inspect SQLS payload %s: %w", displayPath, securefs.WrapErrorForPath(err, path))
	}
	input, err := openWindowsInstallerInput(path)
	if err != nil {
		return "", fmt.Errorf("open SQLS payload %s: %w", displayPath, securefs.WrapErrorForPath(err, path))
	}
	hasher := sha256.New()
	_, copyErr := io.Copy(hasher, input)
	closeErr := input.Close()
	if copyErr != nil {
		return "", fmt.Errorf("hash SQLS payload %s: %w", displayPath, securefs.WrapErrorForPath(copyErr, path))
	}
	if closeErr != nil {
		return "", fmt.Errorf("close SQLS payload %s: %w", displayPath, securefs.WrapErrorForPath(closeErr, path))
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func windowsGoSQLSWritePatchedFile(root, path string, data []byte, expectedHash string) (err error) {
	hash := sha256.Sum256(data)
	actual := hex.EncodeToString(hash[:])
	if !strings.EqualFold(actual, expectedHash) {
		return fmt.Errorf("patched source hash=%s want=%s", actual, expectedHash)
	}
	if err := validateWindowsInstallerPathWithinRoot(root, path, false); err != nil {
		return fmt.Errorf("validate SQLS source target %s: %w", securefs.RedactPath(path), securefs.WrapErrorForPath(err, path))
	}
	if err := validateWindowsInstallerExistingFile(path); err != nil {
		return fmt.Errorf("inspect SQLS source target %s: %w", securefs.RedactPath(path), securefs.WrapErrorForPath(err, path))
	}
	temporary, err := createWindowsInstallerTemp(filepath.Dir(path), ".sqls-patch-")
	if err != nil {
		return fmt.Errorf("create SQLS source patch temporary %s: %w", securefs.RedactPath(path), securefs.WrapErrorForPath(err, path))
	}
	temporaryName := temporary.Name()
	temporaryClosed := false
	published := false
	defer func() {
		if !temporaryClosed {
			err = joinWindowsInstallerCleanupError(err, temporary.Close(), "close SQLS source patch temporary")
		}
		if !published {
			removeErr := removeWindowsInstallerPathChecked(root, temporaryName)
			if removeErr != nil && !os.IsNotExist(removeErr) {
				err = joinWindowsInstallerCleanupError(err, securefs.WrapErrorForPath(removeErr, temporaryName), "remove SQLS source patch temporary")
			}
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict SQLS source patch temporary: %w", securefs.WrapErrorForPath(err, temporaryName))
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write SQLS source patch temporary: %w", securefs.WrapErrorForPath(err, temporaryName))
	}
	if err := temporary.Close(); err != nil {
		temporaryClosed = true
		return fmt.Errorf("close SQLS source patch temporary: %w", securefs.WrapErrorForPath(err, temporaryName))
	}
	temporaryClosed = true
	if err := securefs.RestrictOwnerOnly(temporaryName, 0o600); err != nil {
		return fmt.Errorf("restrict SQLS source patch ACL: %w", securefs.WrapErrorForPath(err, temporaryName))
	}
	if err := renameWindowsInstallerPathChecked(root, temporaryName, path); err != nil {
		return fmt.Errorf("publish SQLS source patch %s: %w", securefs.RedactPath(path), securefs.WrapErrorForPath(err, path))
	}
	published = true
	return nil
}

func windowsGoSQLSBuildArgs(entry WindowsRuntimeDependencyCatalogEntry, outputPath string) ([]string, error) {
	want := []string{"build", "-trimpath", "-mod=readonly", "-o", "bin/sqls.exe", "./"}
	if len(entry.Install.Args) != len(want) {
		return nil, fmt.Errorf("Go SQLS build args drifted: got=%#v want=%#v", entry.Install.Args, want)
	}
	for index := range want {
		if entry.Install.Args[index] != want[index] {
			return nil, fmt.Errorf("Go SQLS build args drifted: got=%#v want=%#v", entry.Install.Args, want)
		}
	}
	args := append([]string(nil), entry.Install.Args...)
	args[4] = outputPath
	return args, nil
}

func windowsGoSQLSManagedGoRoot(stage, architecture string) (string, error) {
	normalized, err := NormalizeWindowsArchitectureAlias(architecture)
	if err != nil {
		return "", err
	}
	cacheRoot := filepath.Join(filepath.Dir(stage), "..", "..")
	result, err := ResolveWindowsRuntimeDependency(WindowsRuntimeDependencyProductGoGopls, cacheRoot)
	if err != nil {
		return "", fmt.Errorf("resolve ready go-gopls cohort: %w", err)
	}
	if result.Architecture != normalized {
		return "", fmt.Errorf("managed Go architecture=%s want=%s", result.Architecture, normalized)
	}
	goRoot := filepath.Join(result.RootPath, "go")
	goPath := filepath.Join(goRoot, "bin", "go.exe")
	if err := validateWindowsInstallerPathWithinRoot(result.RootPath, goPath, false); err != nil {
		return "", fmt.Errorf("validate managed Go executable: %w", err)
	}
	if err := validateWindowsInstallerExistingFile(goPath); err != nil {
		return "", fmt.Errorf("inspect managed Go executable: %w", err)
	}
	return goRoot, nil
}

func materializeWindowsGoSQLSBuildSDK(stage, architecture string, asset WindowsRuntimeDependencyAsset) error {
	if systemRoot, err := windowsGoSQLSDiscoverFullGoSDK(architecture); err == nil {
		if err := copyWindowsRuntimeDependencyDirectory(systemRoot, filepath.Join(stage, "go")); err != nil {
			return fmt.Errorf("stage managed full Go SDK %q: %w", securefs.RedactPath(systemRoot), err)
		}
		return nil
	}
	goRoot, err := windowsGoSQLSManagedGoRoot(stage, architecture)
	if err != nil {
		return err
	}
	managedRoot := filepath.Dir(goRoot)
	source := filepath.Join(managedRoot, ".runtime-assets", "go", "go-1.26.5.zip")
	if err := validateWindowsInstallerExistingFile(source); err != nil {
		return fmt.Errorf("inspect managed Go SDK payload: %w", err)
	}
	destination := filepath.Join(stage, ".runtime-assets", "go", "go-1.26.5.zip")
	if err := ensureDirectoryNoSymlink(filepath.Dir(destination)); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(output, hasher), input); err != nil {
		output.Close()
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), asset.Checksum) {
		return fmt.Errorf("managed Go SDK payload checksum mismatch")
	}
	if err := extractZipAsset(destination, stage, asset.BinaryPath, runtimeDependencyMaxTreeBytes); err != nil {
		return err
	}
	return nil
}

// windowsGoSQLSDiscoverFullGoSDK finds only an explicitly managed or PATH
// Go executable, then validates its exact release, target, and full GOROOT.
// The discovered SDK is copied into the SQLS staging tree and is never used
// as a runtime dependency cohort.
func windowsGoSQLSDiscoverFullGoSDK(architecture string) (string, error) {
	normalized, err := NormalizeWindowsArchitectureAlias(architecture)
	if err != nil {
		return "", err
	}
	candidates := make([]string, 0, 2)
	if root := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_GO_SDK_ROOT")); root != "" {
		candidates = append(candidates, filepath.Join(root, "bin", "go.exe"))
	}
	if path, lookErr := exec.LookPath("go.exe"); lookErr == nil {
		candidates = append(candidates, path)
	}
	var reasons []string
	seen := make(map[string]struct{}, len(candidates))
	for _, executable := range candidates {
		executable, err = filepath.Abs(executable)
		if err != nil {
			continue
		}
		if _, ok := seen[strings.ToLower(executable)]; ok {
			continue
		}
		seen[strings.ToLower(executable)] = struct{}{}
		root := filepath.Dir(filepath.Dir(executable))
		command := exec.Command(executable, "version")
		output, runErr := command.Output()
		version := strings.TrimSpace(string(output))
		wantArch := map[string]string{WindowsHostArchARM64: "arm64", WindowsHostArchX64: "amd64", WindowsHostArchX86: "386"}[normalized]
		if runErr != nil || version != "go version go1.26.5 windows/"+wantArch {
			reasons = append(reasons, fmt.Sprintf("%s version=%q err=%v", securefs.RedactPath(executable), version, runErr))
			continue
		}
		for _, relative := range []string{"src/context", "pkg/tool/windows_" + wantArch, "bin/go.exe"} {
			info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
			if statErr != nil {
				reasons = append(reasons, fmt.Sprintf("%s missing %s: %v", securefs.RedactPath(root), relative, statErr))
				root = ""
				break
			}
			if relative != "bin/go.exe" && !info.IsDir() {
				reasons = append(reasons, fmt.Sprintf("%s %s is not a directory", securefs.RedactPath(root), relative))
				root = ""
				break
			}
		}
		if root != "" {
			return root, nil
		}
	}
	return "", fmt.Errorf("no managed full Go 1.26.5 SDK for Windows %s: %s", normalized, strings.Join(reasons, "; "))
}

// windowsGoSQLSBuildRoot deliberately requires the full staged SDK. The
// gopls runtime cohort contains only go.exe and is not a valid GOROOT.
func windowsGoSQLSBuildRoot(stage, architecture string) (string, error) {
	if _, err := NormalizeWindowsArchitectureAlias(architecture); err != nil {
		return "", err
	}
	root := filepath.Join(stage, "go")
	for _, relative := range []string{"src/context", "pkg/tool", "bin/go.exe"} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Stat(path)
		if err != nil {
			if relative != "bin/go.exe" {
				return "", fmt.Errorf("staged Go SDK is not full: missing %s: %w", relative, err)
			}
			return "", fmt.Errorf("staged Go SDK executable missing: %w", err)
		}
		if relative != "bin/go.exe" && !info.IsDir() {
			return "", fmt.Errorf("staged Go SDK is not full: %s is not a directory", relative)
		}
		if relative == "bin/go.exe" && !info.Mode().IsRegular() {
			return "", fmt.Errorf("staged Go SDK executable is not a regular file")
		}
	}
	return root, nil
}

func windowsGoSQLSBuildEnvironment(stage, architecture string) ([]string, error) {
	normalized, err := NormalizeWindowsArchitectureAlias(architecture)
	if err != nil {
		return nil, fmt.Errorf("select Go SQLS target architecture %q: %w", architecture, err)
	}
	goarch, ok := map[string]string{WindowsHostArchARM64: "arm64", WindowsHostArchX64: "amd64", WindowsHostArchX86: "386"}[normalized]
	if !ok {
		return nil, fmt.Errorf("select Go SQLS target architecture %q: no native Go mapping", normalized)
	}
	buildCacheRoot, err := windowsGoSQLSBuildCacheRoot(stage, normalized)
	if err != nil {
		return nil, err
	}
	goRoot, err := windowsGoSQLSBuildRoot(stage, normalized)
	if err != nil {
		return nil, err
	}
	return []string{
		"CGO_ENABLED=0",
		"GOOS=windows",
		"GOARCH=" + goarch,
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"GO111MODULE=on",
		"GOROOT=" + goRoot,
		"GOBIN=" + filepath.Join(stage, "bin"),
		"GOMODCACHE=" + filepath.Join(buildCacheRoot, "gomodcache"),
		"GOCACHE=" + filepath.Join(buildCacheRoot, "gocache"),
		"GOPROXY=https://proxy.golang.org",
		"GOSUMDB=sum.golang.org",
		"GOPRIVATE=",
		"GONOPROXY=",
		"GONOSUMDB=",
		"PATH=" + filepath.Join(goRoot, "bin"),
	}, nil
}

// ValidateWindowsGoSQLSExecutable 验证 SQLS 输出为目标 NativeArch 的 Windows PE。
func ValidateWindowsGoSQLSExecutable(path, architecture string) error {
	normalized, err := NormalizeWindowsArchitectureAlias(architecture)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWindowsGoSQLSBinaryInvalid, err)
	}
	resolved, err := requireWindowsResolverFile(path, ErrWindowsGoSQLSBinaryInvalid)
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Errorf("%w: read executable: %v", ErrWindowsGoSQLSBinaryInvalid, err)
	}
	image, err := pe.NewFile(bytes.NewReader(contents))
	if err != nil {
		return fmt.Errorf("%w: parse PE: %v", ErrWindowsGoSQLSBinaryInvalid, err)
	}
	defer image.Close()
	want := map[string]uint16{
		WindowsHostArchARM64: WindowsImageFileMachineARM64,
		WindowsHostArchX64:   WindowsImageFileMachineAMD64,
		WindowsHostArchX86:   WindowsImageFileMachineI386,
	}[normalized]
	if image.FileHeader.Machine != want {
		return fmt.Errorf("%w: PE machine=0x%04x want=0x%04x for %s", ErrWindowsGoSQLSBinaryInvalid, image.FileHeader.Machine, want, normalized)
	}
	return nil
}
