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
		fmt.Fprintf(os.Stderr, "rlimit: query NOFILE failed: %v\n", err)
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
	} else {
		fmt.Fprintf(os.Stderr, "rlimit: setting NOFILE target %d failed: %v; trying lower explicit limits\n", target, err)
	}

	applyFallbackLimit(rLimit)
}

// applyFallbackLimit 在最高值设置失败时按较低档位逐级尝试，并记录最终结果。
// 这里故意使用逐项无类型常量：Go 的 syscall.Rlimit 在 FreeBSD/DragonFly 使用 int64，
// 在 Linux/Darwin 等平台使用 uint64；共享实现不能把任一平台的字段强制成另一种类型。
func applyFallbackLimit(rLimit syscall.Rlimit) {
	oldLimit := rLimit.Cur
	for step := 0; step < 4; step++ {
		eligible := true
		switch step {
		case 0:
			eligible = rLimit.Max >= 250000 && oldLimit < 250000
			rLimit.Cur = 250000
		case 1:
			eligible = rLimit.Max >= 65535 && oldLimit < 65535
			rLimit.Cur = 65535
		case 2:
			eligible = rLimit.Max >= 10240 && oldLimit < 10240
			rLimit.Cur = 10240
		case 3:
			eligible = rLimit.Max >= 4096 && oldLimit < 4096
			rLimit.Cur = 4096
		}
		if !eligible {
			continue
		}
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rLimit); err == nil {
			fmt.Fprintf(os.Stderr, "rlimit: NOFILE raised from %d to %d (fallback, max %d)\n", oldLimit, rLimit.Cur, rLimit.Max)
			return
		} else {
			fmt.Fprintf(os.Stderr, "rlimit: setting fallback NOFILE %d failed: %v\n", rLimit.Cur, err)
		}
	}
	fmt.Fprintf(os.Stderr, "rlimit: NOFILE could not be raised from %d (max %d)\n", oldLimit, rLimit.Max)
}
