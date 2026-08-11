package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestProductionLocalHostAdmissionSamplerDoesNotExecuteAtConstruction(t *testing.T) {
	sampler := newProductionLocalHostAdmissionSampler()
	if sampler == nil {
		t.Fatal("production local host sampler is nil")
	}
}

func TestLocalHostAdmissionSamplerConstructionDoesNotCallRunner(t *testing.T) {
	runner, dependencies := newLocalHostSamplerTestDependencies()
	sampler := newLocalHostAdmissionSampler(dependencies)
	if sampler == nil {
		t.Fatal("sampler is nil")
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls during callback construction = %d, want 0", runner.calls)
	}
}

func TestLocalHostAdmissionSamplerDarwinBoundaryAndCapacity(t *testing.T) {
	tests := []struct {
		name        string
		busy        float64
		freePercent float64
		wantAllowed bool
		wantCPU     float64
		wantMemory  float64
	}{
		{
			name:        "70 percent busy is admitted",
			busy:        70,
			freePercent: 25,
			wantAllowed: true,
			wantCPU:     2.4,
			wantMemory:  4,
		},
		{
			name:        "above 70 percent busy is ineligible",
			busy:        70.1,
			freePercent: 50,
			wantAllowed: false,
			wantCPU:     2.392,
			wantMemory:  8,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runner := &localHostSamplerFakeRunner{busy: tc.busy, freePercent: tc.freePercent}
			clock := newLocalHostSamplerFakeClock(time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC))
			sampler := newLocalHostAdmissionSampler(localHostAdmissionSamplerDependencies{
				platform:    "darwin",
				run:         runner.run,
				now:         clock.Now,
				sleep:       clock.Sleep,
				withTimeout: clock.WithTimeout,
			})

			admission, err := sampler(context.Background())
			if err != nil {
				t.Fatalf("sample host: %v", err)
			}
			if admission.Allowed != tc.wantAllowed {
				t.Fatalf("allowed = %v, want %v", admission.Allowed, tc.wantAllowed)
			}
			if math.Abs(admission.AvailableCPU-tc.wantCPU) > 0.000001 {
				t.Fatalf("available CPU = %v, want %v", admission.AvailableCPU, tc.wantCPU)
			}
			if math.Abs(admission.AvailableMemoryGiB-tc.wantMemory) > 0.000001 {
				t.Fatalf("available memory = %v, want %v", admission.AvailableMemoryGiB, tc.wantMemory)
			}
			if admission.CPUSampleCount != localHostSampleCount {
				t.Fatalf("CPU sample count = %d, want %d", admission.CPUSampleCount, localHostSampleCount)
			}
			if got := admission.CPUWindowEnd.Sub(admission.CPUWindowStart); got != localHostSampleInterval*time.Duration(localHostSampleCount-1) {
				t.Fatalf("CPU window = %s, want 30s", got)
			}
			if runner.topCalls != localHostSampleCount {
				t.Fatalf("top calls = %d, want %d", runner.topCalls, localHostSampleCount)
			}
			if len(clock.sleeps) != localHostSampleCount-1 {
				t.Fatalf("sleep calls = %d, want %d", len(clock.sleeps), localHostSampleCount-1)
			}
			assertLocalHostSamplerTimeouts(t, clock.timeouts)
		})
	}
}

func TestLocalHostAdmissionSamplerRejectsInsufficientSamples(t *testing.T) {
	_, err := sampleLocalHostAdmission(context.Background(), newLocalHostAdmissionSamplerDependencies(), localHostSampleCount-1)
	assertLocalHostSamplerError(t, err, "insufficient samples")
}

func TestLocalHostAdmissionSamplerRejectsCommandFailure(t *testing.T) {
	runner, sampler := newLocalHostSamplerTestDependencies()
	runner.err = errors.New("sysctl failed")
	_, err := newLocalHostAdmissionSampler(sampler)(context.Background())
	assertLocalHostSamplerError(t, err, "sysctl failed")
}

func TestLocalHostAdmissionSamplerRejectsUnexpectedOutput(t *testing.T) {
	runner, sampler := newLocalHostSamplerTestDependencies()
	runner.topOutput = "CPU: not a parsable Darwin snapshot\n"
	_, err := newLocalHostAdmissionSampler(sampler)(context.Background())
	assertLocalHostSamplerError(t, err, "CPU output")
}

func TestLocalHostAdmissionSamplerRejectsUnexpectedMemoryPressureOutput(t *testing.T) {
	runner, sampler := newLocalHostSamplerTestDependencies()
	runner.memoryPressureOutput = "System-wide memory free percentage: unknown\n"
	_, err := newLocalHostAdmissionSampler(sampler)(context.Background())
	assertLocalHostSamplerError(t, err, "memory_pressure output")
}

func TestLocalHostAdmissionSamplerAcceptsDarwinTopRoundedPercentages(t *testing.T) {
	runner, sampler := newLocalHostSamplerTestDependencies()
	runner.topOutput = "CPU usage: 33.33% user, 33.33% sys, 33.33% idle\n"
	admission, err := newLocalHostAdmissionSampler(sampler)(context.Background())
	if err != nil {
		t.Fatalf("sample host rounded top output: %v", err)
	}
	if math.Abs(admission.CPUBusyAveragePercent-66.66) > 0.000001 {
		t.Fatalf("CPU busy average = %v, want 66.66", admission.CPUBusyAveragePercent)
	}
}

func TestLocalHostAdmissionSamplerRejectsCancelledContext(t *testing.T) {
	runner, sampler := newLocalHostSamplerTestDependencies()
	ctx, cancel := context.WithCancel(context.Background())
	runner.cancelAfterCall = 1
	runner.cancel = cancel
	t.Cleanup(cancel)
	_, err := newLocalHostAdmissionSampler(sampler)(ctx)
	assertLocalHostSamplerError(t, err, context.Canceled.Error())
}

func TestLocalHostAdmissionSamplerTimeoutCancelsOnlyCommandContext(t *testing.T) {
	_, dependencies := newLocalHostSamplerTestDependencies()
	parent, cancelParent := context.WithCancel(context.Background())
	t.Cleanup(cancelParent)
	var cancelCommand context.CancelFunc
	dependencies.withTimeout = func(parent context.Context, duration time.Duration) (context.Context, context.CancelFunc) {
		if duration != localHostCommandTimeout {
			t.Fatalf("timeout duration = %s, want %s", duration, localHostCommandTimeout)
		}
		commandCtx, cancel := context.WithCancel(parent)
		cancelCommand = cancel
		return commandCtx, cancel
	}
	dependencies.run = func(ctx context.Context, command string, args ...string) (string, error) {
		if cancelCommand == nil {
			t.Fatal("command timeout cancel is nil")
		}
		cancelCommand()
		return "", ctx.Err()
	}
	_, err := runLocalHostCommand(parent, dependencies, localHostSysctlPath, "-n", "hw.logicalcpu")
	assertLocalHostSamplerError(t, err, context.Canceled.Error())
	if err := parent.Err(); err != nil {
		t.Fatalf("parent context was cancelled by command timeout: %v", err)
	}
}

func TestLocalHostAdmissionSamplerRejectsNonDarwin(t *testing.T) {
	_, sampler := newLocalHostSamplerTestDependencies()
	sampler.platform = "linux"
	_, err := newLocalHostAdmissionSampler(sampler)(context.Background())
	assertLocalHostSamplerError(t, err, "only supported on darwin")
}

func newLocalHostAdmissionSamplerDependencies() localHostAdmissionSamplerDependencies {
	_, sampler := newLocalHostSamplerTestDependencies()
	return sampler
}

func newLocalHostSamplerTestDependencies() (*localHostSamplerFakeRunner, localHostAdmissionSamplerDependencies) {
	runner := &localHostSamplerFakeRunner{busy: 10, freePercent: 50}
	clock := newLocalHostSamplerFakeClock(time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC))
	return runner, localHostAdmissionSamplerDependencies{
		platform:    "darwin",
		run:         runner.run,
		now:         clock.Now,
		sleep:       clock.Sleep,
		withTimeout: clock.WithTimeout,
	}
}

func assertLocalHostSamplerError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("sample error = %v, want containing %q", err, want)
	}
}

func assertLocalHostSamplerTimeouts(t *testing.T, timeouts []time.Duration) {
	t.Helper()
	if len(timeouts) != localHostSampleCount+3 {
		t.Fatalf("timeout contexts = %d, want %d", len(timeouts), localHostSampleCount+3)
	}
	for _, timeout := range timeouts {
		if timeout != localHostCommandTimeout {
			t.Fatalf("command timeout = %s, want %s", timeout, localHostCommandTimeout)
		}
	}
}

type localHostSamplerFakeRunner struct {
	busy                 float64
	freePercent          float64
	topOutput            string
	memoryPressureOutput string
	err                  error
	cancelAfterCall      int
	calls                int
	topCalls             int
	cancel               context.CancelFunc
	platform             string
}

func (runner *localHostSamplerFakeRunner) run(ctx context.Context, command string, args ...string) (string, error) {
	runner.calls++
	if runner.cancel != nil && runner.calls == runner.cancelAfterCall {
		runner.cancel()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if runner.err != nil {
		return "", runner.err
	}
	return runner.output(command, args)
}

func (runner *localHostSamplerFakeRunner) output(command string, args []string) (string, error) {
	switch command {
	case localHostSysctlPath:
		return runner.sysctlOutput(args)
	case localHostMemoryPressurePath:
		if runner.memoryPressureOutput != "" {
			return runner.memoryPressureOutput, nil
		}
		return fmt.Sprintf("System-wide memory free percentage: %.3f%%\n", runner.freePercent), nil
	case localHostTopPath:
		runner.topCalls++
		if runner.topOutput != "" {
			return runner.topOutput, nil
		}
		return fmt.Sprintf("CPU usage: %.3f%% user, 0.000%% sys, %.3f%% idle\n", runner.busy, 100-runner.busy), nil
	}
	return "", fmt.Errorf("unexpected command %s %s", command, strings.Join(args, " "))
}

func (runner *localHostSamplerFakeRunner) sysctlOutput(args []string) (string, error) {
	switch strings.Join(args, " ") {
	case "-n hw.logicalcpu":
		return "8\n", nil
	case "-n hw.memsize":
		return fmt.Sprintf("%d\n", uint64(16)*1024*1024*1024), nil
	default:
		return "", fmt.Errorf("unexpected sysctl arguments %s", strings.Join(args, " "))
	}
}

type localHostSamplerFakeClock struct {
	now      time.Time
	sleeps   []time.Duration
	timeouts []time.Duration
}

func newLocalHostSamplerFakeClock(now time.Time) *localHostSamplerFakeClock {
	return &localHostSamplerFakeClock{now: now}
}

func (clock *localHostSamplerFakeClock) Now() time.Time { return clock.now }

func (clock *localHostSamplerFakeClock) Sleep(ctx context.Context, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.sleeps = append(clock.sleeps, duration)
	clock.now = clock.now.Add(duration)
	return nil
}

func (clock *localHostSamplerFakeClock) WithTimeout(ctx context.Context, duration time.Duration) (context.Context, context.CancelFunc) {
	clock.timeouts = append(clock.timeouts, duration)
	return context.WithCancel(ctx)
}
