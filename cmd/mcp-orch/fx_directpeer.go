package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/kelindar/event"
	directpeerstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
)

// newDirectPeerEventProjector 构造 legacy 与 standalone 图共用的 direct-peer 事件投影器。
// 每个 Fx 图只应把该 provider 注入一次，避免同一 outbox 被同进程重复消费。
func newDirectPeerEventProjector(store *directpeerstore.Store, dispatcher *event.Dispatcher, logger *slog.Logger) (*directpeerstore.EventProjector, error) {
	if dispatcher == nil {
		return nil, errors.New("direct peer event projector requires event dispatcher")
	}
	sink := func(ctx context.Context, item directpeerstore.Event) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		event.Publish(dispatcher, directpeerstore.PublishedEvent{Event: item})
		return nil
	}
	return directpeerstore.NewEventProjector(store, sink, logger)
}
