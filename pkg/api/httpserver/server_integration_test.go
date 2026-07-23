package httpserver

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestServeAndContextBoundedShutdown(t *testing.T) {
	t.Parallel()

	server, _ := newTestServer(t, DefaultConfig(), unavailableTracker())
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
	server, logs := newTestServer(t, config, unavailableTracker())
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
	config.ReadBufferBytes = 1_024
	server, logs := newTestServer(t, config, unavailableTracker())
	address, stop := startTestServer(t, server)
	defer stop()

	request, err := http.NewRequest(
		http.MethodGet,
		"http://"+address+readyzPath,
		strings.NewReader(strings.Repeat("body-secret", 128)),
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

func TestRealListenerRejectsNonGETMethods(t *testing.T) {
	t.Parallel()

	server, _ := newTestServer(t, DefaultConfig(), unavailableTracker())
	address, stop := startTestServer(t, server)
	defer stop()

	request, err := http.NewRequest(http.MethodPost, "http://"+address+readyzPath, http.NoBody)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		t.Fatalf("POST /readyz error = %v", err)
	}
	assertResponse(t, response, fiber.StatusMethodNotAllowed, `{"error":"method_not_allowed"}`)
}

func TestShutdownDuringStartupPreventsServing(t *testing.T) {
	t.Parallel()

	server, _ := newTestServer(t, DefaultConfig(), unavailableTracker())
	listener := newGatedListener(t)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	select {
	case <-listener.addrEntered:
	case <-time.After(time.Second):
		t.Fatal("Serve() did not enter listener Addr")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() during startup error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Serve() after startup shutdown error = %v", err)
	}
	if _, err := net.DialTimeout("tcp", listener.address, 100*time.Millisecond); err == nil {
		t.Fatal("listener accepted a connection after successful startup shutdown")
	}
}

func TestDuplicateServeIsRejected(t *testing.T) {
	t.Parallel()

	server, _ := newTestServer(t, DefaultConfig(), unavailableTracker())
	first := newGatedListener(t)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(first)
	}()
	select {
	case <-first.addrEntered:
	case <-time.After(time.Second):
		t.Fatal("first Serve() did not enter listener Addr")
	}

	second, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("second net.Listen() error = %v", err)
	}
	defer second.Close()
	if err := server.Serve(second); !errors.Is(err, ErrServerAlreadyServing) {
		t.Fatalf("second Serve() error = %v, want ErrServerAlreadyServing", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("first Serve() error = %v", err)
	}
}

func TestConcurrentShutdownWaitsForOneServeExit(t *testing.T) {
	t.Parallel()

	server, _ := newTestServer(t, DefaultConfig(), unavailableTracker())
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	server.app.Get("/block", func(ctx fiber.Ctx) error {
		close(requestEntered)
		<-releaseRequest
		return ctx.SendStatus(fiber.StatusNoContent)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()
	waitForHTTP(t, listener.Addr().String()+livezPath)
	requestResult := make(chan error, 1)
	go func() {
		response, requestErr := (&http.Client{Timeout: 2 * time.Second}).Get(
			"http://" + listener.Addr().String() + "/block",
		)
		if requestErr == nil {
			response.Body.Close()
		}
		requestResult <- requestErr
	}()
	select {
	case <-requestEntered:
	case <-time.After(time.Second):
		t.Fatal("blocking request did not enter handler")
	}

	const shutdowns = 8
	results := make(chan error, shutdowns)
	var group sync.WaitGroup
	for range shutdowns {
		group.Add(1)
		go func() {
			defer group.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			results <- server.Shutdown(ctx)
		}()
	}
	earlyReturned := false
	var earlyErr error
	select {
	case earlyErr = <-results:
		earlyReturned = true
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseRequest)
	group.Wait()
	close(results)
	if earlyReturned {
		t.Errorf("Shutdown() returned before active request drained: %v", earlyErr)
	}
	for err := range results {
		if err != nil {
			t.Errorf("concurrent Shutdown() error = %v", err)
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if err := <-requestResult; err != nil {
		t.Fatalf("blocking request error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("repeated Shutdown() error = %v", err)
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
	waitForHTTP(t, listener.Addr().String()+livezPath)

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

func waitForHTTP(t *testing.T, addressAndPath string) {
	t.Helper()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + addressAndPath)
		if err == nil {
			response.Body.Close()
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("HTTP server did not become reachable at %s", addressAndPath)
}

type gatedListener struct {
	net.Listener
	address     string
	addrEntered chan struct{}
	releaseAddr chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
}

func newGatedListener(t *testing.T) *gatedListener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	return &gatedListener{
		Listener:    listener,
		address:     listener.Addr().String(),
		addrEntered: make(chan struct{}),
		releaseAddr: make(chan struct{}),
	}
}

func (listener *gatedListener) Addr() net.Addr {
	listener.enterOnce.Do(func() { close(listener.addrEntered) })
	<-listener.releaseAddr
	return listener.Listener.Addr()
}

func (listener *gatedListener) Close() error {
	listener.releaseOnce.Do(func() { close(listener.releaseAddr) })
	return listener.Listener.Close()
}
