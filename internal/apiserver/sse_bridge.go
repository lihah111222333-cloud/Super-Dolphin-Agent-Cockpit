package apiserver

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
)

// SSEBridge manages Server-Sent Events connections for pushing
// JSON-RPC notifications to clients.
//
// V2 equivalent: manual Notification struct creation + per-client channel
// management scattered in server_transport.go.
// V3: dedicated, self-contained SSE manager.
type SSEBridge struct {
	mu      sync.RWMutex
	clients map[uint64]chan []byte
	nextID  atomic.Uint64
}

// NewSSEBridge creates a new SSE notification bridge.
func NewSSEBridge() *SSEBridge {
	return &SSEBridge{
		clients: make(map[uint64]chan []byte),
	}
}

// Broadcast sends a JSON-RPC notification to all connected SSE clients.
func (b *SSEBridge) Broadcast(method string, params any) {
	data, err := MarshalNotification(method, params)
	if err != nil {
		return
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, ch := range b.clients {
		select {
		case ch <- data:
		default:
			// Drop if client buffer full — prevent slow client from blocking
		}
	}
}

// ServeHTTP handles SSE connections.
func (b *SSEBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Register client
	id := b.nextID.Add(1)
	ch := make(chan []byte, 256)

	b.mu.Lock()
	b.clients[id] = ch
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.clients, id)
		b.mu.Unlock()
		close(ch)
	}()

	// Stream events
	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
