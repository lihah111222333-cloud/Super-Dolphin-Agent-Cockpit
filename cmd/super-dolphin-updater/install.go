package main

import (
	"bytes"
	"context"
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

type commandRunner func(context.Context, time.Duration, string, ...string) (commandResult, error)

type restartCommandRunner func(...string) (commandResult, error)

type processExitWaiter func(int, time.Duration) error

// updaterApp 显式携带 updater 的可替换系统依赖，避免安装流程依赖隐式全局状态。
type updaterApp struct {
	runCommand         commandRunner
	runRestartCommand  restartCommandRunner
	waitForProcessExit processExitWaiter
}

func defaultUpdaterApp() updaterApp {
	return updaterApp{
		runCommand:         runCommand,
		runRestartCommand:  runRestartCommand,
		waitForProcessExit: waitForProcessExit,
	}
}

func (app updaterApp) validate() error {
	if app.runCommand == nil {
		return errors.New("updater command runner is required")
	}
	if app.runRestartCommand == nil {
		return errors.New("updater restart command runner is required")
	}
	if app.waitForProcessExit == nil {
		return errors.New("updater process waiter is required")
	}
	return nil
}

// install 挂载 DMG、执行安装并尽力卸载临时挂载点。
// 安装和清理错误会合并返回，避免清理失败被静默吞掉。
func (app updaterApp) install(req installRequest) error {
	if err := app.validate(); err != nil {
		return err
	}
	if err := validateInstallRequest(req); err != nil {
		return err
	}
	mountPoint, err := app.mountDMG(req.DMGPath)
	if err != nil {
		return err
	}
	installErr := app.installFromMount(req, mountPoint)
	detachErr := app.detachDMG(mountPoint)
	removeErr := os.Remove(mountPoint)
	return errors.Join(installErr, detachErr, removeErr)
}

// installFromMount 从已挂载 DMG 安装 app。
// 复制前后都做结构和签名校验，任一步失败都不会继续重启目标 app。
func installFromMount(req installRequest, mountPoint string) error {
	return defaultUpdaterApp().installFromMount(req, mountPoint)
}

// installFromMount 使用显式 updaterApp 依赖执行已挂载 DMG 的安装流程。
// 等待旧进程、签名校验、复制替换和重启都必须在同一个依赖上下文里完成，避免测试钩子或生产命令来源混用。
func (app updaterApp) installFromMount(req installRequest, mountPoint string) error {
	if err := app.validate(); err != nil {
		return err
	}
	stagedApp, err := findMountedApp(mountPoint)
	if err != nil {
		return err
	}
	if err := validateMountedApp(stagedApp); err != nil {
		return err
	}
	if err := app.waitForRequestedProcess(req); err != nil {
		return err
	}
	teamID := ""
	if !req.AllowUnsigned {
		var err error
		teamID, err = app.expectedTeamID(req.TargetAppPath)
		if err != nil {
			return err
		}
	}
	if err := app.verifyAppSignature(stagedApp, teamID, req.AllowUnsigned); err != nil {
		return fmt.Errorf("verify staged app: %w", err)
	}
	if err := app.replaceTargetApp(stagedApp, req.TargetAppPath, teamID, req.AllowUnsigned); err != nil {
		return err
	}
	if req.Restart {
		return app.restartTargetApp(req.TargetAppPath)
	}
	return nil
}

// waitForRequestedProcess 在替换 app 前等待旧进程退出。
// nil waiter 表示 updater 依赖装配错误，必须 fail-fast，不能跳过等待直接替换。
func (app updaterApp) waitForRequestedProcess(req installRequest) error {
	if req.WaitPID <= 0 {
		return nil
	}
	if app.waitForProcessExit == nil {
		return errors.New("updater process waiter is required")
	}
	return app.waitForProcessExit(req.WaitPID, 30*time.Second)
}

// restartTargetApp 使用 open -n 重启目标 app。
func restartTargetApp(targetApp string) error {
	return defaultUpdaterApp().restartTargetApp(targetApp)
}

func (app updaterApp) restartTargetApp(targetApp string) error {
	if app.runRestartCommand == nil {
		return errors.New("updater restart command runner is required")
	}
	if _, err := app.runRestartCommand("-n", targetApp); err != nil {
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
func (app updaterApp) mountDMG(dmgPath string) (string, error) {
	mountPoint, err := os.MkdirTemp("", "super-dolphin-updater-mount-*")
	if err != nil {
		return "", fmt.Errorf("create mount point: %w", err)
	}
	if _, err := app.runUpdaterCommand("hdiutil", "attach", dmgPath, "-nobrowse", "-readonly", "-mountpoint", mountPoint); err != nil {
		removeErr := os.Remove(mountPoint)
		return "", errors.Join(fmt.Errorf("mount dmg: %w", commandError(err)), removeErr)
	}
	return mountPoint, nil
}

// detachDMG 卸载 DMG，普通卸载失败后再尝试 force。
func (app updaterApp) detachDMG(mountPoint string) error {
	if _, err := app.runUpdaterCommand("hdiutil", "detach", mountPoint); err == nil {
		return nil
	}
	if _, err := app.runUpdaterCommand("hdiutil", "detach", "-force", mountPoint); err != nil {
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
func (app updaterApp) expectedTeamID(targetApp string) (string, error) {
	if teamID := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_EXPECTED_TEAM_ID")); teamID != "" {
		return teamID, nil
	}
	if _, err := os.Stat(targetApp); err != nil {
		return "", fmt.Errorf("SUPER_DOLPHIN_EXPECTED_TEAM_ID is required when target app is unavailable: %w", err)
	}
	teamID, err := app.appTeamID(targetApp)
	if err != nil {
		return "", fmt.Errorf("read installed app Team ID: %w", err)
	}
	return teamID, nil
}

// verifyAppSignature 校验 app 的 codesign、Team ID 和 Gatekeeper 评估。
// allowUnsigned 只允许灰度测试跳过 Team ID/Gatekeeper，codesign 仍必须通过。
func verifyAppSignature(appPath string, expectedTeamID string, allowUnsigned bool) error {
	return defaultUpdaterApp().verifyAppSignature(appPath, expectedTeamID, allowUnsigned)
}

// verifyAppSignature 使用 updaterApp 的命令 runner 校验签名和 Gatekeeper。
// allowUnsigned 只放宽 Team ID/Gatekeeper，codesign 失败仍然阻断安装。
func (app updaterApp) verifyAppSignature(appPath string, expectedTeamID string, allowUnsigned bool) error {
	if allowUnsigned {
		if _, err := app.runUpdaterCommand("codesign", "--verify", "--deep", "--strict", "--verbose=4", appPath); err != nil {
			return fmt.Errorf("codesign verify failed: %w", commandError(err))
		}
		return nil
	}
	if expectedTeamID == "" {
		return errors.New("expected Team ID is required")
	}
	if _, err := app.runUpdaterCommand("codesign", "--verify", "--deep", "--strict", "--verbose=4", appPath); err != nil {
		return fmt.Errorf("codesign verify failed: %w", commandError(err))
	}
	details, err := app.signingDetails(appPath)
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
	if _, err := app.runUpdaterCommand("spctl", "-a", "-vv", "-t", "execute", appPath); err != nil {
		return fmt.Errorf("spctl execute assessment failed: %w", commandError(err))
	}
	return nil
}

// appTeamID 从已安装 app 的签名详情读取 Team ID。
func (app updaterApp) appTeamID(appPath string) (string, error) {
	details, err := app.signingDetails(appPath)
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
func (app updaterApp) signingDetails(appPath string) (string, error) {
	result, err := app.runUpdaterCommand("codesign", "-dv", "--verbose=4", appPath)
	if err != nil {
		return "", fmt.Errorf("codesign details failed: %w", commandError(err))
	}
	return result.stdout + result.stderr, nil
}

// parseSigningValue 从 codesign 输出中提取指定 key。
func parseSigningValue(details string, key string) string {
	prefix := key + "="
	for line := range strings.SplitSeq(details, "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// replaceTargetApp 先备份目标 app，再复制、复验并原子替换目标路径。
// 任一步失败都必须走 rollback，避免留下半替换状态或未清理的 staging 目录。
func (app updaterApp) replaceTargetApp(stagedApp string, targetApp string, expectedTeamID string, allowUnsigned bool) error {
	backupApp := backupPath(targetApp)
	stagedCopyApp := stagedCopyPath(targetApp)
	backupCreated, err := backupTargetApp(targetApp, backupApp)
	if err != nil {
		return err
	}

	if err := app.copyApp(stagedApp, stagedCopyApp); err != nil {
		return rollbackInstallFailure(targetApp, backupApp, stagedCopyApp, backupCreated, fmt.Errorf("copy app: %w", err))
	}
	if err := validateMountedApp(stagedCopyApp); err != nil {
		return rollbackInstallFailure(
			targetApp,
			backupApp,
			stagedCopyApp,
			backupCreated,
			fmt.Errorf("verify copied app structure: %w", err),
		)
	}
	if err := app.verifyAppSignature(stagedCopyApp, expectedTeamID, allowUnsigned); err != nil {
		return rollbackInstallFailure(targetApp, backupApp, stagedCopyApp, backupCreated, fmt.Errorf("verify copied app: %w", err))
	}
	if err := app.clearQuarantine(stagedCopyApp); err != nil {
		return rollbackInstallFailure(targetApp, backupApp, stagedCopyApp, backupCreated, fmt.Errorf("clear quarantine: %w", err))
	}
	if err := os.Rename(stagedCopyApp, targetApp); err != nil {
		return rollbackInstallFailure(targetApp, backupApp, stagedCopyApp, backupCreated, fmt.Errorf("replace target app: %w", err))
	}
	if backupCreated {
		if err := os.RemoveAll(backupApp); err != nil {
			return fmt.Errorf("remove backup app: %w", err)
		}
	}
	return nil
}

// backupTargetApp 如目标 app 已存在则先改名为备份。
// 目标不存在是首次安装的正常分支，其它 stat/rename 错误必须阻断安装。
func backupTargetApp(targetApp string, backupApp string) (bool, error) {
	if _, err := os.Stat(targetApp); err == nil {
		if err := os.Rename(targetApp, backupApp); err != nil {
			return false, fmt.Errorf("backup target app: %w", err)
		}
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect target app: %w", err)
	}
	return false, nil
}

// backupPath 为目标 app 生成本次安装专用备份路径。
func backupPath(targetApp string) string {
	return fmt.Sprintf("%s.updater-backup-%d-%d", targetApp, os.Getpid(), time.Now().UnixNano())
}

// stagedCopyPath 为 ditto 生成同目录临时 .app，验证通过后才会替换最终目标。
func stagedCopyPath(targetApp string) string {
	parent := filepath.Dir(targetApp)
	base := strings.TrimSuffix(filepath.Base(targetApp), filepath.Ext(targetApp))
	return filepath.Join(parent, fmt.Sprintf(".%s.updater-staging-%d-%d.app", base, os.Getpid(), time.Now().UnixNano()))
}

// copyApp 使用 ditto 复制 app bundle，保留 macOS bundle 元数据。
func (app updaterApp) copyApp(stagedApp string, targetApp string) error {
	if _, err := app.runUpdaterCommand("ditto", stagedApp, targetApp); err != nil {
		return commandError(err)
	}
	return nil
}

// clearQuarantine 清理 app bundle 上的 quarantine xattr。
// 权限错误会再检查属性是否仍存在，避免把“已清理但有噪音”误判为失败。
func clearQuarantine(appPath string) error {
	return defaultUpdaterApp().clearQuarantine(appPath)
}

// clearQuarantine 清理 app bundle 的 quarantine 属性，并在权限噪音后复查真实状态。
// 属性仍存在时返回原始 xattr 错误；属性已消失时允许继续安装。
func (app updaterApp) clearQuarantine(appPath string) error {
	result, err := app.runUpdaterCommand("xattr", "-dr", "com.apple.quarantine", appPath)
	if err == nil {
		return nil
	}
	output := result.stdout + result.stderr
	if allLinesAreNoSuchXattr(output) {
		return nil
	}
	if strings.Contains(output, "Permission denied") {
		remains, inspectErr := app.quarantineAttributeRemains(appPath)
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
func (app updaterApp) quarantineAttributeRemains(appPath string) (bool, error) {
	result, err := app.runUpdaterCommand("xattr", "-lr", appPath)
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

// rollbackInstallFailure 恢复备份并清理 staging 目录。
// 清理失败会和原始安装错误一起返回，避免后台安装留下未知半成品。
func rollbackInstallFailure(targetApp string, backupApp string, stagedCopyApp string, backupCreated bool, cause error) error {
	installErr := rollbackAfterFailure(targetApp, backupApp, backupCreated, cause)
	if err := os.RemoveAll(stagedCopyApp); err != nil {
		return errors.Join(installErr, fmt.Errorf("remove staged copy: %w", err))
	}
	return installErr
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
