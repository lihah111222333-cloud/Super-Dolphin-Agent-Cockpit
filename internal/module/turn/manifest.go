package turn

import dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"

type manifestBuilder struct{}

func (b *manifestBuilder) Build(input PrepareInput) dto.MCPManifest {
	return dto.BuildManifest(dto.ManifestContext{
		AgentID:    input.AgentID,
		CWD:        input.CWD,
		ThreadCaps: input.ThreadCaps,
		BinaryDir:  input.BinaryDir,
	})
}
