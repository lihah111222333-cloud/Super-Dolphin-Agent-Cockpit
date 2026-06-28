package bus

import (
	"log/slog"
	"strings"
	"testing"
)

func TestProvideLogSinkRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	dispatcher := NewDispatcher()
	t.Cleanup(func() {
		_ = dispatcher.Close()
	})
	logger := slog.New(slog.DiscardHandler)

	tests := []struct {
		name string
		in   logSinkParams
		want string
	}{
		{
			name: "nil dispatcher",
			in:   logSinkParams{Logger: logger},
			want: "nil dispatcher",
		},
		{
			name: "nil logger",
			in:   logSinkParams{Dispatcher: dispatcher},
			want: "nil logger",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sink, err := provideLogSink(tt.in)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("provideLogSink() error = %v, want substring %q", err, tt.want)
			}
			if sink != nil {
				t.Fatalf("provideLogSink() sink = %#v, want nil on invalid deps", sink)
			}
		})
	}
}
