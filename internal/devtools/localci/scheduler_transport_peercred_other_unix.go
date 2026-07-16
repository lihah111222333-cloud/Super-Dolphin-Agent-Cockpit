//go:build unix && !darwin && !linux

package localci

import "errors"

func schedulerPlatformPeerUID(int) (int, error) {
	return 0, errors.New("scheduler peer credentials are unsupported on this Unix platform")
}
