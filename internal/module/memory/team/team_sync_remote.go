package team

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// 团队记忆远端同步的环境变量和 HTTP 超时。
const (
	envTeamSyncBaseURL    = "MULTI_AGENT_TEAM_SYNC_BASE_URL"
	envTeamSyncOAuthToken = "MULTI_AGENT_TEAM_SYNC_OAUTH_TOKEN"
	teamSyncHTTPTimeout   = 10 * time.Second
)

// ErrTeamSyncOAuthRequired 表示远端同步未配置 base URL 或 OAuth token。
var ErrTeamSyncOAuthRequired = errors.New("team sync oauth is not ready")

// TeamSyncFile 是远端团队记忆文件的传输结构。
type TeamSyncFile struct {
	Content  string `json:"content,omitempty"`
	Checksum string `json:"checksum,omitempty"`
}

// TeamSyncHashesResponse 是远端 hash 快速检查接口的响应。
type TeamSyncHashesResponse struct {
	ETag        string            `json:"etag,omitempty"`
	NotModified bool              `json:"notModified,omitempty"`
	NotFound    bool              `json:"notFound,omitempty"`
	Checksums   map[string]string `json:"checksums,omitempty"`
}

// TeamSyncPullResponse 是远端完整拉取接口的响应。
type TeamSyncPullResponse struct {
	ETag        string                  `json:"etag,omitempty"`
	NotModified bool                    `json:"notModified,omitempty"`
	NotFound    bool                    `json:"notFound,omitempty"`
	Checksums   map[string]string       `json:"checksums,omitempty"`
	Files       map[string]TeamSyncFile `json:"files,omitempty"`
}

// TeamSyncPushRequest 是本地变更推送到远端的请求。
type TeamSyncPushRequest struct {
	RepoSlug      string            `json:"repoSlug,omitempty"`
	IfMatch       string            `json:"ifMatch,omitempty"`
	Uploads       map[string]string `json:"uploads,omitempty"`
	Deletes       []string          `json:"deletes,omitempty"`
	BaseChecksums map[string]string `json:"baseChecksums,omitempty"`
}

// TeamSyncPushResponse 是远端推送接口的响应，包含冲突和容量限制信息。
type TeamSyncPushResponse struct {
	ETag       string            `json:"etag,omitempty"`
	Conflict   bool              `json:"conflict,omitempty"`
	NotFound   bool              `json:"notFound,omitempty"`
	MaxEntries int               `json:"maxEntries,omitempty"`
	Applied    map[string]string `json:"applied,omitempty"`
	Deleted    []string          `json:"deleted,omitempty"`
	Failed     map[string]string `json:"failed,omitempty"`
}

// teamSyncRemote 抽象团队记忆远端服务，便于同步流程测试替换。
type teamSyncRemote interface {
	OAuthReady(context.Context) bool
	PullHashes(context.Context, string, string) (TeamSyncHashesResponse, error)
	PullFiles(context.Context, string, string) (TeamSyncPullResponse, error)
	PushFiles(context.Context, TeamSyncPushRequest) (TeamSyncPushResponse, error)
}

// teamSyncHTTPRemote 是基于 JSON HTTP API 的团队记忆远端实现。
type teamSyncHTTPRemote struct {
	baseURL string
	token   string
	client  *http.Client
}

// newTeamSyncRemoteFromEnv 从环境变量创建远端同步客户端。
func newTeamSyncRemoteFromEnv() teamSyncRemote {
	return &teamSyncHTTPRemote{
		baseURL: strings.TrimRight(strings.TrimSpace(os.Getenv(envTeamSyncBaseURL)), "/"),
		token:   strings.TrimSpace(os.Getenv(envTeamSyncOAuthToken)),
		client:  &http.Client{Timeout: teamSyncHTTPTimeout},
	}
}

// OAuthReady 判断远端 base URL 和 OAuth token 是否都已配置。
func (r *teamSyncHTTPRemote) OAuthReady(context.Context) bool {
	return r != nil && strings.TrimSpace(r.baseURL) != "" && strings.TrimSpace(r.token) != ""
}

// PullHashes 拉取远端 checksum 摘要，ifNoneMatch 非空时会走 If-None-Match 条件请求。
func (r *teamSyncHTTPRemote) PullHashes(ctx context.Context, repoSlug, ifNoneMatch string) (TeamSyncHashesResponse, error) {
	payload := TeamSyncHashesResponse{}
	return payload, r.doJSON(ctx, http.MethodGet, teamSyncRemoteURL(r.baseURL, repoSlug, "hashes"), ifNoneMatch, nil, &payload)
}

// PullFiles 拉取远端完整文件快照。
func (r *teamSyncHTTPRemote) PullFiles(ctx context.Context, repoSlug, _ string) (TeamSyncPullResponse, error) {
	payload := TeamSyncPullResponse{}
	return payload, r.doJSON(ctx, http.MethodGet, teamSyncRemoteURL(r.baseURL, repoSlug, ""), "", nil, &payload)
}

// PushFiles 推送本地上传和删除集合，If-Match 用于发现远端并发变更。
func (r *teamSyncHTTPRemote) PushFiles(ctx context.Context, request TeamSyncPushRequest) (TeamSyncPushResponse, error) {
	payload := TeamSyncPushResponse{}
	return payload, r.doJSON(ctx, http.MethodPost, teamSyncRemoteURL(r.baseURL, request.RepoSlug, ""), request.IfMatch, request, &payload)
}

// doJSON 执行团队记忆远端 JSON 请求并把状态码映射到响应结构。
// OAuth 未就绪时直接返回哨兵错误，避免调用方把未配置误判成远端空数据。
func (r *teamSyncHTTPRemote) doJSON(ctx context.Context, method, rawURL, match string, body any, out any) error {
	if !r.OAuthReady(ctx) {
		return ErrTeamSyncOAuthRequired
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reader, err := teamSyncJSONBodyReader(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return err
	}
	setTeamSyncJSONHeaders(req, r.token, body != nil)
	applyTeamSyncMatchHeader(req, method, match)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	setTeamSyncETag(out, resp.Header.Get("ETag"))
	setTeamSyncMaxEntries(out, resp.Header.Get("X-TeamSync-Max-Entries"))
	return handleTeamSyncJSONResponse(method, rawURL, resp.StatusCode, resp.Status, data, out)
}

// teamSyncJSONBodyReader 将请求体编码为 JSON reader，nil body 对应无请求体。
func teamSyncJSONBodyReader(body any) (io.Reader, error) {
	if body == nil {
		return nil, nil
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(payload), nil
}

// setTeamSyncJSONHeaders 设置鉴权和 JSON 内容协商头。
func setTeamSyncJSONHeaders(req *http.Request, token string, hasBody bool) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
}

// applyTeamSyncMatchHeader 根据请求方法设置 ETag 条件头。
// GET 使用 If-None-Match 做缓存验证，写请求使用 If-Match 做并发冲突保护。
func applyTeamSyncMatchHeader(req *http.Request, method, match string) {
	if match = strings.TrimSpace(match); match == "" {
		return
	}
	if method == http.MethodGet {
		req.Header.Set("If-None-Match", match)
		return
	}
	req.Header.Set("If-Match", match)
}

// handleTeamSyncJSONResponse 将 HTTP 状态转换为 TeamSync 响应字段或错误。
// 409 类冲突通过 412 结构化返回，未知状态直接报错，避免同步流程静默降级。
func handleTeamSyncJSONResponse(method, rawURL string, statusCode int, status string, data []byte, out any) error {
	switch statusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusMultiStatus:
		return decodeOptionalTeamSyncJSON(data, out)
	case http.StatusNotModified:
		setTeamSyncNotModified(out)
		return nil
	case http.StatusNotFound:
		setTeamSyncNotFound(out)
		return nil
	case http.StatusPreconditionFailed:
		setTeamSyncConflict(out)
		decodeBestEffortTeamSyncJSON(data, out)
		return nil
	case http.StatusRequestEntityTooLarge:
		decodeBestEffortTeamSyncJSON(data, out)
		return nil
	default:
		return fmt.Errorf("team sync remote %s %s failed: %s", method, rawURL, status)
	}
}

// decodeOptionalTeamSyncJSON 解码可为空的 JSON 响应体。
func decodeOptionalTeamSyncJSON(data []byte, out any) error {
	if len(data) == 0 || out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// decodeBestEffortTeamSyncJSON 尽力读取错误响应中的结构化字段。
func decodeBestEffortTeamSyncJSON(data []byte, out any) {
	_ = decodeOptionalTeamSyncJSON(data, out)
}

// setTeamSyncETag 把响应头中的 ETag 写回具体响应结构。
func setTeamSyncETag(target any, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	switch typed := target.(type) {
	case *TeamSyncHashesResponse:
		typed.ETag = value
	case *TeamSyncPullResponse:
		typed.ETag = value
	case *TeamSyncPushResponse:
		typed.ETag = value
	}
}

// setTeamSyncMaxEntries 把远端容量上限响应头写入 push 响应。
func setTeamSyncMaxEntries(target any, raw string) {
	if strings.TrimSpace(raw) == "" {
		return
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return
	}
	if typed, ok := target.(*TeamSyncPushResponse); ok {
		typed.MaxEntries = value
	}
}

// setTeamSyncNotModified 把 HTTP 304 结果写入对应响应 DTO，调用方据此跳过文件落盘。
func setTeamSyncNotModified(target any) {
	switch typed := target.(type) {
	case *TeamSyncHashesResponse:
		typed.NotModified = true
	case *TeamSyncPullResponse:
		typed.NotModified = true
	}
}

// setTeamSyncNotFound 把远端仓库缺失状态写入响应 DTO，pull/push 会据此走初始化或提示 OAuth 配置路径。
func setTeamSyncNotFound(target any) {
	switch typed := target.(type) {
	case *TeamSyncHashesResponse:
		typed.NotFound = true
	case *TeamSyncPullResponse:
		typed.NotFound = true
	case *TeamSyncPushResponse:
		typed.NotFound = true
	}
}

// setTeamSyncConflict 标记远端 ETag 冲突，push 流程必须先拉取/重试，不能覆盖他人更新。
func setTeamSyncConflict(target any) {
	if typed, ok := target.(*TeamSyncPushResponse); ok {
		typed.Conflict = true
	}
}

// teamSyncRemoteURL 生成团队记忆远端接口 URL，repoSlug 走路径转义，view 参数走查询转义。
func teamSyncRemoteURL(baseURL, repoSlug, view string) string {
	repoSlug = url.PathEscape(strings.TrimSpace(repoSlug))
	if view = strings.TrimSpace(view); view != "" {
		return baseURL + "/team-memory/" + repoSlug + "?view=" + url.QueryEscape(view)
	}
	return baseURL + "/team-memory/" + repoSlug
}
