//go:build unix

package localci

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

const schedulerMaxUnixSocketPathBytes = 100

// openSchedulerTransportListener 安全清理 stale socket 后绑定私有 listener。
func openSchedulerTransportListener(
	socketPath string,
	ownerUID int,
) (net.Listener, os.FileInfo, error) {
	if len(socketPath) > schedulerMaxUnixSocketPathBytes {
		return nil, nil, fmt.Errorf("scheduler socket path is %d bytes, maximum is %d", len(socketPath), schedulerMaxUnixSocketPathBytes)
	}
	if err := validatePrivateSchedulerParent(socketPath, ownerUID); err != nil {
		return nil, nil, err
	}
	if err := removeStaleSchedulerSocket(socketPath, ownerUID); err != nil {
		return nil, nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, nil, err
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(socketPath, privateSchedulerFileMode); err != nil {
		return nil, nil, closeSchedulerListenerAfterError(listener, socketPath, err)
	}
	info, err := os.Lstat(socketPath)
	if err != nil {
		return nil, nil, closeSchedulerListenerAfterError(listener, socketPath, err)
	}
	if err := validateSchedulerSocketInfo(info, ownerUID); err != nil {
		return nil, nil, closeSchedulerListenerAfterError(listener, socketPath, err)
	}
	return listener, info, nil
}

// removeStaleSchedulerSocket 只移除已验证、无人监听且 inode 未变化的 socket。
func removeStaleSchedulerSocket(socketPath string, ownerUID int) error {
	info, err := os.Lstat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lstat scheduler socket: %w", err)
	}
	if err := validateSchedulerSocketInfo(info, ownerUID); err != nil {
		return err
	}
	conn, dialErr := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
	if dialErr == nil {
		closeErr := conn.Close()
		return errors.Join(ErrSchedulerOwned, errors.New("scheduler socket is active"), closeErr)
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
		return fmt.Errorf("probe existing scheduler socket: %w", dialErr)
	}
	current, err := os.Lstat(socketPath)
	if err != nil {
		return fmt.Errorf("recheck stale scheduler socket: %w", err)
	}
	if !os.SameFile(info, current) {
		return errors.New("scheduler socket changed during stale check")
	}
	if err := os.Remove(socketPath); err != nil {
		return fmt.Errorf("remove stale scheduler socket: %w", err)
	}
	return nil
}

func validateSchedulerSocketInfo(info os.FileInfo, ownerUID int) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
		return errors.New("scheduler socket must be a non-symlink Unix socket")
	}
	if err := validatePrivateOwnerAndMode(info, ownerUID, false); err != nil {
		return fmt.Errorf("scheduler socket ownership: %w", err)
	}
	return nil
}

func closeSchedulerListenerAfterError(listener *net.UnixListener, socketPath string, cause error) error {
	closeErr := listener.Close()
	removeErr := os.Remove(socketPath)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(cause, closeErr, removeErr)
}

// dialSchedulerTransport 连接前后复核 socket inode，并校验 server UID。
func dialSchedulerTransport(ctx context.Context, socketPath string, ownerUID int) (net.Conn, error) {
	if ctx == nil {
		return nil, errors.New("scheduler dial context is required")
	}
	info, err := os.Lstat(socketPath)
	if err != nil {
		return nil, fmt.Errorf("lstat scheduler socket: %w", err)
	}
	if err := validateSchedulerSocketInfo(info, ownerUID); err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	peerUID, err := schedulerTransportPeerUID(conn)
	if err != nil {
		return nil, closeSchedulerConnAfterError(conn, err)
	}
	if peerUID != ownerUID {
		return nil, closeSchedulerConnAfterError(conn, fmt.Errorf("scheduler server UID %d does not match required UID %d", peerUID, ownerUID))
	}
	current, err := os.Lstat(socketPath)
	if err != nil {
		return nil, closeSchedulerConnAfterError(conn, err)
	}
	if !os.SameFile(info, current) {
		return nil, closeSchedulerConnAfterError(conn, errors.New("scheduler socket changed during dial"))
	}
	return conn, nil
}

func closeSchedulerConnAfterError(conn net.Conn, cause error) error {
	if conn == nil {
		return cause
	}
	return errors.Join(cause, conn.Close())
}

func schedulerTransportPeerUID(conn net.Conn) (int, error) {
	syscallConn, ok := conn.(syscall.Conn)
	if !ok {
		return 0, errors.New("scheduler connection has no syscall handle")
	}
	raw, err := syscallConn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("open scheduler connection control: %w", err)
	}
	var peerUID int
	var controlErr error
	if err := raw.Control(func(fd uintptr) {
		peerUID, controlErr = schedulerPlatformPeerUID(int(fd))
	}); err != nil {
		return 0, fmt.Errorf("inspect scheduler peer: %w", err)
	}
	if controlErr != nil {
		return 0, fmt.Errorf("read scheduler peer credentials: %w", controlErr)
	}
	return peerUID, nil
}

// removeSchedulerTransportSocket 仅移除 owner 创建且未被替换的 socket inode。
func removeSchedulerTransportSocket(socketPath string, expected os.FileInfo, ownerUID int) error {
	current, err := os.Lstat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lstat scheduler socket during close: %w", err)
	}
	if err := validateSchedulerSocketInfo(current, ownerUID); err != nil {
		return err
	}
	if expected == nil || !os.SameFile(expected, current) {
		return errors.New("scheduler socket changed before close")
	}
	if err := os.Remove(socketPath); err != nil {
		return fmt.Errorf("remove scheduler socket: %w", err)
	}
	return nil
}
