package gatehook

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// RepositoryIdentity 绑定 hook cwd 所在的活动 Git worktree。
type RepositoryIdentity struct {
	WorktreeRoot string                       `json:"worktree_root"`
	GitDir       string                       `json:"git_dir"`
	CommonDir    string                       `json:"common_dir"`
	ObjectFormat gatecontract.GitObjectFormat `json:"object_format"`
}

// Validate 拒绝缺失或非绝对路径的 repository identity。
func (r RepositoryIdentity) Validate() error {
	for name, value := range map[string]string{
		"worktree_root": r.WorktreeRoot,
		"git_dir":       r.GitDir,
		"common_dir":    r.CommonDir,
	} {
		if strings.TrimSpace(value) == "" || !filepath.IsAbs(value) {
			return fmt.Errorf("repository %s must be an absolute path", name)
		}
	}
	if _, err := gatecontract.ZeroOID(r.ObjectFormat); err != nil {
		return fmt.Errorf("repository object format: %w", err)
	}
	return nil
}

// InvocationIdentity 是 coordinator 用于 owner 鉴权和同事件防重放的稳定身份。
type InvocationIdentity struct {
	Owner string `json:"owner"`
	Key   string `json:"key"`
}

// Validate 校验 owner 与 invocation key 均为 SHA-256 identity。
func (i InvocationIdentity) Validate() error {
	if !validSHA256Identity(i.Owner) {
		return errors.New("invocation owner must be a sha256 identity")
	}
	if !validSHA256Identity(i.Key) {
		return errors.New("invocation key must be a sha256 identity")
	}
	return nil
}

// SubmitRequest 是 hook adapter 交给统一 submit 边界的唯一请求。
type SubmitRequest struct {
	Entrypoint gatecontract.CIEntrypointID `json:"entrypoint"`
	Profile    gatecontract.Profile        `json:"profile"`
	Repository RepositoryIdentity          `json:"repository"`
	Invocation InvocationIdentity          `json:"invocation"`
	Source     gatecontract.SourceSpec     `json:"source"`
}

// Validate 复用 canonical entrypoint registry 和 SourceSpec，不复制 gate registry。
func (r SubmitRequest) Validate() error {
	if err := r.Repository.Validate(); err != nil {
		return err
	}
	if err := r.Invocation.Validate(); err != nil {
		return err
	}
	if err := r.Profile.Validate(); err != nil {
		return err
	}
	if err := r.Source.Validate(); err != nil {
		return fmt.Errorf("source: %w", err)
	}
	if r.Repository.ObjectFormat != r.Source.ObjectFormat {
		return errors.New("repository and source object formats differ")
	}
	entrypoint, err := canonicalEntrypoint(r.Entrypoint)
	if err != nil {
		return err
	}
	if !slices.Contains(entrypoint.AllowedSources, r.Source.Kind) {
		return fmt.Errorf("entrypoint %q rejects source kind %q", r.Entrypoint, r.Source.Kind)
	}
	if !slices.Contains(entrypoint.AllowedProfiles, r.Profile) {
		return fmt.Errorf("entrypoint %q rejects profile %q", r.Entrypoint, r.Profile)
	}
	return nil
}

// StatusRequest 是 hook adapter 交给统一 status 边界的查询。
type StatusRequest struct {
	Repository            RepositoryIdentity `json:"repository"`
	Invocation            InvocationIdentity `json:"invocation"`
	ExpectedSourceTreeSHA string             `json:"expected_source_tree_sha"`
	ParentInvocationOnly  bool               `json:"parent_invocation_only"`
}

// Validate 校验 status 查询完整绑定 invocation 与当前 tree。
func (r StatusRequest) Validate() error {
	if err := r.Repository.Validate(); err != nil {
		return err
	}
	if err := r.Invocation.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.ExpectedSourceTreeSHA) == "" {
		return errors.New("expected source tree sha is required")
	}
	return nil
}

// WaitRequest 是 hook adapter 交给统一 wait 边界的短等待请求。
type WaitRequest struct {
	Repository RepositoryIdentity `json:"repository"`
	Invocation InvocationIdentity `json:"invocation"`
	JobID      string             `json:"job_id"`
}

// Validate 校验 wait 请求不接受可注入命令提示的 job id。
func (r WaitRequest) Validate() error {
	if err := r.Repository.Validate(); err != nil {
		return err
	}
	if err := r.Invocation.Validate(); err != nil {
		return err
	}
	return validateToken("job_id", r.JobID)
}

// RequestKind 标识统一 hook request tagged union 的活动分支。
type RequestKind string

const (
	RequestKindSubmit RequestKind = "submit"
	RequestKindStatus RequestKind = "status"
	RequestKindWait   RequestKind = "wait"
)

// Request 只允许携带一个 submit、status 或 wait 请求。
type Request struct {
	Kind   RequestKind    `json:"kind"`
	Submit *SubmitRequest `json:"submit,omitempty"`
	Status *StatusRequest `json:"status,omitempty"`
	Wait   *WaitRequest   `json:"wait,omitempty"`
}

// Validate 校验 request tagged union 严格互斥。
func (r Request) Validate() error {
	count := 0
	for _, present := range []bool{r.Submit != nil, r.Status != nil, r.Wait != nil} {
		if present {
			count++
		}
	}
	if count != 1 {
		return errors.New("exactly one submit, status, or wait request is required")
	}
	switch r.Kind {
	case RequestKindSubmit:
		if r.Submit == nil {
			return errors.New("submit request is required for kind submit")
		}
		return r.Submit.Validate()
	case RequestKindStatus:
		if r.Status == nil {
			return errors.New("status request is required for kind status")
		}
		return r.Status.Validate()
	case RequestKindWait:
		if r.Wait == nil {
			return errors.New("wait request is required for kind wait")
		}
		return r.Wait.Validate()
	default:
		return fmt.Errorf("unsupported request kind %q", r.Kind)
	}
}

// Coordinator 是后续 CLI 或 hook launcher 接线所需的最薄调用面。
type Coordinator interface {
	Submit(context.Context, SubmitRequest) (JobStatus, error)
	Status(context.Context, StatusRequest) (JobStatus, error)
	Wait(context.Context, WaitRequest) (JobStatus, error)
}

func canonicalEntrypoint(id gatecontract.CIEntrypointID) (gatecontract.CIEntrypoint, error) {
	for _, entrypoint := range gatecontract.CIEntrypointRegistry() {
		if entrypoint.ID == id {
			return entrypoint, nil
		}
	}
	return gatecontract.CIEntrypoint{}, fmt.Errorf("unknown CI entrypoint %q", id)
}

// validSHA256Identity 校验带算法前缀的小写 SHA-256 identity。
func validSHA256Identity(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if !isLowerHex(character) {
			return false
		}
	}
	return true
}

// validateToken 校验进入状态提示的 token，阻断换行与命令字符注入。
func validateToken(name, value string) error {
	if strings.TrimSpace(value) == "" || len(value) > 256 {
		return fmt.Errorf("%s is required and must not exceed 256 bytes", name)
	}
	for _, character := range value {
		if !isTokenCharacter(character) {
			return fmt.Errorf("%s contains unsupported character %q", name, character)
		}
	}
	return nil
}

func isLowerHex(character rune) bool {
	return character >= '0' && character <= '9' || character >= 'a' && character <= 'f'
}

// isTokenCharacter 仅允许状态提示中无控制语义的 ASCII token 字符。
func isTokenCharacter(character rune) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9' ||
		strings.ContainsRune("._:-", character)
}
