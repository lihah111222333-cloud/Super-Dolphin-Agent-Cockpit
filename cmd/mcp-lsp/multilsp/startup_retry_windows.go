//go:build windows

package multilsp

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// windows122RetryPause 是 stderr flush 观察的可替换等待点；生产使用短暂 sleep，
// 测试可用 channel 精确控制异步 stderr 到达，不改变重试次数或错误门禁。
var windows122RetryPause = func(ctx context.Context) {
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// initializeClientWithWindows122Retry 仅重试 Windows 122 的启动瞬态。
// 首次 initialize 失败后必须先关闭并确认旧 owner，再最多创建一次新 client；
// 其他错误、第二次 122 和 context 取消均立即失败，不改变非 Windows 行为。
func initializeClientWithWindows122Retry(
	ctx context.Context,
	client Client,
	initialize func(Client) error,
	restart func() (Client, error),
	cleanup func(Client) error,
) (Client, error) {
	if err := initialize(client); err == nil {
		return client, nil
	} else if !isWindows122StartupError(ctx, client, err) || ctx.Err() != nil {
		return client, err
	} else {
		logWindows122Retry(1, client, err, nil)
		cleanupErr := cleanup(client)
		logWindows122Retry(1, client, err, cleanupErr)
		if cleanupErr != nil {
			return nil, errors.Join(err, fmt.Errorf("Windows 122 startup cleanup: %w", cleanupErr))
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		replacement, restartErr := restart()
		if restartErr != nil {
			return nil, restartErr
		}
		if err := initialize(replacement); err == nil {
			logWindows122Retry(2, replacement, nil, nil)
			return replacement, nil
		} else {
			logWindows122Retry(2, replacement, err, nil)
			cleanupErr := cleanup(replacement)
			logWindows122Retry(2, replacement, err, cleanupErr)
			if cleanupErr != nil {
				return nil, errors.Join(err, fmt.Errorf("Windows 122 retry cleanup: %w", cleanupErr))
			}
			return nil, err
		}
	}
}

func isWindows122StartupError(ctx context.Context, candidate Client, err error) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		logWindows122Decision("context_canceled", candidate, false, false, false, false, false)
		return false
	}
	text := strings.ToLower(err.Error())
	exactWin122 := strings.Contains(text, "the data area passed to a system call is too small")
	transportClosed := windowsTransportClosedError(err) || windowsPipeClosedText(text)
	if !transportClosed && !exactWin122 {
		logWindows122Decision("reject_not_closed_or_122", candidate, transportClosed, exactWin122, false, false, false)
		return false
	}
	if exactWin122 && transportClosed {
		logWindows122Decision("accept_direct_closed_and_122", candidate, transportClosed, exactWin122, false, false, true)
		return true
	}
	concrete, ok := concreteClient(candidate)
	wrapperUnwrapped := ok
	if _, direct := candidate.(*client); direct {
		wrapperUnwrapped = false
	}
	if !ok || concrete == nil || concrete.transport == nil || concrete.transport.stderr == nil {
		logWindows122Decision("reject_no_concrete_transport", candidate, transportClosed, exactWin122, wrapperUnwrapped, false, false)
		return false
	}
	if exactWin122 && transportDoneAndExited(concrete.transport) {
		logWindows122Decision("accept_done_and_exited", candidate, transportClosed, exactWin122, wrapperUnwrapped, true, true)
		return true
	}
	// os/exec 只有在进程退出并完成 stderr drain 后才关闭 done；优先观察该边界，
	// 再进入有界轮询，避免把异步 stderr race 误判为普通 pipe closed。
	if concrete.transport.done != nil {
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-concrete.transport.done:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	for attempt := 0; attempt < 20; attempt++ {
		if ctx != nil && ctx.Err() != nil {
			return false
		}
		stderr, _, _ := concrete.transport.stderr.Snapshot()
		if strings.Contains(strings.ToLower(stderr), "the data area passed to a system call is too small") {
			logWindows122Decision("accept_delayed_stderr", candidate, transportClosed, exactWin122, wrapperUnwrapped, transportDoneAndExited(concrete.transport), true)
			return true
		}
		if attempt < 19 {
			windows122RetryPause(ctx)
		}
	}
	logWindows122Decision("reject_no_delayed_122", candidate, transportClosed, exactWin122, wrapperUnwrapped, transportDoneAndExited(concrete.transport), false)
	return false
}

func transportDoneAndExited(transport *transport) bool {
	if transport == nil || transport.done == nil || transport.cmd == nil || transport.cmd.ProcessState == nil {
		return false
	}
	select {
	case <-transport.done:
		return transport.cmd.ProcessState.Exited()
	default:
		return false
	}
}

func windowsTransportClosedError(err error) bool {
	return errors.Is(err, os.ErrClosed) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, syscall.ERROR_BROKEN_PIPE)
}

func windowsPipeClosedText(text string) bool {
	return strings.Contains(text, "pipe is being closed") ||
		strings.Contains(text, "broken pipe") ||
		strings.Contains(text, "file already closed")
}

func logWindows122Retry(attempt int, client Client, startupErr, cleanupErr error) {
	log := logger.Get()
	if log == nil {
		return
	}
	pid, start := windows122ClientIdentity(client)
	fields := []any{"attempt", attempt, "pid", pid, "start_captured", start != ""}
	if startupErr != nil {
		fields = append(fields, "error_class", "win32_122")
	}
	if cleanupErr != nil {
		fields = append(fields, "cleanup", "failed")
	} else if startupErr != nil {
		fields = append(fields, "cleanup", "ok")
	}
	log.Warn("Windows LSP startup transient observation", fields...)
}

func logWindows122Decision(stage string, candidate Client, closed, exactWin122, wrapperUnwrapped, doneObserved, accepted bool) {
	log := logger.Get()
	if log == nil {
		return
	}
	pid, start := windows122ClientIdentity(candidate)
	identityDigest := ""
	if start != "" {
		identityDigest = fmt.Sprintf("%x", sha256.Sum256([]byte(start)))
	}
	log.Info("Windows LSP startup retry decision", "stage", stage, "closed", closed, "exact_win32_122", exactWin122, "wrapper_unwrapped", wrapperUnwrapped, "done_observed", doneObserved, "accepted", accepted, "pid", pid, "start_identity_sha256", identityDigest)
}

func windows122ClientIdentity(candidate Client) (int, string) {
	concrete, ok := candidate.(*client)
	if !ok || concrete == nil || concrete.transport == nil || concrete.transport.cmd == nil || concrete.transport.cmd.Process == nil {
		return 0, ""
	}
	pid := concrete.transport.cmd.Process.Pid
	start, err := hiddenexec.ProcessStartIdentity(pid)
	if err != nil {
		return pid, ""
	}
	return pid, start
}
