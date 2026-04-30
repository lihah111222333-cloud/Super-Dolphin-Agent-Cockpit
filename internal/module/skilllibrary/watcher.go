package skilllibrary

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// DevWatcher 监听 dev override 源目录变更（spec §3.3 / §5.4）。
// 文件变更时调用 onChange(name)，name 是 srcRoot 下的一级目录名。
//
// onChange 在 watcher 内部 goroutine 中执行；调用方应快速 dispatch
// 到 reconcile 而不是阻塞回调。
type DevWatcher struct {
	w        *fsnotify.Watcher
	stop     chan struct{}
	closeMu  sync.Mutex
	closed   bool
	onChange func(string)
}

// NewDevWatcher 立即启动监听 srcRoot 下所有现存子目录及根目录。
// 不会动态加入新创建的 skill 子目录（dev 场景下 skill 目录通常已存在）。
func NewDevWatcher(srcRoot string, onChange func(name string)) (*DevWatcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := fw.Add(srcRoot); err != nil {
		_ = fw.Close()
		return nil, err
	}
	matches, _ := filepath.Glob(filepath.Join(srcRoot, "*"))
	for _, p := range matches {
		_ = fw.Add(p)
	}

	dw := &DevWatcher{w: fw, stop: make(chan struct{}), onChange: onChange}
	go dw.loop(srcRoot)
	return dw, nil
}

func (d *DevWatcher) loop(srcRoot string) {
	for {
		select {
		case <-d.stop:
			return
		case ev, ok := <-d.w.Events:
			if !ok {
				return
			}
			if name := skillNameFromEvent(srcRoot, ev.Name); name != "" {
				d.onChange(name)
			}
		case <-d.w.Errors:
			// 不致命，继续监听
		}
	}
}

// skillNameFromEvent 把 fsnotify 事件路径映射回 skill 名（srcRoot 下的一级目录）。
func skillNameFromEvent(srcRoot, evPath string) string {
	rel, err := filepath.Rel(srcRoot, evPath)
	if err != nil {
		return ""
	}
	rel = strings.TrimPrefix(rel, string(filepath.Separator))
	parts := strings.SplitN(rel, string(filepath.Separator), 2)
	if len(parts) == 0 || parts[0] == "" || parts[0] == "." {
		return ""
	}
	return parts[0]
}

// Close 关闭 watcher；多次调用安全。
func (d *DevWatcher) Close() error {
	d.closeMu.Lock()
	defer d.closeMu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	close(d.stop)
	return d.w.Close()
}
