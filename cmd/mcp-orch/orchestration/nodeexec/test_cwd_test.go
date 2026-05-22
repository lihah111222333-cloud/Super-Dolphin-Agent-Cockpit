package nodeexec

import (
	"path/filepath"
	"sync"
	"testing"
)

var testCWDCache sync.Map

func testCWD(t *testing.T, name string) string {
	t.Helper()
	key := t.Name() + "\x00" + name
	if value, ok := testCWDCache.Load(key); ok {
		return value.(string)
	}
	value, _ := testCWDCache.LoadOrStore(key, filepath.ToSlash(filepath.Join(t.TempDir(), name)))
	return value.(string)
}
