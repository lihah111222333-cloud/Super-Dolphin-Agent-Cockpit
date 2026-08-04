package hiddenexec

import (
	"errors"
	"os"
)

// isProcessTreeGoneError 只接受明确的进程已退出错误；nil 表示成功观测，不能判为 gone。
func isProcessTreeGoneError(err error, platformErrors ...error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrProcessDone) {
		return true
	}
	for _, platformErr := range platformErrors {
		if platformErr != nil && errors.Is(err, platformErr) {
			return true
		}
	}
	return false
}
