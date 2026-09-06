//go:build linux

package securefs

import (
	"errors"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCheckLocalFilesystemLinuxClassifiesMountTypes(t *testing.T) {
	tests := []struct {
		name string
		kind uint64
		err  error
	}{
		{name: "local", kind: 0xEF53},
		{name: "network", kind: 0x6969, err: ErrNotLocalFilesystem},
		{name: "unknown", kind: 0x12345678, err: ErrNotLocalFilesystem},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkLocalFilesystemLinux("/state", func(_ string, state *unix.Statfs_t) error {
				switch tc.kind {
				case 0xEF53:
					state.Type = 0xEF53
				case 0x6969:
					state.Type = 0x6969
				default:
					state.Type = 0x12345678
				}
				return nil
			})
			if tc.err == nil {
				if err != nil {
					t.Fatalf("local filesystem check error = %v", err)
				}
				return
			}
			if !errors.Is(err, tc.err) {
				t.Fatalf("filesystem check error = %v, want %v", err, tc.err)
			}
		})
	}
}

func TestCheckLocalFilesystemLinuxFailsClosedWhenStatfsUnavailable(t *testing.T) {
	err := checkLocalFilesystemLinux("/state", func(string, *unix.Statfs_t) error { return syscall.EIO })
	if err == nil {
		t.Fatal("checkLocalFilesystemLinux unexpectedly accepted statfs failure")
	}
}
