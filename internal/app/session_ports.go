package app

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"

type sessionPorts struct {
	contract.SessionLifecyclePort
	contract.SessionStatusPort
}

// newSessionPorts 聚合 session lifecycle/read 端口，供 Fx 图暴露统一 contract.SessionPorts。
func newSessionPorts(lifecycle contract.SessionLifecyclePort, status contract.SessionStatusPort) contract.SessionPorts {
	return sessionPorts{SessionLifecyclePort: lifecycle, SessionStatusPort: status}
}

var _ contract.SessionPorts = sessionPorts{}
