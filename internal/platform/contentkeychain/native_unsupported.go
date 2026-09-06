//go:build !darwin || !cgo

package contentkeychain

const nativeSupported = false

// openNative 在不支持的平台明确拒绝，不提供内存或文件兜底。
func openNative(string) (nativeStore, error) { return nil, ErrUnsupported }
