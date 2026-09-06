//go:build !windows && !linux && !darwin

package securefs

import "fmt"

func checkLocalFilesystemNative(string) error {
	return fmt.Errorf("local filesystem detector unavailable on this operating system")
}
