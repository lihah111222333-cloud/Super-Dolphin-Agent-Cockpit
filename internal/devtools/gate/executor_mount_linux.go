//go:build linux

package gate

import (
	"errors"
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

func validateReadOnlyImagePath(path string) error {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	defer file.Close()
	return validateContainingReadOnlyMountInfo(file, path)
}

func validateReadOnlyOCIImagePath(path string) error {
	if path != ExecutorOCIProjectGoBuildCacheSeedRoot {
		return errors.New("OCI Go build cache seed root is not canonical")
	}
	return validateReadOnlyImagePath(path)
}
