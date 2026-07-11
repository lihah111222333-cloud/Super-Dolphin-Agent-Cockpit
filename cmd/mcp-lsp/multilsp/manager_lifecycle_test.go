package multilsp

import (
	"context"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

func TestCreateAndRegisterClientShutsDownDiscardedClientOutsideLockWithTimeout(t *testing.T) {
	t.Parallel()

	existing := noopClient{}
	probe := make(chan bool, 1)
	deadline := make(chan bool, 1)
	m := &manager{
		workspaces: map[string]*workspaceClient{
			"repo:go": {key: "repo:go", client: existing},
		},
	}
	discarded := &lockProbeShutdownClient{
		manager:       m,
		lockAvailable: probe,
		hasDeadline:   deadline,
	}
	m.factory = ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
		return discarded, nil
	})

	got, err := m.createAndRegisterClient(context.Background(), workspaceConfig{
		key:        "repo:go",
		rootPath:   t.TempDir(),
		rootURI:    "file:///repo",
		languageID: "go",
	})
	if err != nil {
		t.Fatalf("createAndRegisterClient() error = %v", err)
	}
	if got != existing {
		t.Fatalf("createAndRegisterClient() returned %#v, want existing client", got)
	}
	if !<-probe {
		t.Fatal("discarded client Shutdown ran while manager lock was still held")
	}
	if !<-deadline {
		t.Fatal("discarded client Shutdown did not receive manager shutdown deadline")
	}
	if discarded.closed != 1 {
		t.Fatalf("discarded Close calls = %d, want 1", discarded.closed)
	}
}

type lockProbeShutdownClient struct {
	noopClient
	manager       *manager
	lockAvailable chan<- bool
	hasDeadline   chan<- bool
	closed        int
}

func (c *lockProbeShutdownClient) Shutdown(ctx context.Context) error {
	lockDone := make(chan struct{})
	safego.Go(ctx, nil, "multilsp.lockProbeShutdownClient.lockProbe", func(context.Context) {
		c.manager.mu.Lock()
		c.manager.mu.Unlock()
		close(lockDone)
	})
	select {
	case <-lockDone:
		c.lockAvailable <- true
	case <-time.After(200 * time.Millisecond):
		c.lockAvailable <- false
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= managerShutdownTimeout && time.Until(deadline) > 0 {
		c.hasDeadline <- true
	} else {
		c.hasDeadline <- false
	}
	return nil
}

func (c *lockProbeShutdownClient) Close() error {
	c.closed++
	return nil
}
