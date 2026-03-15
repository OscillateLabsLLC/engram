package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunStdioProxy(t *testing.T) {
	t.Run("returns error when server is unreachable", func(t *testing.T) {
		err := RunStdioProxy("http://localhost:59999")
		if err == nil {
			t.Fatal("Expected error for unreachable server")
		}

		expected := "cannot connect to engram server at http://localhost:59999. Is 'engram serve' running?"
		if err.Error() != expected {
			t.Errorf("Expected error %q, got %q", expected, err.Error())
		}
	})

	t.Run("returns error when health check returns non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}))
		defer srv.Close()

		err := RunStdioProxy(srv.URL)
		if err == nil {
			t.Fatal("Expected error for non-200 health response")
		}

		expected := "engram server at " + srv.URL + " returned status 503"
		if err.Error() != expected {
			t.Errorf("Expected error %q, got %q", expected, err.Error())
		}
	})

	t.Run("returns error when SSE endpoint is unavailable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		err := RunStdioProxy(srv.URL)
		if err == nil {
			t.Fatal("Expected error when SSE endpoint is unavailable")
		}
	})

	t.Run("strips trailing slash from server URL", func(t *testing.T) {
		err := RunStdioProxy("http://localhost:59999/")
		if err == nil {
			t.Fatal("Expected error for unreachable server")
		}

		expected := "cannot connect to engram server at http://localhost:59999. Is 'engram serve' running?"
		if err.Error() != expected {
			t.Errorf("Expected error %q, got %q", expected, err.Error())
		}
	})
}
