package dashboard

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type stubDashboardSkillInventory struct {
	runtimeItems   []contract.SkillInfo
	runtimeErr     error
	inventoryItems []contract.SkillInfo
	inventoryErr   error
	inventoryCalls int
}

func (s *stubDashboardSkillInventory) ListSkills(context.Context) ([]contract.SkillInfo, error) {
	return s.runtimeItems, s.runtimeErr
}

func (s *stubDashboardSkillInventory) ListSkillInventory(context.Context) ([]contract.SkillInfo, error) {
	s.inventoryCalls++
	return s.inventoryItems, s.inventoryErr
}

func TestGetDashboardPageSkillsUsesInventoryIncludingConflictedDuplicates(t *testing.T) {
	t.Parallel()

	stub := &stubDashboardSkillInventory{
		runtimeItems: []contract.SkillInfo{{Name: "safe", Scope: "project", Dir: "/repo/.agent/skills/safe"}},
		runtimeErr:   contract.ErrSkillSameNameConflict,
		inventoryItems: []contract.SkillInfo{
			{Name: "安全工程师规范", Scope: "project", Dir: "/repo/.agent/skills/security-engineer"},
			{Name: "安全工程师规范", Scope: "project", Dir: "/repo/.agent/skills/security-standards"},
			{Name: "编写计划", Scope: "project", Dir: "/repo/.agent/skills/编写计划"},
			{Name: "编写计划", Scope: "personal", PersonalType: "imported", Dir: "/home/.super-dolphin/skills/personal/imported/编写计划"},
		},
	}
	svc := &service{skills: stub, skillInventory: stub}

	got, err := svc.GetDashboardPage(context.Background(), "skills")
	if err != nil {
		t.Fatalf("GetDashboardPage(skills) error = %v", err)
	}
	if stub.inventoryCalls != 1 {
		t.Fatalf("ListSkillInventory calls = %d, want 1", stub.inventoryCalls)
	}
	if len(got.Skills) != 4 {
		t.Fatalf("GetDashboardPage(skills).Skills = %+v, want all inventory skills", got.Skills)
	}
	assertDashboardSkillDir(t, got.Skills, "安全工程师规范", "project", "", "/repo/.agent/skills/security-engineer")
	assertDashboardSkillDir(t, got.Skills, "安全工程师规范", "project", "", "/repo/.agent/skills/security-standards")
	assertDashboardSkillDir(t, got.Skills, "编写计划", "personal", "imported", "/home/.super-dolphin/skills/personal/imported/编写计划")
}

func assertDashboardSkillDir(t *testing.T, infos []contract.SkillInfo, name, scope, personalType, dir string) {
	t.Helper()
	for _, info := range infos {
		if info.Name == name && info.Scope == scope && info.PersonalType == personalType && info.Dir == dir {
			return
		}
	}
	t.Fatalf("missing dashboard skill name=%q scope=%q personal_type=%q dir=%q in %+v", name, scope, personalType, dir, infos)
}
