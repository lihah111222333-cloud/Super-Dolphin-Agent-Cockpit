package gate

import (
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/godistribution"
)

// ValidateRemoteGoDistributionPlatform 将运行镜像平台绑定到官方 Go 1.26.5 分发锁。
func ValidateRemoteGoDistributionPlatform(goos, goarch string) error {
	asset, err := godistribution.RemoteCIAsset()
	if err != nil {
		return fmt.Errorf("load remote CI Go distribution: %w", err)
	}
	if goos != asset.GOOS || goarch != asset.GOARCH {
		return fmt.Errorf("remote CI Go distribution platform is %s/%s, running binary is %s/%s", asset.GOOS, asset.GOARCH, goos, goarch)
	}
	if err := godistribution.ValidateRemoteCIAsset(asset); err != nil {
		return err
	}
	return nil
}
