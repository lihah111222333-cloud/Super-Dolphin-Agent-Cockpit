package codexapp

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func (p *pendingCall) resolve(result json.RawMessage, err error) {
	p.once.Do(func() {
		p.result = result
		p.err = err
		close(p.done)
	})
}

func normalizeServerURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "ws://") || strings.HasPrefix(raw, "wss://") {
		return raw
	}
	return "ws://" + raw
}

func reserveServerURL() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	addr := listener.Addr().(*net.TCPAddr)
	_ = listener.Close()
	return fmt.Sprintf("ws://127.0.0.1:%d", addr.Port), nil
}

func jsonRPCIDKey(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var num int64
	if err := json.Unmarshal(raw, &num); err == nil {
		return strconv.FormatInt(num, 10)
	}
	return strings.TrimSpace(string(raw))
}

func websocketOrigin(serverURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || parsed.Host == "" {
		return "http://127.0.0.1/"
	}
	scheme := "http"
	if strings.EqualFold(parsed.Scheme, "wss") {
		scheme = "https"
	}
	return scheme + "://" + parsed.Host + "/"
}
