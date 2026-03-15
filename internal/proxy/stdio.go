package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

func RunStdioProxy(serverURL string) error {
	serverURL = strings.TrimRight(serverURL, "/")

	healthClient := &http.Client{Timeout: 2 * time.Second}
	resp, err := healthClient.Get(serverURL + "/health")
	if err != nil {
		return fmt.Errorf("cannot connect to engram server at %s. Is 'engram serve' running?", serverURL)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("engram server at %s returned status %d", serverURL, resp.StatusCode)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sseTransport, err := transport.NewSSE(serverURL + "/mcp/sse")
	if err != nil {
		return fmt.Errorf("failed to create SSE transport: %w", err)
	}

	if err := sseTransport.Start(ctx); err != nil {
		return fmt.Errorf("failed to connect to SSE endpoint: %w", err)
	}
	defer sseTransport.Close()

	sseTransport.SetNotificationHandler(func(notification mcp.JSONRPCNotification) {
		data, err := json.Marshal(notification)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to marshal notification: %v\n", err)
			return
		}
		fmt.Fprintln(os.Stdout, string(data))
	})

	fmt.Fprintf(os.Stderr, "Connected to engram server, proxying messages...\n")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var probe struct {
			ID     *json.RawMessage `json:"id,omitempty"`
			Method string           `json:"method"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to parse message: %v\n", err)
			continue
		}

		if probe.ID == nil {
			var notification mcp.JSONRPCNotification
			if err := json.Unmarshal([]byte(line), &notification); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to parse notification: %v\n", err)
				continue
			}
			if err := sseTransport.SendNotification(ctx, notification); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to forward notification: %v\n", err)
			}
			continue
		}

		var request transport.JSONRPCRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to parse request: %v\n", err)
			continue
		}

		response, err := sseTransport.SendRequest(ctx, request)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to forward request: %v\n", err)
			continue
		}

		data, err := json.Marshal(response)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to marshal response: %v\n", err)
			continue
		}
		fmt.Fprintln(os.Stdout, string(data))
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stdin read error: %w", err)
	}
	return nil
}
