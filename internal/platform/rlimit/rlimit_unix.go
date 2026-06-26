//go:build !windows

package rlimit

import (
	"fmt"
	"os"
	"syscall"
)

// Init 在进程启动早期提升 NOFILE 上限，避免大量连接或文件句柄耗尽。
func Init() {
	raiseLimit()
}

// raiseLimit 优先提升到系统允许的最高值，并把结果写入 stderr 便于启动诊断。
func raiseLimit() {
	var rLimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit); err != nil {
		return
	}

	before := rLimit.Cur

	target := rLimit.Max
	// 某些系统会用极大值表示无限制，主动收敛到 100 万避免溢出或异常设置。
	if target > 1048576 {
		target = 1048576
	}

	if rLimit.Cur >= target {
		fmt.Fprintf(os.Stderr, "rlimit: NOFILE already at %d (max %d), no change needed\n", rLimit.Cur, rLimit.Max)
		return
	}

	rLimit.Cur = target
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rLimit); err == nil {
		fmt.Fprintf(os.Stderr, "rlimit: NOFILE raised from %d to %d (max %d)\n", before, target, rLimit.Max)
		return
	}

	applyFallbackLimit(rLimit, before)
}

// applyFallbackLimit 在最高值设置失败时按较低档位逐级尝试，并记录最终结果。
func applyFallbackLimit(rLimit syscall.Rlimit, oldLimit uint64) {
	for _, fallback := range []uint64{250000, 65535, 10240, 4096} {
		if rLimit.Max >= fallback && oldLimit < fallback {
			rLimit.Cur = fallback
			if syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rLimit) == nil {
				fmt.Fprintf(os.Stderr, "rlimit: NOFILE raised from %d to %d (fallback, max %d)\n", oldLimit, fallback, rLimit.Max)
				return
			}
		}
	}
	fmt.Fprintf(os.Stderr, "rlimit: NOFILE could not be raised from %d (max %d)\n", oldLimit, rLimit.Max)
}
