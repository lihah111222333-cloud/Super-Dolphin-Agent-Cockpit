// Package bus 提供基于 kelindar/event 的进程内事件总线，封装 Dispatcher 的创建、
// 订阅生命周期管理和结构化日志追踪。
package bus

// Subscription 汇总一组 cancel 函数，用于批量注销订阅。
// 它不做并发保护，调用方应在同一生命周期阶段内串行 Add 和 CancelAll。
type Subscription struct {
	cancels []func() // 已注册的取消函数列表
}

// NewSubscription 创建空订阅集合，供 Router 或测试夹具逐个登记取消函数。
func NewSubscription() *Subscription {
	return &Subscription{}
}

// Add 追加一个取消函数到列表；nil 会在 CancelAll 时暴露为调用方错误。
func (s *Subscription) Add(cancel func()) {
	s.cancels = append(s.cancels, cancel)
}

// CancelAll 依次调用所有已注册的取消函数，并清空列表以避免重复取消同一订阅。
func (s *Subscription) CancelAll() {
	for _, cancel := range s.cancels {
		cancel()
	}
	s.cancels = nil
}
