//go:build !windows

package rlimit

import (
	"fmt"
	"os"
	"syscall"
)

// Init raises the open-file descriptor limit. Call it early in main().
func Init() {
	raiseLimit()
}

func raiseLimit() {
	var rLimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rLimit); err != nil {
		return
	}

	before := rLimit.Cur

	target := rLimit.Max
	// Cap it to 1 million to avoid issues with infinity/max uint64 on some OSes.
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
