package skillforge

import "testing"

func TestEmbeddedSkillsAccessor(t *testing.T) {
	names, err := ListEmbeddedSkillNames()
	if err != nil {
		t.Fatalf("ListEmbeddedSkillNames: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no embedded skills found; expected at least 1")
	}
	min := 14
	if len(names) < min {
		t.Errorf("len(names) = %d, want >= %d", len(names), min)
	}
	body, err := ReadEmbeddedSkill(names[0])
	if err != nil {
		t.Fatalf("ReadEmbeddedSkill(%s): %v", names[0], err)
	}
	if len(body) == 0 {
		t.Errorf("empty body for %s", names[0])
	}
}

func TestReadEmbeddedSkill_RejectsPathTraversal(t *testing.T) {
	cases := []string{"../etc/passwd", "foo/bar", "foo\\bar", "..", "."}
	for _, n := range cases {
		if _, err := ReadEmbeddedSkill(n); err == nil {
			t.Errorf("ReadEmbeddedSkill(%q) should error", n)
		}
	}
}

func TestReadEmbeddedSkill_NonexistentReturnsError(t *testing.T) {
	if _, err := ReadEmbeddedSkill("does-not-exist-xyz"); err == nil {
		t.Fatal("ReadEmbeddedSkill(nonexistent) should error")
	}
}

func TestListEmbeddedSkillNames_Sorted(t *testing.T) {
	names, err := ListEmbeddedSkillNames()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("names not sorted: %s > %s at i=%d", names[i-1], names[i], i)
		}
	}
}
