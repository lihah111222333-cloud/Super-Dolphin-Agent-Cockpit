package gateclosure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	runtimeDepsRegistryTimeout = 15 * time.Second
	runtimeDepsChallengeLimit  = 4 << 10
	runtimeDepsTokenLimit      = 64 << 10
)

type runtimeDepsRegistry struct {
	baseURL    string
	repository string
	client     *http.Client
	auditURL   func(*url.URL) error
}

var runtimeDepsRegistryFactory = newRuntimeDepsRegistry

type registryManifest struct {
	Data      []byte
	MediaType string
	Digest    string
}

type registryBearerChallenge struct {
	realm   *url.URL
	service string
	scope   string
}

type registryTokenResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	IssuedAt    string `json:"issued_at"`
}

// validateRuntimeDepsRegistry 在昂贵构建前验证 OCI V2 可匿名读取，允许一次标准 Bearer 挑战。
func validateRuntimeDepsRegistry(repository string) error {
	registry, err := newRuntimeDepsRegistry(repository)
	if err != nil {
		return err
	}
	return validateRuntimeDepsRegistryClient(registry)
}

// validateRuntimeDepsRegistryClient 接受公开根端点或合法 Bearer 挑战，并验证 Registry v2 身份。
func validateRuntimeDepsRegistryClient(registry runtimeDepsRegistry) error {
	endpoint := registry.baseURL + "/v2/"
	response, err := registry.doGet(endpoint, "")
	if err != nil {
		return runtimeDepsRegistryPrerequisiteError(endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		if _, err := registry.parseRegistryPingChallengeHeaders(response.Header.Values("WWW-Authenticate")); err != nil {
			return runtimeDepsRegistryPrerequisiteError(endpoint, err)
		}
	} else if response.StatusCode != http.StatusOK {
		return runtimeDepsRegistryPrerequisiteError(endpoint, fmt.Errorf("status %s", response.Status))
	}
	if !strings.HasPrefix(response.Header.Get("Docker-Distribution-Api-Version"), "registry/2") {
		return runtimeDepsRegistryPrerequisiteError(endpoint, errors.New("missing Docker Registry v2 identity header"))
	}
	return nil
}

// runtimeDepsRegistryPrerequisiteError 指向精确 Git tree 的发布与匿名 pull 验证恢复步骤。
func runtimeDepsRegistryPrerequisiteError(endpoint string, cause error) error {
	return fmt.Errorf("runtime dependency registry prerequisite unavailable at %s: %w; recovery: publish the exact Git tree with refresh-dependencies, then verify anonymous pull with curl -fsS %s", endpoint, cause, endpoint)
}

// validateRuntimeDepsRemoteRegistry 仅允许规范 GHCR 目标；连接时再冻结受审计 DNS 解析结果。
func validateRuntimeDepsRemoteRegistry(repository string) error {
	registry, err := newRuntimeDepsRegistry(repository)
	if err != nil {
		return err
	}
	parsed, err := url.Parse(registry.baseURL)
	if err != nil || parsed.Host != "ghcr.io" {
		return errors.New("runtime dependency registry must use canonical ghcr.io host without an explicit port")
	}
	return nil
}

func newRuntimeDepsRegistry(repository string) (runtimeDepsRegistry, error) {
	host, name, found := strings.Cut(repository, "/")
	if !validRuntimeDepsRepository(repository, host, name, found) {
		return runtimeDepsRegistry{}, errors.New("runtime dependency repository path is invalid")
	}
	baseURL, err := runtimeDepsRegistryBaseURL(host)
	if err != nil {
		return runtimeDepsRegistry{}, errors.New("runtime dependency registry host is invalid")
	}
	return runtimeDepsRegistry{
		baseURL: baseURL.String(), repository: name, client: newRuntimeDepsRegistryClient(), auditURL: auditRuntimeDepsHTTPSURL,
	}, nil
}

// validRuntimeDepsRepository 校验 registry repository 的规范主机与路径结构。
func validRuntimeDepsRepository(repository, host, name string, found bool) bool {
	if !found || strings.ContainsAny(repository, "?#%\\ \t\r\n") || host == "" || name == "" {
		return false
	}
	return path.Clean("/"+name) == "/"+name && !strings.HasSuffix(name, "/")
}

func runtimeDepsRegistryBaseURL(host string) (*url.URL, error) {
	baseURL, err := url.Parse("https://" + host)
	if err != nil || baseURL.User != nil || baseURL.Hostname() == "" || baseURL.Path != "" {
		return nil, errors.New("invalid runtime dependency registry host")
	}
	return baseURL, nil
}

func newRuntimeDepsRegistryClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = guardedRuntimeDepsDialContext
	return &http.Client{
		Timeout:   runtimeDepsRegistryTimeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (registry runtimeDepsRegistry) manifestURL(reference string) (string, error) {
	if reference == "" || strings.ContainsAny(reference, "/@?# \t\r\n") {
		return "", errors.New("runtime dependency manifest reference is invalid")
	}
	return registry.baseURL + "/v2/" + registry.repository + "/manifests/" + reference, nil
}

func (registry runtimeDepsRegistry) blobURL(digest string) (string, error) {
	if !validSHA256(digest) {
		return "", errors.New("runtime dependency blob digest is invalid")
	}
	return registry.baseURL + "/v2/" + registry.repository + "/blobs/" + digest, nil
}

func (registry runtimeDepsRegistry) readManifest(reference string) (registryManifest, error) {
	endpoint, err := registry.manifestURL(reference)
	if err != nil {
		return registryManifest{}, err
	}
	response, err := registry.anonymousGet(endpoint, false)
	if err != nil {
		return registryManifest{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return registryManifest{}, fmt.Errorf("registry manifest GET %s returned %s", reference, response.Status)
	}
	return readRegistryManifest(response)
}

// readRegistryManifest 限长读取 manifest 并校验响应摘要与媒体类型。
func readRegistryManifest(response *http.Response) (registryManifest, error) {
	data, err := io.ReadAll(io.LimitReader(response.Body, runtimeDepsManifestLimit+1))
	if err != nil {
		return registryManifest{}, err
	}
	if len(data) == 0 || len(data) > runtimeDepsManifestLimit {
		return registryManifest{}, errors.New("registry manifest payload is empty or exceeds limit")
	}
	digest := digestBytes(data)
	if headerDigest := response.Header.Get("Docker-Content-Digest"); !validSHA256(headerDigest) || headerDigest != digest {
		return registryManifest{}, errors.New("registry manifest digest header is missing or inconsistent")
	}
	mediaType, _, _ := strings.Cut(response.Header.Get("Content-Type"), ";")
	return registryManifest{Data: data, MediaType: strings.TrimSpace(mediaType), Digest: digest}, nil
}

// readConfigBlob 下载受大小和摘要约束的配置 blob，且仅允许经过审计的一跳重定向。
func (registry runtimeDepsRegistry) readConfigBlob(digest string, size int64) ([]byte, error) {
	if size <= 0 || size > runtimeDepsManifestLimit {
		return nil, errors.New("runtime dependency config blob size is invalid")
	}
	endpoint, err := registry.blobURL(digest)
	if err != nil {
		return nil, err
	}
	response, err := registry.anonymousGet(endpoint, true)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry config blob GET returned %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != size || digestBytes(data) != digest {
		return nil, errors.New("runtime dependency config blob digest or size is inconsistent")
	}
	return data, nil
}

// anonymousGet 只为读取操作执行一次精确 Bearer 挑战，发布路径永不调用该逻辑。
func (registry runtimeDepsRegistry) anonymousGet(endpoint string, allowBlobRedirect bool) (*http.Response, error) {
	response, err := registry.doGet(endpoint, "")
	if err != nil || response.StatusCode != http.StatusUnauthorized {
		return registry.followBlobRedirect(response, err, allowBlobRedirect)
	}
	challenge, err := registry.parseBearerChallengeHeaders(response.Header.Values("WWW-Authenticate"))
	response.Body.Close()
	if err != nil {
		return nil, err
	}
	token, err := registry.fetchBearerToken(challenge)
	if err != nil {
		return nil, err
	}
	response, err = registry.doGet(endpoint, "Bearer "+token)
	return registry.followBlobRedirect(response, err, allowBlobRedirect)
}

func (registry runtimeDepsRegistry) doGet(endpoint, authorization string) (*http.Response, error) {
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", strings.Join([]string{ociIndexMediaType, dockerIndexMediaType, ociManifestMediaType, dockerManifestMediaType}, ", "))
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	return registry.client.Do(request)
}

// followBlobRedirect 只接受允许 blob 的一次 HTTPS 跳转，并在跳转前清除所有认证材料。
func (registry runtimeDepsRegistry) followBlobRedirect(response *http.Response, requestErr error, allowed bool) (*http.Response, error) {
	if requestErr != nil || response == nil || !isHTTPRedirect(response.StatusCode) {
		return response, requestErr
	}
	if !allowed {
		response.Body.Close()
		return nil, errors.New("runtime dependency registry redirects are forbidden")
	}
	location := response.Header.Get("Location")
	response.Body.Close()
	target, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("parse runtime dependency blob redirect: %w", err)
	}
	if err := registry.auditRedirect(target); err != nil {
		return nil, err
	}
	return registry.doBlobCDNGet(target.String())
}

// auditRedirect 将 blob 重定向限制为同 registry host，GHCR 仅允许 githubusercontent CDN 后缀。
func (registry runtimeDepsRegistry) auditRedirect(target *url.URL) error {
	if target == nil || !target.IsAbs() || target.Scheme != "https" || target.User != nil || target.Fragment != "" {
		return errors.New("runtime dependency blob redirect must target audited HTTPS CDN URL")
	}
	if !registry.allowedBlobRedirectHost(target.Hostname()) {
		return errors.New("runtime dependency blob redirect host is not allowed")
	}
	if registry.auditURL == nil {
		return errors.New("runtime dependency blob redirect auditor is required")
	}
	return registry.auditURL(target)
}

func (registry runtimeDepsRegistry) doBlobCDNGet(endpoint string) (*http.Response, error) {
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := registry.client.Do(request)
	if err != nil {
		return nil, err
	}
	if isHTTPRedirect(response.StatusCode) {
		response.Body.Close()
		return nil, errors.New("runtime dependency blob redirect exceeds one hop")
	}
	return response, nil
}

func isHTTPRedirect(status int) bool {
	return status == http.StatusMovedPermanently || status == http.StatusFound || status == http.StatusSeeOther ||
		status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect
}

// parseBearerChallengeHeaders 拒绝重复或缺失的 WWW-Authenticate 字段，防止第二条挑战被静默忽略。
func (registry runtimeDepsRegistry) parseBearerChallengeHeaders(headers []string) (registryBearerChallenge, error) {
	return registry.parseBearerChallengeHeadersForScope(headers, true)
}

func (registry runtimeDepsRegistry) parseRegistryPingChallengeHeaders(headers []string) (registryBearerChallenge, error) {
	return registry.parseBearerChallengeHeadersForScope(headers, false)
}

func (registry runtimeDepsRegistry) parseBearerChallengeHeadersForScope(headers []string, exactScope bool) (registryBearerChallenge, error) {
	if len(headers) != 1 {
		return registryBearerChallenge{}, errors.New("runtime dependency registry must return exactly one WWW-Authenticate challenge")
	}
	return registry.parseBearerChallengeForScope(headers[0], exactScope)
}

// parseBearerChallenge 解析唯一的 Bearer 挑战并约束其 realm、service 与 pull scope。
func (registry runtimeDepsRegistry) parseBearerChallenge(header string) (registryBearerChallenge, error) {
	return registry.parseBearerChallengeForScope(header, true)
}

func (registry runtimeDepsRegistry) parseBearerChallengeForScope(header string, exactScope bool) (registryBearerChallenge, error) {
	if len(header) == 0 || len(header) > runtimeDepsChallengeLimit || hasControlCharacters(header) {
		return registryBearerChallenge{}, errors.New("runtime dependency registry Bearer challenge is invalid")
	}
	attributes, err := parseBearerChallengeAttributes(header)
	if err != nil {
		return registryBearerChallenge{}, err
	}
	return registry.validateBearerChallenge(attributes, exactScope)
}

// parseBearerChallengeAttributes 严格解析唯一 Bearer challenge 的属性集合。
func parseBearerChallengeAttributes(header string) (map[string]string, error) {
	scheme, values, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return nil, errors.New("runtime dependency registry must use one Bearer challenge")
	}
	attributes := make(map[string]string, 3)
	for len(values) > 0 {
		key, value, remainder, err := parseBearerAttribute(values)
		if _, exists := attributes[key]; err != nil || exists {
			return nil, errors.New("runtime dependency registry Bearer challenge is malformed")
		}
		attributes[key] = value
		values = remainder
	}
	return attributes, nil
}

// parseBearerAttribute 解析一个双引号 Bearer 属性并返回剩余输入。
func parseBearerAttribute(values string) (string, string, string, error) {
	key, rest, found := strings.Cut(values, "=")
	if !found || key == "" || strings.ContainsAny(key, " ,\t\r\n") || !strings.HasPrefix(rest, "\"") {
		return "", "", "", errors.New("invalid Bearer attribute")
	}
	valueEnd := strings.Index(rest[1:], "\"")
	if valueEnd < 0 {
		return "", "", "", errors.New("unterminated Bearer attribute")
	}
	value := rest[1 : valueEnd+1]
	remainder := rest[valueEnd+2:]
	if remainder == "" {
		return key, value, "", nil
	}
	if !strings.HasPrefix(remainder, ",") || len(remainder) == 1 {
		return "", "", "", errors.New("invalid Bearer attribute separator")
	}
	return key, value, remainder[1:], nil
}

func (registry runtimeDepsRegistry) validateBearerChallenge(attributes map[string]string, exactScope bool) (registryBearerChallenge, error) {
	if err := registry.validateBearerScope(attributes, exactScope); err != nil {
		return registryBearerChallenge{}, err
	}
	realm, err := registry.validateBearerRealm(attributes["realm"])
	if err != nil {
		return registryBearerChallenge{}, err
	}
	scope := attributes["scope"]
	if exactScope {
		scope = registry.pullScope()
	}
	return registryBearerChallenge{realm: realm, service: attributes["service"], scope: scope}, nil
}

// validateBearerScope 校验匿名 token 只授予当前 repository 的 pull scope。
func (registry runtimeDepsRegistry) validateBearerScope(attributes map[string]string, exactScope bool) error {
	if !validBearerChallengeShape(attributes) {
		return errors.New("runtime dependency registry Bearer challenge is incomplete")
	}
	if err := registry.validateBearerScopeValue(attributes["scope"], exactScope); err != nil {
		return err
	}
	if attributes["service"] != registry.serviceHost() {
		return errors.New("runtime dependency registry Bearer service does not match registry host")
	}
	return nil
}

func validBearerChallengeShape(attributes map[string]string) bool {
	return len(attributes) >= 2 && len(attributes) <= 3 && attributes["realm"] != "" && attributes["service"] != ""
}

// validateBearerScopeValue 区分目标 manifest 的精确 scope 与 registry ping 的占位 pull scope。
func (registry runtimeDepsRegistry) validateBearerScopeValue(scope string, exactScope bool) error {
	if scope == "" {
		return nil
	}
	if exactScope && scope != registry.pullScope() {
		return errors.New("runtime dependency registry Bearer scope is not exact repository pull scope")
	}
	if !exactScope && (!strings.HasPrefix(scope, "repository:") || !strings.HasSuffix(scope, ":pull")) {
		return errors.New("runtime dependency registry ping Bearer scope is invalid")
	}
	return nil
}

// validateBearerRealm 校验 token realm 使用受允许主机上的 HTTPS 地址。
func (registry runtimeDepsRegistry) validateBearerRealm(value string) (*url.URL, error) {
	realm, err := url.Parse(value)
	if err != nil || realm.Scheme != "https" || realm.User != nil || realm.Fragment != "" || realm.Hostname() == "" {
		return nil, errors.New("runtime dependency registry Bearer realm is invalid")
	}
	if !registry.allowedRealmHost(realm.Hostname()) {
		return nil, errors.New("runtime dependency registry Bearer realm host is not allowed")
	}
	if registry.auditURL == nil {
		return nil, errors.New("runtime dependency registry Bearer realm auditor is required")
	}
	if err := registry.auditURL(realm); err != nil {
		return nil, err
	}
	return realm, nil
}

// fetchBearerToken 从受审计 realm 获取一次有界匿名 token，且不接受 realm 预置授权查询。
func (registry runtimeDepsRegistry) fetchBearerToken(challenge registryBearerChallenge) (string, error) {
	query := challenge.realm.Query()
	if query.Has("service") || query.Has("scope") {
		return "", errors.New("runtime dependency registry Bearer realm predefines service or scope")
	}
	query.Set("service", challenge.service)
	if challenge.scope != "" {
		query.Set("scope", challenge.scope)
	}
	tokenURL := *challenge.realm
	tokenURL.RawQuery = query.Encode()
	response, err := registry.client.Get(tokenURL.String())
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("runtime dependency registry token endpoint returned %s", response.Status)
	}
	return decodeRegistryBearerToken(response.Body)
}

func decodeRegistryBearerToken(body io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(body, runtimeDepsTokenLimit+1))
	if err != nil {
		return "", err
	}
	if !validRegistryTokenPayload(data) {
		return "", errors.New("runtime dependency registry token response is invalid")
	}
	response, err := decodeRegistryTokenResponse(data)
	if err != nil {
		return "", err
	}
	return registryToken(response)
}

func validRegistryTokenPayload(data []byte) bool {
	return len(bytes.TrimSpace(data)) > 0 && len(data) <= runtimeDepsTokenLimit
}

func decodeRegistryTokenResponse(data []byte) (registryTokenResponse, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var response registryTokenResponse
	if err := decoder.Decode(&response); err != nil {
		return registryTokenResponse{}, fmt.Errorf("decode runtime dependency registry token: %w", err)
	}
	if err := rejectTrailingDocument(decoder); err != nil {
		return registryTokenResponse{}, err
	}
	return response, nil
}

// registryToken 提取唯一且不含控制字符的匿名 Bearer token。
func registryToken(response registryTokenResponse) (string, error) {
	token := response.Token
	if token == "" {
		token = response.AccessToken
	}
	if token == "" || (response.Token != "" && response.AccessToken != "" && response.Token != response.AccessToken) || hasControlCharacters(token) {
		return "", errors.New("runtime dependency registry token is invalid")
	}
	return token, nil
}

func (registry runtimeDepsRegistry) pullScope() string {
	return "repository:" + registry.repository + ":pull"
}

func (registry runtimeDepsRegistry) serviceHost() string {
	parsed, _ := url.Parse(registry.baseURL)
	return strings.ToLower(parsed.Host)
}

// allowedRealmHost 限制 token realm 在 registry 的解析 host，避免匿名 token 请求跨主机。
func (registry runtimeDepsRegistry) allowedRealmHost(host string) bool {
	realmHost := normalizedRuntimeDepsHost(host)
	registryHost := registry.registryHost()
	return realmHost == registryHost || registryHost == "ghcr.io" && realmHost == "ghcr.io"
}

// allowedBlobRedirectHost 限制 blob 到 registry host；GHCR 可以使用其 githubusercontent CDN 域。
func (registry runtimeDepsRegistry) allowedBlobRedirectHost(host string) bool {
	targetHost := normalizedRuntimeDepsHost(host)
	registryHost := registry.registryHost()
	return targetHost == registryHost || registryHost == "ghcr.io" && isGHCRBlobCDNHost(targetHost)
}

// isGHCRBlobCDNHost 只接受 githubusercontent.com 本身或其受控子域，拒绝相似拼写域。
func isGHCRBlobCDNHost(host string) bool {
	return host == "githubusercontent.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

// registryHost 返回不含端口的规范 registry 主机名，供 redirect 和 realm 域名比较使用。
func (registry runtimeDepsRegistry) registryHost() string {
	parsed, _ := url.Parse(registry.baseURL)
	return normalizedRuntimeDepsHost(parsed.Hostname())
}

// auditRuntimeDepsHTTPSURL 对允许 URL 执行 HTTPS 形状校验和 DNS/IP 审计。
func auditRuntimeDepsHTTPSURL(target *url.URL) error {
	if target == nil || target.Scheme != "https" || target.User != nil || target.Fragment != "" {
		return errors.New("runtime dependency URL must be audited HTTPS without credentials or fragment")
	}
	return validateRuntimeDepsResolvedHost(target.Hostname())
}

func guardedRuntimeDepsDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return dialRuntimeDepsAddress(ctx, network, address, net.DefaultResolver.LookupNetIP, (&net.Dialer{}).DialContext)
}

type runtimeDepsLookupIP func(context.Context, string, string) ([]netip.Addr, error)

type runtimeDepsDialContext func(context.Context, string, string) (net.Conn, error)

// dialRuntimeDepsAddress 冻结经审计的公网 DNS 结果后直连该地址。
func dialRuntimeDepsAddress(ctx context.Context, network, address string, lookup runtimeDepsLookupIP, dial runtimeDepsDialContext) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, errors.New("runtime dependency registry dial address is invalid")
	}
	addresses, err := lookup(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve runtime dependency registry host: %w", err)
	}
	ip, err := validatedRuntimeDepsIP(host, addresses)
	if err != nil {
		return nil, err
	}
	return dial(ctx, network, net.JoinHostPort(ip.String(), port))
}

func validateRuntimeDepsResolvedHost(host string) error {
	addresses, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip", host)
	if err != nil {
		return fmt.Errorf("resolve runtime dependency URL host: %w", err)
	}
	_, err = validatedRuntimeDepsIP(host, addresses)
	return err
}

// validatedRuntimeDepsIP 要求全部 DNS 结果均为允许的公网地址并选择首个地址。
func validatedRuntimeDepsIP(host string, addresses []netip.Addr) (netip.Addr, error) {
	if len(addresses) == 0 {
		return netip.Addr{}, errors.New("runtime dependency host resolved without addresses")
	}
	var selected netip.Addr
	for _, address := range addresses {
		candidate := address.Unmap()
		if !candidate.IsValid() || prohibitedRuntimeDepsIP(candidate) && !allowedRuntimeDepsProxyAddress(host, candidate) {
			return netip.Addr{}, errors.New("runtime dependency host resolved to prohibited address")
		}
		if !selected.IsValid() {
			selected = candidate
		}
	}
	return selected, nil
}

// allowedRuntimeDepsProxyAddress 仅为已锁定的 GHCR TLS 域接受 Docker Desktop 使用的基准测试代理网段。
func allowedRuntimeDepsProxyAddress(host string, address netip.Addr) bool {
	host = normalizedRuntimeDepsHost(host)
	return runtimeDepsBenchmarkProxyPrefix().Contains(address.Unmap()) &&
		(host == "ghcr.io" || isGHCRBlobCDNHost(host))
}

// prohibitedRuntimeDepsIP 判断地址是否属于本机、私网或保留网络。
func prohibitedRuntimeDepsIP(address netip.Addr) bool {
	address = address.Unmap()
	if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsUnspecified() || address.IsMulticast() {
		return true
	}
	return runtimeDepsReservedPrefixes().contains(address)
}

type runtimeDepsIPPrefixes []netip.Prefix

func (prefixes runtimeDepsIPPrefixes) contains(address netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func runtimeDepsReservedPrefixes() runtimeDepsIPPrefixes {
	return []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.2.0/24"),
		runtimeDepsBenchmarkProxyPrefix(), netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("2001:db8::/32"),
	}
}

func runtimeDepsBenchmarkProxyPrefix() netip.Prefix {
	return netip.MustParsePrefix("198.18.0.0/15")
}

func normalizedRuntimeDepsHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func hasControlCharacters(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0
}
