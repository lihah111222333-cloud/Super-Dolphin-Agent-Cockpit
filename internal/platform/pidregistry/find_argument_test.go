package pidregistry

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestMatchProcessByArgumentFencesIdentityAroundArgv(t *testing.T) {
	argument := "--exact-token"
	executable := "/Applications/Test.app/Contents/MacOS/test"
	tests := []struct {
		name   string
		before StableProcessIdentity
		after  StableProcessIdentity
	}{
		{
			name:   "start token changes",
			before: StableProcessIdentity{PID: 42, ProcessStartToken: "generation-1", ExecutableIdentity: executable},
			after:  StableProcessIdentity{PID: 42, ProcessStartToken: "generation-2", ExecutableIdentity: executable},
		},
		{
			name:   "executable changes",
			before: StableProcessIdentity{PID: 42, ProcessStartToken: "generation-1", ExecutableIdentity: executable},
			after:  StableProcessIdentity{PID: 42, ProcessStartToken: "generation-1", ExecutableIdentity: "/tmp/replacement"},
		},
		{
			name:   "pid changes",
			before: StableProcessIdentity{PID: 42, ProcessStartToken: "generation-1", ExecutableIdentity: executable},
			after:  StableProcessIdentity{PID: 43, ProcessStartToken: "generation-1", ExecutableIdentity: executable},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captures := 0
			identity, found, err := matchProcessByArgumentWithOps(
				context.Background(), 42, argument, executable,
				func(int) (StableProcessIdentity, error) {
					captures++
					if captures == 1 {
						return tt.before, nil
					}
					return tt.after, nil
				},
				func(int) ([]string, bool, error) { return []string{argument}, false, nil },
			)
			if err != nil || found || identity != (StableProcessIdentity{}) {
				t.Fatalf("match = (%+v, %t, %v), want no match", identity, found, err)
			}
		})
	}
}

func TestProcessGoneAfterArgumentReadClassifiesOnlyVanishedPID(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessGoneAfterArgumentReadHelper$")
	cmd.Env = append(os.Environ(), "SUPER_DOLPHIN_ARGUMENT_GONE_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	childPID := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	readErr := errors.New("argument read failed")
	gone, err := processGoneAfterArgumentRead(childPID, readErr)
	if err != nil || !gone {
		t.Fatalf("reaped child classification = gone %t, error %v", gone, err)
	}
	gone, err = processGoneAfterArgumentRead(os.Getpid(), readErr)
	if err != nil || gone {
		t.Fatalf("live process classification = gone %t, error %v", gone, err)
	}
}

func TestProcessGoneAfterArgumentReadHelper(t *testing.T) {
	if os.Getenv("SUPER_DOLPHIN_ARGUMENT_GONE_HELPER") != "1" {
		return
	}
}
