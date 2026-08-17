package securefs

// redactedPathError 保留错误链，并使用 securefs 统一规则渲染脱敏路径。
type redactedPathError struct {
	err  error
	path string
}

func (e *redactedPathError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return SafeErrorForPath(e.err, e.path)
}

func (e *redactedPathError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// WrapErrorForPath 保留 errors.Is/errors.As，同时避免底层错误暴露原始路径。
// Windows 下原始 Win32 5/1314 会提升为 WindowsPermissionError；其他平台只做路径脱敏并保留错误链。
func WrapErrorForPath(err error, path string) error {
	if err == nil {
		return nil
	}
	err = wrapWindowsPermissionError(err, path)
	return &redactedPathError{err: err, path: path}
}
