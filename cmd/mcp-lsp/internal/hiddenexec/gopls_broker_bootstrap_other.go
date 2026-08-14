//go:build !windows

package hiddenexec

import (
	"errors"
	"io"
)

const windowsGoplsBrokerBootstrapModeArgument = "__super_dolphin_windows_gopls_broker_bootstrap_v1"

var errWindowsGoplsBrokerBootstrapUnsupported = errors.New("Windows gopls broker bootstrap is unavailable on this platform")

// WindowsGoplsBrokerBootstrapProcess 保留跨平台签名；非 Windows 不提供实例。
type WindowsGoplsBrokerBootstrapProcess struct {
	PID                int
	StartIdentity      string
	ExecutablePath     string
	ImageSHA256        string
	VolumeSerialNumber uint32
	FileID             uint64
}

// StartWindowsGoplsBrokerBootstrap 在非 Windows 立即拒绝专用 breakaway 启动。
func StartWindowsGoplsBrokerBootstrap() (*WindowsGoplsBrokerBootstrapProcess, error) {
	return nil, errWindowsGoplsBrokerBootstrapUnsupported
}

// RunWindowsGoplsBrokerBootstrapIfRequested 在非 Windows 遇到内部 marker 时 fail-fast。
func RunWindowsGoplsBrokerBootstrapIfRequested(args []string, run func(io.Reader, io.Writer) int) (handled bool, exitCode int) {
	_ = run
	if len(args) >= 2 && args[1] == windowsGoplsBrokerBootstrapModeArgument {
		return true, 1
	}
	return false, 0
}

// RequestWriter 在不支持的平台不返回伪造请求通道。
func (p *WindowsGoplsBrokerBootstrapProcess) RequestWriter() io.WriteCloser { return nil }

// ResponseReader 在不支持的平台不返回伪造响应通道。
func (p *WindowsGoplsBrokerBootstrapProcess) ResponseReader() io.ReadCloser { return nil }

// KillAndWait 在不支持的平台立即拒绝伪造进程权限。
func (p *WindowsGoplsBrokerBootstrapProcess) KillAndWait() error {
	return errWindowsGoplsBrokerBootstrapUnsupported
}

// ReleaseAuthority 在不支持的平台立即拒绝伪造权限移交。
func (p *WindowsGoplsBrokerBootstrapProcess) ReleaseAuthority() error {
	return errWindowsGoplsBrokerBootstrapUnsupported
}
