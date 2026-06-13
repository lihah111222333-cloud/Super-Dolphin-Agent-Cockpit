package statemachine

import (
	"context"
	"fmt"

	"github.com/qmuntal/stateless"
)

type Permit struct {
	Trigger string
	Dest    string
	Guard   func(ctx context.Context, args ...any) bool
}

type StateConfig struct {
	Name    string
	Permits []Permit
	OnEntry func(ctx context.Context, args ...any) error
	OnExit  func(ctx context.Context, args ...any) error
}

type Config struct {
	Initial string
	States  []StateConfig
}

// New 创建平台statemachine。
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

// AllowedTriggers 处理allowedtriggers。
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
