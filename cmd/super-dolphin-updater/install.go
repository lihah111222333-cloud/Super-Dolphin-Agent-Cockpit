package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Super Dolphin macOS app bundle 内关键路径和期望标识。
const (
	expectedBundleID = "com.superdolphin.app"
	launcherPath     = "Contents/MacOS/agent-terminal"
	manifestPath     = "Contents/Resources/runtime-manifest.json"
	infoPlistPath    = "Contents/Info.plist"
)

// installRequest 描述一次 updater 安装请求。
type installRequest struct {
	DMGPath       string
	TargetAppPath string
	Restart       bool
	AllowUnsigned bool
	WaitPID       int
	LogPath       string
}

// commandResult 保存外部命令 stdout/stderr。
type commandResult struct {
	stdout string
	stderr string
}

// runCommand 执行系统命令，测试会替换它以避免真实改系统。
var runCommand = func(name string, args ...string) (commandResult, error) {
	cmd := exec.Command(name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return commandResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
	}, err
}

// runRestartCommand 调用 open 重启目标 app，并清理会污染桌面进程的开发环境变量。
var runRestartCommand = func(args ...string) (commandResult, error) {
	cmd := exec.Command("open", args...)
	cmd.Env = sanitizedRestartEnv(os.Environ())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return commandResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
	}, err
}

// waitForProcessExit 等待旧 app 进程退出，测试会替换它避免真实等待。
var waitForProcessExit = waitForProcessExitImpl

// install 挂载 DMG、执行安装并尽力卸载临时挂载点。
// 安装和清理错误会合并返回，避免清理失败被静默吞掉。
func install(req installRequest) error {
	if err := validateInstallRequest(req); err != nil {
		return err
	}
	mountPoint, err := mountDMG(req.DMGPath)
	if err != nil {
		return err
	}
	installErr := installFromMount(req, mountPoint)
	detachErr := detachDMG(mountPoint)
	removeErr := os.Remove(mountPoint)
	return errors.Join(installErr, detachErr, removeErr)
}

// installFromMount 从已挂载 DMG 安装 app。
// 复制前后都做结构和签名校验，任一步失败都不会继续重启目标 app。
func installFromMount(req installRequest, mountPoint string) error {
	stagedApp, err := findMountedApp(mountPoint)
	if err != nil {
		return err
	}
	if err := validateMountedApp(stagedApp); err != nil {
		return err
	}
	if req.WaitPID > 0 {
		if err := waitForProcessExit(req.WaitPID, 30*time.Second); err != nil {
			return err
		}
	}
	teamID := ""
	if !req.AllowUnsigned {
		var err error
		teamID, err = expectedTeamID(req.TargetAppPath)
		if err != nil {
			return err
		}
	}
	if err := verifyAppSignature(stagedApp, teamID, req.AllowUnsigned); err != nil {
		return fmt.Errorf("verify staged app: %w", err)
	}
	if err := replaceTargetApp(stagedApp, req.TargetAppPath, teamID, req.AllowUnsigned); err != nil {
		return err
	}
	if req.Restart {
		return restartTargetApp(req.TargetAppPath)
	}
	return nil
}

// restartTargetApp 使用 open -n 重启目标 app。
func restartTargetApp(targetApp string) error {
	if _, err := runRestartCommand("-n", targetApp); err != nil {
		return fmt.Errorf("restart target app: %w", commandError(err))
	}
	return nil
}

// sanitizedRestartEnv 清除启动桌面 app 时不应继承的开发/敏感环境变量。
func sanitizedRestartEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		if shouldDropRestartEnv(entry) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// shouldDropRestartEnv 判断某个环境变量是否需要从重启环境中剔除。
func shouldDropRestartEnv(entry string) bool {
	key, _, ok := strings.Cut(entry, "=")
	if !ok {
		key = entry
	}
	switch key {
	case "DATABASE_URL", "POSTGRES_CONNECTION_STRING", "VERSION":
		return true
	}
	for _, prefix := range []string{"SUPER_DOLPHIN_", "GO_AGENT_", "VITE_", "FRONTEND_DEVSERVER_"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// validateInstallRequest 校验 DMG 和目标 app 路径。
// 目标父目录必须可写，避免安装中途才发现无法替换 app。
func validateInstallRequest(req installRequest) error {
	dmgPath := strings.TrimSpace(req.DMGPath)
	targetPath := strings.TrimSpace(req.TargetAppPath)
	if dmgPath == "" {
		return errors.New("dmg path is required")
	}
	if targetPath == "" {
		return errors.New("target app path is required")
	}
	if !strings.EqualFold(filepath.Ext(dmgPath), ".dmg") {
		return fmt.Errorf("dmg path must end with .dmg: %s", dmgPath)
	}
	if !strings.EqualFold(filepath.Ext(targetPath), ".app") {
		return fmt.Errorf("target app path must end with .app: %s", targetPath)
	}
	if info, err := os.Stat(dmgPath); err != nil {
		return fmt.Errorf("dmg path is not available: %w", err)
	} else if info.IsDir() {
		return fmt.Errorf("dmg path must be a file: %s", dmgPath)
	}

	parent := filepath.Dir(targetPath)
	if info, err := os.Stat(parent); err != nil {
		return fmt.Errorf("target parent is not available: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("target parent is not a directory: %s", parent)
	}
	if err := verifyWritableDir(parent); err != nil {
		return fmt.Errorf("target parent is not writable: %w", err)
	}
	return validateWaitPID(req.WaitPID)
}

// validateWaitPID 校验等待退出的 PID 参数。
func validateWaitPID(pid int) error {
	if pid < 0 {
		return fmt.Errorf("wait pid must be non-negative: %d", pid)
	}
	return nil
}

// waitForProcessExitImpl 轮询等待目标进程退出。
func waitForProcessExitImpl(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		exists, err := processExists(pid)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for process %d to exit", pid)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// processExists 使用 kill -0 判断进程是否仍存在。
func processExists(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	cmd := exec.Command("kill", "-0", strconv.Itoa(pid))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		output := stderr.String()
		if strings.Contains(output, "No such process") {
			return false, nil
		}
		return false, fmt.Errorf("inspect process %d: %w: %s", pid, err, strings.TrimSpace(output))
	}
	return true, nil
}

// verifyWritableDir 通过创建临时文件验证目录可写。
func verifyWritableDir(dir string) error {
	probe, err := os.CreateTemp(dir, ".super-dolphin-updater-write-test-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(name)
	return errors.Join(closeErr, removeErr)
}

// validateMountedApp 校验挂载或复制后的 app bundle 结构。
// launcher、runtime manifest 和 CFBundleIdentifier 都必须符合预期。
func validateMountedApp(appPath string) error {
	if !strings.EqualFold(filepath.Ext(appPath), ".app") {
		return fmt.Errorf("mounted app must end with .app: %s", appPath)
	}
	launcher := filepath.Join(appPath, launcherPath)
	launcherInfo, err := os.Stat(launcher)
	if err != nil {
		return fmt.Errorf("missing launcher %s: %w", launcherPath, err)
	}
	if launcherInfo.IsDir() {
		return fmt.Errorf("launcher is a directory: %s", launcher)
	}
	if launcherInfo.Mode()&0o111 == 0 {
		return fmt.Errorf("launcher is not executable: %s", launcher)
	}
	if info, err := os.Stat(filepath.Join(appPath, manifestPath)); err != nil {
		return fmt.Errorf("missing %s: %w", manifestPath, err)
	} else if info.IsDir() {
		return fmt.Errorf("%s is a directory", manifestPath)
	}
	bundleID, err := readBundleID(filepath.Join(appPath, infoPlistPath))
	if err != nil {
		return err
	}
	if bundleID != expectedBundleID {
		return fmt.Errorf("Info.plist CFBundleIdentifier mismatch: expected %s, got %s", expectedBundleID, bundleID)
	}
	return nil
}

// readBundleID 从 Info.plist 读取 CFBundleIdentifier。
func readBundleID(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read Info.plist: %w", err)
	}
	defer file.Close()

	value, err := plistStringValue(xml.NewDecoder(file), "CFBundleIdentifier")
	if err != nil {
		return "", err
	}
	return value, nil
}

// plistStringValue 在 plist XML 中查找指定 key 的 string 值。
func plistStringValue(decoder *xml.Decoder, key string) (string, error) {
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("Info.plist missing %s", key)
		}
		if err != nil {
			return "", fmt.Errorf("parse Info.plist: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "key" {
			continue
		}
		var key string
		if err := decoder.DecodeElement(&key, &start); err != nil {
			return "", fmt.Errorf("parse Info.plist key: %w", err)
		}
		if key != "CFBundleIdentifier" {
			continue
		}
		return nextPlistString(decoder)
	}
}

// nextPlistString 读取紧跟 key 后的 string 值。
// 不是 string 会报错，避免把其它 plist 类型误当 bundle id。
func nextPlistString(decoder *xml.Decoder) (string, error) {
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", fmt.Errorf("parse Info.plist bundle id: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != "string" {
			return "", fmt.Errorf("CFBundleIdentifier must be a string, got %s", start.Name.Local)
		}
		var value string
		if err := decoder.DecodeElement(&value, &start); err != nil {
			return "", fmt.Errorf("parse Info.plist bundle id string: %w", err)
		}
		return value, nil
	}
}

// mountDMG 只读挂载 DMG 到临时目录。
func mountDMG(dmgPath string) (string, error) {
	mountPoint, err := os.MkdirTemp("", "super-dolphin-updater-mount-*")
	if err != nil {
		return "", fmt.Errorf("create mount point: %w", err)
	}
	if _, err := runCommand("hdiutil", "attach", dmgPath, "-nobrowse", "-readonly", "-mountpoint", mountPoint); err != nil {
		removeErr := os.Remove(mountPoint)
		return "", errors.Join(fmt.Errorf("mount dmg: %w", commandError(err)), removeErr)
	}
	return mountPoint, nil
}

// detachDMG 卸载 DMG，普通卸载失败后再尝试 force。
func detachDMG(mountPoint string) error {
	if _, err := runCommand("hdiutil", "detach", mountPoint); err == nil {
		return nil
	}
	if _, err := runCommand("hdiutil", "detach", "-force", mountPoint); err != nil {
		return fmt.Errorf("detach dmg with force: %w", commandError(err))
	}
	return nil
}

// findMountedApp 查找 DMG 顶层唯一的 .app bundle。
func findMountedApp(mountPoint string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(mountPoint, "*.app"))
	if err != nil {
		return "", fmt.Errorf("find mounted app: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("mounted dmg does not contain a top-level .app: %s", mountPoint)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("mounted dmg contains multiple top-level .app bundles: %s", strings.Join(matches, ", "))
	}
	return matches[0], nil
}

// expectedTeamID 解析安装时应匹配的 Developer Team ID。
// 目标 app 不存在时必须通过环境显式提供，避免首次安装信任未知签名。
func expectedTeamID(targetApp string) (string, error) {
	if teamID := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_EXPECTED_TEAM_ID")); teamID != "" {
		return teamID, nil
	}
	if _, err := os.Stat(targetApp); err != nil {
		return "", fmt.Errorf("SUPER_DOLPHIN_EXPECTED_TEAM_ID is required when target app is unavailable: %w", err)
	}
	teamID, err := appTeamID(targetApp)
	if err != nil {
		return "", fmt.Errorf("read installed app Team ID: %w", err)
	}
	return teamID, nil
}

// verifyAppSignature 校验 app 的 codesign、Team ID 和 Gatekeeper 评估。
// allowUnsigned 只允许灰度测试跳过 Team ID/Gatekeeper，codesign 仍必须通过。
func verifyAppSignature(appPath string, expectedTeamID string, allowUnsigned bool) error {
	if allowUnsigned {
		if _, err := runCommand("codesign", "--verify", "--deep", "--strict", "--verbose=4", appPath); err != nil {
			return fmt.Errorf("codesign verify failed: %w", commandError(err))
		}
		return nil
	}
	if expectedTeamID == "" {
		return errors.New("expected Team ID is required")
	}
	if _, err := runCommand("codesign", "--verify", "--deep", "--strict", "--verbose=4", appPath); err != nil {
		return fmt.Errorf("codesign verify failed: %w", commandError(err))
	}
	details, err := signingDetails(appPath)
	if err != nil {
		return err
	}
	teamID := parseSigningValue(details, "TeamIdentifier")
	if teamID == "" {
		return errors.New("codesign details missing TeamIdentifier")
	}
	if teamID != expectedTeamID {
		return fmt.Errorf("Team ID mismatch: expected %s, got %s", expectedTeamID, teamID)
	}
	if !strings.Contains(details, "Authority=Developer ID Application:") {
		return errors.New("codesign details missing Developer ID Application authority")
	}
	if _, err := runCommand("spctl", "-a", "-vv", "-t", "execute", appPath); err != nil {
		return fmt.Errorf("spctl execute assessment failed: %w", commandError(err))
	}
	return nil
}

// appTeamID 从已安装 app 的签名详情读取 Team ID。
func appTeamID(appPath string) (string, error) {
	details, err := signingDetails(appPath)
	if err != nil {
		return "", err
	}
	teamID := parseSigningValue(details, "TeamIdentifier")
	if teamID == "" {
		return "", errors.New("codesign details missing TeamIdentifier")
	}
	return teamID, nil
}

// signingDetails 调用 codesign 读取签名详情。
func signingDetails(appPath string) (string, error) {
	result, err := runCommand("codesign", "-dv", "--verbose=4", appPath)
	if err != nil {
		return "", fmt.Errorf("codesign details failed: %w", commandError(err))
	}
	return result.stdout + result.stderr, nil
}

// parseSigningValue 从 codesign 输出中提取指定 key。
func parseSigningValue(details string, key string) string {
	prefix := key + "="
	for _, line := range strings.Split(details, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

// replaceTargetApp 用已校验的 staged app 替换目标 app。
// 复制后再次验证结构和签名；失败会尽力恢复原 app。
func replaceTargetApp(stagedApp string, targetApp string, expectedTeamID string, allowUnsigned bool) error {
	backupApp := backupPath(targetApp)
	backupCreated := false
	if _, err := os.Stat(targetApp); err == nil {
		if err := os.Rename(targetApp, backupApp); err != nil {
			return fmt.Errorf("backup target app: %w", err)
		}
		backupCreated = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect target app: %w", err)
	}

	if err := copyApp(stagedApp, targetApp); err != nil {
		return rollbackAfterFailure(targetApp, backupApp, backupCreated, fmt.Errorf("copy app: %w", err))
	}
	if err := validateMountedApp(targetApp); err != nil {
		return rollbackAfterFailure(targetApp, backupApp, backupCreated, fmt.Errorf("verify copied app structure: %w", err))
	}
	if err := verifyAppSignature(targetApp, expectedTeamID, allowUnsigned); err != nil {
		return rollbackAfterFailure(targetApp, backupApp, backupCreated, fmt.Errorf("verify copied app: %w", err))
	}
	if err := clearQuarantine(targetApp); err != nil {
		return rollbackAfterFailure(targetApp, backupApp, backupCreated, fmt.Errorf("clear quarantine: %w", err))
	}
	if backupCreated {
		if err := os.RemoveAll(backupApp); err != nil {
			return fmt.Errorf("remove backup app: %w", err)
		}
	}
	return nil
}

// backupPath 为目标 app 生成本次安装专用备份路径。
func backupPath(targetApp string) string {
	return fmt.Sprintf("%s.updater-backup-%d-%d", targetApp, os.Getpid(), time.Now().UnixNano())
}

// copyApp 使用 ditto 复制 app bundle，保留 macOS bundle 元数据。
func copyApp(stagedApp string, targetApp string) error {
	if _, err := runCommand("ditto", stagedApp, targetApp); err != nil {
		return commandError(err)
	}
	return nil
}

// clearQuarantine 清理 app bundle 上的 quarantine xattr。
// 权限错误会再检查属性是否仍存在，避免把“已清理但有噪音”误判为失败。
func clearQuarantine(appPath string) error {
	result, err := runCommand("xattr", "-dr", "com.apple.quarantine", appPath)
	if err == nil {
		return nil
	}
	output := result.stdout + result.stderr
	if allLinesAreNoSuchXattr(output) {
		return nil
	}
	if strings.Contains(output, "Permission denied") {
		remains, inspectErr := quarantineAttributeRemains(appPath)
		if inspectErr != nil {
			return errors.Join(commandError(err), inspectErr)
		}
		if !remains {
			return nil
		}
	}
	return commandError(err)
}

// quarantineAttributeRemains 检查 quarantine 属性是否仍存在。
func quarantineAttributeRemains(appPath string) (bool, error) {
	result, err := runCommand("xattr", "-lr", appPath)
	output := result.stdout + result.stderr
	if err != nil {
		return false, fmt.Errorf("inspect quarantine attributes: %w", commandError(err))
	}
	return strings.Contains(output, "com.apple.quarantine:"), nil
}

// allLinesAreNoSuchXattr 判断 xattr 输出是否全是“属性不存在”。
func allLinesAreNoSuchXattr(output string) bool {
	lines := strings.Split(output, "\n")
	seen := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		seen = true
		if !strings.Contains(line, "No such xattr") {
			return false
		}
	}
	return seen
}

// rollbackAfterFailure 在安装失败后恢复目标 app，并保留原始失败原因。
func rollbackAfterFailure(targetApp string, backupApp string, backupCreated bool, cause error) error {
	rollbackErr := rollbackTargetApp(targetApp, backupApp, backupCreated)
	if rollbackErr != nil {
		return errors.Join(cause, fmt.Errorf("rollback failed: %w", rollbackErr))
	}
	return cause
}

// rollbackTargetApp 删除失败的新 app 并恢复备份。
func rollbackTargetApp(targetApp string, backupApp string, backupCreated bool) error {
	if removeErr := os.RemoveAll(targetApp); removeErr != nil {
		return fmt.Errorf("remove failed target app: %w", removeErr)
	}
	if backupCreated {
		if err := os.Rename(backupApp, targetApp); err != nil {
			return fmt.Errorf("restore backup app: %w", err)
		}
	}
	return nil
}

// commandError 在外部命令失败时补充 stderr。
func commandError(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
	}
	return err
}
