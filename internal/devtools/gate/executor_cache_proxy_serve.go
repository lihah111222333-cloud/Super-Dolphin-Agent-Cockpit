package gate

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// serveGoBuildCacheProxy 顺序处理官方 GOCACHEPROG 请求，并在协议结束时返回首个请求错误。
func serveGoBuildCacheProxy(config goBuildCacheProxyConfig, input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	encoder := json.NewEncoder(output)
	if err := encoder.Encode(goBuildCacheProxyResponse{
		ID: 0, KnownCommands: []string{"get", "put", "close"},
	}); err != nil {
		return err
	}
	var firstRequestError error
	for {
		request, err := readGoBuildCacheProxyRequest(reader)
		if errors.Is(err, io.EOF) {
			return firstRequestError
		}
		if err != nil {
			return errors.Join(firstRequestError, err)
		}
		response, stop, err := handleGoBuildCacheProxyRequest(config, reader, request)
		if err != nil {
			if firstRequestError == nil {
				firstRequestError = fmt.Errorf("handle Go build cache proxy request %d: %w", request.ID, err)
			}
			response = goBuildCacheProxyResponse{ID: request.ID, Err: err.Error()}
		}
		if err := encoder.Encode(response); err != nil {
			return errors.Join(firstRequestError, err)
		}
		if stop {
			return firstRequestError
		}
	}
}
