package bus

// Subscription groups cancel functions for bulk cleanup.
type Subscription struct {
	cancels []func()
}

// NewSubscription 创建subscription。
func NewSubscription() *Subscription {
	return &Subscription{}
}

// Add 添加平台bus。
func (s *Subscription) Add(cancel func()) {
	s.cancels = append(s.cancels, cancel)
}

// CancelAll 处理cancelall。
func (s *Subscription) CancelAll() {
	for _, cancel := range s.cancels {
		cancel()
	}
	s.cancels = nil
}
