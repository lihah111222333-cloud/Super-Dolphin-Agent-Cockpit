package tools

import "sync"

// editLockRegistry 保存按绝对路径分片的编辑锁，避免同一文件被并发写入。
type editLockRegistry struct{ m sync.Map }

// lockEditFile 获取单文件编辑锁，并返回必须由调用方 defer 的释放函数。
func lockEditFile(owner *editLockRegistry, path string) func() {
	return lockEditFiles(owner, []string{path})
}

// lockEditFiles 按传入顺序获取文件锁，释放时反向解锁。
// 调用方应先传入已排序或已去重的路径；这里仅跳过空路径和连续重复项，避免
// replace/rename 的同文件写入互相交错。
func lockEditFiles(owner *editLockRegistry, paths []string) func() {
	if len(paths) == 0 {
		return func() {}
	}
	locks := make([]*sync.Mutex, 0, len(paths))
	last := ""
	for _, path := range paths {
		if path == "" || path == last {
			continue
		}
		last = path
		value, _ := owner.m.LoadOrStore(path, &sync.Mutex{})
		mu := value.(*sync.Mutex)
		mu.Lock()
		locks = append(locks, mu)
	}
	return func() {
		for idx := len(locks) - 1; idx >= 0; idx-- {
			locks[idx].Unlock()
		}
	}
}
