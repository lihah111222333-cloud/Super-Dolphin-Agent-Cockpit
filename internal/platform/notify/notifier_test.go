package notify

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// testResolver implements Resolver for tests without touching
// the JSON parser.
type testResolver struct {
	known map[string]ChannelConfig
}

func (r *testResolver) Resolve(alias string) (ChannelConfig, error) {
	if r == nil {
		return ChannelConfig{}, ErrAliasNotFound
	}
	c, ok := r.known[alias]
	if !ok {
		return ChannelConfig{}, ErrAliasNotFound
	}
	return c, nil
}

func newResolver(aliases ...string) Resolver {
	known := map[string]ChannelConfig{}
	for _, a := range aliases {
		known[a] = ChannelConfig{
			Platform: PlatformSlack,
			URL:      "https://hooks.slack.com/services/" + a,
		}
	}
	return &testResolver{known: known}
}

func TestTryEnqueueRejectsEmptyAlias(t *testing.T) {
	t.Parallel()
	n := NewNotifier(slog.Default(), newResolver("slack.default"), 4)
	err := n.TryEnqueue(context.Background(), contract.NotifyRequest{ChannelAlias: " "})
	if !errors.Is(err, contract.ErrNotifyAliasNotFound) {
		t.Fatalf("want ErrNotifyAliasNotFound for empty alias, got %v", err)
	}
}

func TestTryEnqueueRejectsUnknownAlias(t *testing.T) {
	t.Parallel()
	n := NewNotifier(slog.Default(), newResolver("slack.default"), 4)
	err := n.TryEnqueue(context.Background(), contract.NotifyRequest{ChannelAlias: "not-here"})
	if !errors.Is(err, contract.ErrNotifyAliasNotFound) {
		t.Fatalf("want ErrNotifyAliasNotFound, got %v", err)
	}
}

func TestTryEnqueueRejectsOnFullQueue(t *testing.T) {
	t.Parallel()
	n := NewNotifier(slog.Default(), newResolver("slack.default"), 1)
	req := contract.NotifyRequest{ChannelAlias: "slack.default"}
	if err := n.TryEnqueue(context.Background(), req); err != nil {
		t.Fatalf("first enqueue must succeed: %v", err)
	}
	err := n.TryEnqueue(context.Background(), req)
	if !errors.Is(err, contract.ErrNotifyQueueFull) {
		t.Fatalf("second enqueue must be ErrNotifyQueueFull, got %v", err)
	}
	if n.Dropped() != 1 {
		t.Fatalf("Dropped = %d, want 1", n.Dropped())
	}
}

func TestTryEnqueueTrimsAliasBeforeLookup(t *testing.T) {
	t.Parallel()
	n := NewNotifier(slog.Default(), newResolver("slack.default"), 4)
	err := n.TryEnqueue(context.Background(), contract.NotifyRequest{ChannelAlias: "  slack.default  "})
	if err != nil {
		t.Fatalf("trim should allow the alias to resolve: %v", err)
	}
	// The queued request must carry the trimmed alias so the flusher
	// looks it up by the same string.
	select {
	case got := <-n.queueForFlusher():
		if got.ChannelAlias != "slack.default" {
			t.Fatalf("queued alias = %q, want trimmed", got.ChannelAlias)
		}
	default:
		t.Fatal("queue should have exactly one entry")
	}
}

func TestNewNotifierAcceptsNilResolver(t *testing.T) {
	t.Parallel()
	n := NewNotifier(slog.Default(), nil, 4)
	err := n.TryEnqueue(context.Background(), contract.NotifyRequest{ChannelAlias: "anything"})
	if !errors.Is(err, contract.ErrNotifyAliasNotFound) {
		t.Fatalf("nil resolver must surface ErrNotifyAliasNotFound, got %v", err)
	}
}
