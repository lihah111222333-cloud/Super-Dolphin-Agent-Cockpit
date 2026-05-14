//go:build ignore

// Package metricsample is a test fixture for MeasureFileMetrics.
// It intentionally contains various code quality issues for testing.
package metricsample

import (
	"fmt"
	"os"
)

// TODO: this is a test TODO comment.

var globalCounter int // global var (non-exempt)

func panicFunc() {
	panic("boom")
}

func nakedReturnFunc() (result int, err error) {
	result = 42
	return // naked return
}

func emptyFunc() {
}

func deepNesting() {
	if true {
		for i := 0; i < 10; i++ {
			if i > 5 {
				if i > 7 {
					fmt.Println(i) // nesting depth 4
				}
			}
		}
	}
}

func manyParams(a, b, c, d, e, f, g, h int) int {
	return a + b + c + d + e + f + g + h
}

func manyReturns() (int, int, int, int, error) {
	return 1, 2, 3, 4, nil
}

type BigStruct struct {
	A, B, C, D, E, F, G, H, I, J int
	K, L, M, N, O, P             int // 16 fields total
}

// TODO: another TODO

func complexFunc(x int) int {
	if x > 0 {
		if x > 10 {
			switch {
			case x > 100:
				return x * 2
			case x > 50:
				return x
			default:
				if x > 30 {
					return x - 1
				}
			}
		}
	} else if x < -10 {
		return -x
	}
	return 0
}

func useGlobals() {
	_ = os.Stdout
	_ = globalCounter
}
