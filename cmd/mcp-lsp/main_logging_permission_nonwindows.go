//go:build !windows

package main

// wrapSidecarLogPathError 在非 Windows 平台保持原始错误链，不引入 Windows ACL 分类。
func wrapSidecarLogPathError(_ string, _ string, err error) error {
	return err
}
