package shared

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestSkillMirrorReportItemConsumesEveryJSONField binds startup-gate decisions to the real conflict consumer.
func TestSkillMirrorReportItemConsumesEveryJSONField(t *testing.T) {
	t.Run("scope", func(t *testing.T) {
		baseline := skillMirrorReportItemFixture("claude:user-global:user", "project", "same_name")
		archtest.AssertWireDTOMapperConsumesProducerFieldsFrom(
			t,
			baseline,
			skillMirrorReportGateOutput,
			skillMirrorReportExemptions("scope"),
			[]archtest.WireDTOMapperProjection{
				{Field: "scope", ConsumerKey: "blocked", Transform: func(any, any) any { return false }},
			},
		)
	})
	t.Run("target id", func(t *testing.T) {
		baseline := skillMirrorReportItemFixture("claude:project:repo", "personal", "same_name")
		archtest.AssertWireDTOMapperConsumesProducerFieldsFrom(
			t,
			baseline,
			skillMirrorReportGateOutput,
			skillMirrorReportExemptions("target_id"),
			[]archtest.WireDTOMapperProjection{
				{Field: "target_id", ConsumerKey: "blocked", Transform: func(any, any) any { return false }},
			},
		)
	})
	t.Run("conflict kind", func(t *testing.T) {
		baseline := skillMirrorReportItemFixture("claude:user-global:user", "personal", "same_name")
		archtest.AssertWireDTOMapperConsumesProducerFieldsFrom(
			t,
			baseline,
			skillMirrorReportGateOutput,
			skillMirrorReportExemptions("conflict_kind"),
			[]archtest.WireDTOMapperProjection{
				{Field: "conflict_kind", ConsumerKey: "blocked", Transform: func(any, any) any { return true }},
			},
		)
	})
}

func skillMirrorReportItemFixture(targetID, scope, conflictKind string) contract.SkillMirrorReportItem {
	return contract.SkillMirrorReportItem{
		TargetID:           targetID,
		Provider:           contract.SkillProviderClaude,
		Scope:              scope,
		RelativeMirrorPath: "build",
		CanonicalID:        "project/build",
		OldHash:            "old-hash",
		NewHash:            "new-hash",
		ConflictKind:       conflictKind,
		Error:              "mirror conflict detail",
	}
}

func skillMirrorReportGateOutput(input contract.SkillMirrorReportItem) map[string]any {
	err := EnsureNoSkillMirrorConflicts(contract.SkillMirrorReport{Conflicts: []contract.SkillMirrorReportItem{input}})
	return map[string]any{"blocked": err != nil}
}

func skillMirrorReportExemptions(mapped string) []archtest.WireDTOMapperExemption {
	allFields := []string{
		"target_id",
		"provider",
		"scope",
		"relative_mirror_path",
		"canonical_id",
		"old_hash",
		"new_hash",
		"conflict_kind",
		"error",
	}
	exemptions := make([]archtest.WireDTOMapperExemption, 0, len(allFields)-1)
	for _, field := range allFields {
		if field == mapped {
			continue
		}
		exemptions = append(exemptions, archtest.WireDTOMapperExemption{
			Field:     field,
			Direction: "SkillMirrorReportItem -> EnsureNoSkillMirrorConflicts startup gate",
			Reason:    "the selected gate decision is controlled by the registered field in this one-hot branch",
			Evidence:  "internal/provider/shared/provider_home.go:505",
			Owner:     "internal/provider/shared",
		})
	}
	return exemptions
}
