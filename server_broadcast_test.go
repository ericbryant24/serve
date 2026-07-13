package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestBroadcastConcurrent verifies that broadcasting from many goroutines at
// once does not race or panic. gorilla/websocket panics with "concurrent write
// to websocket connection" if two goroutines call WriteMessage on the same
// conn simultaneously, and its isWriting/writeBuf fields are unsynchronized so
// `go test -race` reports the data race even when the panic's timing window is
// missed. Run with -race for the definitive signal.
func TestBroadcastConcurrent(t *testing.T) {
	s := &Server{wsClients: map[*websocket.Conn]*sync.Mutex{}}

	srv := httptest.NewServer(http.HandlerFunc(s.handleWebSocket))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	const nClients = 8
	clients := make([]*websocket.Conn, 0, nClients)
	for i := 0; i < nClients; i++ {
		c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		defer c.Close()
		// Drain continuously so server-side writes never block (keeps the test
		// from hanging); the overlap window stays wide enough for -race.
		go func(c *websocket.Conn) {
			for {
				if _, _, err := c.ReadMessage(); err != nil {
					return
				}
			}
		}(c)
		clients = append(clients, c)
	}

	// Wait for the server to register all connections.
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		n := len(s.wsClients)
		s.mu.Unlock()
		if n == nClients {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d clients registered", n, nClients)
		}
		time.Sleep(5 * time.Millisecond)
	}

	var mu sync.Mutex
	var panics []interface{}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					panics = append(panics, r)
					mu.Unlock()
				}
			}()
			for k := 0; k < 40; k++ {
				s.broadcast(map[string]interface{}{"type": "reload", "g": g, "k": k})
			}
		}(g)
	}
	wg.Wait()

	if len(panics) > 0 {
		t.Fatalf("broadcast panicked under concurrency (%d goroutines hit it): %v", len(panics), panics[0])
	}
}
