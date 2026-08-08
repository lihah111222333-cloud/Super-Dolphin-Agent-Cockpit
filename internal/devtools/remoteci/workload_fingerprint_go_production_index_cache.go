package remoteci

// remoteGoProductionIndexCacheEntry 保存一个 snapshot/profile 的生产索引或其错误。
// 索引只读返回，错误也缓存，避免同一失败输入被每个 selector 重复解析。
type remoteGoProductionIndexCacheEntry struct {
	index remoteGoProductionIndex
	err   error
}

// cachedRemoteGoProductionIndex 在 snapshot 内按 profile 串行构建生产索引。
// 该缓存不跨 tree、进程或 SQLite；调用方收到深拷贝，不能污染缓存中的 map/slice。
func (snapshot *remoteGitTreeSnapshot) cachedRemoteGoProductionIndex(profile remoteGoBuildProfile) (remoteGoProductionIndex, error) {
	key := profile.cacheKey()
	snapshot.productionIndexMu.Lock()
	defer snapshot.productionIndexMu.Unlock()
	if snapshot.productionIndexCache == nil {
		snapshot.productionIndexCache = make(map[string]remoteGoProductionIndexCacheEntry)
	}
	if cached, ok := snapshot.productionIndexCache[key]; ok {
		return cloneRemoteGoProductionIndexCacheEntry(cached)
	}
	snapshot.cacheMu.Lock()
	snapshot.productionIndexComputations++
	snapshot.cacheMu.Unlock()
	index, err := snapshot.buildRemoteGoProductionIndex(profile)
	cached := remoteGoProductionIndexCacheEntry{index: index, err: err}
	snapshot.productionIndexCache[key] = cached
	return cloneRemoteGoProductionIndexCacheEntry(cached)
}

func cloneRemoteGoProductionIndexCacheEntry(cached remoteGoProductionIndexCacheEntry) (remoteGoProductionIndex, error) {
	if cached.err != nil {
		return remoteGoProductionIndex{}, cached.err
	}
	return cloneRemoteGoProductionIndex(cached.index), nil
}

func cloneRemoteGoProductionIndex(index remoteGoProductionIndex) remoteGoProductionIndex {
	clone := remoteGoProductionIndex{byPackage: make(map[string]map[string][]remoteGoProductionDeclaration, len(index.byPackage))}
	for directory, declarations := range index.byPackage {
		byName := make(map[string][]remoteGoProductionDeclaration, len(declarations))
		for name, values := range declarations {
			byName[name] = append([]remoteGoProductionDeclaration(nil), values...)
		}
		clone.byPackage[directory] = byName
	}
	return clone
}
