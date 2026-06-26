package contract

import "context"

// ProjectsSnapshot 是用户项目目录的 contract 层 DTO。
// 它只携带已知项目路径和当前激活路径，让 UI 层读取项目状态时不反向导入 uistate 模块。
type ProjectsSnapshot struct {
	Projects []string `json:"projects"`
	Active   string   `json:"active"`
}

// UIProjectStateFacade 是 UI 前端枚举项目根目录的窄读接口。
// 生产实现由 uistate adapter 提供；接口只返回 ProjectsSnapshot，避免泄露 uistate 私有结构。
type UIProjectStateFacade interface {
	GetProjects(ctx context.Context) (*ProjectsSnapshot, error)
}
