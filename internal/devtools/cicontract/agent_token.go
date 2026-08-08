package cicontract

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	// AgentTokenFlag 是远程 CI agent 身份唯一允许的命令行入口。
	AgentTokenFlag = "--agent-token"
	// AgentTokenEnvironment 是远程 CI agent 身份唯一允许的环境变量入口。
	AgentTokenEnvironment = "SUPER_DOLPHIN_CI_AGENT_TOKEN"
	// AgentTokenIssueValue 是第二阶段唯一允许的显式令牌签发请求值。
	AgentTokenIssueValue = "issue"
	// AgentTokenDigestAlgorithm 是所有 authority/receipt 持久化身份唯一允许的摘要算法。
	AgentTokenDigestAlgorithm = "sha256"

	agentTokenPrefix       = "sdci1_"
	agentTokenEntropyBytes = 32
)

// AgentTokenPhase 区分首次签发与已认证的后续请求；首次签发绝不执行 CI/ECI。
type AgentTokenPhase string

const (
	AgentTokenPhaseApplication   AgentTokenPhase = "application"
	AgentTokenPhaseIssued        AgentTokenPhase = "issued"
	AgentTokenPhaseAuthenticated AgentTokenPhase = "authenticated"
)

// AgentTokenGuidance 是首次响应中机器可读的同一 agent 重用和重试说明。
type AgentTokenGuidance struct {
	IssueArgument    string `json:"issue_argument"`
	IssueEnvironment string `json:"issue_environment"`
	ReuseFlag        string `json:"reuse_flag"`
	ReuseEnvironment string `json:"reuse_environment"`
	RetryArgument    string `json:"retry_argument"`
}

// AgentTokenApplication 是既未携带命令行令牌也未携带环境变量令牌时的第一阶段响应。
// 它不会签发令牌，也不会启动 CI。
type AgentTokenApplication struct {
	Phase     AgentTokenPhase    `json:"phase"`
	ExecuteCI bool               `json:"execute_ci"`
	Guidance  AgentTokenGuidance `json:"guidance"`
}

// AgentTokenBootstrap 是没有 token 的首次请求唯一允许返回的响应。AgentToken
// 仅可回到调用 CLI 的内存；跨进程边界只能携带 AgentTokenDigest。
type AgentTokenBootstrap struct {
	Phase            AgentTokenPhase    `json:"phase"`
	ExecuteCI        bool               `json:"execute_ci"`
	AgentToken       string             `json:"agent_token"`
	AgentTokenDigest string             `json:"agent_token_digest"`
	Guidance         AgentTokenGuidance `json:"guidance"`
}

// IssueAgentTokenBootstrap 签发不可猜测的一次性引导响应。
// 响应不含执行权限；调用方必须携带该令牌重试后，才能代表同一 agent。
func IssueAgentTokenBootstrap() (AgentTokenBootstrap, error) {
	token, err := GenerateAgentToken()
	if err != nil {
		return AgentTokenBootstrap{}, err
	}
	digest, err := AgentTokenDigest(token)
	if err != nil {
		return AgentTokenBootstrap{}, err
	}
	return AgentTokenBootstrap{
		Phase:            AgentTokenPhaseIssued,
		ExecuteCI:        false,
		AgentToken:       token,
		AgentTokenDigest: digest,
		Guidance: AgentTokenGuidance{
			IssueArgument:    AgentTokenFlag + "=" + AgentTokenIssueValue,
			IssueEnvironment: AgentTokenEnvironment + "=" + AgentTokenIssueValue,
			ReuseFlag:        AgentTokenFlag,
			ReuseEnvironment: AgentTokenEnvironment,
			RetryArgument:    AgentTokenFlag + " <agent-token>",
		},
	}, nil
}

// AgentTokenApplicationResponse 返回第一阶段指引而不生成密钥。
// 调用方必须仅通过一个来源显式提交 issue。
func AgentTokenApplicationResponse() AgentTokenApplication {
	return AgentTokenApplication{
		Phase:     AgentTokenPhaseApplication,
		ExecuteCI: false,
		Guidance: AgentTokenGuidance{
			IssueArgument:    AgentTokenFlag + "=" + AgentTokenIssueValue,
			IssueEnvironment: AgentTokenEnvironment + "=" + AgentTokenIssueValue,
			ReuseFlag:        AgentTokenFlag,
			ReuseEnvironment: AgentTokenEnvironment,
			RetryArgument:    AgentTokenFlag + " <agent-token>",
		},
	}
}

// ClassifyAgentTokenRequest 实现三阶段输入契约。
// 命令行参数与环境变量即使内容相同也互斥。
func ClassifyAgentTokenRequest(flagValue, environmentValue string) (AgentTokenPhase, error) {
	if flagValue != "" && environmentValue != "" {
		return "", errors.New("remote CI agent token flag and environment cannot both be set")
	}
	value := flagValue
	if value == "" {
		value = environmentValue
	}
	if value == "" {
		return AgentTokenPhaseApplication, nil
	}
	if value == AgentTokenIssueValue {
		return AgentTokenPhaseIssued, nil
	}
	if err := ValidateAgentToken(value); err != nil {
		return "", err
	}
	return AgentTokenPhaseAuthenticated, nil
}

// ValidateGitHookAgentToken 强制 hook 边界：hook 不签发也不保存令牌。
// 它只能继承调用方持有的真实环境变量令牌。
func ValidateGitHookAgentToken(environmentValue string) error {
	if environmentValue == "" {
		return errors.New("git hook requires caller-owned SUPER_DOLPHIN_CI_AGENT_TOKEN")
	}
	if environmentValue == AgentTokenIssueValue {
		return errors.New("git hook cannot issue an agent token")
	}
	return ValidateAgentToken(environmentValue)
}

// GenerateAgentToken 生成格式为 sdci1_<base64url 32 bytes> 的规范 token。
func GenerateAgentToken() (string, error) {
	entropy := make([]byte, agentTokenEntropyBytes)
	if _, err := rand.Read(entropy); err != nil {
		return "", fmt.Errorf("generate remote CI agent token: %w", err)
	}
	return agentTokenPrefix + base64.RawURLEncoding.EncodeToString(entropy), nil
}

// AgentTokenDigest 返回仅限调用方持有的原始令牌的规范摘要。
func AgentTokenDigest(token string) (string, error) {
	if err := ValidateAgentToken(token); err != nil {
		return "", err
	}
	return AgentTokenDigestAlgorithm + ":" + fmt.Sprintf("%x", sha256.Sum256([]byte(token))), nil
}

// ValidateAgentToken 拒绝缺失或非规范的调用方令牌。
func ValidateAgentToken(token string) error {
	if strings.TrimSpace(token) != token {
		return errors.New("remote CI agent token has surrounding whitespace")
	}
	if !strings.HasPrefix(token, agentTokenPrefix) {
		return errors.New("remote CI agent token has an invalid prefix")
	}
	encodedEntropy := strings.TrimPrefix(token, agentTokenPrefix)
	entropy, err := base64.RawURLEncoding.DecodeString(encodedEntropy)
	if err != nil || len(entropy) != agentTokenEntropyBytes {
		return errors.New("remote CI agent token has an invalid length")
	}
	if base64.RawURLEncoding.EncodeToString(entropy) != encodedEntropy {
		return errors.New("remote CI agent token is not canonical base64url")
	}
	return nil
}

// ParseAgentToken 解析 CLI 边界的规范原始 token。
func ParseAgentToken(token string) (string, error) {
	if err := ValidateAgentToken(token); err != nil {
		return "", err
	}
	return token, nil
}

// ValidateAgentTokenDigest 在持久化、ECI、OSS、日志、标签、检查点和回执边界拒绝原始令牌及非规范摘要。
func ValidateAgentTokenDigest(digest string) error {
	prefix := AgentTokenDigestAlgorithm + ":"
	if !strings.HasPrefix(digest, prefix) || len(strings.TrimPrefix(digest, prefix)) != sha256.Size*2 {
		return errors.New("remote CI agent token digest is not canonical sha256")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(digest, prefix)); err != nil {
		return errors.New("remote CI agent token digest is not canonical hexadecimal")
	}
	if strings.ToLower(digest) != digest {
		return errors.New("remote CI agent token digest is not lowercase hexadecimal")
	}
	return nil
}

// ValidateAgentTokenContinuation 验证后续请求提供了持久化摘要所代表的原始令牌。
// 摘要不匹配时立即拒绝。
func ValidateAgentTokenContinuation(token, digest string) error {
	if err := ValidateAgentTokenDigest(digest); err != nil {
		return err
	}
	actual, err := AgentTokenDigest(token)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(actual), []byte(digest)) != 1 {
		return errors.New("remote CI agent token does not match its digest")
	}
	return nil
}
