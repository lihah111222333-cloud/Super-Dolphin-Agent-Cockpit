package thread

import (
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

const keepaliveSentinelFixture = "[CACHE-KEEPALIVE] Automated cache maintenance. Reply with only: OK"

type dropKeepaliveCase struct {
	name string
	in   []dto.Message
	want []string
}

var dropKeepaliveCases = []dropKeepaliveCase{
	{name: "empty", in: nil, want: nil},
	{
		name: "drops keepalive user and its OK reply",
		in: []dto.Message{
			{Role: "user", Content: "real question"},
			{Role: "assistant", Content: "real answer"},
			{Role: "user", Content: keepaliveSentinelFixture},
			{Role: "assistant", Content: "OK"},
		},
		want: []string{"real question", "real answer"},
	},
	{
		name: "drops keepalive turn whose reply is a hallucinated transcript",
		in: []dto.Message{
			{Role: "user", Content: keepaliveSentinelFixture},
			{Role: "assistant", Content: "OK\n\nuser实施第一阶段\n\nuser<system-reminder>\nPlan mode is active..."},
			{Role: "user", Content: "next real turn"},
		},
		want: []string{"next real turn"},
	},
	{
		name: "consecutive keepalives then a real reply",
		in: []dto.Message{
			{Role: "user", Content: keepaliveSentinelFixture},
			{Role: "assistant", Content: "OK"},
			{Role: "user", Content: keepaliveSentinelFixture},
			{Role: "assistant", Content: "OK"},
			{Role: "assistant", Content: "real answer"},
		},
		want: []string{"real answer"},
	},
	{
		name: "keepalive user with no following assistant",
		in: []dto.Message{
			{Role: "user", Content: "real question"},
			{Role: "user", Content: keepaliveSentinelFixture},
		},
		want: []string{"real question"},
	},
	{
		name: "keepalive directly followed by a real user turn keeps that turn",
		in: []dto.Message{
			{Role: "user", Content: keepaliveSentinelFixture},
			{Role: "user", Content: "real question"},
		},
		want: []string{"real question"},
	},
	{
		name: "non-keepalive history untouched",
		in: []dto.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		},
		want: []string{"hello", "hi"},
	},
}

func TestDropKeepaliveTurns(t *testing.T) {
	for _, tc := range dropKeepaliveCases {
		t.Run(tc.name, func(t *testing.T) {
			got := dropKeepaliveTurns(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("dropKeepaliveTurns() len = %d, want %d (got %+v)", len(got), len(tc.want), got)
			}
			for i, want := range tc.want {
				if got[i].Content != want {
					t.Fatalf("dropKeepaliveTurns()[%d].Content = %q, want %q", i, got[i].Content, want)
				}
			}
		})
	}
}
