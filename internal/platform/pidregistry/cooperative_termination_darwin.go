//go:build darwin

package pidregistry

import (
	"bufio"
	"context"
	cryptorand "crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	"golang.org/x/sys/unix"
)

const (
	cooperativeRequestMaxBytes  = 512
	cooperativeCommandTerminate = "TERMINATE"
)

// CooperativeTerminationServer 管理 candidate 的认证退出监听与清理生命周期。
type CooperativeTerminationServer struct {
	listener   *net.UnixListener
	endpoint   string
	once       sync.Once
	done       chan struct{}
	commit     chan struct{}
	commitOnce sync.Once
}

// StartCooperativeTerminationServer 启动 0600 Unix socket，并仅接受 token 认证的退出请求。
func StartCooperativeTerminationServer(endpoint, token string, terminate func()) (*CooperativeTerminationServer, error) {
	server, err := startCooperativeTerminationServer(endpoint, token, terminate)
	if err != nil {
		return nil, err
	}
	server.commitOnce.Do(func() { close(server.commit) })
	return server, nil
}

// StartParkedCooperativeTerminationServer 启动认证控制端点，并在 COMMIT 前保持调用方 parked。
func StartParkedCooperativeTerminationServer(endpoint, token string, terminate func()) (*CooperativeTerminationServer, error) {
	return startCooperativeTerminationServer(endpoint, token, terminate)
}

// startCooperativeTerminationServer 创建 owner-only socket 并托管唯一 serve 循环。
func startCooperativeTerminationServer(endpoint, token string, terminate func()) (*CooperativeTerminationServer, error) {
	return startCooperativeTerminationServerWithPublishHook(endpoint, token, terminate, nil)
}

// startCooperativeTerminationServerWithPublishHook 在测试屏障后发布协作终止端点。
func startCooperativeTerminationServerWithPublishHook(
	endpoint, token string,
	terminate func(),
	beforePublish func(string) error,
) (*CooperativeTerminationServer, error) {
	if endpoint == "" || token == "" || terminate == nil {
		return nil, errors.New("pidregistry: cooperative termination endpoint, token, and callback are required")
	}
	if _, err := os.Lstat(endpoint); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("pidregistry: cooperative termination endpoint already exists")
	}
	listener, err := listenAndPublishCooperativeEndpoint(endpoint, beforePublish)
	if err != nil {
		return nil, err
	}
	server := &CooperativeTerminationServer{
		listener: listener, endpoint: endpoint, done: make(chan struct{}), commit: make(chan struct{}),
	}
	safego.Go(context.Background(), nil, "pidregistry.cooperative-termination", func(context.Context) {
		server.serve(token, terminate)
	})
	return server, nil
}

// listenAndPublishCooperativeEndpoint 在随机 staging 路径收敛权限后原子发布正式 endpoint。
func listenAndPublishCooperativeEndpoint(endpoint string, beforePublish func(string) error) (*net.UnixListener, error) {
	stagingEndpoint, err := newCooperativeTerminationStagingEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: stagingEndpoint, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("pidregistry: listen cooperative termination endpoint: %w", err)
	}
	cleanupStaging := func() {
		_ = listener.Close()
		_ = os.Remove(stagingEndpoint)
	}
	if beforePublish != nil {
		if err := beforePublish(stagingEndpoint); err != nil {
			cleanupStaging()
			return nil, fmt.Errorf("pidregistry: prepare cooperative termination endpoint publication: %w", err)
		}
	}
	if err := os.Chmod(stagingEndpoint, 0o600); err != nil {
		cleanupStaging()
		return nil, err
	}
	if err := unix.RenameatxNp(unix.AT_FDCWD, stagingEndpoint, unix.AT_FDCWD, endpoint, unix.RENAME_EXCL); err != nil {
		cleanupStaging()
		return nil, fmt.Errorf("pidregistry: publish cooperative termination endpoint: %w", err)
	}
	listener.SetUnlinkOnClose(false)
	return listener, nil
}

// newCooperativeTerminationStagingEndpoint 创建不长于正式 basename 的随机同目录 socket 路径。
func newCooperativeTerminationStagingEndpoint(endpoint string) (string, error) {
	const randomBytes = 8
	if len(filepath.Base(endpoint)) < 1+hex.EncodedLen(randomBytes) {
		return "", errors.New("pidregistry: cooperative termination endpoint basename is too short for private staging")
	}
	var nonce [randomBytes]byte
	if _, err := cryptorand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("pidregistry: generate cooperative termination staging endpoint: %w", err)
	}
	return filepath.Join(filepath.Dir(endpoint), "."+hex.EncodeToString(nonce[:])), nil
}

func (server *CooperativeTerminationServer) serve(token string, terminate func()) {
	defer close(server.done)
	for {
		connection, err := server.listener.AcceptUnix()
		if err != nil {
			return
		}
		shouldStop := server.handleConnection(connection, token, terminate)
		if shouldStop {
			return
		}
	}
}

// handleConnection 解析单个有界认证命令，返回是否结束 serve 循环。
func (server *CooperativeTerminationServer) handleConnection(connection *net.UnixConn, token string, terminate func()) bool {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(io.LimitReader(connection, cooperativeRequestMaxBytes+1)).ReadString('\n')
	if err != nil || len(line) > cooperativeRequestMaxBytes {
		return false
	}
	command, gotToken, ok := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
	if !ok || subtle.ConstantTimeCompare([]byte(gotToken), []byte(token)) != 1 {
		return false
	}
	switch command {
	case cooperativeCommandReady:
		_, _ = connection.Write([]byte("READY\n"))
	case cooperativeCommandCommit:
		server.commitOnce.Do(func() { close(server.commit) })
		_, _ = connection.Write([]byte("COMMITTED\n"))
	case cooperativeCommandTerminate:
		_, _ = connection.Write([]byte("ACK\n"))
		terminate()
		return true
	}
	return false
}

// WaitForCommit 等待认证 COMMIT 或调用方取消。
func (server *CooperativeTerminationServer) WaitForCommit(ctx context.Context) error {
	if server == nil || ctx == nil {
		return errors.New("pidregistry: cooperative commit server and context are required")
	}
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-server.commit:
		return nil
	}
}

// Close 关闭协作终止监听并删除 endpoint。
func (server *CooperativeTerminationServer) Close() error {
	if server == nil {
		return nil
	}
	var err error
	server.once.Do(func() {
		err = server.listener.Close()
		<-server.done
		removeErr := os.Remove(server.endpoint)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		err = errors.Join(err, removeErr)
	})
	return err
}

// CleanupCooperativeTerminationEndpoint 只删除当前用户拥有的 0600 Unix socket。
func CleanupCooperativeTerminationEndpoint(endpoint string) error {
	if !filepath.IsAbs(endpoint) || filepath.Clean(endpoint) != endpoint {
		return errors.New("pidregistry: cooperative termination endpoint must be absolute and clean")
	}
	info, err := os.Lstat(endpoint)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("pidregistry: inspect cooperative termination endpoint: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		return errors.New("pidregistry: cooperative termination endpoint is not an owned 0600 Unix socket")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("pidregistry: cooperative termination endpoint owner mismatch")
	}
	if err := os.Remove(endpoint); err != nil {
		return fmt.Errorf("pidregistry: remove cooperative termination endpoint: %w", err)
	}
	return nil
}

// requestCooperativeTermination 发送认证 token，并要求 receiver 返回 ACK。
func requestCooperativeTermination(ctx context.Context, endpoint, token string) error {
	return requestCooperativeControl(ctx, endpoint, token, cooperativeCommandTerminate, "ACK\n")
}

// requestCooperativeControl 校验 socket owner 后交换单个认证命令与响应。
func requestCooperativeControl(ctx context.Context, endpoint, token, command, expectedResponse string) error {
	if err := validateOwnedCooperativeEndpoint(endpoint); err != nil {
		return err
	}
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "unix", endpoint)
	if err != nil {
		return fmt.Errorf("pidregistry: dial cooperative termination endpoint: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if _, err := connection.Write([]byte(command + " " + token + "\n")); err != nil {
		return err
	}
	response, err := bufio.NewReader(connection).ReadString('\n')
	if err != nil {
		return err
	}
	if response != expectedResponse {
		return errors.New("pidregistry: cooperative control response mismatch")
	}
	return nil
}

// validateOwnedCooperativeEndpoint 拒绝非 socket、非 0600 或非当前用户的 endpoint。
func validateOwnedCooperativeEndpoint(endpoint string) error {
	info, err := os.Lstat(endpoint)
	if err != nil {
		return fmt.Errorf("pidregistry: inspect cooperative termination endpoint: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		return errors.New("pidregistry: cooperative termination endpoint is not a 0600 Unix socket")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("pidregistry: cooperative termination endpoint owner mismatch")
	}
	return nil
}
