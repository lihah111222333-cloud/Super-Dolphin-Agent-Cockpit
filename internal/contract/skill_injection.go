package contract

import (
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

const SkillInjectionDescriptorGroupTag = `group:"skill_injection_descriptors"`

type SkillInjectionPort interface {
	InjectL1Manifest(baseInstructions, manifest string) string
	BuildTurnSection(refs []dto.SkillRef) (string, bool)
	ReservedTokens() int
}

type NativeSkillDetector interface {
	DetectNativeSkills(root string) []string
}

type NativeSkillOverridePort interface {
	ApplyNativeOverrides(refs []dto.SkillRef, gitRoot, cwd string) []dto.SkillRef
}

type SkillInjectionPortResolver interface {
	ResolveSkillInjectionPort(provider string) (SkillInjectionPort, bool)
}

type SkillInjectionPortDescriptor struct {
	Name string
	Port SkillInjectionPort
}

func NewSkillInjectionPortDescriptor(name string, port SkillInjectionPort) (SkillInjectionPortDescriptor, bool) {
	name = strings.TrimSpace(name)
	if name == "" || port == nil {
		return SkillInjectionPortDescriptor{}, false
	}
	return SkillInjectionPortDescriptor{Name: name, Port: port}, true
}
