package skill

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestValidateSkillName(t *testing.T) {
	cases := []struct {
		in      string
		ok      bool
		wantOut string
	}{
		{"foo", true, "foo"},
		{"go-testing", true, "go-testing"},
		{"  foo  ", true, "foo"}, // 首尾空白被剥离
		{"a", true, "a"},
		{"a1", true, "a1"},
		{"a-b-c-123", true, "a-b-c-123"},

		// 非法场景
		{"", false, ""},
		{"   ", false, ""},
		{"Foo", false, ""},       // 大写
		{"foo_bar", false, ""},   // 下划线
		{"foo/bar", false, ""},   // 路径分隔
		{"foo\\bar", false, ""},  // windows 分隔
		{"../etc", false, ""},    // 路径逃逸
		{"foo bar", false, ""},   // 空格
		{"foo.md", false, ""},    // 点号
		{"-foo", false, ""},      // 连字符开头
		{"foo:bar", false, ""},   // 冒号
		{"\x00abc", false, ""},   // 控制字符
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := validateSkillName(c.in)
			if c.ok {
				if err != nil {
					t.Fatalf("expected ok, got err: %v", err)
				}
				if got != c.wantOut {
					t.Fatalf("normalized = %q want %q", got, c.wantOut)
				}
			} else {
				if err == nil {
					t.Fatalf("expected err for %q, got ok", c.in)
				}
				if !errors.Is(err, ErrInvalidSkillName) {
					t.Fatalf("err should wrap ErrInvalidSkillName: %v", err)
				}
			}
		})
	}

	// 64 字符边界测试
	maxName := ""
	for i := 0; i < 64; i++ {
		maxName += "a"
	}
	if _, err := validateSkillName(maxName); err != nil {
		t.Fatalf("64-char name should pass: %v", err)
	}
	overflow := maxName + "a"
	if _, err := validateSkillName(overflow); err == nil {
		t.Fatalf("65-char name should fail")
	}
}

func TestParseTrustScope(t *testing.T) {
	cases := map[string]TrustScope{
		"user":       TrustUser,
		"trusted":    TrustUser,
		"  User  ":   TrustUser,
		"project":    TrustProject,
		"untrusted":  TrustProject,
		"workspace":  TrustProject,
		"signed":     TrustSigned,
		"verified":   TrustSigned,
		"":           TrustUnknown,
		"random":     TrustUnknown,
		"admin":      TrustUnknown,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got := parseTrustScope(in)
			if got != want {
				t.Fatalf("parseTrustScope(%q) = %q want %q", in, got, want)
			}
		})
	}
}

func TestTrustScopeMethods(t *testing.T) {
	if !TrustUser.Valid() || !TrustProject.Valid() || !TrustSigned.Valid() {
		t.Fatalf("known trust scopes must be valid")
	}
	if TrustUnknown.Valid() {
		t.Fatalf("TrustUnknown must not be valid")
	}
	if !TrustUser.Trusted() || !TrustSigned.Trusted() {
		t.Fatalf("user/signed must be Trusted")
	}
	if TrustProject.Trusted() {
		t.Fatalf("project must NOT be Trusted")
	}
}

func TestInferTrustFromRoot(t *testing.T) {
	tmp := t.TempDir()
	projectRoot := filepath.Join(tmp, "proj", ".agent", "skills")
	userRoot := filepath.Join(tmp, "home", ".multi-agent", "skills")

	cases := []struct {
		name string
		dir  string
		want TrustScope
	}{
		{"project-hit", filepath.Join(projectRoot, "foo"), TrustProject},
		{"user-hit", filepath.Join(userRoot, "bar"), TrustUser},
		{"unknown-falls-back-to-project", filepath.Join(tmp, "somewhere", "else"), TrustProject},
		{"empty-dir-falls-back", "", TrustProject},
		{"dot-dir-falls-back", ".", TrustProject},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := inferTrustFromRoot(c.dir, projectRoot, userRoot)
			if got != c.want {
				t.Fatalf("inferTrustFromRoot(%q, ...) = %q want %q", c.dir, got, c.want)
			}
		})
	}
}
