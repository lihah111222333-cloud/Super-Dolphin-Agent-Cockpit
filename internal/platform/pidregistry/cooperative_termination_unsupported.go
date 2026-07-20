//go:build !darwin && !linux

package pidregistry

import (
	"context"
)

// CooperativeTerminationServer 是非 Darwin/Linux 平台的 fail-closed 占位类型。
type CooperativeTerminationServer struct{}

// StartCooperativeTerminationServer 在非 Darwin/Linux 平台保持 fail-closed。
func StartCooperativeTerminationServer(string, string, func()) (*CooperativeTerminationServer, error) {
	return nil, ErrExactProcessTerminationUnsupported
}

// StartParkedCooperativeTerminationServer 在非 Darwin/Linux 平台保持 fail-closed。
func StartParkedCooperativeTerminationServer(string, string, func()) (*CooperativeTerminationServer, error) {
	return nil, ErrExactProcessTerminationUnsupported
}

// WaitForActivation 在非 Darwin/Linux 平台保持 fail-closed。
func (server *CooperativeTerminationServer) WaitForActivation(context.Context) error {
	return ErrExactProcessTerminationUnsupported
}

// CaptureCooperativeEndpointIdentity 在非 Darwin/Linux 平台保持 fail-closed。
func CaptureCooperativeEndpointIdentity(string) (CooperativeEndpointIdentity, error) {
	return CooperativeEndpointIdentity{}, ErrExactProcessTerminationUnsupported
}

// CleanupCooperativeTerminationEndpoint 在非 Darwin/Linux 平台保持 fail-closed。
func CleanupCooperativeTerminationEndpoint(string) error {
	return ErrExactProcessTerminationUnsupported
}

// CleanupCooperativeTerminationEndpointInstance 在非 Darwin/Linux 平台保持 fail-closed。
func CleanupCooperativeTerminationEndpointInstance(string, CooperativeEndpointIdentity) error {
	return ErrExactProcessTerminationUnsupported
}

// CleanupStaleCooperativeTerminationEndpoint 在非 Darwin/Linux 平台保持 fail-closed。
func CleanupStaleCooperativeTerminationEndpoint(context.Context, string) error {
	return ErrExactProcessTerminationUnsupported
}

// Close 在 unsupported stub 上保持幂等。
func (server *CooperativeTerminationServer) Close() error { return nil }

func requestCooperativeTermination(context.Context, string, string, int, CooperativeEndpointIdentity) error {
	return ErrExactProcessTerminationUnsupported
}

func requestCooperativeControl(context.Context, string, string, string, string, int, CooperativeEndpointIdentity) error {
	return ErrExactProcessTerminationUnsupported
}
