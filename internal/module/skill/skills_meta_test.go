package skill

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestParseSkillRecord_RejectsOversizedFile 防 DoS：恶意项目在 .agents/skills/evil/SKILL.md
// 塞进大于 maxSkillFileBytes (1MB) 的内容，扫盘期必须拒绝而不是读入内存。
// 这条回归防止未来重构误删 size check 。
func TestParseSkillRecord_RejectsOversizedFile(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "evil")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, skillMainFile)
	// 书写比 maxSkillFileBytes 略大的内容（1MB + 1 字节）
	big := bytes.Repeat([]byte("x"), maxSkillFileBytes+1)
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatalf("write oversized: %v", err)
	}
	_, err := parseSkillRecord(tmp, path, TrustProject)
	if err == nil {
		t.Fatalf("expected error for oversized SKILL.md, got nil")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error should mention size limit: %v", err)
	}
}

// TestParseSkillRecord_AcceptsNormalFile 确保大小检查不会误伤正常文件。
func TestParseSkillRecord_AcceptsNormalFile(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "foo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, skillMainFile)
	content := "---\nname: foo\ndescription: hi\n---\nbody"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	rec, err := parseSkillRecord(tmp, path, TrustProject)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.info.Name != "foo" {
		t.Fatalf("name = %q", rec.info.Name)
	}
}

func TestParseSkillInfo_ReadsDisplayNameFrontmatter(t *testing.T) {
	content := "---\nname: docker-container-deploy\ndisplay_name: Docker 容器化部署\ndescription: hi\n---\nbody"
	info := helperParse(t, content, TrustProject)
	if info.Name != "docker-container-deploy" {
		t.Fatalf("name = %q", info.Name)
	}
	if info.DisplayName != "Docker 容器化部署" {
		t.Fatalf("display name = %q", info.DisplayName)
	}
}

func TestParseSkillInfo_ReadsTitleAsDisplayNameAlias(t *testing.T) {
	content := "---\nname: docker-container-deploy\ntitle: Docker 容器化部署\n---\nbody"
	info := helperParse(t, content, TrustProject)
	if info.DisplayName != "Docker 容器化部署" {
		t.Fatalf("display name = %q", info.DisplayName)
	}
}

// helperParse wraps parseSkillInfo with a fixed rel/dir so tests focus on content parsing.
func helperParse(t *testing.T, content string, defaultTrust TrustScope) SkillInfo {
	t.Helper()
	info, err := parseSkillInfo("foo", "/tmp/foo", content, defaultTrust)
	if err != nil {
		t.Fatalf("parse skill info: %v", err)
	}
	return info
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

func TestParseSkillInfo_ProjectDefaultCapsFrontmatterTrust(t *testing.T) {
	content := "---\nname: foo\ntrust: signed\n---\n\nbody"
	info := helperParse(t, content, TrustProject)
	if info.Trust != TrustProject {
		t.Fatalf("project default should cap frontmatter trust: got %q", info.Trust)
	}
}

func TestParseSkillInfo_PersonalDefaultDoesNotSelfDeclareSigned(t *testing.T) {
	content := "---\nname: foo\ntrust: signed\n---\n\nbody"
	info := helperParse(t, content, TrustUser)
	if info.Trust != TrustUser {
		t.Fatalf("personal default should downgrade unsigned signed trust to user: got %q", info.Trust)
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

func TestParseSkillInfo_InvalidFrontmatterTrustRejected(t *testing.T) {
	for _, defaultTrust := range []TrustScope{TrustUser, TrustProject, TrustUnknown} {
		_, err := parseSkillInfo("foo", "/tmp/foo", "---\nname: foo\ntrust: banana\n---\nbody", defaultTrust)
		if err == nil || !strings.Contains(err.Error(), "trust must be user, project, or signed") {
			t.Fatalf("default trust %q invalid trust error = %v", defaultTrust, err)
		}
	}
}

func TestParseSkillInfo_AllowedTools(t *testing.T) {
	content := "---\nname: foo\nallowed-tools: [Read, skill_read_section]\n---\n\nbody"
	info := helperParse(t, content, TrustProject)
	if len(info.AllowedTools) != 2 {
		t.Fatalf("AllowedTools len = %d, want 2: %v", len(info.AllowedTools), info.AllowedTools)
	}
	joined := strings.Join(info.AllowedTools, ",")
	if !strings.Contains(joined, "Read") || !strings.Contains(joined, "skill_read_section") {
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

func TestParseSkillInfo_ReplacesNativeYAMLFrontmatter(t *testing.T) {
	content := strings.Join([]string{
		"---",
		"name: foo",
		"replaces_native:",
		"  codex:",
		"    - shell",
		"    - apply_patch",
		"  claude: [Read, Bash]",
		`  "*":`,
		"    - WebFetch",
		"---",
		"",
		"body",
	}, "\n")
	info := helperParse(t, content, TrustProject)
	if got := info.ReplacesNative["codex"]; !reflect.DeepEqual(got, []string{"shell", "apply_patch"}) {
		t.Fatalf("codex replacements = %#v", got)
	}
	if got := info.ReplacesNative["claude"]; !reflect.DeepEqual(got, []string{"Read", "Bash"}) {
		t.Fatalf("claude replacements = %#v", got)
	}
	if got := info.ReplacesNative["*"]; !reflect.DeepEqual(got, []string{"WebFetch"}) {
		t.Fatalf("wildcard replacements = %#v", got)
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
	for _, falsy := range []string{"false", "no", "n", "0", "off"} {
		content := "---\nname: foo\ndisable-model-invocation: " + falsy + "\n---\n\nbody"
		info := helperParse(t, content, TrustProject)
		if info.DisableModelInvocation {
			t.Fatalf("%q should parse as false", falsy)
		}
	}
}

type yamlSecurityMetadataCase struct {
	name           string
	frontmatter    string
	defaultTrust   TrustScope
	wantTrust      TrustScope
	wantDisable    bool
	wantErrContain string
}

func TestParseSkillInfo_YAMLSecurityMetadataSemantics(t *testing.T) {
	tests := []yamlSecurityMetadataCase{
		{
			name:         "quoted disable key with inline comment",
			frontmatter:  "\"disable_model_invocation\": true # manual only",
			defaultTrust: TrustProject,
			wantTrust:    TrustProject,
			wantDisable:  true,
		},
		{
			name:         "quoted trust key overrides personal root default",
			frontmatter:  "\"trust\": project",
			defaultTrust: TrustUser,
			wantTrust:    TrustProject,
		},
		{
			name:         "trust accepts inline comment",
			frontmatter:  "trust: project # least privilege",
			defaultTrust: TrustUser,
			wantTrust:    TrustProject,
		},
		{
			name:         "security aliases remain supported",
			frontmatter:  "trust_scope: project\ndisablemodelinvocation: yes",
			defaultTrust: TrustUser,
			wantTrust:    TrustProject,
			wantDisable:  true,
		},
		{
			name:           "invalid trust fails closed",
			frontmatter:    "\"trust\": banana",
			defaultTrust:   TrustUser,
			wantErrContain: "trust must be user, project, or signed",
		},
		{
			name:           "invalid disable value fails closed",
			frontmatter:    "disable_model_invocation: random",
			defaultTrust:   TrustProject,
			wantErrContain: "disable_model_invocation must be a boolean",
		},
		{
			name:           "empty disable value fails closed",
			frontmatter:    "disable_model_invocation:",
			defaultTrust:   TrustProject,
			wantErrContain: "disable_model_invocation must be a boolean",
		},
		{
			name:           "malformed yaml fails closed",
			frontmatter:    "disable_model_invocation: [true",
			defaultTrust:   TrustProject,
			wantErrContain: "parse skill frontmatter YAML",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertYAMLSecurityMetadata(t, tt)
		})
	}
}

func assertYAMLSecurityMetadata(t *testing.T, tt yamlSecurityMetadataCase) {
	t.Helper()
	content := "---\nname: foo\n" + tt.frontmatter + "\n---\nbody"
	info, err := parseSkillInfo("foo", "/tmp/foo", content, tt.defaultTrust)
	if tt.wantErrContain != "" {
		if err == nil || !strings.Contains(err.Error(), tt.wantErrContain) {
			t.Fatalf("parse error = %v, want substring %q", err, tt.wantErrContain)
		}
		return
	}
	if err != nil {
		t.Fatalf("parse skill info: %v", err)
	}
	if info.Trust != tt.wantTrust {
		t.Fatalf("trust = %q, want %q", info.Trust, tt.wantTrust)
	}
	if info.DisableModelInvocation != tt.wantDisable {
		t.Fatalf("disable_model_invocation = %v, want %v", info.DisableModelInvocation, tt.wantDisable)
	}
}

type invalidSecurityYAMLCase struct {
	name        string
	frontmatter string
}

func invalidSecurityYAMLCases() []invalidSecurityYAMLCase {
	return []invalidSecurityYAMLCase{
		{name: "null root", frontmatter: "null"},
		{name: "list root", frontmatter: "- name: foo\n- trust: project"},
		{name: "alias at root", frontmatter: "name: &skill-name foo\ndescription: *skill-name"},
		{name: "merge at root", frontmatter: "defaults: &defaults\n  trust: user\n<<: *defaults\nname: foo"},
		{name: "non scalar trust", frontmatter: "name: foo\ntrust: [project]"},
		{name: "non scalar disable", frontmatter: "name: foo\ndisable_model_invocation: {enabled: true}"},
		{name: "exact duplicate", frontmatter: "name: foo\ntrust: user\ntrust: project"},
		{
			name:        "alias duplicate",
			frontmatter: "name: foo\ndisable_model_invocation: true\ndisablemodelinvocation: false",
		},
	}
}

func TestParseSkillInfoRejectsUnsupportedYAMLShapes(t *testing.T) {
	for _, tt := range invalidSecurityYAMLCases() {
		t.Run(tt.name, func(t *testing.T) {
			content := "---\n" + tt.frontmatter + "\n---\nbody"
			if _, err := parseSkillInfo("foo", "/tmp/foo", content, TrustProject); err == nil {
				t.Fatalf("expected invalid frontmatter to fail: %q", tt.frontmatter)
			}
		})
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

func TestApplyMetaLine_ScalarAliases(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		check func(t *testing.T, info SkillInfo)
	}{
		{
			name:  "name",
			key:   "name",
			value: ` "demo" `,
			check: func(t *testing.T, info SkillInfo) {
				t.Helper()
				if info.Name != "demo" {
					t.Fatalf("name = %q, want demo", info.Name)
				}
			},
		},
		{
			name:  "description",
			key:   "description",
			value: ` "short summary" `,
			check: func(t *testing.T, info SkillInfo) {
				t.Helper()
				if info.Description != "short summary" {
					t.Fatalf("description = %q", info.Description)
				}
			},
		},
		{
			name:  "digest alias writes summary",
			key:   "digest",
			value: ` "headline" `,
			check: func(t *testing.T, info SkillInfo) {
				t.Helper()
				if info.Summary != "headline" {
					t.Fatalf("summary = %q", info.Summary)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var info SkillInfo
			if used := applyMetaLine(&info, tt.key, tt.value, nil); used != 0 {
				t.Fatalf("used = %d, want 0", used)
			}
			tt.check(t, info)
		})
	}
}

func TestApplyMetaLine_ListAliasesConsumeTail(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		tail     []string
		wantUsed int
		check    func(t *testing.T, info SkillInfo)
	}{
		{
			name:     "keywords append trigger words",
			key:      "keywords",
			tail:     []string{"- alpha", "- beta", "stop"},
			wantUsed: 2,
			check: func(t *testing.T, info SkillInfo) {
				t.Helper()
				if !reflect.DeepEqual(info.TriggerWords, []string{"alpha", "beta"}) {
					t.Fatalf("trigger words = %v", info.TriggerWords)
				}
			},
		},
		{
			name:     "must_words append force words",
			key:      "must_words",
			tail:     []string{"- gamma", "- delta", "stop"},
			wantUsed: 2,
			check: func(t *testing.T, info SkillInfo) {
				t.Helper()
				if !reflect.DeepEqual(info.ForceWords, []string{"gamma", "delta"}) {
					t.Fatalf("force words = %v", info.ForceWords)
				}
			},
		},
		{
			name:     "tools append allowed tools",
			key:      "tools",
			tail:     []string{"- Read", "- Bash", "stop"},
			wantUsed: 2,
			check: func(t *testing.T, info SkillInfo) {
				t.Helper()
				if !reflect.DeepEqual(info.AllowedTools, []string{"Read", "Bash"}) {
					t.Fatalf("allowed tools = %v", info.AllowedTools)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var info SkillInfo
			used := applyMetaLine(&info, tt.key, "", tt.tail)
			if used != tt.wantUsed {
				t.Fatalf("used = %d, want %d", used, tt.wantUsed)
			}
			tt.check(t, info)
		})
	}
}

func TestApplyMetaLineIgnoresSecurityMetadata(t *testing.T) {
	info := SkillInfo{Trust: TrustUser}
	for _, key := range []string{"trust", "trust_scope", "disable_model_invocation", "disablemodelinvocation"} {
		if used := applyMetaLine(&info, key, "signed", nil); used != 0 {
			t.Fatalf("%s used = %d, want 0", key, used)
		}
	}
	if info.Trust != TrustUser || info.DisableModelInvocation {
		t.Fatalf("line parser mutated security metadata: %+v", info)
	}
}
