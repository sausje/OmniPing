package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Hub pushes live updates to every open page using Server-Sent Events.
// Samples are batched four times a second; other events go out at once.
type Hub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
	samples [][3]any
}

func newHub() *Hub { return &Hub{clients: map[chan []byte]struct{}{}} }

func frame(event string, data any) []byte {
	b, err := json.Marshal(data)
	if err != nil {
		b = []byte("null")
	}
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, b))
}

// Broadcast sends an event to every connected page.
func (h *Hub) Broadcast(event string, data any) {
	f := frame(event, data)
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c <- f:
		default: // a page that cannot keep up is dropped; it reconnects and reloads
			delete(h.clients, c)
			close(c)
		}
	}
}

// Sample queues one ping result for the next batch.
func (h *Hub) Sample(id string, t int64, r float32) {
	h.mu.Lock()
	if len(h.clients) > 0 {
		h.samples = append(h.samples, [3]any{id, t, r})
	}
	h.mu.Unlock()
}

func (h *Hub) run(ctx context.Context) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			h.mu.Lock()
			batch := h.samples
			h.samples = nil
			h.mu.Unlock()
			if len(batch) > 0 {
				h.Broadcast("samples", batch)
			}
		}
	}
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	c := make(chan []byte, 1024)
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if _, ok := h.clients[c]; ok {
			delete(h.clients, c)
			close(c)
		}
		h.mu.Unlock()
	}()

	fmt.Fprint(w, "retry: 3000\n\n")
	w.Write(frame("hello", map[string]any{"now": time.Now().UnixMilli()}))
	fl.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case f, ok := <-c:
			if !ok {
				return
			}
			if _, err := w.Write(f); err != nil {
				return
			}
			fl.Flush()
		case <-ping.C:
			fmt.Fprintf(w, ": keepalive %d\n\n", time.Now().UnixMilli())
			fl.Flush()
		}
	}
}
