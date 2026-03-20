package bus

import (
	"sync/atomic"
	"testing"
)

type typedEvent struct {
	ID int
}

func (typedEvent) Type() uint32 { return 4 }

func TestTypedEmitterEmitAndOn(t *testing.T) {
	t.Parallel()

	emitter := NewTypedEmitter[typedEvent](NewDispatcher())
	received := make(chan typedEvent, 1)
	cancel := emitter.On(func(ev typedEvent) {
		received <- ev
	})
	defer cancel()

	want := typedEvent{ID: 7}
	emitter.Emit(want)

	if got := mustReceive(t, received); got != want {
		t.Fatalf("received event = %#v, want %#v", got, want)
	}
}

func TestTypedEmitterNilSafe(t *testing.T) {
	t.Parallel()

	var nilEmitter *TypedEmitter[typedEvent]
	assertNoPanic(t, func() {
		nilEmitter.Emit(typedEvent{ID: 1})
	})

	cancel := nilEmitter.On(func(typedEvent) {
		t.Fatal("nil emitter should not invoke handlers")
	})
	assertNoPanic(t, cancel)

	emptyEmitter := &TypedEmitter[typedEvent]{}
	assertNoPanic(t, func() {
		emptyEmitter.Emit(typedEvent{ID: 2})
	})

	cancel = emptyEmitter.On(func(typedEvent) {
		t.Fatal("emitter without dispatcher should not invoke handlers")
	})
	assertNoPanic(t, cancel)
}

func TestTypedEmitterCancel(t *testing.T) {
	t.Parallel()

	emitter := NewTypedEmitter[typedEvent](NewDispatcher())
	var calls int32
	cancel := emitter.On(func(typedEvent) {
		atomic.AddInt32(&calls, 1)
	})

	emitter.Emit(typedEvent{ID: 1})
	waitForValue(t, int32(1), func() int32 { return atomic.LoadInt32(&calls) }, "typed emitter call count before cancel")
	cancel()
	emitter.Emit(typedEvent{ID: 2})
	assertValueAfterDelay(t, int32(1), func() int32 { return atomic.LoadInt32(&calls) }, "typed emitter call count after cancel")
}
