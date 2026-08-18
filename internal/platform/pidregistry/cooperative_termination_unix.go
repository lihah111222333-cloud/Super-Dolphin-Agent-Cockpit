//go:build darwin || linux

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
)

const (
	cooperativeRequestMaxBytes  = 512
	cooperativeCommandTerminate = "TERMINATE"
)

// CooperativeTerminationServer 管理 candidate 的认证退出监听与清理生命周期。
type CooperativeTerminationServer struct {
	listener         *net.UnixListener
	endpoint         string
	endpointIdentity CooperativeEndpointIdentity
	once             sync.Once
	done             chan struct{}
	activation       chan struct{}
	activationOnce   sync.Once
	stateMu          sync.Mutex
	ready            bool
	prepared         bool
	activated        bool
}

// StartCooperativeTerminationServer 启动 0600 Unix socket，并仅接受 token 认证的退出请求。
func StartCooperativeTerminationServer(endpoint, token string, terminate func()) (*CooperativeTerminationServer, error) {
	server, err := startCooperativeTerminationServerWithMode(endpoint, token, terminate, nil, true)
	if err != nil {
		return nil, err
	}
	return server, nil
}

// StartParkedCooperativeTerminationServer 启动认证控制端点，并在 ACTIVATE 前保持调用方 parked。
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
	return startCooperativeTerminationServerWithMode(endpoint, token, terminate, beforePublish, false)
}

// startCooperativeTerminationServerWithMode 发布端点并初始化 parked 或 active 状态。
func startCooperativeTerminationServerWithMode(
	endpoint, token string,
	terminate func(),
	beforePublish func(string) error,
	startActivated bool,
) (*CooperativeTerminationServer, error) {
	if endpoint == "" || token == "" || terminate == nil {
		return nil, errors.New("pidregistry: cooperative termination endpoint, token, and callback are required")
	}
	if _, err := os.Lstat(endpoint); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("pidregistry: cooperative termination endpoint already exists")
	}
	listener, endpointIdentity, err := listenAndPublishCooperativeEndpoint(endpoint, beforePublish)
	if err != nil {
		return nil, err
	}
	server := &CooperativeTerminationServer{
		listener: listener, endpoint: endpoint, endpointIdentity: endpointIdentity,
		done: make(chan struct{}), activation: make(chan struct{}),
	}
	if startActivated {
		server.ready = true
		server.prepared = true
		server.activated = true
		server.activationOnce.Do(func() { close(server.activation) })
	}
	safego.Go(context.Background(), nil, "pidregistry.cooperative-termination", func(context.Context) {
		server.serve(token, terminate)
	})
	return server, nil
}

// listenAndPublishCooperativeEndpoint 在随机 staging 路径收敛权限后原子发布正式 endpoint。
func listenAndPublishCooperativeEndpoint(
	endpoint string,
	beforePublish func(string) error,
) (*net.UnixListener, CooperativeEndpointIdentity, error) {
	stagingEndpoint, err := newCooperativeTerminationStagingEndpoint(endpoint)
	if err != nil {
		return nil, CooperativeEndpointIdentity{}, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: stagingEndpoint, Net: "unix"})
	if err != nil {
		return nil, CooperativeEndpointIdentity{}, fmt.Errorf("pidregistry: listen cooperative termination endpoint: %w", err)
	}
	stagingIdentity, err := prepareCooperativeEndpointStaging(listener, stagingEndpoint, beforePublish)
	if err != nil {
		return nil, CooperativeEndpointIdentity{}, err
	}
	if err := renameCooperativeEndpointNoReplace(stagingEndpoint, endpoint); err != nil {
		return nil, CooperativeEndpointIdentity{}, errors.Join(
			fmt.Errorf("pidregistry: publish cooperative termination endpoint: %w", err),
			cleanupCooperativeListenerPath(listener, stagingEndpoint),
		)
	}
	listener.SetUnlinkOnClose(false)
	identity, err := CaptureCooperativeEndpointIdentity(endpoint)
	if err != nil || identity != stagingIdentity {
		return nil, CooperativeEndpointIdentity{}, errors.Join(
			err, ErrCooperativeEndpointIdentityMismatch, listener.Close(),
		)
	}
	return listener, identity, nil
}

func prepareCooperativeEndpointStaging(
	listener *net.UnixListener,
	stagingEndpoint string,
	beforePublish func(string) error,
) (CooperativeEndpointIdentity, error) {
	if beforePublish != nil {
		if err := beforePublish(stagingEndpoint); err != nil {
			return CooperativeEndpointIdentity{}, errors.Join(
				fmt.Errorf("pidregistry: prepare cooperative termination endpoint publication: %w", err),
				cleanupCooperativeListenerPath(listener, stagingEndpoint),
			)
		}
	}
	if err := os.Chmod(stagingEndpoint, 0o600); err != nil {
		return CooperativeEndpointIdentity{}, errors.Join(err, cleanupCooperativeListenerPath(listener, stagingEndpoint))
	}
	identity, err := CaptureCooperativeEndpointIdentity(stagingEndpoint)
	if err != nil {
		return CooperativeEndpointIdentity{}, errors.Join(err, cleanupCooperativeListenerPath(listener, stagingEndpoint))
	}
	return identity, nil
}

func cleanupCooperativeListenerPath(listener *net.UnixListener, path string) error {
	closeErr := listener.Close()
	removeErr := os.Remove(path)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(closeErr, removeErr)
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
	command, ok := readAuthenticatedCooperativeCommand(connection, token)
	if !ok {
		return false
	}
	switch command {
	case cooperativeCommandReady:
		server.handleReady(connection)
	case cooperativeCommandActivate:
		server.handleActivate(connection)
	case cooperativeCommandTerminate:
		if !writeCooperativeResponse(connection, "ACK\n") {
			return false
		}
		terminate()
		return true
	}
	return false
}

// readAuthenticatedCooperativeCommand 有界读取并恒定时间认证单个控制命令。
func readAuthenticatedCooperativeCommand(connection *net.UnixConn, token string) (string, bool) {
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return "", false
	}
	line, err := bufio.NewReader(io.LimitReader(connection, cooperativeRequestMaxBytes+1)).ReadString('\n')
	if err != nil || len(line) > cooperativeRequestMaxBytes {
		return "", false
	}
	command, gotToken, ok := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
	if !ok || subtle.ConstantTimeCompare([]byte(gotToken), []byte(token)) != 1 {
		return "", false
	}
	return command, true
}

func (server *CooperativeTerminationServer) handleReady(connection *net.UnixConn) {
	server.stateMu.Lock()
	if server.ready {
		server.prepared = true
	} else {
		server.ready = true
	}
	server.stateMu.Unlock()
	writeCooperativeResponse(connection, "READY\n")
}

func (server *CooperativeTerminationServer) handleActivate(connection *net.UnixConn) {
	server.stateMu.Lock()
	if !server.prepared {
		server.stateMu.Unlock()
		writeCooperativeResponse(connection, "NOT_PREPARED\n")
		return
	}
	if !server.activated {
		server.activated = true
		server.activationOnce.Do(func() { close(server.activation) })
	}
	server.stateMu.Unlock()
	writeCooperativeResponse(connection, "COMMITTED\n")
}

func writeCooperativeResponse(connection *net.UnixConn, response string) bool {
	_, err := connection.Write([]byte(response))
	return err == nil
}

// WaitForActivation 等待 ACK 后认证 ACTIVATE 或调用方取消。
func (server *CooperativeTerminationServer) WaitForActivation(ctx context.Context) error {
	if server == nil || ctx == nil {
		return errors.New("pidregistry: cooperative activation server and context are required")
	}
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-server.activation:
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
		removeErr := CleanupCooperativeTerminationEndpointInstance(server.endpoint, server.endpointIdentity)
		err = errors.Join(err, removeErr)
	})
	return err
}

// CaptureCooperativeEndpointIdentity 捕获当前用户拥有的 0600 Unix socket 发布身份。
func CaptureCooperativeEndpointIdentity(endpoint string) (CooperativeEndpointIdentity, error) {
	if err := validateCooperativeEndpointPath(endpoint); err != nil {
		return CooperativeEndpointIdentity{}, err
	}
	info, err := os.Lstat(endpoint)
	if err != nil {
		return CooperativeEndpointIdentity{}, fmt.Errorf("pidregistry: inspect cooperative termination endpoint: %w", err)
	}
	stat, err := validateCooperativeEndpointInfo(info)
	if err != nil {
		return CooperativeEndpointIdentity{}, err
	}
	creationTimeSec, creationTimeNsec, err := validateCooperativeEndpointCreationTime(endpoint, stat)
	if err != nil {
		return CooperativeEndpointIdentity{}, err
	}
	return CooperativeEndpointIdentity{
		Device: uint64(stat.Dev), Inode: uint64(stat.Ino), UID: stat.Uid, Mode: uint32(stat.Mode),
		CreationTimeSec: creationTimeSec, CreationTimeNsec: creationTimeNsec,
	}, nil
}

// validateCooperativeEndpointPath 验证端点路径为绝对且已清理的规范路径。
func validateCooperativeEndpointPath(endpoint string) error {
	if !filepath.IsAbs(endpoint) || filepath.Clean(endpoint) != endpoint {
		return errors.New("pidregistry: cooperative termination endpoint must be absolute and clean")
	}
	return nil
}

// validateCooperativeEndpointInfo 验证端点为当前用户拥有且权限为 0600 的 Unix socket。
func validateCooperativeEndpointInfo(info os.FileInfo) (*syscall.Stat_t, error) {
	if info.Mode()&os.ModeSocket == 0 {
		return nil, errors.New("pidregistry: cooperative termination endpoint is not a Unix socket")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("pidregistry: cooperative termination endpoint owner mismatch")
	}
	if info.Mode().Perm() != 0o600 {
		return nil, errors.Join(
			ErrCooperativeEndpointNotReady,
			errors.New("pidregistry: cooperative termination endpoint mode is not 0600"),
		)
	}
	return stat, nil
}

// validateCooperativeEndpointCreationTime 读取并验证端点创建时间字段。
func validateCooperativeEndpointCreationTime(endpoint string, stat *syscall.Stat_t) (int64, int64, error) {
	creationTimeSec, creationTimeNsec, err := cooperativeEndpointCreationTime(endpoint, stat)
	if err != nil {
		return 0, 0, err
	}
	if creationTimeSec <= 0 || creationTimeNsec < 0 || creationTimeNsec >= int64(time.Second) {
		return 0, 0, errors.New("pidregistry: cooperative termination endpoint creation time is invalid")
	}
	return creationTimeSec, creationTimeNsec, nil
}

// CleanupCooperativeTerminationEndpoint 原子隔离并删除当前 stale endpoint，保留随后发布的 replacement。
func CleanupCooperativeTerminationEndpoint(endpoint string) error {
	return cleanupCooperativeTerminationEndpoint(endpoint, CooperativeEndpointIdentity{})
}

// CleanupCooperativeTerminationEndpointInstance 仅删除匹配的 endpoint 发布实例。
func CleanupCooperativeTerminationEndpointInstance(endpoint string, expected CooperativeEndpointIdentity) error {
	if expected == (CooperativeEndpointIdentity{}) {
		return errors.New("pidregistry: cooperative endpoint identity is required")
	}
	return cleanupCooperativeTerminationEndpoint(endpoint, expected)
}

// CleanupStaleCooperativeTerminationEndpoint 仅删除 quarantine 后不可连接的 stale socket。
func CleanupStaleCooperativeTerminationEndpoint(ctx context.Context, endpoint string) error {
	if ctx == nil {
		return errors.New("pidregistry: stale endpoint cleanup context is required")
	}
	quarantine, err := quarantineCooperativeTerminationEndpoint(endpoint, CooperativeEndpointIdentity{})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return removeStaleCooperativeEndpointQuarantine(ctx, endpoint, quarantine)
}

// removeStaleCooperativeEndpointQuarantine 删除不可连接实例，live 或不确定状态则恢复。
func removeStaleCooperativeEndpointQuarantine(ctx context.Context, endpoint, quarantine string) error {
	dialer := net.Dialer{}
	connection, dialErr := dialer.DialContext(ctx, "unix", quarantine)
	if dialErr == nil {
		closeErr := connection.Close()
		return restoreCooperativeEndpointQuarantine(
			endpoint, quarantine, errors.Join(errors.New("pidregistry: cooperative endpoint is still live"), closeErr),
		)
	}
	if cause := context.Cause(ctx); cause != nil {
		return restoreCooperativeEndpointQuarantine(endpoint, quarantine, errors.Join(cause, dialErr))
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
		return restoreCooperativeEndpointQuarantine(endpoint, quarantine, dialErr)
	}
	if err := os.Remove(quarantine); err != nil {
		return errors.Join(dialErr, fmt.Errorf("pidregistry: remove stale endpoint quarantine: %w", err))
	}
	return nil
}

func cleanupCooperativeTerminationEndpoint(endpoint string, expected CooperativeEndpointIdentity) error {
	quarantine, err := quarantineCooperativeTerminationEndpoint(endpoint, expected)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.Remove(quarantine); err != nil {
		return fmt.Errorf("pidregistry: remove quarantined cooperative termination endpoint: %w", err)
	}
	return nil
}

// quarantineCooperativeTerminationEndpoint 原子隔离指定发布实例并复核其身份未变化。
func quarantineCooperativeTerminationEndpoint(
	endpoint string,
	expected CooperativeEndpointIdentity,
) (string, error) {
	current, err := CaptureCooperativeEndpointIdentity(endpoint)
	if err != nil {
		return "", err
	}
	if expected != (CooperativeEndpointIdentity{}) && current != expected {
		return "", ErrCooperativeEndpointIdentityMismatch
	}
	quarantine, err := newCooperativeEndpointQuarantine(endpoint)
	if err != nil {
		return "", err
	}
	if err := renameCooperativeEndpointNoReplace(endpoint, quarantine); err != nil {
		return "", fmt.Errorf("pidregistry: quarantine cooperative termination endpoint: %w", err)
	}
	quarantined, inspectErr := CaptureCooperativeEndpointIdentity(quarantine)
	if inspectErr != nil || quarantined != current {
		return "", restoreCooperativeEndpointQuarantine(
			endpoint, quarantine, errors.Join(inspectErr, ErrCooperativeEndpointIdentityMismatch),
		)
	}
	return quarantine, nil
}

func newCooperativeEndpointQuarantine(endpoint string) (string, error) {
	var nonce [8]byte
	if _, err := cryptorand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("pidregistry: generate endpoint quarantine: %w", err)
	}
	return filepath.Join(filepath.Dir(endpoint), "."+filepath.Base(endpoint)+".quarantine."+hex.EncodeToString(nonce[:])), nil
}

func restoreCooperativeEndpointQuarantine(endpoint, quarantine string, primary error) error {
	if err := renameCooperativeEndpointNoReplace(quarantine, endpoint); err != nil {
		return errors.Join(primary, fmt.Errorf("pidregistry: restore endpoint quarantine %q: %w", quarantine, err))
	}
	return primary
}

// requestCooperativeTermination 发送认证 token，并要求 receiver 返回 ACK。
func requestCooperativeTermination(
	ctx context.Context,
	endpoint, token string,
	expectedPID int,
	expectedEndpoint CooperativeEndpointIdentity,
) error {
	return requestCooperativeControl(ctx, endpoint, token, cooperativeCommandTerminate, "ACK\n", expectedPID, expectedEndpoint)
}

// requestCooperativeControl 将路径实例和已连接 peer PID 绑定后交换单个认证命令与响应。
func requestCooperativeControl(
	ctx context.Context,
	endpoint, token, command, expectedResponse string,
	expectedPID int,
	expectedEndpoint CooperativeEndpointIdentity,
) (err error) {
	connection, err := dialVerifiedCooperativeEndpoint(ctx, endpoint, expectedPID, expectedEndpoint)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, connection.Close()) }()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return err
		}
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

// dialVerifiedCooperativeEndpoint 将连接绑定到预期路径实例和内核 peer PID。
func dialVerifiedCooperativeEndpoint(
	ctx context.Context,
	endpoint string,
	expectedPID int,
	expectedEndpoint CooperativeEndpointIdentity,
) (*net.UnixConn, error) {
	if ctx == nil {
		return nil, errors.New("pidregistry: cooperative control context is required")
	}
	currentEndpoint, err := CaptureCooperativeEndpointIdentity(endpoint)
	if err != nil {
		return nil, err
	}
	if expectedEndpoint == (CooperativeEndpointIdentity{}) {
		expectedEndpoint = currentEndpoint
	} else if currentEndpoint != expectedEndpoint {
		return nil, ErrCooperativeEndpointIdentityMismatch
	}
	dialer := net.Dialer{}
	rawConnection, err := dialer.DialContext(ctx, "unix", endpoint)
	if err != nil {
		return nil, cooperativeEndpointDialError(err)
	}
	connection, ok := rawConnection.(*net.UnixConn)
	if !ok {
		return nil, errors.Join(
			errors.New("pidregistry: cooperative termination connection is not Unix"), rawConnection.Close(),
		)
	}
	if err := validateCooperativePeerPID(connection, expectedPID); err != nil {
		return nil, errors.Join(err, connection.Close())
	}
	afterDial, err := CaptureCooperativeEndpointIdentity(endpoint)
	if err != nil {
		return nil, errors.Join(err, connection.Close())
	}
	if afterDial != expectedEndpoint {
		return nil, errors.Join(ErrCooperativeEndpointIdentityMismatch, connection.Close())
	}
	return connection, nil
}

func cooperativeEndpointDialError(err error) error {
	dialErr := fmt.Errorf("pidregistry: dial cooperative termination endpoint: %w", err)
	if errors.Is(err, syscall.ECONNREFUSED) {
		return errors.Join(ErrCooperativeEndpointNotReady, dialErr)
	}
	return dialErr
}

// validateCooperativePeerPID 验证已连接 Unix peer 的真实 PID。
func validateCooperativePeerPID(connection *net.UnixConn, expectedPID int) error {
	if expectedPID <= 1 {
		return errors.New("pidregistry: cooperative control requires expected peer PID")
	}
	raw, err := connection.SyscallConn()
	if err != nil {
		return fmt.Errorf("pidregistry: access cooperative peer socket: %w", err)
	}
	var peerPID int
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		peerPID, socketErr = cooperativePeerPID(int(fd))
	}); err != nil {
		return fmt.Errorf("pidregistry: inspect cooperative peer PID: %w", err)
	}
	if socketErr != nil {
		return fmt.Errorf("pidregistry: inspect cooperative peer PID: %w", socketErr)
	}
	if peerPID != expectedPID {
		return ErrStableProcessIdentityMismatch
	}
	return nil
}
