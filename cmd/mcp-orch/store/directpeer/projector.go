package directpeer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// EventSink 是 direct-peer 事件投影的窄输出端口；调用方负责幂等消费 EventID。
type EventSink func(context.Context, Event) error

// EventProjector 负责从 durable outbox 读取事件并在成功投影后 ACK。
type EventProjector struct {
	store    *Store
	sink     EventSink
	workerID string
	logger   *slog.Logger
}

// NewEventProjector 创建带独立 worker 身份的事件投影器。
func NewEventProjector(store *Store, sink EventSink, logger *slog.Logger) (*EventProjector, error) {
	if store == nil || sink == nil {
		return nil, errors.New("direct peer event projector requires store and sink")
	}
	token, err := newDirectPeerClaimToken()
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &EventProjector{store: store, sink: sink, workerID: "direct-peer-event-projector-" + token, logger: logger}, nil
}

// Run 周期领取事件；投影失败时保留 claim，租约到期后由本 worker 或 successor 重试。
func (p *EventProjector) Run(ctx context.Context) error {
	if p == nil || p.store == nil || p.sink == nil {
		return errors.New("direct peer event projector is not configured")
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := p.Process(ctx, 30*time.Second, 32); err != nil && ctx.Err() == nil {
			p.logger.Warn("direct peer event projector retry", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Process 领取一批事件，按 durable 顺序投影，并仅在 sink 成功后确认。
func (p *EventProjector) Process(ctx context.Context, lease time.Duration, limit int) error {
	if p == nil || p.store == nil || p.sink == nil {
		return errors.New("direct peer event projector is not configured")
	}
	if strings.TrimSpace(p.workerID) == "" {
		return errors.New("direct peer event projector worker id is required")
	}
	items, err := p.store.ClaimDirectPeerEventOutbox(ctx, p.workerID, lease, limit)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := p.sink(ctx, item.Event); err != nil {
			return fmt.Errorf("project direct peer event %s: %w", item.Event.EventID, err)
		}
		if err := p.store.MarkDirectPeerEventOutboxProjected(ctx, item.ID, p.workerID, item.ClaimToken); err != nil {
			return err
		}
	}
	return nil
}
