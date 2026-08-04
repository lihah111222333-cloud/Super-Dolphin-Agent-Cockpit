package hiddenexec

import (
	"errors"
	"os"
	"testing"
)

func TestIsProcessTreeGoneErrorRequiresAnError(t *testing.T) {
	platformGone := errors.New("platform process gone")
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "success is not gone", err: nil, want: false},
		{name: "process done", err: os.ErrProcessDone, want: true},
		{name: "wrapped process done", err: errors.Join(errors.New("wrapped"), os.ErrProcessDone), want: true},
		{name: "platform gone", err: platformGone, want: true},
		{name: "unrelated failure", err: errors.New("permission denied"), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isProcessTreeGoneError(test.err, platformGone); got != test.want {
				t.Fatalf("isProcessTreeGoneError(%v) = %t, want %t", test.err, got, test.want)
			}
		})
	}
}
