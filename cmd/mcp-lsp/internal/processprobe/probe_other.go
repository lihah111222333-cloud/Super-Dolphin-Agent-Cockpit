//go:build !darwin && !linux && !windows

package processprobe

import "context"

// Probe fails closed on platforms without a native, query-only implementation.
func Probe(_ context.Context, _ int) (Snapshot, error) {
	return unsupportedSnapshot("unsupported")
}
