//go:build !darwin

package pidregistry

import (
	"context"
)

// CooperativeTerminationServer 是非 Darwin 平台的 fail-closed 占位类型。
type CooperativeTerminationServer struct{}

// StartCooperativeTerminationServer 在非 Darwin 平台保持 fail-closed。
func StartCooperativeTerminationServer(string, string, func()) (*CooperativeTerminationServer, error) {
	return nil, ErrExactProcessTerminationUnsupported
}

// Close 在 unsupported stub 上保持幂等。
func (server *CooperativeTerminationServer) Close() error { return nil }

func requestCooperativeTermination(context.Context, string, string) error {
	return ErrExactProcessTerminationUnsupported
}
