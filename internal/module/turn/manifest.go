package turn

import (
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type manifestBuilder struct {
	binaryDir string
}

func newManifestBuilder(binaryDir string) *manifestBuilder {
	return &manifestBuilder{binaryDir: strings.TrimSpace(binaryDir)}
}

func (b *manifestBuilder) Build(input PrepareInput) dto.MCPManifest {
	return dto.BuildManifest(dto.ManifestContext{
		AgentID:    input.AgentID,
		CWD:        input.CWD,
		ThreadCaps: input.ThreadCaps,
		BinaryDir:  b.binaryDirFor(input.BinaryDir),
	})
}

func (b *manifestBuilder) binaryDirFor(binaryDir string) string {
	if binaryDir = strings.TrimSpace(binaryDir); binaryDir != "" {
		return binaryDir
	}
	return strings.TrimSpace(b.binaryDir)
}
