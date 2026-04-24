package bus

import (
	"context"
	"reflect"
	"testing"

	"github.com/kelindar/event"
)

type subscriberTestEvent struct{}

func (subscriberTestEvent) Type() uint32 { return 990001 }

func TestSubscriberGroupRegistersCancelsAndStopsIntake(t *testing.T) {
	dispatcher := event.NewDispatcher()
	var order []string
	group := NewSubscriberGroup(subscriberGroupIn{Dispatcher: dispatcher, Specs: []SubscriberSpec{{
		EventType:     "subscriberTestEvent",
		HandlerSymbol: "TestSubscriberGroupRegistersCancelsAndStopsIntake.handler",
		OwnerModule:   "internal/platform/bus",
		CancelOwner:   "BusModule",
		ShutdownClass: "subscriber",
		TestFixtureID: "P22.1-P0-bus-subscriber",
		Register: func(*event.Dispatcher) context.CancelFunc {
			order = append(order, "register")
			return func() { order = append(order, "cancel") }
		},
	}}})
	if err := group.Start(); err != nil {
		t.Fatalf("start subscriber group: %v", err)
	}
	group.StopIntake()
	group.Cancel()
	if err := dispatcher.Close(); err != nil {
		t.Fatalf("close dispatcher: %v", err)
	}
	want := []string{"register", "cancel"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if err := group.Start(); err != ErrSubscriberIntakeStopped {
		t.Fatalf("restart after stop-intake = %v", err)
	}
}
