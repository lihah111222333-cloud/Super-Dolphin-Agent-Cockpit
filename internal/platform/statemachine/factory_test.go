package statemachine

import (
	"context"
	"errors"
	"testing"
)

func TestNew_BasicTransition(t *testing.T) {
	t.Parallel()
	sm := New(Config{
		Initial: "idle",
		States: []StateConfig{
			{Name: "idle", Permits: []Permit{{Trigger: "start", Dest: "running"}}},
			{Name: "running", Permits: []Permit{{Trigger: "stop", Dest: "idle"}}},
		},
	}, nil, nil)

	state, err := sm.State(context.Background())
	if err != nil {
		t.Fatalf("State() error = %v", err)
	}
	if state != "idle" {
		t.Fatalf("initial state = %v, want idle", state)
	}

	if err := sm.Fire("start"); err != nil {
		t.Fatalf("Fire(start) error = %v", err)
	}
	state, _ = sm.State(context.Background())
	if state != "running" {
		t.Fatalf("state after start = %v, want running", state)
	}

	if err := sm.Fire("stop"); err != nil {
		t.Fatalf("Fire(stop) error = %v", err)
	}
	state, _ = sm.State(context.Background())
	if state != "idle" {
		t.Fatalf("state after stop = %v, want idle", state)
	}
}

func TestNew_InvalidTransitionErrors(t *testing.T) {
	t.Parallel()
	sm := New(Config{
		Initial: "idle",
		States: []StateConfig{
			{Name: "idle", Permits: []Permit{{Trigger: "start", Dest: "running"}}},
			{Name: "running"},
		},
	}, nil, nil)

	if err := sm.Fire("stop"); err == nil {
		t.Fatal("expected error for invalid trigger from idle")
	}
}

func TestNew_WithGuard(t *testing.T) {
	t.Parallel()
	allowed := false
	sm := New(Config{
		Initial: "idle",
		States: []StateConfig{
			{Name: "idle", Permits: []Permit{{
				Trigger: "start",
				Dest:    "running",
				Guard:   func(_ context.Context, _ ...any) bool { return allowed },
			}}},
			{Name: "running"},
		},
	}, nil, nil)

	// Guard blocks transition
	if err := sm.Fire("start"); err == nil {
		t.Fatal("expected error when guard returns false")
	}

	// Guard allows transition
	allowed = true
	if err := sm.Fire("start"); err != nil {
		t.Fatalf("Fire(start) with guard=true error = %v", err)
	}
	state, _ := sm.State(context.Background())
	if state != "running" {
		t.Fatalf("state = %v, want running", state)
	}
}

func TestNew_WithOnEntryOnExit(t *testing.T) {
	t.Parallel()
	var entryLog, exitLog []string
	sm := New(Config{
		Initial: "idle",
		States: []StateConfig{
			{
				Name:    "idle",
				Permits: []Permit{{Trigger: "start", Dest: "running"}},
				OnExit:  func(_ context.Context, _ ...any) error { exitLog = append(exitLog, "exit_idle"); return nil },
			},
			{
				Name:    "running",
				Permits: []Permit{{Trigger: "stop", Dest: "idle"}},
				OnEntry: func(_ context.Context, _ ...any) error { entryLog = append(entryLog, "enter_running"); return nil },
			},
		},
	}, nil, nil)

	if err := sm.Fire("start"); err != nil {
		t.Fatalf("Fire(start) error = %v", err)
	}
	if len(exitLog) != 1 || exitLog[0] != "exit_idle" {
		t.Fatalf("exitLog = %v, want [exit_idle]", exitLog)
	}
	if len(entryLog) != 1 || entryLog[0] != "enter_running" {
		t.Fatalf("entryLog = %v, want [enter_running]", entryLog)
	}
}

func TestNew_OnEntryError(t *testing.T) {
	t.Parallel()
	sm := New(Config{
		Initial: "idle",
		States: []StateConfig{
			{Name: "idle", Permits: []Permit{{Trigger: "start", Dest: "running"}}},
			{Name: "running", OnEntry: func(_ context.Context, _ ...any) error {
				return errors.New("entry failed")
			}},
		},
	}, nil, nil)

	if err := sm.Fire("start"); err == nil {
		t.Fatal("expected error from OnEntry failure")
	}
}

func TestNew_ExternalStorage(t *testing.T) {
	t.Parallel()
	externalState := "idle"
	sm := New(Config{
		Initial: "idle",
		States: []StateConfig{
			{Name: "idle", Permits: []Permit{{Trigger: "start", Dest: "running"}}},
			{Name: "running", Permits: []Permit{{Trigger: "stop", Dest: "idle"}}},
		},
	},
		func() string { return externalState },
		func(s string) { externalState = s },
	)

	if err := sm.Fire("start"); err != nil {
		t.Fatalf("Fire(start) error = %v", err)
	}
	if externalState != "running" {
		t.Fatalf("externalState = %q, want running", externalState)
	}
}

func TestAllowedTriggers(t *testing.T) {
	t.Parallel()
	sm := New(Config{
		Initial: "idle",
		States: []StateConfig{
			{Name: "idle", Permits: []Permit{
				{Trigger: "start", Dest: "running"},
				{Trigger: "shutdown", Dest: "stopped"},
			}},
			{Name: "running"},
			{Name: "stopped"},
		},
	}, nil, nil)

	triggers := AllowedTriggers(sm, context.Background())
	if len(triggers) != 2 {
		t.Fatalf("AllowedTriggers() = %v, want 2 triggers", triggers)
	}

	sm.Fire("start")
	triggers = AllowedTriggers(sm, context.Background())
	if len(triggers) != 0 {
		t.Fatalf("AllowedTriggers() from running = %v, want 0", triggers)
	}
}
