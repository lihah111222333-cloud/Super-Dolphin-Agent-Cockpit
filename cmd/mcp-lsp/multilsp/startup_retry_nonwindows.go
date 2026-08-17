//go:build !windows

package multilsp

import "context"

// initializeClientWithWindows122Retry 在非 Windows 上保持一次 initialize 的原有行为。
func initializeClientWithWindows122Retry(
	_ context.Context,
	client Client,
	initialize func(Client) error,
	_ func() (Client, error),
	_ func(Client) error,
) (Client, error) {
	if err := initialize(client); err != nil {
		return client, err
	}
	return client, nil
}
