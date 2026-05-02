package contract

import "time"

type SkillDisclosureStats map[string]*SkillDisclosureSkillStats

type SkillDisclosureSkillStats struct {
	Calls []time.Time
}

type SkillDisclosureConfig struct {
	HalfLife       time.Duration
	FrozenDuration time.Duration
	WSMinCalls     int
	WSWeight       float64
}

type SkillDisclosureSnapshot struct {
	Workspace SkillDisclosureStats
	Global    SkillDisclosureStats
	Config    SkillDisclosureConfig
}

type SkillDisclosureTierSource interface {
	Enabled() bool
	DisclosureSnapshot() SkillDisclosureSnapshot
}
