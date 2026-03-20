package bus

import (
	"sync/atomic"
	"testing"

	"github.com/kelindar/event"
)

type subscriptionEvent struct {
	ID int
}

func (subscriptionEvent) Type() uint32 { return 3 }

func TestSubscriptionCancelAll(t *testing.T) {
	t.Parallel()

	dispatcher := NewDispatcher()
	subscription := NewSubscription()
	var calls int32
	subscription.Add(event.Subscribe(dispatcher, func(subscriptionEvent) {
		atomic.AddInt32(&calls, 1)
	}))
	subscription.Add(event.Subscribe(dispatcher, func(subscriptionEvent) {
		atomic.AddInt32(&calls, 1)
	}))

	event.Publish(dispatcher, subscriptionEvent{ID: 1})
	waitForValue(t, int32(2), func() int32 { return atomic.LoadInt32(&calls) }, "call count before cancel")

	subscription.CancelAll()
	assertNoPanic(t, subscription.CancelAll)

	event.Publish(dispatcher, subscriptionEvent{ID: 2})
	assertValueAfterDelay(t, int32(2), func() int32 { return atomic.LoadInt32(&calls) }, "call count after cancel")
}
