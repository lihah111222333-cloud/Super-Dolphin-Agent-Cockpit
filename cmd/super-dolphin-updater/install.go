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

const (
	expectedBundleID = "com.superdolphin.app"
	launcherPath     = "Contents/MacOS/agent-terminal"
	manifestPath     = "Contents/Resources/runtime-manifest.json"
	infoPlistPath    = "Contents/Info.plist"
)

type installRequest struct {
	DMGPath       string
	TargetAppPath string
	Restart       bool
	AllowUnsigned bool
	WaitPID       int
	LogPath       string
}

type commandResult struct {
	stdout string
	stderr string
}

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

var waitForProcessExit = waitForProcessExitImpl

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

// installFromMount 从mount处理安装。
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

func restartTargetApp(targetApp string) error {
	if _, err := runRestartCommand("-n", targetApp); err != nil {
		return fmt.Errorf("restart target app: %w", commandError(err))
	}
	return nil
}

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

// validateInstallRequest 校验安装请求。
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

func validateWaitPID(pid int) error {
	if pid < 0 {
		return fmt.Errorf("wait pid must be non-negative: %d", pid)
	}
	return nil
}

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

// validateMountedApp 校验mountedapp。
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
		return fmt.Errorf("info.plist CFBundleIdentifier mismatch: expected %s, got %s", expectedBundleID, bundleID)
	}
	return nil
}

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

// plistStringValue 处理pliststring值。
func plistStringValue(decoder *xml.Decoder, key string) (string, error) {
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("info.plist missing %s", key)
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

// nextPlistString 处理nextpliststring。
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

func detachDMG(mountPoint string) error {
	if _, err := runCommand("hdiutil", "detach", mountPoint); err == nil {
		return nil
	}
	if _, err := runCommand("hdiutil", "detach", "-force", mountPoint); err != nil {
		return fmt.Errorf("detach dmg with force: %w", commandError(err))
	}
	return nil
}

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

// verifyAppSignature 验证app签名。
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
		return fmt.Errorf("team ID mismatch: expected %s, got %s", expectedTeamID, teamID)
	}
	if !strings.Contains(details, "Authority=Developer ID Application:") {
		return errors.New("codesign details missing Developer ID Application authority")
	}
	if _, err := runCommand("spctl", "-a", "-vv", "-t", "execute", appPath); err != nil {
		return fmt.Errorf("spctl execute assessment failed: %w", commandError(err))
	}
	return nil
}

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

func signingDetails(appPath string) (string, error) {
	result, err := runCommand("codesign", "-dv", "--verbose=4", appPath)
	if err != nil {
		return "", fmt.Errorf("codesign details failed: %w", commandError(err))
	}
	return result.stdout + result.stderr, nil
}

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

// replaceTargetApp 替换targetapp。
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

func backupPath(targetApp string) string {
	return fmt.Sprintf("%s.updater-backup-%d-%d", targetApp, os.Getpid(), time.Now().UnixNano())
}

func copyApp(stagedApp string, targetApp string) error {
	if _, err := runCommand("ditto", stagedApp, targetApp); err != nil {
		return commandError(err)
	}
	return nil
}

// clearQuarantine 清理quarantine。
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

func quarantineAttributeRemains(appPath string) (bool, error) {
	result, err := runCommand("xattr", "-lr", appPath)
	output := result.stdout + result.stderr
	if err != nil {
		return false, fmt.Errorf("inspect quarantine attributes: %w", commandError(err))
	}
	return strings.Contains(output, "com.apple.quarantine:"), nil
}

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

func rollbackAfterFailure(targetApp string, backupApp string, backupCreated bool, cause error) error {
	rollbackErr := rollbackTargetApp(targetApp, backupApp, backupCreated)
	if rollbackErr != nil {
		return errors.Join(cause, fmt.Errorf("rollback failed: %w", rollbackErr))
	}
	return cause
}

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

func commandError(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
	}
	return err
}
