package securefs

import (
	"errors"
	"testing"
)

func TestCheckLocalFilesystemRejectsNetworkAndMalformedPaths(t *testing.T) {
	for _, path := range []string{"relative/state.db", "//server/share/state.db", `\\server\share\state.db`, `\\?\UNC\server\share\state.db`} {
		t.Run(path, func(t *testing.T) {
			if err := CheckLocalFilesystem(path); !errors.Is(err, ErrNotLocalFilesystem) {
				t.Fatalf("CheckLocalFilesystem(%q) error = %v, want ErrNotLocalFilesystem", path, err)
			}
		})
	}
}
