package acpnode

import (
	"sync"
	"testing"
)

// runTestAsync 启动可等待的测试协程，并在用例退出前确认其完成。
func runTestAsync(t *testing.T, action func()) {
	t.Helper()
	group := &sync.WaitGroup{}
	group.Go(action)
	t.Cleanup(group.Wait)
}
