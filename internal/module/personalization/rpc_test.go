package personalization

import (
	"context"
	"encoding/json"
	"testing"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestProfileRPCGetAndSave(t *testing.T) {
	server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewHandlers(NewService(newProfilePreferenceStore())).Handlers)

	var saved ProfileResult
	raw, err := server.Dispatch(context.Background(), "personalization/profile/save", json.RawMessage(`{
		"cwd":"/repo/app",
		"profile":{
			"displayName":" 小海 ",
			"role":"后端工程师",
			"background":"熟悉 Go",
			"customInstructions":"回答要直接"
		}
	}`))
	if err != nil {
		t.Fatalf("save RPC error = %v", err)
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("decode save RPC response: %v", err)
	}
	if saved.Profile.DisplayName != "小海" {
		t.Fatalf("saved profile = %#v, want trimmed displayName", saved.Profile)
	}

	var got ProfileResult
	raw, err = server.Dispatch(context.Background(), "personalization/profile/get", json.RawMessage(`{"cwd":"/repo/app"}`))
	if err != nil {
		t.Fatalf("get RPC error = %v", err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode get RPC response: %v", err)
	}
	if got.Profile != saved.Profile {
		t.Fatalf("get RPC = %#v, want %#v", got.Profile, saved.Profile)
	}
}
