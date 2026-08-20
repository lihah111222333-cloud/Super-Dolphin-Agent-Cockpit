//go:build windows

package pidregistry

import (
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

func registryFileOwnedByCurrentUser(path string, info os.FileInfo) bool {
	return securefs.CheckPrivateOwnerOnly(path, info) == nil
}
