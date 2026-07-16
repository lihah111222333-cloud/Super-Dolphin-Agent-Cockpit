//go:build !darwin

package pidregistry

import "errors"

func processIDs() ([]int, error) {
	return nil, errors.New("pidregistry: process argument discovery is unsupported on this platform")
}

func processArguments(int) ([]string, error) {
	return nil, errors.New("pidregistry: process argument discovery is unsupported on this platform")
}
