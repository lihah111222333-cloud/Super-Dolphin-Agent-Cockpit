// Package toolstore 定义 Skill 工具校验、RPC 形状和消费端持久化端口。
package toolstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

const maxLimit = 500

var (
	ErrStoreNotConfigured  = errors.New("skill tool store is not configured")
	ErrIDRequired          = errors.New("skill tool: id is required")
	ErrLimitRequired       = errors.New("skill tool: limit must be positive")
	ErrMethodNameRequired  = errors.New("skill tool: methodName is required")
	ErrMethodNameInvalid   = errors.New("skill tool: methodName is invalid")
	ErrDescriptionRequired = errors.New("skill tool: description is required")
	ErrEnabledRequired     = errors.New("skill tool: enabled is required")
	ErrNotFound            = errors.New("skill tool not found")
)

// Persistence 是 Skill 工具编排所需的消费端持久化窄端口。
type Persistence interface {
	Create(context.Context, MutationParams) (Result, error)
	List(context.Context, ListParams) (ListResult, error)
	Get(context.Context, IDParams) (Result, error)
	GetByMethod(context.Context, MethodParams) (Result, error)
	Update(context.Context, UpdateParams) (Result, error)
	Delete(context.Context, IDParams) error
}

// MutationParams 是 Skill 工具创建和更新共用的领域输入。
type MutationParams struct {
	CWD             string `json:"cwd"`
	MethodName      string `json:"methodName"`
	MethodNameSnake string `json:"method_name,omitempty"`
	Description     string `json:"description"`
	Enabled         *bool  `json:"enabled"`
}

// UpdateParams 按 ID 更新一个 Skill 工具。
type UpdateParams struct {
	ID int64 `json:"id"`
	MutationParams
}

// IDParams 按项目和 ID 定位一个 Skill 工具。
type IDParams struct {
	CWD string `json:"cwd"`
	ID  int64  `json:"id"`
}

// MethodParams 按项目和方法名定位一个 Skill 工具。
type MethodParams struct {
	CWD        string `json:"cwd"`
	MethodName string `json:"methodName"`
}

// ListParams 过滤项目内的 Skill 工具列表。
type ListParams struct {
	CWD     string `json:"cwd"`
	Keyword string `json:"keyword"`
	Limit   int32  `json:"limit"`
}

// Result 是 RPC 和动态工具面共用的 Skill 自有 DTO。
type Result struct {
	ID          int64     `json:"id"`
	CWD         string    `json:"cwd"`
	MethodName  string    `json:"methodName"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ListResult 返回项目内 Skill 工具，并保持空数组 wire 形状。
type ListResult struct {
	Tools []Result `json:"tools"`
}

// DeleteResult 返回 Skill 工具删除确认。
type DeleteResult struct {
	ID      int64 `json:"id"`
	Deleted bool  `json:"deleted"`
}

// CWDResolver 把调用方工作目录解析到项目边界。
type CWDResolver func(string) (string, error)

// ErrorMapper 把领域错误映射到 RPC 错误契约。
type ErrorMapper func(error) error

// ContentReader 在动态工具解析完成后读取 Skill 全文。
type ContentReader func(context.Context, string, string) (string, error)

type normalizedMutation struct {
	CWD         string
	MethodName  string
	Description string
	Enabled     bool
}

// InvalidParamsError 判断错误是否属于 RPC 参数非法类别。
func InvalidParamsError(err error) bool {
	return errors.Is(err, ErrIDRequired) ||
		errors.Is(err, ErrLimitRequired) ||
		errors.Is(err, ErrMethodNameRequired) ||
		errors.Is(err, ErrMethodNameInvalid) ||
		errors.Is(err, ErrDescriptionRequired) ||
		errors.Is(err, ErrEnabledRequired)
}

// Handlers 返回 Skill 工具 CRUD 的 RPC handler 集合。
func Handlers(store Persistence, resolve CWDResolver, mapError ErrorMapper) handler.Map {
	return handler.Map{
		"skills/tools/create": platformrpc.StrictHandler(createHandler(store, resolve, mapError)),
		"skills/tools/list":   platformrpc.StrictHandler(listHandler(store, resolve, mapError)),
		"skills/tools/get":    platformrpc.StrictHandler(getHandler(store, resolve, mapError)),
		"skills/tools/update": platformrpc.StrictHandler(updateHandler(store, resolve, mapError)),
		"skills/tools/delete": platformrpc.StrictHandler(deleteHandler(store, resolve, mapError)),
	}
}

// ListForSurface 返回动态 Provider 工具面需要的已启用工具定义。
func ListForSurface(ctx context.Context, store Persistence, resolve CWDResolver, cwd string) ([]contract.SkillToolSurfaceTool, error) {
	if store == nil {
		return nil, ErrStoreNotConfigured
	}
	resolved := strings.TrimSpace(cwd)
	if err := resolveParamCWD(&resolved, resolve); err != nil {
		return nil, err
	}
	p := ListParams{CWD: resolved, Limit: maxLimit}
	if err := validateListParams(p); err != nil {
		return nil, err
	}
	result, err := store.List(ctx, p)
	if err != nil {
		return nil, err
	}
	tools := make([]contract.SkillToolSurfaceTool, 0, len(result.Tools))
	for _, tool := range result.Tools {
		if tool.Enabled {
			tools = append(tools, contract.SkillToolSurfaceTool{Name: strings.TrimSpace(tool.MethodName), Description: strings.TrimSpace(tool.Description)})
		}
	}
	return tools, nil
}

// CallForSurface 解析已启用工具并读取对应 Skill 全文。
func CallForSurface(ctx context.Context, store Persistence, resolve CWDResolver, read ContentReader, call contract.SkillToolCall) (string, error) {
	if store == nil || read == nil {
		return "", ErrStoreNotConfigured
	}
	resolved := strings.TrimSpace(call.CWD)
	if err := resolveParamCWD(&resolved, resolve); err != nil {
		return "", err
	}
	p := MethodParams{CWD: resolved, MethodName: strings.TrimSpace(call.Name)}
	if err := validateMethodParams(p); err != nil {
		return "", err
	}
	tool, err := store.GetByMethod(ctx, p)
	if err != nil {
		return "", err
	}
	if !tool.Enabled {
		return "", ErrNotFound
	}
	return read(ctx, resolved, tool.MethodName)
}

func createHandler(store Persistence, resolve CWDResolver, mapError ErrorMapper) func(context.Context, MutationParams) (Result, error) {
	return func(ctx context.Context, p MutationParams) (Result, error) {
		if err := resolveParamCWD(&p.CWD, resolve); err != nil {
			return Result{}, mapHandlerError(err, mapError)
		}
		p, err := validatedMutationParams(p)
		if err != nil {
			return Result{}, mapHandlerError(err, mapError)
		}
		if store == nil {
			return Result{}, mapHandlerError(ErrStoreNotConfigured, mapError)
		}
		result, err := store.Create(ctx, p)
		return result, mapHandlerError(err, mapError)
	}
}

func listHandler(store Persistence, resolve CWDResolver, mapError ErrorMapper) func(context.Context, ListParams) (ListResult, error) {
	return func(ctx context.Context, p ListParams) (ListResult, error) {
		if err := resolveParamCWD(&p.CWD, resolve); err != nil {
			return ListResult{}, mapHandlerError(err, mapError)
		}
		if err := validateListParams(p); err != nil {
			return ListResult{}, mapHandlerError(err, mapError)
		}
		if store == nil {
			return ListResult{}, mapHandlerError(ErrStoreNotConfigured, mapError)
		}
		p.Keyword = strings.TrimSpace(p.Keyword)
		p.Limit = normalizeLimit(p.Limit)
		result, err := store.List(ctx, p)
		return result, mapHandlerError(err, mapError)
	}
}

func getHandler(store Persistence, resolve CWDResolver, mapError ErrorMapper) func(context.Context, IDParams) (Result, error) {
	return func(ctx context.Context, p IDParams) (Result, error) {
		if err := resolveParamCWD(&p.CWD, resolve); err != nil {
			return Result{}, mapHandlerError(err, mapError)
		}
		if err := validateIDParams(p); err != nil {
			return Result{}, mapHandlerError(err, mapError)
		}
		if store == nil {
			return Result{}, mapHandlerError(ErrStoreNotConfigured, mapError)
		}
		result, err := store.Get(ctx, p)
		return result, mapHandlerError(err, mapError)
	}
}

func updateHandler(store Persistence, resolve CWDResolver, mapError ErrorMapper) func(context.Context, UpdateParams) (Result, error) {
	return func(ctx context.Context, p UpdateParams) (Result, error) {
		if err := resolveParamCWD(&p.CWD, resolve); err != nil {
			return Result{}, mapHandlerError(err, mapError)
		}
		if p.ID <= 0 {
			return Result{}, mapHandlerError(ErrIDRequired, mapError)
		}
		validated, err := validatedMutationParams(p.MutationParams)
		if err != nil {
			return Result{}, mapHandlerError(err, mapError)
		}
		p.MutationParams = validated
		if store == nil {
			return Result{}, mapHandlerError(ErrStoreNotConfigured, mapError)
		}
		result, err := store.Update(ctx, p)
		return result, mapHandlerError(err, mapError)
	}
}

func deleteHandler(store Persistence, resolve CWDResolver, mapError ErrorMapper) func(context.Context, IDParams) (DeleteResult, error) {
	return func(ctx context.Context, p IDParams) (DeleteResult, error) {
		if err := resolveParamCWD(&p.CWD, resolve); err != nil {
			return DeleteResult{}, mapHandlerError(err, mapError)
		}
		if err := validateIDParams(p); err != nil {
			return DeleteResult{}, mapHandlerError(err, mapError)
		}
		if store == nil {
			return DeleteResult{}, mapHandlerError(ErrStoreNotConfigured, mapError)
		}
		if err := store.Delete(ctx, p); err != nil {
			return DeleteResult{}, mapHandlerError(err, mapError)
		}
		return DeleteResult{ID: p.ID, Deleted: true}, nil
	}
}

func resolveParamCWD(cwd *string, resolve CWDResolver) error {
	if resolve == nil {
		return ErrStoreNotConfigured
	}
	resolved, err := resolve(*cwd)
	if err != nil {
		return err
	}
	*cwd = resolved
	return nil
}

func mapHandlerError(err error, mapError ErrorMapper) error {
	if err == nil {
		return nil
	}
	if mapError == nil {
		return err
	}
	return mapError(err)
}

func validatedMutationParams(p MutationParams) (MutationParams, error) {
	normalized, err := normalizeMutation(p)
	if err != nil {
		return MutationParams{}, err
	}
	p.CWD = normalized.CWD
	p.MethodName = normalized.MethodName
	p.MethodNameSnake = ""
	p.Description = normalized.Description
	p.Enabled = &normalized.Enabled
	return p, nil
}

func normalizeMutation(p MutationParams) (normalizedMutation, error) {
	cwd := strings.TrimSpace(p.CWD)
	if cwd == "" {
		return normalizedMutation{}, ErrStoreNotConfigured
	}
	methodName := strings.TrimSpace(firstNonEmpty(p.MethodName, p.MethodNameSnake))
	if err := validateMethodName(methodName); err != nil {
		return normalizedMutation{}, err
	}
	description := strings.TrimSpace(p.Description)
	if description == "" {
		return normalizedMutation{}, ErrDescriptionRequired
	}
	if p.Enabled == nil {
		return normalizedMutation{}, ErrEnabledRequired
	}
	return normalizedMutation{CWD: cwd, MethodName: methodName, Description: description, Enabled: *p.Enabled}, nil
}

func validateListParams(p ListParams) error {
	if strings.TrimSpace(p.CWD) == "" {
		return ErrStoreNotConfigured
	}
	if p.Limit <= 0 {
		return ErrLimitRequired
	}
	return nil
}

func validateIDParams(p IDParams) error {
	if strings.TrimSpace(p.CWD) == "" {
		return ErrStoreNotConfigured
	}
	if p.ID <= 0 {
		return ErrIDRequired
	}
	return nil
}

func validateMethodParams(p MethodParams) error {
	if strings.TrimSpace(p.CWD) == "" {
		return ErrStoreNotConfigured
	}
	return validateMethodName(p.MethodName)
}

func validateMethodName(methodName string) error {
	methodName = strings.TrimSpace(methodName)
	if methodName == "" {
		return ErrMethodNameRequired
	}
	if strings.ContainsAny(methodName, " \t\r\n/\\") {
		return fmt.Errorf("%w: %s", ErrMethodNameInvalid, methodName)
	}
	return nil
}

func normalizeLimit(limit int32) int32 {
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
