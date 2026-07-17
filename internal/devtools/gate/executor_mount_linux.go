//go:build linux

package gate

import (
	"os"
)

func validateReadOnlyMount(path string) error {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	defer file.Close()
	return validateMountInfo(file, path)
}
