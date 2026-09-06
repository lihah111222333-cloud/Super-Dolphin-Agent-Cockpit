package main

import (
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskeventsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskprincipal"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/taskcredential"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"go.uber.org/fx"
)

// taskIdentityEventOwnerModule 只构造并收窄 task identity/event producer。
//
// 这些端口是 future task consumers 的候选依赖；本模块不注册 unified_v2
// tools、Room 能力或 provider callback，也不把 operator principal 转成 task
// principal。具体 store 仍由 canonicalOwnerModules 中的唯一 owner 提供。
func taskIdentityEventOwnerModule() fx.Option {
	return fx.Provide(
		provideTaskPrincipalIdentityReader,
		fx.Annotate(provideTaskPrincipalResolver, fx.As(new(taskcredential.CurrentTaskPrincipalResolver))),
		provideTaskEventAppender,
		fx.Annotate(provideTaskEventProducer, fx.As(new(taskeventsource.TaskEventProducer))),
	)
}

// provideTaskPrincipalIdentityReader 将唯一 identity store 收窄为 resolver 的只读链端口。
func provideTaskPrincipalIdentityReader(store *identity.Store) (taskprincipal.IdentityChainReader, error) {
	if store == nil {
		return nil, errors.New("task principal identity reader requires identity store")
	}
	return store, nil
}

// provideTaskPrincipalResolver 构造 fail-closed current task principal resolver。
func provideTaskPrincipalResolver(reader taskprincipal.IdentityChainReader) (*taskprincipal.Resolver, error) {
	return taskprincipal.New(reader)
}

// provideTaskEventAppender 将唯一 canonical event store 收窄为 producer 的 append 端口。
func provideTaskEventAppender(store *eventstore.Store) (taskeventsource.Appender, error) {
	if store == nil {
		return nil, errors.New("task event appender requires event store")
	}
	return store, nil
}

// provideTaskEventProducer 构造不自动写入事件的 bounded producer candidate。
func provideTaskEventProducer(appender taskeventsource.Appender) (*taskeventsource.Producer, error) {
	return taskeventsource.NewProducer(appender)
}
