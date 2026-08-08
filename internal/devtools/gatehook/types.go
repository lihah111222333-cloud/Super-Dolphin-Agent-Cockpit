package gatehook

import (
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

// RequestKind 标识统一 hook request 的活动分支。
type RequestKind string

const (
	RequestKindSubmit RequestKind = "submit"
)

// Request 只允许携带一个 submit 请求。
type Request struct {
	Kind   RequestKind    `json:"kind"`
	Submit *SubmitRequest `json:"submit,omitempty"`
}

// Validate 校验 request tagged union 严格互斥。
func (r Request) Validate() error {
	count := 0
	for _, present := range []bool{r.Submit != nil} {
		if present {
			count++
		}
	}
	if count != 1 {
		return errors.New("exactly one submit request is required")
	}
	switch r.Kind {
	case RequestKindSubmit:
		if r.Submit == nil {
			return errors.New("submit request is required for kind submit")
		}
		return r.Submit.Validate()
	default:
		return fmt.Errorf("unsupported request kind %q", r.Kind)
	}
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

func isLowerHex(character rune) bool {
	return character >= '0' && character <= '9' || character >= 'a' && character <= 'f'
}
