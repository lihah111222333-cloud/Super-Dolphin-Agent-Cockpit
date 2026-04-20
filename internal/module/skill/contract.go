package skill

import (
	"context"
	"errors"
	"strings"
)

type skillCWDContextKey struct{}

var ErrMissingCWD = errors.New("cwd is required")
var ErrInvalidSkillScope = errors.New("invalid skill scope")

// WithCWD scopes a skill request to a specific cwd. Empty cwd is a no-op so
// downstream callers can detect the missing scope explicitly.
func WithCWD(ctx context.Context, cwd string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ctx
	}
	return context.WithValue(ctx, skillCWDContextKey{}, cwd)
}

func cwdFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(skillCWDContextKey{}).(string)
	return strings.TrimSpace(value)
}

func requireCWD(ctx context.Context) (string, error) {
	cwd := cwdFromContext(ctx)
	if cwd == "" {
		return "", ErrMissingCWD
	}
	return cwd, nil
}

func RequireCWD(ctx context.Context) (string, error) {
	return requireCWD(ctx)
}

type Service interface {
	ExecCommand(ctx context.Context, command string, args []string, cwd string, env map[string]string) (ExecResult, error)
	ListSkills(ctx context.Context) ([]SkillInfo, error)
	ReadLocal(ctx context.Context, path string) (any, error)
	ListLocalFiles(ctx context.Context, p listSkillFilesParams) (any, error)
	WriteLocal(ctx context.Context, path, content string, scope ...string) (any, error)
	ImportLocalDir(ctx context.Context, p importSkillDirParams) (any, error)
	DeleteLocal(ctx context.Context, name string) (any, error)
	ReadRemote(ctx context.Context, url string) (any, error)
	WriteRemote(ctx context.Context, name, content string) (any, error)
	ReadConfig(ctx context.Context, agentID string) (any, error)
	WriteSkillContent(ctx context.Context, name, content string) (any, error)
	WriteSummary(ctx context.Context, name, summary string) (any, error)
	MatchPreview(ctx context.Context, agentID, threadID, text string, input []UserInput) (any, error)
	Expand(ctx context.Context, p skillExpandParams) (skillExpandResult, error)
	// ExpandBody P20.1 Phase 6：按 name 读取 SKILL.md body（可选 Markdown 锚点切片）。
	ExpandBody(ctx context.Context, p ExpandBodyParams) (ExpandBodyResult, error)
	// ReadResource P20.1 Phase 6：按 name + 相对路径读取 skill 目录内资源文件。
	ReadResource(ctx context.Context, p ReadResourceParams) (ReadResourceResult, error)
}
