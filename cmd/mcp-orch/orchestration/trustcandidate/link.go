package trustcandidate

import (
	"fmt"
	"strconv"
	"strings"
)

// NewLinkMetadata 解析并固定 canonical URI，同时保持用户同意与 fetch receipt 为空。
func NewLinkMetadata(uri, redirectPolicyID, egressPolicyDigest string) (LinkMetadata, error) {
	scheme, host, port, path, err := parseURI(uri)
	if err != nil {
		return LinkMetadata{}, err
	}
	if !validText(redirectPolicyID, 256) || !validDigest(egressPolicyDigest) {
		return LinkMetadata{}, fmt.Errorf("%w: redirect/egress policy binding is invalid", ErrInvalidLink)
	}
	link := LinkMetadata{
		URI: uri, URIDigest: hashBytes([]byte(uri)), Scheme: scheme, Host: host, PortOrAbsent: port,
		PathDigest: hashBytes([]byte(path)), RedirectPolicyID: redirectPolicyID,
		EgressPolicyDigest: egressPolicyDigest,
	}
	if err := link.validate(); err != nil {
		return LinkMetadata{}, err
	}
	return link, nil
}

// Validate 校验 URI digest、canonical authority、SSRF denylist 和无副作用 receipt。
func (l LinkMetadata) Validate() error { return l.validate() }

func (l LinkMetadata) validate() error {
	scheme, host, port, path, err := parseURI(l.URI)
	if err != nil {
		return err
	}
	if err := validateLinkDerived(l, scheme, host, port, path); err != nil {
		return err
	}
	return validateLinkPolicy(l)
}

// validateLinkDerived 校验 URI 派生 digest、scheme、host 与 port 字段逐字相等。
func validateLinkDerived(link LinkMetadata, scheme, host, port, path string) error {
	if link.URIDigest != hashBytes([]byte(link.URI)) || link.Scheme != scheme || link.Host != host ||
		link.PortOrAbsent != port || link.PathDigest != hashBytes([]byte(path)) {
		return fmt.Errorf("%w: URI-derived fields do not match", ErrInvalidLink)
	}
	return nil
}

// validateLinkPolicy 校验 redirect/egress policy 与无 receipt 的候选约束。
func validateLinkPolicy(link LinkMetadata) error {
	if !validText(link.RedirectPolicyID, 256) || !validDigest(link.EgressPolicyDigest) {
		return fmt.Errorf("%w: URI policy fields are invalid", ErrInvalidLink)
	}
	if link.UserConsentID != "" || link.FetchReceipt != "" {
		return fmt.Errorf("%w: candidate link cannot claim consent or fetch receipt", ErrInvalidLink)
	}
	return nil
}

// parseURI 只解析和拒绝危险 URI；它不打开连接、不查询 DNS，也不执行 navigation。
func parseURI(uri string) (scheme, host, port, path string, err error) {
	if err := validateURIText(uri); err != nil {
		return "", "", "", "", err
	}
	scheme, remainder, err := parseURIScheme(uri)
	if err != nil {
		return "", "", "", "", err
	}
	authority, path := splitURIRemainder(remainder)
	host, port, err = splitHostPort(authority)
	if err != nil {
		return "", "", "", "", err
	}
	if err := validateURIHost(host, port); err != nil {
		return "", "", "", "", err
	}
	return scheme, host, port, path, nil
}

// validateURIText 拒绝空白、控制字符和超长 URI。
func validateURIText(uri string) error {
	if !validText(uri, 4096) || strings.ContainsAny(uri, " \t\r\n") {
		return fmt.Errorf("%w: URI text is invalid", ErrInvalidLink)
	}
	return nil
}

// parseURIScheme 提取并校验 http/https scheme 与 authority 起始位置。
func parseURIScheme(uri string) (string, string, error) {
	colon := strings.IndexByte(uri, ':')
	if colon <= 0 {
		return "", "", fmt.Errorf("%w: URI scheme is missing", ErrInvalidLink)
	}
	scheme := uri[:colon]
	if scheme != lowerASCII(scheme) || !validScheme(scheme) {
		return "", "", fmt.Errorf("%w: URI scheme is not canonical", ErrInvalidLink)
	}
	if scheme != "http" && scheme != "https" {
		return "", "", fmt.Errorf("%w: URI scheme is not fetch-eligible", ErrInvalidLink)
	}
	if !strings.HasPrefix(uri[colon+1:], "//") {
		return "", "", fmt.Errorf("%w: URI authority is missing", ErrInvalidLink)
	}
	return scheme, uri[colon+3:], nil
}

// splitURIRemainder 按 URI authority 分隔符提取 path/query/fragment 原文。
func splitURIRemainder(remainder string) (authority, path string) {
	cut := len(remainder)
	for _, marker := range []byte{'/', '?', '#'} {
		if index := strings.IndexByte(remainder, marker); index >= 0 && index < cut {
			cut = index
		}
	}
	return remainder[:cut], remainder[cut:]
}

// validateURIHost 校验 authority 的 canonical host、端口和私网拒绝规则。
func validateURIHost(host, port string) error {
	if host == "" || isPrivateHost(host) {
		return fmt.Errorf("%w: URI host is private or malformed", ErrInvalidLink)
	}
	if host != lowerASCII(host) || !validHost(host) || !validPort(port) {
		return fmt.Errorf("%w: URI host is not canonical", ErrInvalidLink)
	}
	return nil
}

// splitHostPort 按 bracketed IPv6 或普通 authority 解析 host/port。
func splitHostPort(authority string) (host, port string, err error) {
	if strings.HasPrefix(authority, "[") {
		return splitBracketedHost(authority)
	}
	return splitPlainHost(authority)
}

// splitBracketedHost 解析方括号 IPv6 authority，拒绝非法后缀。
func splitBracketedHost(authority string) (host, port string, err error) {
	end := strings.IndexByte(authority, ']')
	if end < 0 {
		return "", "", fmt.Errorf("%w: IPv6 bracket is incomplete", ErrInvalidLink)
	}
	host = authority[1:end]
	if end+1 == len(authority) {
		return host, "", nil
	}
	if authority[end+1] != ':' {
		return "", "", fmt.Errorf("%w: IPv6 authority suffix is invalid", ErrInvalidLink)
	}
	if end+2 == len(authority) {
		return "", "", fmt.Errorf("%w: URI port is missing", ErrInvalidLink)
	}
	return host, authority[end+2:], nil
}

// splitPlainHost 解析普通 host:port authority，拒绝未加括号 IPv6。
func splitPlainHost(authority string) (host, port string, err error) {
	if strings.Count(authority, ":") > 1 {
		return "", "", fmt.Errorf("%w: IPv6 address must be bracketed", ErrInvalidLink)
	}
	parts := strings.SplitN(authority, ":", 2)
	host = parts[0]
	if len(parts) == 1 {
		return host, "", nil
	}
	if parts[1] == "" {
		return "", "", fmt.Errorf("%w: URI port is missing", ErrInvalidLink)
	}
	return host, parts[1], nil
}

// validPort 判断显式端口是否为 1..65535 的十进制值。
func validPort(port string) bool {
	if port == "" {
		return true
	}
	value, err := strconv.Atoi(port)
	return err == nil && value > 0 && value <= 65535
}

// validScheme 判断 URI scheme 的 ASCII grammar。
func validScheme(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		if !validSchemeRune(index, char) {
			return false
		}
	}
	return true
}

// validSchemeRune 判断 scheme 中一个字符是否符合 URI grammar。
func validSchemeRune(index int, char rune) bool {
	if char >= 'a' && char <= 'z' {
		return true
	}
	if index == 0 {
		return false
	}
	return char >= '0' && char <= '9' || char == '+' || char == '-' || char == '.'
}

// validHost 判断受限 authority 字符集。
func validHost(value string) bool {
	for _, char := range value {
		if !validHostRune(char) {
			return false
		}
	}
	return true
}

// validHostRune 判断 host 字符是否属于受限 ASCII authority 字符集。
func validHostRune(char rune) bool {
	return char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '.' || char == '-' || char == ':'
}

// isPrivateHost 统一拒绝本地域名、IPv4/IPv6 私有和链路本地目标。
func isPrivateHost(host string) bool {
	lower := lowerASCII(host)
	if isPrivateName(lower) {
		return true
	}
	if strings.Contains(lower, ":") {
		return isPrivateIPv6(lower)
	}
	return isPrivateIPv4(lower)
}

// isPrivateName 拒绝 localhost、容器元数据和本地域名解析入口。
func isPrivateName(lower string) bool {
	for _, suffix := range []string{".localhost", ".local"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return lower == "localhost" || lower == "metadata.google.internal" || lower == "host.docker.internal" || lower == "0.0.0.0"
}

// isPrivateIPv4 解析 dotted IPv4 并交给私网范围判定。
func isPrivateIPv4(lower string) bool {
	parts := strings.Split(lower, ".")
	if len(parts) != 4 {
		return false
	}
	values := [4]int{}
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || value > 255 {
			return false
		}
		values[index] = value
	}
	return privateIPv4Octets(values)
}

// privateIPv4Octets 组合私有、回环、链路本地与共享地址范围。
func privateIPv4Octets(values [4]int) bool {
	if privateIPv4ClassA(values) {
		return true
	}
	return privateIPv4ClassB(values)
}

// privateIPv4ClassA 匹配 0/8、10/8 与 127/8 地址。
func privateIPv4ClassA(values [4]int) bool {
	return values[0] == 10 || values[0] == 127 || values[0] == 0
}

// privateIPv4ClassB 匹配链路本地、RFC1918、共享地址范围。
func privateIPv4ClassB(values [4]int) bool {
	if values[0] == 169 && values[1] == 254 {
		return true
	}
	if values[0] == 192 && values[1] == 168 {
		return true
	}
	return values[0] == 172 && values[1] >= 16 && values[1] <= 31 || values[0] == 100 && values[1] >= 64 && values[1] <= 127
}

// isPrivateIPv6 拒绝 loopback、unspecified、ULA、link-local 与 mapped 私网地址。
func isPrivateIPv6(host string) bool {
	lower := lowerASCII(host)
	if lower == "::1" || lower == "::" {
		return true
	}
	for _, prefix := range []string{"fc", "fd", "fe8", "fe9", "fea", "feb", "::ffff:127.", "::ffff:10.", "::ffff:192.168."} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}
