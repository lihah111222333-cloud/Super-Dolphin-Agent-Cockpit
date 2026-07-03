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
		{"Foo", true, "Foo"},         // Unicode letter：大写 ASCII 兼容
		{"foo_bar", true, "foo_bar"}, // 下划线允许（首字符除外）

		// 非法场景
		{"", false, ""},
		{"   ", false, ""},
		{"foo/bar", false, ""},  // 路径分隔
		{"foo\\bar", false, ""}, // windows 分隔
		{"../etc", false, ""},   // 路径逃逸
		{"foo bar", false, ""},  // 运行时名称不允许空格，展示名走 display_name
		{"foo.md", false, ""},   // 点号
		{"-foo", false, ""},     // 连字符开头
		{"_foo", false, ""},     // 下划线开头
		{"foo:bar", false, ""},  // 冒号
		{"\x00abc", false, ""},  // 控制字符
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
		"user":      TrustUser,
		"trusted":   TrustUser,
		"  User  ":  TrustUser,
		"project":   TrustProject,
		"untrusted": TrustProject,
		"workspace": TrustProject,
		"signed":    TrustSigned,
		"verified":  TrustSigned,
		"":          TrustUnknown,
		"random":    TrustUnknown,
		"admin":     TrustUnknown,
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

// 以下 artifact helper 测试锁住 skill 资源定位的兼容规则，避免路径逃逸或错误 kind 被接受。

func TestIsValidArtifactKind(t *testing.T) {
	for _, ok := range []string{ArtifactKindMetadata, ArtifactKindBody, ArtifactKindResource} {
		if !IsValidArtifactKind(ok) {
			t.Fatalf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "body ", "BODY", "exec", "unknown"} {
		if IsValidArtifactKind(bad) {
			t.Fatalf("%q should NOT be valid", bad)
		}
	}
}

func TestRepoFingerprint_EmptyInputReturnsEmpty(t *testing.T) {
	if got := RepoFingerprint(""); got != "" {
		t.Fatalf("empty input → empty, got %q", got)
	}
	if got := RepoFingerprint("   "); got != "" {
		t.Fatalf("whitespace input → empty, got %q", got)
	}
}

func TestRepoFingerprint_StableForSamePath(t *testing.T) {
	tmp := t.TempDir()
	a := RepoFingerprint(tmp)
	b := RepoFingerprint(tmp)
	if a == "" || b == "" {
		t.Fatalf("expected non-empty fingerprint, got %q %q", a, b)
	}
	if a != b {
		t.Fatalf("fingerprint not stable for same path: %q vs %q", a, b)
	}
	// 32 个十六进制字符对应 128 bit，长度变化会影响外部持久化键。
	if len(a) != 32 {
		t.Fatalf("fingerprint should be 32 hex chars, got %d: %q", len(a), a)
	}
}

func TestRepoFingerprint_DifferentPathsYieldDifferentFingerprints(t *testing.T) {
	tmp1 := t.TempDir()
	tmp2 := t.TempDir()
	fp1 := RepoFingerprint(tmp1)
	fp2 := RepoFingerprint(tmp2)
	if fp1 == fp2 {
		t.Fatalf("different paths should yield different fingerprints; got both %q", fp1)
	}
}

func TestNormalizeArtifactLocator_Metadata(t *testing.T) {
	if got, err := NormalizeArtifactLocator(ArtifactKindMetadata, ""); err != nil || got != "" {
		t.Fatalf("metadata empty → empty/nil, got (%q, %v)", got, err)
	}
	if _, err := NormalizeArtifactLocator(ArtifactKindMetadata, "anything"); err == nil {
		t.Fatalf("metadata non-empty locator should reject")
	}
}

func TestNormalizeArtifactLocator_Body(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "SKILL.md", true},
		{"SKILL.md", "SKILL.md", true},
		{"SKILL.md#Usage", "SKILL.md#Usage", true},
		{"SKILL.md#", "SKILL.md", true}, // 空 anchor 视同无 anchor
		{"  SKILL.md#Overview  ", "SKILL.md#Overview", true},
		{"README.md", "", false},        // 非 SKILL.md
		{"SKILL.md#../evil", "", false}, // anchor 含 ..
		{"SKILL.md#a/b", "", false},     // anchor 含 /
	}
	for _, c := range cases {
		got, err := NormalizeArtifactLocator(ArtifactKindBody, c.in)
		if c.ok {
			if err != nil {
				t.Fatalf("body %q: unexpected err %v", c.in, err)
			}
			if got != c.want {
				t.Fatalf("body %q → %q, want %q", c.in, got, c.want)
			}
		} else {
			if err == nil {
				t.Fatalf("body %q should reject, got %q", c.in, got)
			}
		}
	}
}

func TestNormalizeArtifactLocator_Resource(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"references/api.md", "references/api.md", true},
		{"scripts/setup.sh", "scripts/setup.sh", true},
		{"./references/api.md", "references/api.md", true}, // Clean 去掉 ./
		{"references//api.md", "references/api.md", true},  // Clean 压缩
		{"", "", false},
		{"/abs/path", "", false},               // 绝对路径
		{"../etc/passwd", "", false},           // 路径逃逸
		{"references/../../escape", "", false}, // 中间 ..
		{"..", "", false},
	}
	for _, c := range cases {
		got, err := NormalizeArtifactLocator(ArtifactKindResource, c.in)
		if c.ok {
			if err != nil {
				t.Fatalf("resource %q: unexpected err %v", c.in, err)
			}
			if got != c.want {
				t.Fatalf("resource %q → %q, want %q", c.in, got, c.want)
			}
		} else {
			if err == nil {
				t.Fatalf("resource %q should reject, got %q", c.in, got)
			}
		}
	}
}

func TestNormalizeArtifactLocator_InvalidKind(t *testing.T) {
	if _, err := NormalizeArtifactLocator("exec", "anything"); err == nil {
		t.Fatalf("invalid kind should reject")
	}
}

func TestInferTrustFromRoot(t *testing.T) {
	tmp := t.TempDir()
	projectRoot := filepath.Join(tmp, "proj", ".agents", "skills")
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
