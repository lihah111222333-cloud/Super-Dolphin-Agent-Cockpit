package coordinatoradmission

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"golang.org/x/sync/errgroup"
)

func TestDispatcherRejectsInvalidCapacity(t *testing.T) {
	if dispatcher, err := New(0); !errors.Is(err, errInvalidDispatcher) || dispatcher != nil {
		t.Fatalf("New(0) = %#v, %v", dispatcher, err)
	}
}

func TestDispatcherRunsReservationsFIFO(t *testing.T) {
	dispatcher, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan string, 3)
	group := errgroup.Group{}
	group.Go(func() error {
		return dispatcher.Run(ctx, func(_ context.Context, reservation localci.WorkloadReservation) error {
			started <- reservation.WorkloadID
			return nil
		})
	})
	for _, id := range []string{"job-1", "job-2", "job-3"} {
		if err := dispatcher.Enqueue(ctx, localci.WorkloadReservation{WorkloadID: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"job-1", "job-2", "job-3"} {
		if got := <-started; got != want {
			t.Fatalf("reservation order = %q, want %q", got, want)
		}
	}
	cancel()
	if err := group.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
}
