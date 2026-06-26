package statemachine

import (
	"context"
	"fmt"

	"github.com/qmuntal/stateless"
)

// Permit 声明一个 trigger 可到达的目标状态；Guard 失败时 stateless 会拒绝本次触发。
type Permit struct {
	Trigger string
	Dest    string
	Guard   func(ctx context.Context, args ...any) bool
}

// StateConfig 汇总单个状态的转移和回调，回调错误会沿 stateless 触发链路返回。
type StateConfig struct {
	Name    string
	Permits []Permit
	OnEntry func(ctx context.Context, args ...any) error
	OnExit  func(ctx context.Context, args ...any) error
}

// Config 是状态机装配输入；Initial 为空或 States 缺失会交给 stateless 保持原错误行为。
type Config struct {
	Initial string
	States  []StateConfig
}

// New 创建使用外部 accessor/mutator 的 stateless 状态机。
// 两个存储函数必须成对提供；缺失时才退回进程内状态，避免半持久化导致状态漂移。
func New(cfg Config, accessor func() string, mutator func(string)) *stateless.StateMachine {
	if accessor == nil || mutator == nil {
		state := cfg.Initial
		accessor = func() string { return state }
		mutator = func(next string) { state = next }
	}

	sm := stateless.NewStateMachineWithExternalStorage(
		func(context.Context) (stateless.State, error) {
			return accessor(), nil
		},
		func(_ context.Context, state stateless.State) error {
			mutator(fmt.Sprint(state))
			return nil
		},
		stateless.FiringQueued,
	)

	for _, stateCfg := range cfg.States {
		cfgState := stateCfg
		conf := sm.Configure(cfgState.Name)
		for _, permit := range cfgState.Permits {
			current := permit
			if current.Guard != nil {
				conf.Permit(stateless.Trigger(current.Trigger), current.Dest, func(ctx context.Context, args ...any) bool {
					return current.Guard(ctx, args...)
				})
				continue
			}
			conf.Permit(stateless.Trigger(current.Trigger), current.Dest)
		}
		if cfgState.OnEntry != nil {
			conf.OnEntry(cfgState.OnEntry)
		}
		if cfgState.OnExit != nil {
			conf.OnExit(cfgState.OnExit)
		}
	}

	return sm
}

// AllowedTriggers 返回当前状态允许的 trigger 名称；底层计算失败时保留原错误上下文。
func AllowedTriggers(sm *stateless.StateMachine, ctx context.Context) ([]string, error) {
	triggers, err := sm.PermittedTriggersCtx(ctx)
	if err != nil {
		return nil, fmt.Errorf("statemachine: PermittedTriggersCtx failed: %w", err)
	}
	result := make([]string, 0, len(triggers))
	for _, trigger := range triggers {
		result = append(result, fmt.Sprint(trigger))
	}
	return result, nil
}
