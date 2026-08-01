package coordinatoradmission

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

var errInvalidDispatcher = errors.New("coordinator admission dispatcher is invalid")

// Dispatcher 保序缓存 job reservation，并由单一消费者执行 admission。
type Dispatcher struct {
	queue chan localci.WorkloadReservation
}

// New 创建显式有界的 admission dispatcher。
func New(capacity int) (*Dispatcher, error) {
	if capacity <= 0 {
		return nil, errInvalidDispatcher
	}
	return &Dispatcher{queue: make(chan localci.WorkloadReservation, capacity)}, nil
}

// Enqueue 按调用顺序写入 reservation，并接受上下文取消。
func (dispatcher *Dispatcher) Enqueue(ctx context.Context, reservation localci.WorkloadReservation) error {
	if dispatcher == nil || dispatcher.queue == nil || ctx == nil {
		return errInvalidDispatcher
	}
	select {
	case dispatcher.queue <- reservation:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run 串行消费 reservation，handler 失败时立即终止。
func (dispatcher *Dispatcher) Run(
	ctx context.Context,
	handler func(context.Context, localci.WorkloadReservation) error,
) error {
	if dispatcher == nil || dispatcher.queue == nil || ctx == nil || handler == nil {
		return errInvalidDispatcher
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case reservation := <-dispatcher.queue:
			if err := handler(ctx, reservation); err != nil {
				return err
			}
		}
	}
}
