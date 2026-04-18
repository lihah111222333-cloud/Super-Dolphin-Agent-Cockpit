package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// helperParse wraps parseSkillInfo with a fixed rel/dir so tests focus on content parsing.
func helperParse(t *testing.T, content string, defaultTrust TrustScope) SkillInfo {
	t.Helper()
	return parseSkillInfo("foo", "/tmp/foo", content, defaultTrust)
}

func TestParseSkillInfo_ContentHashStableAndCorrect(t *testing.T) {
	content := "---\nname: foo\ndescription: test\n---\n\nhello"
	info := helperParse(t, content, TrustProject)
	sum := sha256.Sum256([]byte(content))
	want := hex.EncodeToString(sum[:])
	if info.ContentHash != want {
		t.Fatalf("content hash = %q want %q", info.ContentHash, want)
	}
	// 确认同输入产生同哈希（确定性）
	again := helperParse(t, content, TrustProject)
	if again.ContentHash != info.ContentHash {
		t.Fatalf("hash not stable: %q vs %q", again.ContentHash, info.ContentHash)
	}
}

func TestParseSkillInfo_FrontmatterTrustOverridesDefault(t *testing.T) {
	content := "---\nname: foo\ntrust: signed\n---\n\nbody"
	info := helperParse(t, content, TrustProject)
	if info.Trust != TrustSigned {
		t.Fatalf("frontmatter trust should win: got %q", info.Trust)
	}
}

func TestParseSkillInfo_DefaultTrustWhenFrontmatterMissing(t *testing.T) {
	content := "---\nname: foo\ndescription: hi\n---\n\nbody"
	info := helperParse(t, content, TrustUser)
	if info.Trust != TrustUser {
		t.Fatalf("default trust should apply: got %q", info.Trust)
	}
}

func TestParseSkillInfo_SafetyFallbackToProject(t *testing.T) {
	content := "body only, no frontmatter"
	info := helperParse(t, content, TrustUnknown)
	if info.Trust != TrustProject {
		t.Fatalf("safety fallback should be TrustProject: got %q", info.Trust)
	}
}

// 非法 trust 值（如 "banana"）应被 parseTrustScope 返回 TrustUnknown，
// 使 parseSkillInfo 的回填逻辑回落到 defaultTrust，而不是被误写为 TrustUnknown。
func TestParseSkillInfo_InvalidFrontmatterTrustUsesDefault(t *testing.T) {
	content := "---\nname: foo\ntrust: banana\n---\nbody"
	info := helperParse(t, content, TrustUser)
	if info.Trust != TrustUser {
		t.Fatalf("invalid trust value should fall back to default (TrustUser): got %q", info.Trust)
	}
}

// defaultTrust 也非法时（TrustUnknown）最终应回落到安全兑底 TrustProject。
func TestParseSkillInfo_InvalidFrontmatterAndInvalidDefault_FallbackToProject(t *testing.T) {
	content := "---\nname: foo\ntrust: nonsense\n---\nbody"
	info := helperParse(t, content, TrustUnknown)
	if info.Trust != TrustProject {
		t.Fatalf("both invalid should safety-fallback to TrustProject: got %q", info.Trust)
	}
}

func TestParseSkillInfo_AllowedTools(t *testing.T) {
	content := "---\nname: foo\nallowed-tools: [Read, skill_expand]\n---\n\nbody"
	info := helperParse(t, content, TrustProject)
	if len(info.AllowedTools) != 2 {
		t.Fatalf("AllowedTools len = %d, want 2: %v", len(info.AllowedTools), info.AllowedTools)
	}
	joined := strings.Join(info.AllowedTools, ",")
	if !strings.Contains(joined, "Read") || !strings.Contains(joined, "skill_expand") {
		t.Fatalf("AllowedTools missing values: %v", info.AllowedTools)
	}
}

func TestParseSkillInfo_AllowedToolsAliases(t *testing.T) {
	// allowed_tools 下划线别名
	content := "---\nname: foo\nallowed_tools: Read,Bash\n---\n\nbody"
	info := helperParse(t, content, TrustProject)
	if len(info.AllowedTools) != 2 {
		t.Fatalf("expected 2 tools via alias, got %v", info.AllowedTools)
	}
}

func TestParseSkillInfo_DisableModelInvocationTrue(t *testing.T) {
	for _, truthy := range []string{"true", "yes", "y", "on", "1", "True", "YES"} {
		content := "---\nname: foo\ndisable-model-invocation: " + truthy + "\n---\n\nbody"
		info := helperParse(t, content, TrustProject)
		if !info.DisableModelInvocation {
			t.Fatalf("%q should parse as true", truthy)
		}
	}
}

func TestParseSkillInfo_DisableModelInvocationFalse(t *testing.T) {
	for _, falsy := range []string{"false", "no", "0", "off", "", "random"} {
		content := "---\nname: foo\ndisable-model-invocation: " + falsy + "\n---\n\nbody"
		info := helperParse(t, content, TrustProject)
		if info.DisableModelInvocation {
			t.Fatalf("%q should parse as false", falsy)
		}
	}
}

func TestParseBoolScalar(t *testing.T) {
	if !parseBoolScalar("true") || !parseBoolScalar(" YES ") || !parseBoolScalar("1") || !parseBoolScalar("on") {
		t.Fatalf("truthy cases failed")
	}
	if parseBoolScalar("false") || parseBoolScalar("") || parseBoolScalar("maybe") {
		t.Fatalf("falsy cases failed")
	}
}
