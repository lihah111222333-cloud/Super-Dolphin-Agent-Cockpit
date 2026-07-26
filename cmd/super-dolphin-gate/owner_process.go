package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"runtime"
	"strings"
	"syscall"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

const ownerHandshakeMaximumBytes = 16 << 10

// coordinatorOwnerToolchainPath is a fixed allowlist: owner processes must
// never resolve Git or Docker from the hook caller's PATH.
const coordinatorOwnerToolchainPath = "/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin:/opt/homebrew/bin:/Applications/Docker.app/Contents/Resources/bin"

type ownerHandshake struct {
	Ready bool   `json:"ready"`
	Error string `json:"error,omitempty"`
}

type executableOwnerStarter struct{}

// StartCoordinatorOwner 启动隐藏 owner，并等待单行严格握手后才返回。
func (executableOwnerStarter) StartCoordinatorOwner(ctx context.Context, checkpoint localci.DockerDaemonIdentityCheckpoint) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve coordinator executable: %w", err)
	}
	args := []string{
		"_owner", "--identity-key", checkpoint.IdentityKey,
	}
	command := newCoordinatorOwnerCommand(executable, args...)
	command.Stderr = os.Stderr
	return startCoordinatorOwnerCommand(ctx, command)
}

// newCoordinatorOwnerCommand 让 daemon 生命周期独立于有界握手 context。
func newCoordinatorOwnerCommand(executable string, args ...string) *exec.Cmd {
	command := exec.Command(executable, args...)
	command.Env = coordinatorOwnerEnvironment(os.Environ())
	detachCoordinatorOwnerCommand(command)
	return command
}

// detachCoordinatorOwnerCommand 防止调用方终端退出时向全局 owner 传播挂断信号。
func detachCoordinatorOwnerCommand(command *exec.Cmd) {
	if command == nil || runtime.GOOS == "windows" {
		return
	}
	command.SysProcAttr = &syscall.SysProcAttr{}
	setsid := reflect.ValueOf(command.SysProcAttr).Elem().FieldByName("Setsid")
	setsid.SetBool(true)
}

// coordinatorOwnerCommandDetached 检查当前平台的 owner 脱离约束是否已满足。
func coordinatorOwnerCommandDetached(command *exec.Cmd) bool {
	if command == nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	if command.SysProcAttr == nil {
		return false
	}
	setsid := reflect.ValueOf(command.SysProcAttr).Elem().FieldByName("Setsid")
	return setsid.IsValid() && setsid.Kind() == reflect.Bool && setsid.Bool()
}

// coordinatorOwnerEnvironment 移除调用方可控状态，并以固定 Git/Docker 工具链替换 PATH。
func coordinatorOwnerEnvironment(environment []string) []string {
	blocked := map[string]struct{}{
		"GIT_DIR": {}, "GIT_WORK_TREE": {}, "GIT_COMMON_DIR": {}, "GIT_INDEX_FILE": {}, "GIT_OBJECT_DIRECTORY": {},
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": {}, "GIT_CONFIG": {}, "GIT_CONFIG_GLOBAL": {}, "GIT_CONFIG_SYSTEM": {}, "GIT_CONFIG_COUNT": {},
		"PATH": {},
	}
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found {
			if _, blocked := blocked[name]; blocked {
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, "PATH="+coordinatorOwnerToolchainPath)
}

// startCoordinatorOwnerCommand 在调用方 deadline 内启动、校验握手并移交 owner 子进程。
func startCoordinatorOwnerCommand(ctx context.Context, command *exec.Cmd) error {
	if ctx == nil || command == nil {
		return errors.New("coordinator owner command and context are required")
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open owner handshake pipe: %w", err)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start coordinator owner: %w", err)
	}
	if err := readOwnerHandshake(ctx, stdout); err != nil {
		return errors.Join(err, terminateOwnerCommand(command))
	}
	if err := command.Process.Release(); err != nil {
		return errors.Join(fmt.Errorf("release coordinator owner process: %w", err), terminateOwnerCommand(command))
	}
	return nil
}

func terminateOwnerCommand(command *exec.Cmd) error {
	killErr := command.Process.Kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	return errors.Join(killErr, command.Wait())
}

// readOwnerHandshake 通过可关闭 pipe 读取严格单行握手，使 context 取消能解除阻塞。
func readOwnerHandshake(ctx context.Context, reader io.ReadCloser) error {
	if ctx == nil || reader == nil {
		return errors.New("coordinator owner handshake context and reader are required")
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = reader.Close() })
	defer stopCancel()
	defer reader.Close()
	line, err := bufio.NewReader(io.LimitReader(reader, ownerHandshakeMaximumBytes)).ReadBytes('\n')
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("read coordinator owner handshake: %w", ctxErr)
		}
		return fmt.Errorf("read coordinator owner handshake: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("read coordinator owner handshake: %w", err)
	}
	var handshake ownerHandshake
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&handshake); err != nil {
		return fmt.Errorf("decode coordinator owner handshake: %w", err)
	}
	if handshake.Ready == (handshake.Error != "") {
		return errors.New("coordinator owner handshake must contain exactly one outcome")
	}
	if handshake.Error != "" {
		return errors.New(handshake.Error)
	}
	return nil
}

// runOwnerProcess 重新探测 Docker identity，并在打开 owner 后发送唯一 ready 握手。
func runOwnerProcess(args []string, stdout io.Writer) error {
	expectedIdentityKey, err := parseOwnerIdentity(args)
	if err != nil {
		return writeOwnerFailure(stdout, err)
	}
	checkpoint, err := localci.ProbeDockerSchedulerAuthority(context.Background())
	if err != nil {
		return writeOwnerFailure(stdout, err)
	}
	if checkpoint.IdentityKey != expectedIdentityKey {
		return writeOwnerFailure(stdout, errors.New("coordinator owner Docker identity changed during startup"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dependencies, err := productionCoordinatorDependencies(ctx)
	if err != nil {
		return writeOwnerFailure(stdout, err)
	}
	owner, err := openCoordinatorOwner(ctx, checkpoint, dependencies)
	if err != nil {
		return writeOwnerFailure(stdout, err)
	}
	if err := json.NewEncoder(stdout).Encode(ownerHandshake{Ready: true}); err != nil {
		return errors.Join(fmt.Errorf("write coordinator owner ready handshake: %w", err), owner.Close())
	}
	return owner.Serve(ctx)
}

func parseOwnerIdentity(args []string) (string, error) {
	flags := flag.NewFlagSet("_owner", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	identityKey := flags.String("identity-key", "", "expected Docker identity key")
	if err := flags.Parse(args); err != nil {
		return "", protocolError("parse owner flags: %v", err)
	}
	if flags.NArg() != 0 {
		return "", protocolError("unexpected owner positional arguments: %v", flags.Args())
	}
	if len(*identityKey) != 64 {
		return "", protocolError("--identity-key must be a SHA-256 hex digest")
	}
	return *identityKey, nil
}

func writeOwnerFailure(stdout io.Writer, ownerErr error) error {
	if err := json.NewEncoder(stdout).Encode(ownerHandshake{Error: ownerErr.Error()}); err != nil {
		return errors.Join(ownerErr, fmt.Errorf("write coordinator owner failure handshake: %w", err))
	}
	return infrastructureError("coordinator owner startup failed: %v", ownerErr)
}
