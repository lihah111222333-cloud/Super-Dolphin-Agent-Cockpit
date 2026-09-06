package main

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"go.uber.org/fx"
)

// canonicalOwnerModules 在 standalone 与 legacy 图共享唯一 identity/event/wait owner。
func canonicalOwnerModules() fx.Option {
	return fx.Options(
		identity.Module,
		eventstore.Module,
		accesswait.Module,
		orchestrationcommand.Module,
		taskIdentityEventOwnerModule(),
		fx.Provide(provideAccessWaitIdentityReader, provideAccessWaitEventReader, provideAccessWaitEventStatusReader, provideDisabledRoomCapabilityGate),
	)
}

// provideAccessWaitIdentityReader 将 identity owner 收窄为 wait admission 只读端口。
func provideAccessWaitIdentityReader(store *identity.Store) accesswait.IdentityReader { return store }

// provideAccessWaitEventReader 将 canonical event owner 收窄为 wait 只读端口。
func provideAccessWaitEventReader(store *eventstore.Store) accesswait.EventReader { return store }

// provideAccessWaitEventStatusReader 显式暴露 source gap/quarantine 状态读取窄口。
func provideAccessWaitEventStatusReader(store *eventstore.Store) accesswait.EventSourceStatusReader {
	return store
}

// provideDisabledRoomCapabilityGate 在 ROOM 祖先未批准前提供 concrete fail-closed gate。
func provideDisabledRoomCapabilityGate() contract.RoomCapabilityGate {
	return contract.DisabledRoomCapabilityGate{}
}
