package tools

import "sync"

type editLockRegistry struct{ m sync.Map }

var editFileLocks = &editLockRegistry{}

func lockEditFile(path string) func() {
	return lockEditFiles([]string{path})
}

func lockEditFiles(paths []string) func() {
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
		value, _ := editFileLocks.m.LoadOrStore(path, &sync.Mutex{})
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
