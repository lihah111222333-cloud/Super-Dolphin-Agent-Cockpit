// Package bus 提供基于 kelindar/event 的进程内事件总线，封装 Dispatcher 的创建、
// 订阅生命周期管理和结构化日志追踪。
package bus

// Subscription 汇总一组 cancel 函数，用于批量注销订阅。
type Subscription struct {
	cancels []func() // 已注册的取消函数列表
}

// NewSubscription 创建空的 Subscription 实例。
func NewSubscription() *Subscription {
	return &Subscription{}
}

// Add 追加一个取消函数到列表。
func (s *Subscription) Add(cancel func()) {
	s.cancels = append(s.cancels, cancel)
}

// CancelAll 依次调用所有已注册的取消函数，并清空列表。
func (s *Subscription) CancelAll() {
	for _, cancel := range s.cancels {
		cancel()
	}
	s.cancels = nil
}
