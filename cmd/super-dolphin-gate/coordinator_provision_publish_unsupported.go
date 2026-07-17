//go:build !darwin

package main

import "errors"

// publishProductionProvisionRoot 阻断缺少当前受信原子发布原语的平台。
func publishProductionProvisionRoot(string, string) error {
	return errors.New("production provision root publication requires macOS renamex_np(RENAME_EXCL)")
}
