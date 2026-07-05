package orchestration

import (
	"sync"
	"testing"
)

type testGoroutineGroup struct {
	wg sync.WaitGroup
}

func newTestGoroutineGroup(t *testing.T) *testGoroutineGroup {
	t.Helper()
	group := &testGoroutineGroup{}
	t.Cleanup(group.Wait)
	return group
}

func (g *testGoroutineGroup) Go(fn func()) {
	g.wg.Go(fn)
}

func (g *testGoroutineGroup) Wait() {
	g.wg.Wait()
}

func closeTestSignalOnce(ch chan struct{}) func() {
	var once sync.Once
	return func() {
		once.Do(func() { close(ch) })
	}
}
