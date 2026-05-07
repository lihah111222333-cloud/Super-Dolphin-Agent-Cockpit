package skillforge

import (
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// Module 通过 fx 提供 skillforge 的接口实现，供下游 skilllibrary 等模块依赖注入。
var Module = fx.Module("skillforge",
	fx.Provide(func() contract.SkillForger { return &forgerAdapter{} }),
	fx.Provide(func() contract.EmbeddedSkillReader { return &embeddedReaderAdapter{} }),
	fx.Provide(func() contract.SkillDescriptionParser { return &descriptionParserAdapter{} }),
)

// forgerAdapter 将包级函数 Forge/RecoverStaging 适配为 contract.SkillForger 接口。
type forgerAdapter struct{}

func (a *forgerAdapter) Forge(libDir, cacheDir, name string, summaryOverride map[string]string) error {
	return Forge(libDir, cacheDir, name, summaryOverride)
}

func (a *forgerAdapter) RecoverStaging(cacheDir string) (*contract.StagingRecoveryReport, error) {
	rr, err := RecoverStaging(cacheDir)
	if err != nil {
		return nil, err
	}
	return &contract.StagingRecoveryReport{Errors: rr.Errors}, nil
}

// embeddedReaderAdapter 将包级函数 ListEmbeddedSkillNames/ReadEmbeddedSkill 适配为
// contract.EmbeddedSkillReader 接口。
type embeddedReaderAdapter struct{}

func (a *embeddedReaderAdapter) ListNames() ([]string, error) {
	return ListEmbeddedSkillNames()
}

func (a *embeddedReaderAdapter) Read(name string) ([]byte, error) {
	return ReadEmbeddedSkill(name)
}

type descriptionParserAdapter struct{}

func (a *descriptionParserAdapter) Description(skillMD string) string {
	parsed, err := Parse(skillMD)
	if err != nil {
		return ""
	}
	return parsed.Description
}
