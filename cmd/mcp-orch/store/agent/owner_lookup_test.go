package agent

import (
	"context"
	"errors"
	"testing"
)

// TestLookupOwnerByThreadFailClosed 验证线程归属查询在正常、缺失和冲突场景下均按契约返回。
func TestLookupOwnerByThreadFailClosed(t *testing.T) {
	for _, test := range ownerLookupFailClosedCases() {
		t.Run(test.name, func(t *testing.T) {
			runOwnerLookupCase(t, test)
		})
	}
}

// ownerLookupFailClosedCases 返回线程归属查询的完整失败闭环用例集。
func ownerLookupFailClosedCases() []ownerLookupCase {
	return []ownerLookupCase{
		{name: "zero matches", threadID: "missing", wantErr: ErrTerminalOwnerNotFound},
		{
			name:     "archived only is not found",
			threadID: "archived-thread",
			bindings: []ownerLookupFixture{{agentID: "agent-archived", provider: "codex", providerThreadID: "archived-thread", archived: true}},
			wantErr:  ErrTerminalOwnerNotFound,
		},
		{
			name:      "provider thread single owner",
			threadID:  "provider-thread",
			bindings:  []ownerLookupFixture{{agentID: "agent-1", provider: "claude", providerThreadID: "provider-thread"}},
			wantOwner: "agent-1",
		},
		{
			name:      "codex thread single owner",
			threadID:  "codex-thread",
			bindings:  []ownerLookupFixture{{agentID: "agent-1", provider: "codex", codexThreadID: "codex-thread"}},
			wantOwner: "agent-1",
		},
		{
			name:      "codex binding has both identity columns",
			threadID:  "dual-identity-thread",
			bindings:  []ownerLookupFixture{{agentID: "agent-1", provider: "codex", providerThreadID: "dual-identity-thread", codexThreadID: "dual-identity-thread"}},
			wantOwner: "agent-1",
		},
		{
			name:     "multiple owners",
			threadID: "shared-thread",
			bindings: []ownerLookupFixture{
				{agentID: "agent-1", provider: "codex", providerThreadID: "provider-1", codexThreadID: "shared-thread"},
				{agentID: "agent-2", provider: "codex", providerThreadID: "provider-2", codexThreadID: "shared-thread"},
			},
			wantErr: ErrTerminalOwnerAmbiguous,
		},
		{
			name:     "cross provider collision",
			threadID: "provider-collision",
			bindings: []ownerLookupFixture{
				{agentID: "agent-1", provider: "codex", providerThreadID: "provider-collision"},
				{agentID: "agent-2", provider: "claude", providerThreadID: "provider-collision"},
			},
			wantErr: ErrTerminalOwnerAmbiguous,
		},
		{
			name:     "cross provider public-provider collision",
			threadID: "shared-provider-public",
			bindings: []ownerLookupFixture{
				{agentID: "agent-codex", provider: "codex", providerThreadID: "codex-native", codexThreadID: "shared-provider-public"},
				{agentID: "agent-claude", provider: "claude", providerThreadID: "shared-provider-public", codexThreadID: "claude-public"},
			},
			wantErr: ErrTerminalOwnerAmbiguous,
		},
		{
			name:     "active and archived owner collision",
			threadID: "active-archived-collision",
			bindings: []ownerLookupFixture{
				{agentID: "agent-active", provider: "codex", providerThreadID: "active-archived-collision"},
				{agentID: "agent-archived", provider: "codex", providerThreadID: "archived-provider", codexThreadID: "active-archived-collision", archived: true},
			},
			wantErr: ErrTerminalOwnerAmbiguous,
		},
		{
			name:     "unsupported provider is malformed",
			threadID: "unknown-provider-thread",
			bindings: []ownerLookupFixture{{agentID: "agent-1", provider: "other", providerThreadID: "unknown-provider-thread"}},
			wantErr:  ErrTerminalOwnerAmbiguous,
		},
		{
			name:     "agent placeholder provider thread is malformed",
			threadID: "placeholder-public",
			bindings: []ownerLookupFixture{{agentID: "agent-1", provider: "codex", providerThreadID: "agent-1", codexThreadID: "placeholder-public"}},
			wantErr:  ErrTerminalOwnerAmbiguous,
		},
	}
}

// runOwnerLookupCase 写入绑定夹具并断言线程归属查询结果。
func runOwnerLookupCase(t *testing.T, test ownerLookupCase) {
	t.Helper()
	db := sessionTestDatabase(t)
	for _, binding := range test.bindings {
		_, err := db.ExecContext(context.Background(), `INSERT INTO agent_provider_binding
			(agent_id, provider, provider_thread_id, codex_thread_id, cwd, archived, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, binding.agentID, binding.provider,
			binding.providerThreadID, binding.codexThreadID, "/tmp", binding.archived, 1, 1)
		if err != nil {
			t.Fatalf("insert binding %q: %v", binding.agentID, err)
		}
	}
	owner, err := (&threadStore{db: db}).LookupOwnerByThread(context.Background(), test.threadID)
	if test.wantErr != nil {
		if !errors.Is(err, test.wantErr) {
			t.Fatalf("LookupOwnerByThread() err = %v, want %v", err, test.wantErr)
		}
		if owner != "" {
			t.Fatalf("LookupOwnerByThread() owner = %q on failure, want empty", owner)
		}
		return
	}
	if err != nil || owner != test.wantOwner {
		t.Fatalf("LookupOwnerByThread() = (%q, %v), want (%q, nil)", owner, err, test.wantOwner)
	}
}

// TestLookupOwnerByThreadRejectsMissingIdentity 验证未配置数据库或空线程标识时安全拒绝查询。
func TestLookupOwnerByThreadRejectsMissingIdentity(t *testing.T) {
	store := &threadStore{}
	owner, err := store.LookupOwnerByThread(context.Background(), "thread")
	if !errors.Is(err, ErrTerminalOwnerNotFound) || owner != "" {
		t.Fatalf("LookupOwnerByThread(unconfigured) = (%q, %v), want not-found and empty owner", owner, err)
	}
	owner, err = store.LookupOwnerByThread(context.Background(), " ")
	if !errors.Is(err, ErrTerminalOwnerNotFound) || owner != "" {
		t.Fatalf("LookupOwnerByThread(blank) = (%q, %v), want not-found and empty owner", owner, err)
	}
}

type ownerLookupCase struct {
	name      string
	threadID  string
	bindings  []ownerLookupFixture
	wantOwner string
	wantErr   error
}

type ownerLookupFixture struct {
	agentID          string
	provider         string
	providerThreadID string
	codexThreadID    string
	archived         bool
}
