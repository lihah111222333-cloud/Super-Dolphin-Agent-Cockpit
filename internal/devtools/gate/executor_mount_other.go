//go:build !linux

package gate

import "errors"

func validateReadOnlyMount(string) error {
	return errors.New("read-only source mount verification requires Linux")
}
