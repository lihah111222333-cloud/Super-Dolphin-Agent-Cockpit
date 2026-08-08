//go:build unix

package trustedlauncher

import (
	"os"
	"syscall"
)

// trustedFileOwnerUID 返回 Unix 文件 owner；无法证明 owner 时拒绝继续。
func trustedFileOwnerUID(info os.FileInfo) (int, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(stat.Uid), true
}
