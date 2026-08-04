//go:build windows

package hiddenexec

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestProcessTreeProcessGoneDoesNotTreatSuccessAsGone(t *testing.T) {
	if processTreeProcessGone(nil) {
		t.Fatal("processTreeProcessGone(nil) = true, want false")
	}
	for _, err := range []error{
		os.ErrProcessDone,
		windows.ERROR_INVALID_PARAMETER,
		windows.ERROR_NOT_FOUND,
	} {
		if !processTreeProcessGone(err) {
			t.Fatalf("processTreeProcessGone(%v) = false, want true", err)
		}
	}
}

func TestTerminateJobProcessTreeTreatsSuccessfulCallAsSuccess(t *testing.T) {
	called := false
	err := terminateJobProcessTreeWith(windows.Handle(17), func(job windows.Handle, exitCode uint32) error {
		called = true
		if job != windows.Handle(17) || exitCode != 1 {
			t.Fatalf("terminate callback args = (%d, %d), want (17, 1)", job, exitCode)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("terminateJobProcessTreeWith(success) error = %v", err)
	}
	if !called {
		t.Fatal("terminate callback was not called")
	}
}

func TestTerminateJobProcessTreeTreatsGoneCallAsSuccess(t *testing.T) {
	err := terminateJobProcessTreeWith(windows.Handle(17), func(windows.Handle, uint32) error {
		return errors.Join(errors.New("wrapped"), windows.ERROR_NOT_FOUND)
	})
	if err != nil {
		t.Fatalf("terminateJobProcessTreeWith(gone) error = %v", err)
	}
}
