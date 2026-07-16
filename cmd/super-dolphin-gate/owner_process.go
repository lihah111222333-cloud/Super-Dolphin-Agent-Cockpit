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
	"syscall"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

const ownerHandshakeMaximumBytes = 16 << 10

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
	command := exec.Command(executable, args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open owner handshake pipe: %w", err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start coordinator owner: %w", err)
	}
	if err := readOwnerHandshake(stdout); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return err
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("release coordinator owner process: %w", err)
	}
	return nil
}

func readOwnerHandshake(reader io.Reader) error {
	line, err := bufio.NewReader(io.LimitReader(reader, ownerHandshakeMaximumBytes)).ReadBytes('\n')
	if err != nil {
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
	checkpoint, err := probeDockerDaemonIdentity(context.Background())
	if err != nil {
		return writeOwnerFailure(stdout, err)
	}
	if checkpoint.IdentityKey != expectedIdentityKey {
		return writeOwnerFailure(stdout, errors.New("coordinator owner Docker identity changed during startup"))
	}
	dependencies, err := productionCoordinatorDependencies()
	if err != nil {
		return writeOwnerFailure(stdout, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
