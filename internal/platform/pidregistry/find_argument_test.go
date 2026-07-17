package pidregistry

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

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
