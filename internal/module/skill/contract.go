package skill

import (
	"context"
	"errors"
	"os"
)

type Service interface {
	ExecCommand(ctx context.Context, command string, args []string, cwd string, env map[string]string) (ExecResult, error)
	ListSkills(ctx context.Context) ([]SkillInfo, error)
	Expand(ctx context.Context, p SkillExpandParams) (SkillExpandResult, error)
	ReadLocal(ctx context.Context, path string) (any, error)
	ListLocalFiles(ctx context.Context, p listSkillFilesParams) (any, error)
	WriteLocal(ctx context.Context, path, content string) (any, error)
	ImportLocalDir(ctx context.Context, p importSkillDirParams) (any, error)
	DeleteLocal(ctx context.Context, name string) (any, error)
	ReadRemote(ctx context.Context, url string) (any, error)
	WriteRemote(ctx context.Context, name, content string) (any, error)
	ReadConfig(ctx context.Context, agentID string) (any, error)
	WriteSkillContent(ctx context.Context, name, content string) (any, error)
	WriteSummary(ctx context.Context, name, summary string) (any, error)
	MatchPreview(ctx context.Context, agentID, threadID, text string, input []UserInput) (any, error)
	// ExpandBody P20.1 Phase 6：按 name 读取 SKILL.md body（可选 Markdown 锚点切片）。
	ExpandBody(ctx context.Context, p ExpandBodyParams) (ExpandBodyResult, error)
	// ReadResource P20.1 Phase 6：按 name + 相对路径读取 skill 目录内资源文件。
	ReadResource(ctx context.Context, p ReadResourceParams) (ReadResourceResult, error)
}

func IsExpandInvalidParams(err error) bool {
	return errors.Is(err, ErrInvalidSkillName) || errors.Is(err, errInvalidSkillExpandRequest)
}

func IsExpandNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
