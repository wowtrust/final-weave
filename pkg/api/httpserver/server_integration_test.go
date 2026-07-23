package httpserver

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestServeAndContextBoundedShutdown(t *testing.T) {
	t.Parallel()

	server, _ := newTestServer(t, DefaultConfig(), unavailableProvider())
	address, stop := startTestServer(t, server)

	response, err := (&http.Client{Timeout: time.Second}).Get("http://" + address + livezPath)
	if err != nil {
		t.Fatalf("GET /livez error = %v", err)
	}
	assertSecurityHeaders(t, response)
	assertResponse(t, response, fiber.StatusOK, `{"status":"live"}`)

	stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
}

func TestRealListenerRejectsOversizedHeadersWithoutDisclosure(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.ReadBufferBytes = 1_024
	server, logs := newTestServer(t, config, unavailableProvider())
	address, stop := startTestServer(t, server)
	defer stop()

	request, err := http.NewRequest(http.MethodGet, "http://"+address+livezPath, http.NoBody)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	request.Header.Set("X-Oversized", strings.Repeat("header-secret", 200))
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatalf("oversized-header request error = %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != fiber.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf(
			"oversized-header status = %d, want %d",
			response.StatusCode,
			fiber.StatusRequestHeaderFieldsTooLarge,
		)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("io.ReadAll(response.Body) error = %v", err)
	}
	if strings.TrimSpace(string(body)) != `{"error":"request_headers_too_large"}` {
		t.Fatalf("oversized-header body = %q", body)
	}
	if strings.Contains(logs.String(), "header-secret") {
		t.Fatalf("oversized header leaked to logs: %q", logs.String())
	}
}

func TestRealListenerReturnsStableBodyLimitError(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.MaxHTTPWireBytes = 64
	server, logs := newTestServer(t, config, unavailableProvider())
	address, stop := startTestServer(t, server)
	defer stop()

	request, err := http.NewRequest(
		http.MethodGet,
		"http://"+address+readyzPath,
		strings.NewReader(strings.Repeat("body-secret", 7)),
	)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatalf("oversized-body request error = %v", err)
	}
	assertResponse(t, response, fiber.StatusRequestEntityTooLarge, `{"error":"request_too_large"}`)
	if strings.Contains(logs.String(), "body-secret") {
		t.Fatalf("oversized body leaked to logs: %q", logs.String())
	}
}

func startTestServer(t *testing.T, server *Server) (string, func()) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, net.ErrClosed) {
				t.Errorf("Serve() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Error("Serve() did not return after Shutdown")
		}
	}

	return listener.Addr().String(), stop
}
