package rpc

import "github.com/creachadair/jrpc2/handler"

func Registry(parts ...handler.Map) handler.Map {
	merged := handler.Map{}
	for _, part := range parts {
		for name, handlerFunc := range part {
			merged[name] = handlerFunc
		}
	}
	return merged
}
