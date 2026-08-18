//go:build !darwin && !linux && !freebsd

package appupdaterecovery

import "os"

// rollbackRestartEndpointRoot 保留命名管道等非 Unix 平台的系统临时根选择。
func rollbackRestartEndpointRoot(string) (string, error) {
	return os.TempDir(), nil
}
