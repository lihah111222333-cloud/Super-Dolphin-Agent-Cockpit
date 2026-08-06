package skill

import (
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestSkillProviderMirrorTargetConsumesEveryJSONField binds the provider-facing target DTO to the real target normalizer.
func TestSkillProviderMirrorTargetConsumesEveryJSONField(t *testing.T) {
	project := t.TempDir()
	home := filepath.Join(t.TempDir(), "explicit-provider-home")
	svc := &service{
		projectRoot:       project,
		projectSkillsRoot: defaultProjectSkillsRoot(project),
		superDolphinHome:  filepath.Join(t.TempDir(), ".super-dolphin"),
	}
	baseline := contract.SkillProviderMirrorTarget{
		Provider:          "claude",
		HomeRoot:          home,
		SkillsRoot:        filepath.Join(home, "skills"),
		AllowExplicitHome: true,
	}
	mapper := func(input contract.SkillProviderMirrorTarget) map[string]any {
		return skillProviderTargetOutput(svc, project, input)
	}
	archtest.AssertWireDTOMapperConsumesProducerFieldsFrom(t, baseline, mapper, nil,
		skillProviderTargetProjections(mapper,
			"provider", "home_root", "skills_root", "allow_explicit_home"),
	)
}

func skillProviderTargetOutput(svc *service, project string, input contract.SkillProviderMirrorTarget) map[string]any {
	targets, err := svc.providerMirrorTargets(project, []contract.SkillProviderMirrorTarget{input})
	output := map[string]any{"error": skillProviderTargetError(err)}
	if len(targets) == 1 {
		output["target"] = map[string]any{
			"target_id":         targets[0].TargetID,
			"provider":          targets[0].Provider,
			"scope":             targets[0].Scope,
			"root":              targets[0].Root,
			"canonical_root_id": targets[0].CanonicalRootID,
		}
	}
	return output
}

func skillProviderTargetProjections(
	mapper func(contract.SkillProviderMirrorTarget) map[string]any,
	fields ...string,
) []archtest.WireDTOMapperProjection {
	projections := make([]archtest.WireDTOMapperProjection, 0, len(fields)*2)
	for _, field := range fields {
		projections = append(projections,
			archtest.WireDTOMapperProjection{
				Field:       field,
				ConsumerKey: "error",
				Transform: func(input any, _ any) any {
					return mapper(input.(contract.SkillProviderMirrorTarget))["error"]
				},
			},
			archtest.WireDTOMapperProjection{
				Field:       field,
				ConsumerKey: "target",
				Transform:   func(any, any) any { return nil },
			},
		)
	}
	return projections
}

func skillProviderTargetError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
