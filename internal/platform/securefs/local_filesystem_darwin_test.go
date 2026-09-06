//go:build darwin

package securefs

import (
	"testing"

	"golang.org/x/sys/unix"
)

// TestCheckLocalFilesystemDarwinClassifiesMountTypes 验证 macOS 网络与未知挂载类型均 fail-closed。
func TestCheckLocalFilesystemDarwinClassifiesMountTypes(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{name: "apfs", want: true},
		{name: "nfs", want: false},
		{name: "unknown", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkLocalFilesystemDarwin("/state", func(_ string, state *unix.Statfs_t) error {
				copy(state.Fstypename[:], tc.name)
				return nil
			})
			if tc.want && err != nil {
				t.Fatalf("local filesystem check error = %v", err)
			}
			if !tc.want && err == nil {
				t.Fatal("network or unknown filesystem was accepted")
			}
		})
	}
}
