package bus

// Subscription groups cancel functions for bulk cleanup.
type Subscription struct {
	cancels []func()
}

func NewSubscription() *Subscription {
	return &Subscription{}
}

func (s *Subscription) Add(cancel func()) {
	s.cancels = append(s.cancels, cancel)
}

func (s *Subscription) CancelAll() {
	for _, cancel := range s.cancels {
		cancel()
	}
	s.cancels = nil
}
