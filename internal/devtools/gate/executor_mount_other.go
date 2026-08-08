//go:build !linux

package gate

import "errors"

func validateReadOnlyMount(string) error {
	return errors.New("read-only source mount verification requires Linux")
}

func validateReadOnlyImagePath(string) error {
	return errors.New("read-only image path verification requires Linux")
}

func validateReadOnlyOCIImagePath(path string) error {
	if path != ExecutorOCIProjectGoBuildCacheSeedRoot {
		return errors.New("OCI Go build cache seed root is not canonical")
	}
	return validateReadOnlyImagePath(path)
}
