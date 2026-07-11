package prompt

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kelindar/event"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/stretchr/testify/require"
)

func TestPromptsRPCWritePublishesUIPromptsChanged(t *testing.T) {
	t.Parallel()

	const promptKey = "main/scoped"
	store := newInMemoryPromptStore()
	template := scopedPromptTemplate(promptKey, "/repo/a")
	template.ID = 7
	store.templates[promptKey] = template
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()
	events := make(chan uidto.UIPromptsChanged, 1)
	cancel := event.Subscribe(dispatcher, func(ev uidto.UIPromptsChanged) { events <- ev })
	defer cancel()
	server := platformrpc.NewServer(platformrpc.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(buildPromptHandlersWithService(newPromptService(store), dispatcher).Handlers)

	_, err := server.Dispatch(context.Background(), "prompts/write", json.RawMessage(`{
		"id":"main/scoped",
		"name":"Scoped Prompt",
		"content":"updated by user",
		"cwd":"/repo/a"
	}`))
	require.NoError(t, err)

	ev := receiveUIPromptsChanged(t, events)
	require.Equal(t, "/repo/a", ev.Cwd)
	require.Equal(t, "main/scoped", ev.PromptKey)
	require.Equal(t, "write", ev.Action)
}

func receiveUIPromptsChanged(t *testing.T, ch <-chan uidto.UIPromptsChanged) uidto.UIPromptsChanged {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("expected UIPromptsChanged event")
		return uidto.UIPromptsChanged{}
	}
}
