package turn

import (
	"fmt"
	"regexp"

	"github.com/anthropic-ai/super-agent-v3/internal/util/repofingerprint"
)

// Redactor 是 skill 提炼链路的二次脱敏接口。
// Redact 返回脱敏文本、命中的规则名和脱敏器内部错误；未命中不是错误。
//
// hits 非空表示至少一条规则命中；err 非空表示规则执行失败，调用方必须视为脱敏失败。
type Redactor interface {
	Redact(input string) (output string, hits []string, err error)
}

// redactionPattern 描述一条必须覆盖的脱敏规则。
// 命中后会替换为 [REDACTED:<name>] 并记录规则名；新增规则时需同步测试以锁住覆盖面。
type redactionPattern struct {
	name string
	re   *regexp.Regexp
}

// DefaultRedactor 使用固定正则规则集实现 Redactor。
// 规则按声明顺序执行，同一输入允许一次 Redact 命中多条规则。
type DefaultRedactor struct {
	patterns []redactionPattern
}

// NewDefaultRedactor 编译固定脱敏规则；内置规则编译失败会 panic，确保启动阶段暴露问题。
func NewDefaultRedactor() *DefaultRedactor {
	specs := []struct {
		name, expr string
	}{
		{"bearer_token", `(?i)Bearer\s+[A-Za-z0-9\-._~+/]+=*`},
		{"jwt", `eyJ[A-Za-z0-9_=\-]+\.[A-Za-z0-9_=\-]+\.?[A-Za-z0-9_.+/=\-]*`},
		{"anthropic_api_key", `\bsk-ant-[A-Za-z0-9_-]{20,}\b`},
		{"openai_api_key", `\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b`},
		{"github_token", `\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{20,}\b`},
		{"slack_token", `\bxox[baprs]-[A-Za-z0-9-]{10,}\b`},
		{"aws_access_key_id", `\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`},
		{"google_api_key", `\bAIza[0-9A-Za-z_-]{35}\b`},
		{"stripe_secret_key", `\bsk_(?:live|test)_[0-9A-Za-z]{16,}\b`},
		{"npm_token", `\bnpm_[A-Za-z0-9]{36}\b`},
		{"pypi_token", `\bpypi-[A-Za-z0-9_-]{20,}\b`},
		{"private_key_header", `-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`},
		{"age_sops_header", `-----BEGIN AGE ENCRYPTED FILE-----`},
		{"uri_credentials", `(?i)\b[a-z][a-z0-9+.-]*://[^/\s:@]+:[^/\s@]+@`},
		{"ssh_public_key", `\bssh-(?:rsa|ed25519) [A-Za-z0-9+/=]{40,}`},
		// 凭据环境变量值只截到下一个空白，避免误吞后续普通文本。
		{"credential_env", `(?i)\b(OPENAI_API_KEY|ANTHROPIC_API_KEY|AWS_(?:SECRET_)?ACCESS_KEY(?:_ID)?|GITHUB_TOKEN|HF_TOKEN|HUGGINGFACE_TOKEN|SLACK_(?:BOT_)?TOKEN|STRIPE_SECRET_KEY|GOOGLE_API_KEY|NPM_TOKEN|PYPI_TOKEN|DATABASE_URL|POSTGRES_CONNECTION_STRING|SUPER_DOLPHIN_SQLITE_PATH|SUPER_DOLPHIN_INTERNAL_SQLITE_PATH|SENTRY_AUTH_TOKEN|DATABRICKS_TOKEN|AZURE_CLIENT_SECRET)\s*[=:]\s*\S+`},
		// Cookie 头通常是一整行敏感内容，需要整体替换。
		{"http_cookie", `(?i)\b(?:Cookie|Set-Cookie)\s*:\s*[^\r\n]+`},
		// 长 hex 也是合法 base64，先匹配更具体的 hex 才能保留准确命中标签。
		{"long_hex", `\b[0-9a-fA-F]{32,}\b`},
		{"long_base64", `[A-Za-z0-9+/=]{32,}`},
	}
	rs := make([]redactionPattern, 0, len(specs))
	for _, s := range specs {
		re, err := regexp.Compile(s.expr)
		if err != nil {
			// archguard:ignore panic_count -- 内置脱敏正则必须在启动阶段编译成功。
			panic(fmt.Sprintf("redaction pattern %q compile failed: %v", s.name, err))
		}
		rs = append(rs, redactionPattern{name: s.name, re: re})
	}
	return &DefaultRedactor{patterns: rs}
}

// Redact 按声明顺序应用全部脱敏规则；nil receiver 是测试和局部 wiring 的显式 no-op。
func (r *DefaultRedactor) Redact(input string) (string, []string, error) {
	if r == nil || len(r.patterns) == 0 {
		return input, nil, nil
	}
	out := input
	var hits []string
	for _, p := range r.patterns {
		if !p.re.MatchString(out) {
			continue
		}
		replacement := "[REDACTED:" + p.name + "]"
		out = p.re.ReplaceAllString(out, replacement)
		hits = append(hits, p.name)
	}
	return out, hits, nil
}

// RepoFingerprint 生成仓库作用域指纹，复用 shared 实现以保证 turn/skill/cron 结果一致。
func RepoFingerprint(cwd string) string {
	return repofingerprint.MustCompute(cwd)
}

// 编译期断言确保 DefaultRedactor 持续满足 Redactor 接口。
var _ Redactor = (*DefaultRedactor)(nil)
