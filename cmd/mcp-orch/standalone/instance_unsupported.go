//go:build !darwin && !linux && !windows

package standalone

import (
	"errors"
	"os"
)

// openInstanceHandle 在尚未验证的平台拒绝分配虚假的单实例锁句柄。
func openInstanceHandle(*os.Root) (*os.File, error) {
	return nil, errors.New("orch singleton locking is unsupported on this operating system")
}

// tryInstanceLock 在没有受验证内核锁的平台拒绝启动，禁止退化为 PID 文件锁。
func tryInstanceLock(*os.File) error {
	return errors.New("orch singleton locking is unsupported on this operating system")
}

// unlockInstance 明确报告当前平台不具备锁实现。
func unlockInstance(*os.File) error {
	return errors.New("orch singleton locking is unsupported on this operating system")
}

// instanceLockBusy 不把未支持的平台错误伪装成正常锁竞争。
func instanceLockBusy(error) bool { return false }
