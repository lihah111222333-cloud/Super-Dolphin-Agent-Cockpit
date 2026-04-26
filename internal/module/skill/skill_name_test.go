package skill

import (
	"errors"
	"strings"
	"testing"
)

func Test_validateSkillName_AcceptsChineseLetters(t *testing.T) {
	names := []string{
		"使用git工作区",
		"使用超能力",
		"头脑风暴",
		"子代理驱动开发",
		"完成前验证",
		"执行计划",
		"接收代码审查",
		"测试驱动开发",
		"系统化调试",
		"结束开发分支",
		"编写技能",
		"编写计划",
		"请求代码审查",
		"调度并行代理",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			assertValidSkillName(t, name)
		})
	}
}

func Test_validateSkillName_AcceptsAsciiLetters_BackwardCompat(t *testing.T) {
	for _, name := range []string{"my-skill", "skill1"} {
		t.Run(name, func(t *testing.T) {
			assertValidSkillName(t, name)
		})
	}
}

func Test_validateSkillName_RejectsLeadingHyphen(t *testing.T) {
	assertInvalidSkillName(t, "-bad")
}

func Test_validateSkillName_RejectsLeadingUnderscore(t *testing.T) {
	assertInvalidSkillName(t, "_bad")
}

func Test_validateSkillName_RejectsControlCharacters(t *testing.T) {
	for _, name := range []string{"bad\x00name", "bad\x01name", "bad\nname"} {
		t.Run(name, func(t *testing.T) {
			assertInvalidSkillName(t, name)
		})
	}
}

func Test_validateSkillName_RejectsPathTraversal(t *testing.T) {
	for _, name := range []string{"bad/name", `bad\name`, "../bad", "bad..name"} {
		t.Run(name, func(t *testing.T) {
			assertInvalidSkillName(t, name)
		})
	}
}

func Test_validateSkillName_RejectsTooLong(t *testing.T) {
	assertInvalidSkillName(t, strings.Repeat("a", 65))
}

func Test_validateSkillName_RejectsEmpty(t *testing.T) {
	assertInvalidSkillName(t, "")
}

func Test_validateSkillName_AcceptsMixedZHEN(t *testing.T) {
	for _, name := range []string{"Skill-中文-Test_v2", "skill-中文-Test_2"} {
		t.Run(name, func(t *testing.T) {
			assertValidSkillName(t, name)
		})
	}
}

func Test_validateSkillName_RejectsDangerousChars(t *testing.T) {
	for _, ch := range []string{"<", ">", ":", "*", "?", `"`, "|"} {
		name := "bad" + ch + "name"
		t.Run(name, func(t *testing.T) {
			assertInvalidSkillName(t, name)
		})
	}
}

func assertValidSkillName(t *testing.T, name string) {
	t.Helper()
	got, err := validateSkillName(name)
	if err != nil {
		t.Fatalf("validateSkillName(%q) unexpected err: %v", name, err)
	}
	if got != strings.TrimSpace(name) {
		t.Fatalf("validateSkillName(%q) = %q, want trimmed input", name, got)
	}
}

func assertInvalidSkillName(t *testing.T, name string) {
	t.Helper()
	if got, err := validateSkillName(name); err == nil {
		t.Fatalf("validateSkillName(%q) = %q, want error", name, got)
	} else if !errors.Is(err, ErrInvalidSkillName) {
		t.Fatalf("validateSkillName(%q) err = %v, want ErrInvalidSkillName", name, err)
	}
}
