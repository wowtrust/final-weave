package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/wowtrust/final-weave/pkg/api/health"
	"github.com/wowtrust/final-weave/pkg/observability"
)

func TestNewValidatesDependenciesAndLimits(t *testing.T) {
	t.Parallel()

	tracker := health.NewTracker()
	logger, _ := testLogger(t)
	valid := DefaultConfig()
	var typedNilRoot *panicOnUseContext

	tests := []struct {
		name      string
		root      context.Context
		config    Config
		readiness *health.Tracker
		logger    *observability.Logger
		want      error
	}{
		{
			name:      "nil root context",
			config:    valid,
			readiness: tracker,
			logger:    logger,
			want:      ErrNilRootContext,
		},
		{
			name:      "typed nil root context",
			root:      typedNilRoot,
			config:    valid,
			readiness: tracker,
			logger:    logger,
			want:      ErrNilRootContext,
		},
		{
			name:   "nil readiness tracker",
			root:   context.Background(),
			config: valid,
			logger: logger,
			want:   ErrNilReadinessTracker,
		},
		{
			name:      "nil logger",
			root:      context.Background(),
			config:    valid,
			readiness: tracker,
			want:      ErrNilLogger,
		},
	}

	invalidConfigs := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "zero read buffer", mutate: func(config *Config) { config.ReadBufferBytes = 0 }},
		{name: "excessive read buffer", mutate: func(config *Config) { config.ReadBufferBytes = maxReadBuffer + 1 }},
		{name: "zero connections", mutate: func(config *Config) { config.MaxConnections = 0 }},
		{name: "excessive connections", mutate: func(config *Config) { config.MaxConnections = maxConnections + 1 }},
		{
			name: "excessive aggregate read buffer",
			mutate: func(config *Config) {
				config.ReadBufferBytes = maxReadBuffer
				config.MaxConnections = maxAggregateReadBufferSize/maxReadBuffer + 1
			},
		},
		{name: "zero read timeout", mutate: func(config *Config) { config.ReadTimeout = 0 }},
		{name: "negative write timeout", mutate: func(config *Config) { config.WriteTimeout = -time.Second }},
		{name: "excessive idle timeout", mutate: func(config *Config) { config.IdleTimeout = maxTimeout + time.Nanosecond }},
		{name: "zero request timeout", mutate: func(config *Config) { config.RequestTimeout = 0 }},
	}
	for _, invalid := range invalidConfigs {
		config := valid
		invalid.mutate(&config)
		tests = append(tests, struct {
			name      string
			root      context.Context
			config    Config
			readiness *health.Tracker
			logger    *observability.Logger
			want      error
		}{
			name:      invalid.name,
			root:      context.Background(),
			config:    config,
			readiness: tracker,
			logger:    logger,
			want:      ErrInvalidConfig,
		})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(test.root, test.config, test.readiness, test.logger)
			if !errors.Is(err, test.want) {
				t.Fatalf("New() error = %v, want %v", err, test.want)
			}
		})
	}

	boundary := valid
	boundary.ReadBufferBytes = maxReadBuffer
	boundary.MaxConnections = maxAggregateReadBufferSize / maxReadBuffer
	if _, err := New(context.Background(), boundary, tracker, logger); err != nil {
		t.Fatalf("New() at aggregate read-buffer boundary error = %v", err)
	}
}

func TestNewHasNoListenerSideEffect(t *testing.T) {
	t.Parallel()

	server, _ := newTestServer(t, DefaultConfig(), unavailableTracker())
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		t.Fatalf("Shutdown() before Serve error = %v", err)
	}
	if err := server.Serve(nil); !errors.Is(err, ErrNilListener) {
		t.Fatalf("Serve(nil) error = %v, want ErrNilListener", err)
	}
	var typedNil *net.TCPListener
	if err := server.Serve(typedNil); !errors.Is(err, ErrNilListener) {
		t.Fatalf("Serve(typed nil) error = %v, want ErrNilListener", err)
	}
	if err := server.Shutdown(nil); !errors.Is(err, ErrNilShutdownContext) {
		t.Fatalf("Shutdown(nil) error = %v, want ErrNilShutdownContext", err)
	}
	var typedNilShutdown *panicOnUseContext
	if err := server.Shutdown(typedNilShutdown); !errors.Is(err, ErrNilShutdownContext) {
		t.Fatalf("Shutdown(typed nil) error = %v, want ErrNilShutdownContext", err)
	}
	if err := server.Shutdown(context.Background()); !errors.Is(err, ErrUnboundedShutdownContext) {
		t.Fatalf("Shutdown(background) error = %v, want ErrUnboundedShutdownContext", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer listener.Close()
	if err := server.Serve(listener); !errors.Is(err, ErrServerStopped) {
		t.Fatalf("Serve() after terminal Shutdown error = %v, want ErrServerStopped", err)
	}
}

type panicOnUseContext struct{}

func (*panicOnUseContext) Deadline() (time.Time, bool) {
	panic("typed nil context must be rejected before Deadline")
}

func (*panicOnUseContext) Done() <-chan struct{} {
	panic("typed nil context must be rejected before Done")
}

func (*panicOnUseContext) Err() error {
	panic("typed nil context must be rejected before Err")
}

func (*panicOnUseContext) Value(any) any {
	panic("typed nil context must be rejected before Value")
}

func TestOperationalProbesFollowReadinessTracker(t *testing.T) {
	t.Parallel()

	tracker := health.NewTracker()
	server, _ := newTestServer(t, DefaultConfig(), tracker)

	live := testRequest(t, server, http.MethodGet, livezPath, http.NoBody, nil)
	assertResponse(t, live, fiber.StatusOK, `{"status":"live"}`)
	assertSecurityHeaders(t, live)

	notReady := testRequest(t, server, http.MethodGet, readyzPath, http.NoBody, nil)
	assertResponse(t, notReady, fiber.StatusServiceUnavailable, `{"status":"not_ready","reason":"runtime_unavailable"}`)

	tracker.Store(health.Readiness{Ready: true})
	readyResponse := testRequest(t, server, http.MethodGet, readyzPath, http.NoBody, nil)
	assertResponse(t, readyResponse, fiber.StatusOK, `{"status":"ready"}`)

	for _, path := range []string{"/livez/", "/LIVEZ", "/missing"} {
		response := testRequest(t, server, http.MethodGet, path, http.NoBody, nil)
		assertResponse(t, response, fiber.StatusNotFound, `{"error":"not_found"}`)
	}
	head := testRequest(t, server, http.MethodHead, livezPath, http.NoBody, nil)
	if head.StatusCode != fiber.StatusMethodNotAllowed {
		t.Fatalf("HEAD %s status = %d, want %d", livezPath, head.StatusCode, fiber.StatusMethodNotAllowed)
	}
	head.Body.Close()
}

func TestUnknownReadinessReasonIsSanitized(t *testing.T) {
	t.Parallel()

	tracker := health.NewTracker()
	tracker.Store(health.Readiness{Reason: health.Reason("kms://secret-provider-detail")})
	server, logs := newTestServer(t, DefaultConfig(), tracker)
	response := testRequest(t, server, http.MethodGet, readyzPath, http.NoBody, nil)
	assertResponse(t, response, fiber.StatusServiceUnavailable, `{"status":"not_ready","reason":"unavailable"}`)
	if strings.Contains(logs.String(), "secret-provider-detail") {
		t.Fatalf("logs disclose readiness detail: %q", logs.String())
	}
}

func TestRequestMetadataAndLogsExcludeSecrets(t *testing.T) {
	t.Parallel()

	server, logs := newTestServer(t, DefaultConfig(), unavailableTracker())
	request := httptest.NewRequest(
		http.MethodGet,
		readyzPath+"?token=query-secret",
		http.NoBody,
	)
	request.Header.Set(requestIDHeader, "safe.request-1")
	request.Header.Set("Authorization", "Bearer authorization-secret")
	request.Header.Set("Cookie", "session=cookie-secret")
	request.Header.Set("X-Custom-Secret", "header-secret")

	response, err := server.app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer response.Body.Close()
	if response.Header.Get(requestIDHeader) != "safe.request-1" {
		t.Fatalf("response request ID = %q, want safe.request-1", response.Header.Get(requestIDHeader))
	}

	logText := logs.String()
	for _, secret := range []string{
		"query-secret",
		"authorization-secret",
		"cookie-secret",
		"header-secret",
	} {
		if strings.Contains(logText, secret) {
			t.Errorf("access log discloses %q: %s", secret, logText)
		}
	}

	entry := decodeLogEntry(t, []byte(strings.TrimSpace(logText)))
	wantKeys := []string{
		"component",
		"duration_ms",
		"event",
		"level",
		"message",
		"method",
		"monotonic_sequence",
		"request_id",
		"response_bytes",
		"route",
		"status_code",
		"timestamp",
		"transport",
	}
	gotKeys := make([]string, 0, len(entry))
	for key := range entry {
		gotKeys = append(gotKeys, key)
	}
	sort.Strings(gotKeys)
	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("access log fields = %v, want %v", gotKeys, wantKeys)
	}
	assertLogString(t, entry, "component", "http")
	assertLogString(t, entry, "event", "http_request_completed")
	assertLogString(t, entry, "method", http.MethodGet)
	assertLogString(t, entry, "route", readyzPath)
	assertLogString(t, entry, "request_id", "safe.request-1")
	assertLogString(t, entry, "transport", "http")
}

func TestUnsafeRequestIDIsReplaced(t *testing.T) {
	t.Parallel()

	server, logs := newTestServer(t, DefaultConfig(), unavailableTracker())
	request := httptest.NewRequest(http.MethodGet, livezPath, http.NoBody)
	request.Header.Set(requestIDHeader, "unsafe/id?request-secret")
	response, err := server.app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer response.Body.Close()

	requestID := response.Header.Get(requestIDHeader)
	if !regexp.MustCompile(`^fw-http-[0-9a-z]+$`).MatchString(requestID) {
		t.Fatalf("generated request ID = %q", requestID)
	}
	if strings.Contains(logs.String(), "request-secret") {
		t.Fatalf("access log contains rejected request ID: %q", logs.String())
	}
	if !strings.Contains(logs.String(), requestID) {
		t.Fatalf("access log %q does not contain generated request ID %q", logs.String(), requestID)
	}
}

func TestPanicRecoveryDoesNotExposePanicValue(t *testing.T) {
	t.Parallel()

	logger, logs := testLogger(t)
	app := fiber.New(fiber.Config{ErrorHandler: stableErrorHandler})
	app.Use(requestContextMiddleware(context.Background(), time.Second, &atomic.Uint64{}))
	app.Use(accessLogMiddleware(logger.Component("http")))
	app.Use(recoverMiddleware(logger.Component("http")))
	app.Get("/panic", func(fiber.Ctx) error {
		panic("panic-secret")
	})

	request := httptest.NewRequest(http.MethodGet, "/panic", http.NoBody)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	assertResponse(t, response, fiber.StatusInternalServerError, `{"error":"internal_error"}`)
	if strings.Contains(logs.String(), "panic-secret") {
		t.Fatalf("panic response logs disclose panic value: %q", logs.String())
	}
	if !strings.Contains(logs.String(), `"event":"http_request_panicked"`) {
		t.Fatalf("panic log event missing: %q", logs.String())
	}

	secondResponse, err := app.Test(httptest.NewRequest(http.MethodGet, "/panic", http.NoBody))
	if err != nil {
		t.Fatalf("second app.Test() error = %v", err)
	}
	if secondResponse.StatusCode != fiber.StatusInternalServerError {
		t.Fatalf("server did not continue after recovered panic: status %d", secondResponse.StatusCode)
	}
	secondResponse.Body.Close()
}

func TestRequestContextCarriesDeadlineAndRequestID(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.RequestTimeout = 200 * time.Millisecond
	var observedDeadline time.Time
	var observedRequestID string
	app := fiber.New()
	app.Use(requestContextMiddleware(context.Background(), config.RequestTimeout, &atomic.Uint64{}))
	app.Get("/context", func(ctx fiber.Ctx) error {
		observedDeadline, _ = ctx.Context().Deadline()
		observedRequestID = RequestID(ctx.Context())
		return ctx.SendStatus(fiber.StatusNoContent)
	})

	before := time.Now()
	request := httptest.NewRequest(http.MethodGet, "/context", http.NoBody)
	request.Header.Set(requestIDHeader, "deadline-test")
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	response.Body.Close()

	if observedDeadline.IsZero() {
		t.Fatal("request context has no deadline")
	}
	if observedDeadline.Before(before) || observedDeadline.After(before.Add(config.RequestTimeout+100*time.Millisecond)) {
		t.Fatalf("request deadline = %s, want within request timeout", observedDeadline)
	}
	if observedRequestID != "deadline-test" {
		t.Fatalf("request ID = %q, want deadline-test", observedRequestID)
	}
	if got := RequestID(nil); got != "" {
		t.Fatalf("RequestID(nil) = %q, want empty", got)
	}
}

func TestProbeRejectsAnyRequestBodyWithoutDisclosure(t *testing.T) {
	t.Parallel()

	server, logs := newTestServer(t, DefaultConfig(), unavailableTracker())
	response := testRequest(t, server, http.MethodGet, readyzPath, strings.NewReader("body-secret"), nil)
	assertResponse(t, response, fiber.StatusBadRequest, `{"error":"invalid_request"}`)
	if strings.Contains(logs.String(), "body-secret") {
		t.Fatalf("probe body leaked to logs: %q", logs.String())
	}
}

func TestConcurrentReadinessRequestsAreRaceSafe(t *testing.T) {
	t.Parallel()

	tracker := health.NewTracker()
	server, _ := newTestServer(t, DefaultConfig(), tracker)

	const requestCount = 32
	var group sync.WaitGroup
	for index := 0; index < requestCount; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			if index%2 == 0 {
				tracker.Store(health.Readiness{Ready: true})
			} else {
				tracker.Store(health.Readiness{Reason: health.ReasonRecovering})
			}
			request := httptest.NewRequest(http.MethodGet, readyzPath, http.NoBody)
			response, err := server.app.Test(request)
			if err != nil {
				t.Errorf("app.Test() error = %v", err)
				return
			}
			defer response.Body.Close()
			if response.StatusCode != fiber.StatusOK && response.StatusCode != fiber.StatusServiceUnavailable {
				t.Errorf("readiness status = %d", response.StatusCode)
			}
		}(index)
	}
	group.Wait()
}

func unavailableTracker() *health.Tracker {
	return health.NewTracker()
}

func newTestServer(
	t *testing.T,
	config Config,
	tracker *health.Tracker,
) (*Server, *bytes.Buffer) {
	t.Helper()

	logger, output := testLogger(t)
	server, err := New(context.Background(), config, tracker, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return server, output
}

func testLogger(t *testing.T) (*observability.Logger, *bytes.Buffer) {
	t.Helper()

	var output bytes.Buffer
	logger, err := observability.NewLogger(&output, observability.DefaultLogConfig())
	if err != nil {
		t.Fatalf("observability.NewLogger() error = %v", err)
	}
	return logger, &output
}

func testRequest(
	t *testing.T,
	server *Server,
	method string,
	path string,
	body io.Reader,
	headers map[string]string,
) *http.Response {
	t.Helper()

	request := httptest.NewRequest(method, path, body)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := server.app.Test(request, fiber.TestConfig{Timeout: time.Second, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("app.Test(%s %s) error = %v", method, path, err)
	}
	return response
}

func assertResponse(t *testing.T, response *http.Response, wantStatus int, wantBody string) {
	t.Helper()
	defer response.Body.Close()

	if response.StatusCode != wantStatus {
		t.Fatalf("response status = %d, want %d", response.StatusCode, wantStatus)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("io.ReadAll(response.Body) error = %v", err)
	}
	if strings.TrimSpace(string(body)) != wantBody {
		t.Fatalf("response body = %q, want %q", body, wantBody)
	}
}

func assertSecurityHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	if got := response.Header.Get("Server"); got != "" {
		t.Fatalf("Server header = %q, want empty", got)
	}
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

func decodeLogEntry(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", data, err)
	}
	return entry
}

func assertLogString(t *testing.T, entry map[string]any, field, want string) {
	t.Helper()
	if got, ok := entry[field].(string); !ok || got != want {
		t.Fatalf("log field %s = %#v, want %q", field, entry[field], want)
	}
}
