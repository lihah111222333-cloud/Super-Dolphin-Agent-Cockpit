package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// updaterDetachedEnv 标记当前进程是否已经是后台安装子进程。
const updaterDetachedEnv = "SUPER_DOLPHIN_UPDATER_DETACHED"

// 外部安装命令的默认上限，避免 hdiutil/ditto/codesign 挂起后 updater 永久占用后台进程。
const (
	updaterCommandTimeout        = 10 * time.Minute
	updaterCommandKillWait       = 2 * time.Second
	windowsCreateNewProcessGroup = 0x00000200
)

// main 是 updater CLI 入口。
// 首次启动只负责 detach，后台子进程才执行真实安装，避免替换正在运行的 app。
func main() {
	pkglogger.InitWithConsoleWriter(os.Stderr)
	req, err := parseInstallRequest(os.Args[1:])
	if err != nil {
		pkglogger.Get().Error("super-dolphin-updater request failed", "error", err)
		os.Exit(2)
	}
	if os.Getenv(updaterDetachedEnv) != "1" {
		if err := startDetachedUpdater(req.LogPath); err != nil {
			pkglogger.Get().Error("super-dolphin-updater detach failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := install(req); err != nil {
		pkglogger.Get().Error("super-dolphin-updater install failed", "error", err)
		os.Exit(1)
	}
}

// parseInstallRequest 解析 updater CLI 参数。
func parseInstallRequest(args []string) (installRequest, error) {
	flags := flag.NewFlagSet("super-dolphin-updater", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var req installRequest
	flags.StringVar(&req.DMGPath, "dmg", "", "path to the signed Super Dolphin DMG")
	flags.StringVar(&req.TargetAppPath, "target", "", "path to the installed Super Dolphin.app")
	flags.BoolVar(&req.Restart, "restart", false, "reopen the target app after replacement")
	flags.BoolVar(&req.AllowUnsigned, "allow-unsigned", false, "allow ad-hoc signed gray test updates without Developer ID")
	flags.IntVar(&req.WaitPID, "wait-pid", 0, "wait for the current app process to exit before replacement")
	flags.StringVar(&req.LogPath, "log", "", "path to write detached updater logs")
	if err := flags.Parse(args); err != nil {
		return installRequest{}, err
	}
	if flags.NArg() != 0 {
		return installRequest{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	return req, nil
}

// runCommand 执行系统命令，测试会替换它以避免真实改系统。
// 命令会放入独立进程组；超时或上游取消时必须杀掉整组，防止 ditto/hdiutil 子进程残留。
var runCommand = func(ctx context.Context, timeout time.Duration, name string, args ...string) (commandResult, error) {
	if ctx == nil {
		return commandResult{}, errors.New("command context is required")
	}
	if timeout <= 0 {
		return commandResult{}, fmt.Errorf("command timeout must be positive: %s", timeout)
	}
	commandCtx, cancel := ctxutil.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.Command(name, args...)
	configureCommandProcessGroup(cmd)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return commandResult{
			stdout: stdout.String(),
			stderr: stderr.String(),
		}, err
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case err := <-waitCh:
		return commandResult{
			stdout: stdout.String(),
			stderr: stderr.String(),
		}, err
	case <-commandCtx.Done():
		err := killTimedOutCommand(cmd, commandCtx.Err(), waitCh)
		return commandResult{
			stdout: stdout.String(),
			stderr: stderr.String(),
		}, err
	}
}

// runUpdaterCommand 用默认 timeout 执行 updater 外部命令。
func runUpdaterCommand(name string, args ...string) (commandResult, error) {
	return runCommand(context.Background(), updaterCommandTimeout, name, args...)
}

// configureCommandProcessGroup 为外部命令设置独立进程组。
// 用反射写平台字段，避免在非 Unix 构建中直接引用不存在的 SysProcAttr 字段。
func configureCommandProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	attr := &syscall.SysProcAttr{}
	setSysProcBool(attr, "Setpgid", true)
	setSysProcBool(attr, "HideWindow", true)
	setSysProcUint(attr, "CreationFlags", windowsCreateNewProcessGroup)
	cmd.SysProcAttr = attr
}

func setSysProcBool(attr *syscall.SysProcAttr, field string, value bool) {
	target := reflect.ValueOf(attr).Elem().FieldByName(field)
	if target.IsValid() && target.CanSet() && target.Kind() == reflect.Bool {
		target.SetBool(value)
	}
}

func setSysProcUint(attr *syscall.SysProcAttr, field string, value uint64) {
	target := reflect.ValueOf(attr).Elem().FieldByName(field)
	if target.IsValid() && target.CanSet() && target.CanUint() {
		target.SetUint(value)
	}
}

// killTimedOutCommand 在 deadline 后强制结束命令树，并保留 timeout 作为根因。
func killTimedOutCommand(cmd *exec.Cmd, cause error, waitCh <-chan error) error {
	killErr := killCommandProcessGroup(cmd)
	select {
	case waitErr := <-waitCh:
		if killErr != nil {
			return errors.Join(cause, fmt.Errorf("kill command process group: %w", killErr), waitErr)
		}
		return cause
	case <-time.After(updaterCommandKillWait):
		return errors.Join(cause, fmt.Errorf("wait for killed command: %w", killErr))
	}
}

// killCommandProcessGroup 优先终止整棵命令树，失败时回退到当前进程。
func killCommandProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return errors.New("command pid is invalid")
	}
	if runtime.GOOS == "windows" {
		return killWindowsProcessTree(cmd, pid)
	}
	if err := signalUnixProcessGroup(pid); err == nil || commandProcessGone(err) {
		return nil
	}
	return killSingleProcess(cmd)
}

func signalUnixProcessGroup(pid int) error {
	group, err := os.FindProcess(-pid)
	if err != nil {
		return err
	}
	return group.Signal(os.Kill)
}

func killWindowsProcessTree(cmd *exec.Cmd, pid int) error {
	output, taskkillErr := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
	if taskkillErr == nil {
		return nil
	}
	return errors.Join(
		fmt.Errorf("taskkill process tree: %w: %s", taskkillErr, strings.TrimSpace(string(output))),
		killSingleProcess(cmd),
	)
}

func killSingleProcess(cmd *exec.Cmd) error {
	err := cmd.Process.Kill()
	if commandProcessGone(err) {
		return nil
	}
	return err
}

func commandProcessGone(err error) bool {
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return true
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such process") || strings.Contains(text, "process already finished")
}

// startDetachedUpdater 重新启动一个脱离当前进程组的 updater 子进程。
func startDetachedUpdater(logPath string) error {
	output, closeOutput, err := openDetachedOutput(logPath)
	if err != nil {
		return err
	}
	defer closeOutput()
	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Env = append(os.Environ(), updaterDetachedEnv+"=1")
	cmd.Stdout = output
	cmd.Stderr = output
	configureDetachedCommand(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start detached updater: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release detached updater: %w", err)
	}
	return nil
}

// openDetachedOutput 打开后台 updater 的输出目标。
// 未指定日志时写入 devnull，避免后台进程持有前台终端。
func openDetachedOutput(logPath string) (*os.File, func(), error) {
	path := strings.TrimSpace(logPath)
	if path == "" {
		file, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return nil, func() {}, fmt.Errorf("open updater devnull: %w", err)
		}
		return file, func() { _ = file.Close() }, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, func() {}, fmt.Errorf("create updater log dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open updater log: %w", err)
	}
	return file, func() { _ = file.Close() }, nil
}
