package bus

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kelindar/event"
)

type busEvent struct {
	ID   int
	Name string
}

func (busEvent) Type() uint32 { return 1 }

type otherBusEvent struct {
	Code string
}

func (otherBusEvent) Type() uint32 { return 2 }

func TestPublishSubscribe(t *testing.T) {
	t.Parallel()

	dispatcher := NewDispatcher()
	received := make(chan busEvent, 1)
	cancel := event.Subscribe(dispatcher, func(ev busEvent) {
		received <- ev
	})
	defer cancel()

	want := busEvent{ID: 1, Name: "basic"}
	event.Publish(dispatcher, want)

	if got := mustReceive(t, received); got != want {
		t.Fatalf("received event = %#v, want %#v", got, want)
	}
}

func TestMultipleSubscribers(t *testing.T) {
	t.Parallel()

	dispatcher := NewDispatcher()
	first := make(chan busEvent, 1)
	second := make(chan busEvent, 1)
	cancelFirst := event.Subscribe(dispatcher, func(ev busEvent) {
		first <- ev
	})
	defer cancelFirst()
	cancelSecond := event.Subscribe(dispatcher, func(ev busEvent) {
		second <- ev
	})
	defer cancelSecond()

	want := busEvent{ID: 2, Name: "multi"}
	event.Publish(dispatcher, want)

	if got := mustReceive(t, first); got != want {
		t.Fatalf("first subscriber got %#v, want %#v", got, want)
	}
	if got := mustReceive(t, second); got != want {
		t.Fatalf("second subscriber got %#v, want %#v", got, want)
	}
}

func TestUnsubscribe(t *testing.T) {
	t.Parallel()

	dispatcher := NewDispatcher()
	var calls atomic.Int32
	cancel := event.Subscribe(dispatcher, func(busEvent) {
		calls.Add(1)
	})

	event.Publish(dispatcher, busEvent{ID: 1})
	waitForValue(t, int32(1), calls.Load, "call count before cancel")
	cancel()
	event.Publish(dispatcher, busEvent{ID: 2})
	assertValueAfterDelay(t, int32(1), calls.Load, "call count after cancel")
}

func TestTypeSafety(t *testing.T) {
	t.Parallel()

	dispatcher := NewDispatcher()
	var busCalls atomic.Int32
	var otherCalls atomic.Int32
	cancelBus := event.Subscribe(dispatcher, func(busEvent) {
		busCalls.Add(1)
	})
	defer cancelBus()
	cancelOther := event.Subscribe(dispatcher, func(otherBusEvent) {
		otherCalls.Add(1)
	})
	defer cancelOther()

	event.Publish(dispatcher, busEvent{ID: 1})
	waitForValue(t, int32(1), busCalls.Load, "bus event calls after bus publish")
	assertValueAfterDelay(t, int32(0), otherCalls.Load, "other event calls after bus publish")

	event.Publish(dispatcher, otherBusEvent{Code: "x"})
	waitForValue(t, int32(1), otherCalls.Load, "other event calls after other publish")
	assertValueAfterDelay(t, int32(1), busCalls.Load, "bus event calls after other publish")
}

func TestConcurrentPublish(t *testing.T) {
	t.Parallel()

	const workers = 16
	const perWorker = 50

	dispatcher := NewDispatcher()
	var calls atomic.Int64
	cancel := event.Subscribe(dispatcher, func(busEvent) {
		calls.Add(1)
	})
	defer cancel()

	start := make(chan struct{})
	var wg sync.WaitGroup
	workersDone := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-workersDone:
		case <-time.After(time.Second):
			t.Fatal("bus publish goroutines did not stop")
		}
	})
	for worker := range workers {
		wg.Go(func() {
			<-start
			for i := range perWorker {
				event.Publish(dispatcher, busEvent{ID: worker*perWorker + i})
			}
		})
	}

	close(start)
	wg.Wait()
	close(workersDone)

	waitForValue(t, int64(workers*perWorker), calls.Load, "call count")
}

func TestDispatcherWrapperNilSafe(t *testing.T) {
	t.Parallel()

	var b *Bus
	if got := b.Dispatcher(); got != nil {
		t.Fatalf("dispatcher = %#v, want nil", got)
	}
}

func assertNoPanic(t *testing.T, fn func()) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()

	fn()
}

func mustReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()

	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatal("expected value, channel timed out")
		var zero T
		return zero
	}
}

func waitForValue[T comparable](t *testing.T, want T, load func() T, label string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := load(); got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	if got := load(); got != want {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

func assertValueAfterDelay[T comparable](t *testing.T, want T, load func() T, label string) {
	t.Helper()

	time.Sleep(100 * time.Millisecond)
	if got := load(); got != want {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}
