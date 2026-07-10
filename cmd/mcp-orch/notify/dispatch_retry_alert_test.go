package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformnotify "github.com/anthropic-ai/super-agent-v3/internal/platform/notify"
)

func TestDispatchRetryAlertNotifierEnqueuesWebhookAlias(t *testing.T) {
	rec := &recordingMessageNotifier{}
	nodeConfig, _ := json.Marshal(map[string]any{"notify_channel": "ops.dag"})
	store := &fakeStore{
		listNodesFn: func(context.Context, string) ([]taskdag.Node, error) {
			return []taskdag.Node{{NodeKey: "node-hot", Config: nodeConfig}}, nil
		},
		getDAGFn: func(context.Context, string) (*taskdag.DAG, error) {
			return &taskdag.DAG{DagKey: "dag-alert"}, nil
		},
	}
	sink := NewDispatchRetryAlertNotifier(slog.Default(), rec, store)

	err := sink.AlertDispatchRetry(context.Background(), orchestration.DispatchRetryAlert{
		DagKey:       "dag-alert",
		NodeKey:      "node-hot",
		AttemptCount: 3,
		LastError:    "connection refused",
	})
	if err != nil {
		t.Fatalf("AlertDispatchRetry err = %v", err)
	}
	if rec.len() != 1 {
		t.Fatalf("enqueued alerts = %d, want 1", rec.len())
	}
	req := rec.reqs[0]
	if req.ChannelAlias != "ops.dag" {
		t.Fatalf("ChannelAlias = %q, want ops.dag", req.ChannelAlias)
	}
	if req.Message.Level != contract.NotifyLevelWarn {
		t.Fatalf("Level = %q, want warn", req.Message.Level)
	}
	if !strings.Contains(req.Message.Body, "dag-alert") || !strings.Contains(req.Message.Body, "node-hot") || !strings.Contains(req.Message.Body, "3") {
		t.Fatalf("alert body missing retry context:\n%s", req.Message.Body)
	}
}

func TestDispatchRetryAlertNotifierDropsWithoutAlias(t *testing.T) {
	rec := &recordingMessageNotifier{}
	store := &fakeStore{
		listNodesFn: func(context.Context, string) ([]taskdag.Node, error) {
			return []taskdag.Node{{NodeKey: "node-cold"}}, nil
		},
		getDAGFn: func(context.Context, string) (*taskdag.DAG, error) {
			return &taskdag.DAG{DagKey: "dag-alert"}, nil
		},
	}
	sink := NewDispatchRetryAlertNotifier(slog.Default(), rec, store)

	err := sink.AlertDispatchRetry(context.Background(), orchestration.DispatchRetryAlert{
		DagKey:       "dag-alert",
		NodeKey:      "node-cold",
		AttemptCount: 3,
	})
	if err != nil {
		t.Fatalf("AlertDispatchRetry err = %v", err)
	}
	if rec.len() != 0 {
		t.Fatalf("enqueued alerts = %d, want 0 without notify_channel", rec.len())
	}
}

func TestDispatchRetryAlertNotifierWebhookCapture(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resolver, err := platformnotify.ParseChannelsJSON(fmt.Sprintf(
		`{"ops.dag":{"platform":"slack","url":%q}}`,
		srv.URL,
	))
	if err != nil {
		t.Fatalf("ParseChannelsJSON: %v", err)
	}
	notifier := platformnotify.NewNotifier(slog.Default(), resolver, 4)
	client := platformnotify.NewWebhookClient(platformnotify.WebhookClientConfig{
		Timeout:          2 * time.Second,
		AllowPrivateCIDR: true,
	})
	client.HTTPClient().Transport.(*http.Transport).TLSClientConfig =
		srv.Client().Transport.(*http.Transport).TLSClientConfig
	flusher := platformnotify.NewFlusher(slog.Default(), notifier, client, 100*time.Millisecond)

	nodeConfig, _ := json.Marshal(map[string]any{"notify_channel": "ops.dag"})
	store := &fakeStore{
		listNodesFn: func(context.Context, string) ([]taskdag.Node, error) {
			return []taskdag.Node{{NodeKey: "node-hot", Config: nodeConfig}}, nil
		},
		getDAGFn: func(context.Context, string) (*taskdag.DAG, error) {
			return &taskdag.DAG{DagKey: "dag-alert"}, nil
		},
	}
	sink := NewDispatchRetryAlertNotifier(slog.Default(), notifier, store)
	if err := sink.AlertDispatchRetry(context.Background(), orchestration.DispatchRetryAlert{
		DagKey:       "dag-alert",
		NodeKey:      "node-hot",
		AttemptCount: 3,
		LastError:    "connection refused",
	}); err != nil {
		t.Fatalf("AlertDispatchRetry err = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() { done <- flusher.Run(ctx) })
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(bodies)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("flusher.Run err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf("webhook bodies = %d, want 1", len(bodies))
	}
	if !strings.Contains(bodies[0], "dag\\\\-alert") || !strings.Contains(bodies[0], "node\\\\-hot") {
		t.Fatalf("webhook body missing retry alert context:\n%s", bodies[0])
	}
}

func TestDispatchRetryAlertBodyFormatsRetryCountAsInt64(t *testing.T) {
	source, err := os.ReadFile("dispatch_retry_alert.go")
	if err != nil {
		t.Fatalf("read dispatch retry alert source: %v", err)
	}
	if strings.Contains(string(source), "strconv.Itoa(int(alert.RetryCount))") {
		t.Fatal("RetryCount must not narrow int64 to int before formatting")
	}
}

func TestBuildDispatchRetryAlertBodyPreservesMaxRetryCount(t *testing.T) {
	body := buildDispatchRetryAlertBody(orchestration.DispatchRetryAlert{
		DagKey:     "dag-alert",
		NodeKey:    "node-hot",
		RetryCount: math.MaxInt64,
	}, nil, nil)
	if !strings.Contains(body, "Process retry count: 9223372036854775807") {
		t.Fatalf("retry count was not preserved in alert body:\n%s", body)
	}
}
