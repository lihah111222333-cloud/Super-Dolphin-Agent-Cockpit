package skill

import "context"

type Service interface {
	ListCards(ctx context.Context) ([]Card, error)
	GetCard(ctx context.Context, key string) (*Card, error)
	CreateCard(ctx context.Context, card Card) (*Card, error)
	UpdateCard(ctx context.Context, card Card) (*Card, error)
	DeleteCard(ctx context.Context, key string) error
	RunCard(ctx context.Context, key string, args map[string]any) (CardRunResult, error)
	ListCardVersions(ctx context.Context, key string) ([]CardVersion, error)
	ExecCommand(ctx context.Context, command string, args []string, cwd string, env map[string]string) (ExecResult, error)
	ListSkills(ctx context.Context) ([]SkillInfo, error)
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
}
