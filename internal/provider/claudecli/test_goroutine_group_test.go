package claudecli

import (
	"runtime/debug"
	"sync"
	"testing"
)

type testGoroutineGroup struct {
	t      testing.TB
	wg     sync.WaitGroup
	mu     sync.Mutex
	panics []testGoroutinePanic
}

type testGoroutinePanic struct {
	value any
	stack []byte
}

func newTestGoroutineGroup(t testing.TB) *testGoroutineGroup {
	t.Helper()
	group := &testGoroutineGroup{t: t}
	t.Cleanup(group.Wait)
	return group
}

func (g *testGoroutineGroup) Go(fn func()) {
	g.wg.Go(func() {
		defer g.capturePanic()
		fn()
	})
}

func (g *testGoroutineGroup) capturePanic() {
	if recovered := recover(); recovered != nil {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.panics = append(g.panics, testGoroutinePanic{value: recovered, stack: debug.Stack()})
	}
}

func (g *testGoroutineGroup) Wait() {
	g.t.Helper()
	g.wg.Wait()
	panics := g.takePanics()
	for _, panicRecord := range panics {
		g.t.Errorf("test goroutine panic: %v\n%s", panicRecord.value, panicRecord.stack)
	}
	if len(panics) > 0 {
		g.t.FailNow()
	}
}

func (g *testGoroutineGroup) takePanics() []testGoroutinePanic {
	g.mu.Lock()
	defer g.mu.Unlock()
	panics := append([]testGoroutinePanic(nil), g.panics...)
	g.panics = nil
	return panics
}
