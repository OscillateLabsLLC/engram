package api

import (
	"bufio"
	"context"
	"net/http"
	"testing"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
)

// TestShutdownWithOpenSSEConnection reproduces the deploy hang: an MCP client
// holds an SSE stream open, which never drains voluntarily. Shutdown must
// return within the grace period (plus slack) by force-closing, so the
// process can reach store.Close() before the platform escalates to SIGKILL.
func TestShutdownWithOpenSSEConnection(t *testing.T) {
	s := setupTestServer(t)
	mcpSrv := mcpserver.NewMCPServer("test", "0.0.0")
	s.AddMCPServer(mcpSrv)

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Serve() }()

	// Wait for the listener
	var addr string
	for i := 0; i < 100; i++ {
		if addr = s.Addr(); addr != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if addr == "" {
		t.Fatal("server never started listening")
	}

	// Open an SSE stream and read its first event to prove it is live
	resp, err := http.Get("http://" + addr + "/mcp/sse")
	if err != nil {
		t.Fatalf("failed to open SSE stream: %v", err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line == "" {
		t.Fatalf("SSE stream not live: %q err=%v", line, err)
	}

	start := time.Now()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed > shutdownGrace+5*time.Second {
		t.Errorf("Shutdown took %s — hung past the grace period", elapsed)
	}

	select {
	case err := <-serveErr:
		if err != nil {
			t.Errorf("Serve returned error after shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Serve did not return after Shutdown")
	}
}

// TestShutdownFastWithNoConnections: with nothing connected, shutdown must
// not burn the grace period.
func TestShutdownFastWithNoConnections(t *testing.T) {
	s := setupTestServer(t)
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Serve() }()
	for i := 0; i < 100; i++ {
		if s.Addr() != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	start := time.Now()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Idle shutdown took %s — should be near-instant", elapsed)
	}
	<-serveErr
}
