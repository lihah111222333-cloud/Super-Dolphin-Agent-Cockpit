package remoteci

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"

// OCIProjectGoBuildCachePath 是验收后 OCI 基线镜像内唯一允许的只读 Go 构建缓存路径。
// 候选任务不得把它用作可写 GOCACHE 根目录。
const OCIProjectGoBuildCachePath = cicontract.AcceptedBaselineOCIProjectCachePath

// BaselineOCIProjectCache 将不可变基线镜像内的项目构建缓存绑定到精确源码和工具链。
type BaselineOCIProjectCache = cicontract.AcceptedBaselineOCIProjectCache

func validImageCacheIdentifier(value string) bool {
	return cicontract.ValidAcceptedBaselineIdentifier(value)
}

func cloneBaselineOCIProjectCache(cache *BaselineOCIProjectCache) *BaselineOCIProjectCache {
	if cache == nil {
		return nil
	}
	copy := *cache
	return &copy
}
