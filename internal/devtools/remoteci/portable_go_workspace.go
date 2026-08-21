package remoteci

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"

const (
	// PortableGoWorkspaceDescriptorPath 是组合树内唯一 workspace 模型文件。
	PortableGoWorkspaceDescriptorPath = gate.PortableGoWorkspaceDescriptorPath
	// PortableGoWorkspaceSchema 是组合树 workspace 描述符的固定版本。
	PortableGoWorkspaceSchema = gate.PortableGoWorkspaceSchema
)

// PortableGoWorkspaceDescriptor 复用 gate-owned engine/target workspace wire contract。
type PortableGoWorkspaceDescriptor = gate.PortableGoWorkspaceDescriptor

// DecodePortableGoWorkspaceDescriptor 严格解码 gate-owned workspace 描述符。
func DecodePortableGoWorkspaceDescriptor(data []byte) (PortableGoWorkspaceDescriptor, error) {
	return gate.DecodePortableGoWorkspaceDescriptor(data)
}

// InstallPortableGoWorkspace 原子安装 gate-owned worker workspace。
func InstallPortableGoWorkspace(sourceRoot string) error {
	return gate.InstallPortableGoWorkspace(sourceRoot)
}
