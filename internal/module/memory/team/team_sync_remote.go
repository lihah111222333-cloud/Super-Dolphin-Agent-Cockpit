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

const (
	envTeamSyncBaseURL    = "MULTI_AGENT_TEAM_SYNC_BASE_URL"
	envTeamSyncOAuthToken = "MULTI_AGENT_TEAM_SYNC_OAUTH_TOKEN"
	teamSyncHTTPTimeout   = 10 * time.Second
)

var ErrTeamSyncOAuthRequired = errors.New("team sync oauth is not ready")

type TeamSyncFile struct {
	Content  string `json:"content,omitempty"`
	Checksum string `json:"checksum,omitempty"`
}

type TeamSyncHashesResponse struct {
	ETag        string            `json:"etag,omitempty"`
	NotModified bool              `json:"notModified,omitempty"`
	NotFound    bool              `json:"notFound,omitempty"`
	Checksums   map[string]string `json:"checksums,omitempty"`
}

type TeamSyncPullResponse struct {
	ETag        string                  `json:"etag,omitempty"`
	NotModified bool                    `json:"notModified,omitempty"`
	NotFound    bool                    `json:"notFound,omitempty"`
	Checksums   map[string]string       `json:"checksums,omitempty"`
	Files       map[string]TeamSyncFile `json:"files,omitempty"`
}

type TeamSyncPushRequest struct {
	RepoSlug      string            `json:"repoSlug,omitempty"`
	IfMatch       string            `json:"ifMatch,omitempty"`
	Uploads       map[string]string `json:"uploads,omitempty"`
	Deletes       []string          `json:"deletes,omitempty"`
	BaseChecksums map[string]string `json:"baseChecksums,omitempty"`
}

type TeamSyncPushResponse struct {
	ETag       string            `json:"etag,omitempty"`
	Conflict   bool              `json:"conflict,omitempty"`
	NotFound   bool              `json:"notFound,omitempty"`
	MaxEntries int               `json:"maxEntries,omitempty"`
	Applied    map[string]string `json:"applied,omitempty"`
	Deleted    []string          `json:"deleted,omitempty"`
	Failed     map[string]string `json:"failed,omitempty"`
}

type teamSyncRemote interface {
	OAuthReady(context.Context) bool
	PullHashes(context.Context, string, string) (TeamSyncHashesResponse, error)
	PullFiles(context.Context, string, string) (TeamSyncPullResponse, error)
	PushFiles(context.Context, TeamSyncPushRequest) (TeamSyncPushResponse, error)
}

type teamSyncHTTPRemote struct {
	baseURL string
	token   string
	client  *http.Client
}

func newTeamSyncRemoteFromEnv() teamSyncRemote {
	return &teamSyncHTTPRemote{
		baseURL: strings.TrimRight(strings.TrimSpace(os.Getenv(envTeamSyncBaseURL)), "/"),
		token:   strings.TrimSpace(os.Getenv(envTeamSyncOAuthToken)), // guard:allow-secret env lookup, not a literal secret.
		client:  &http.Client{Timeout: teamSyncHTTPTimeout},
	}
}

// OAuthReady 处理o认证ready。
func (r *teamSyncHTTPRemote) OAuthReady(context.Context) bool {
	return r != nil && strings.TrimSpace(r.baseURL) != "" && strings.TrimSpace(r.token) != ""
}

// PullHashes 处理pullhashes。
func (r *teamSyncHTTPRemote) PullHashes(ctx context.Context, repoSlug, ifNoneMatch string) (TeamSyncHashesResponse, error) {
	payload := TeamSyncHashesResponse{}
	return payload, r.doJSON(ctx, http.MethodGet, teamSyncRemoteURL(r.baseURL, repoSlug, "hashes"), ifNoneMatch, nil, &payload)
}

// PullFiles 处理pull文件。
func (r *teamSyncHTTPRemote) PullFiles(ctx context.Context, repoSlug, _ string) (TeamSyncPullResponse, error) {
	payload := TeamSyncPullResponse{}
	return payload, r.doJSON(ctx, http.MethodGet, teamSyncRemoteURL(r.baseURL, repoSlug, ""), "", nil, &payload)
}

// PushFiles 处理push文件。
func (r *teamSyncHTTPRemote) PushFiles(ctx context.Context, request TeamSyncPushRequest) (TeamSyncPushResponse, error) {
	payload := TeamSyncPushResponse{}
	return payload, r.doJSON(ctx, http.MethodPost, teamSyncRemoteURL(r.baseURL, request.RepoSlug, ""), request.IfMatch, request, &payload)
}

// doJSON 处理doJSON。
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

func setTeamSyncJSONHeaders(req *http.Request, token string, hasBody bool) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
}

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

// handleTeamSyncJSONResponse 处理teamsyncJSON响应。
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

func decodeOptionalTeamSyncJSON(data []byte, out any) error {
	if len(data) == 0 || out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func decodeBestEffortTeamSyncJSON(data []byte, out any) {
	_ = decodeOptionalTeamSyncJSON(data, out)
}

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

func setTeamSyncNotModified(target any) {
	switch typed := target.(type) {
	case *TeamSyncHashesResponse:
		typed.NotModified = true
	case *TeamSyncPullResponse:
		typed.NotModified = true
	}
}

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

func setTeamSyncConflict(target any) {
	if typed, ok := target.(*TeamSyncPushResponse); ok {
		typed.Conflict = true
	}
}

func teamSyncRemoteURL(baseURL, repoSlug, view string) string {
	repoSlug = url.PathEscape(strings.TrimSpace(repoSlug))
	if view = strings.TrimSpace(view); view != "" {
		return baseURL + "/team-memory/" + repoSlug + "?view=" + url.QueryEscape(view)
	}
	return baseURL + "/team-memory/" + repoSlug
}
