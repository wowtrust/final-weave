// Package httpserver provides the bounded Fiber operational HTTP adapter.
//
// Constructing a Server binds no listener and starts no goroutine. A recovered
// node composition root owns listeners and may call Serve only after all
// required identity, storage, and runtime checks have passed.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/wowtrust/final-weave/pkg/api/health"
	"github.com/wowtrust/final-weave/pkg/observability"
)

const (
	livezPath  = "/livez"
	readyzPath = "/readyz"

	requestIDHeader = "X-Request-ID"

	maxReadBuffer              = 64 << 10
	maxConnections             = 65_536
	maxAggregateReadBufferSize = 64 << 20
	maxTimeout                 = 10 * time.Minute
)

var (
	// ErrNilRootContext reports that requests have no owned process context.
	ErrNilRootContext = errors.New("HTTP root context must not be nil")
	// ErrNilReadinessTracker reports a missing bounded readiness projection.
	ErrNilReadinessTracker = errors.New("HTTP readiness tracker must not be nil")
	// ErrNilLogger reports a missing explicitly injected logger.
	ErrNilLogger = errors.New("HTTP logger must not be nil")
	// ErrInvalidConfig reports an unsafe or unusable HTTP limit.
	ErrInvalidConfig = errors.New("invalid HTTP server config")
	// ErrNilListener reports a missing listener passed to Serve.
	ErrNilListener = errors.New("HTTP listener must not be nil")
	// ErrNilShutdownContext reports a missing shutdown deadline owner.
	ErrNilShutdownContext = errors.New("HTTP shutdown context must not be nil")
	// ErrUnboundedShutdownContext reports a shutdown request without a deadline.
	ErrUnboundedShutdownContext = errors.New("HTTP shutdown context must have a deadline")
	// ErrServerAlreadyServing reports a duplicate concurrent Serve call.
	ErrServerAlreadyServing = errors.New("HTTP server is already serving")
	// ErrServerStopped reports an attempt to serve after terminal shutdown.
	ErrServerStopped = errors.New("HTTP server is stopped")
)

// Config contains local operational HTTP limits. It is not a protocol object.
type Config struct {
	ReadBufferBytes int
	MaxConnections  int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	RequestTimeout  time.Duration
}

// DefaultConfig follows the current FinalWeave configuration examples and the
// bounded lifecycle practices used by TrustDB.
func DefaultConfig() Config {
	return Config{
		ReadBufferBytes: 16 << 10,
		MaxConnections:  1_024,
		ReadTimeout:     10 * time.Second,
		WriteTimeout:    10 * time.Second,
		IdleTimeout:     2 * time.Minute,
		RequestTimeout:  10 * time.Second,
	}
}

// Server is a Fiber-backed operational HTTP adapter.
type Server struct {
	app *fiber.App

	mu           sync.Mutex
	state        lifecycleState
	listener     net.Listener
	serveDone    chan struct{}
	shutdownDone chan struct{}
	shutdownErr  error
}

type lifecycleState uint8

const (
	stateIdle lifecycleState = iota
	stateStarting
	stateServing
	stateStopping
	stateStopped
)

// New constructs the adapter without opening a listener or starting a
// goroutine. The readiness tracker is the sole local projection for /readyz.
func New(
	root context.Context,
	config Config,
	readiness *health.Tracker,
	logger *observability.Logger,
) (*Server, error) {
	if root == nil {
		return nil, ErrNilRootContext
	}
	if readiness == nil {
		return nil, ErrNilReadinessTracker
	}
	if logger == nil {
		return nil, ErrNilLogger
	}
	if err := config.validate(); err != nil {
		return nil, err
	}

	httpLogger := logger.Component("http")
	app := fiber.New(fiber.Config{
		ServerHeader:            "",
		StrictRouting:           true,
		CaseSensitive:           true,
		DisableHeadAutoRegister: true,
		BodyLimit:               config.ReadBufferBytes,
		Concurrency:             config.MaxConnections,
		GETOnly:                 true,
		ReadTimeout:             config.ReadTimeout,
		WriteTimeout:            config.WriteTimeout,
		IdleTimeout:             config.IdleTimeout,
		ReadBufferSize:          config.ReadBufferBytes,
		ErrorHandler:            stableErrorHandler,
	})

	requestIDs := &atomic.Uint64{}
	app.Use(requestContextMiddleware(root, config.RequestTimeout, requestIDs))
	app.Use(accessLogMiddleware(httpLogger))
	app.Use(recoverMiddleware(httpLogger))
	app.Use(rejectProbeBodyMiddleware)
	app.Get(livezPath, liveHandler)
	app.Get(readyzPath, readyHandler(readiness))

	return &Server{app: app}, nil
}

// Serve serves the already-bound listener. The caller owns listener creation,
// goroutine ownership, TLS wrapping, failure supervision, and shutdown order.
func (s *Server) Serve(listener net.Listener) error {
	if isNilInterface(listener) {
		return ErrNilListener
	}

	s.mu.Lock()
	switch s.state {
	case stateIdle:
		s.state = stateStarting
		s.listener = listener
		s.serveDone = make(chan struct{})
		s.shutdownDone = make(chan struct{})
	case stateStarting, stateServing:
		s.mu.Unlock()
		return ErrServerAlreadyServing
	case stateStopping, stateStopped:
		s.mu.Unlock()
		return ErrServerStopped
	default:
		s.mu.Unlock()
		panic("httpserver: invalid lifecycle state")
	}
	s.mu.Unlock()

	err := s.mustApp().Listener(listener, fiber.ListenConfig{
		DisableStartupMessage: true,
		EnablePrefork:         false,
		BeforeServeFunc: func(*fiber.App) error {
			s.mu.Lock()
			defer s.mu.Unlock()
			switch s.state {
			case stateStarting:
				s.state = stateServing
				return nil
			case stateStopping, stateStopped:
				return ErrServerStopped
			default:
				return fmt.Errorf("%w: unexpected lifecycle state %d", ErrServerStopped, s.state)
			}
		},
	})

	s.mu.Lock()
	stopping := s.state == stateStopping || s.state == stateStopped
	s.listener = nil
	close(s.serveDone)
	if s.state == stateStarting || s.state == stateServing {
		s.state = stateStopped
		close(s.shutdownDone)
	}
	s.mu.Unlock()

	if stopping && (err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, ErrServerStopped)) {
		return nil
	}
	return err
}

// Shutdown drains the Fiber server until ctx expires. Calling Shutdown before
// Serve or after a completed shutdown is harmless.
func (s *Server) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return ErrNilShutdownContext
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return ErrUnboundedShutdownContext
	}

	s.mustApp()
	s.mu.Lock()
	switch s.state {
	case stateIdle:
		s.state = stateStopped
		s.mu.Unlock()
		return nil
	case stateStopped:
		err := s.shutdownErr
		s.mu.Unlock()
		return err
	case stateStopping:
		done := s.shutdownDone
		s.mu.Unlock()
		return s.waitForShutdown(ctx, done)
	case stateStarting:
		s.state = stateStopping
		listener := s.listener
		serveDone := s.serveDone
		s.mu.Unlock()

		closeErr := listener.Close()
		if errors.Is(closeErr, net.ErrClosed) {
			closeErr = nil
		}
		return s.finishShutdown(errors.Join(closeErr, waitForServeExit(ctx, serveDone)))
	case stateServing:
		s.state = stateStopping
		serveDone := s.serveDone
		s.mu.Unlock()

		shutdownErr := s.app.ShutdownWithContext(ctx)
		if errors.Is(shutdownErr, fiber.ErrNotRunning) || errors.Is(shutdownErr, net.ErrClosed) {
			shutdownErr = nil
		}
		return s.finishShutdown(errors.Join(shutdownErr, waitForServeExit(ctx, serveDone)))
	default:
		s.mu.Unlock()
		panic("httpserver: invalid lifecycle state")
	}
}

func (s *Server) finishShutdown(err error) error {
	s.mu.Lock()
	s.shutdownErr = err
	s.state = stateStopped
	close(s.shutdownDone)
	s.mu.Unlock()
	return err
}

func (s *Server) waitForShutdown(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		s.mu.Lock()
		err := s.shutdownErr
		s.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func waitForServeExit(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) mustApp() *fiber.App {
	if s == nil || s.app == nil {
		panic("httpserver: Server must be created with New")
	}
	return s.app
}

func (config Config) validate() error {
	if config.ReadBufferBytes <= 0 || config.ReadBufferBytes > maxReadBuffer {
		return fmt.Errorf(
			"%w: ReadBufferBytes must be in [1,%d]",
			ErrInvalidConfig,
			maxReadBuffer,
		)
	}
	if config.MaxConnections <= 0 || config.MaxConnections > maxConnections {
		return fmt.Errorf(
			"%w: MaxConnections must be in [1,%d]",
			ErrInvalidConfig,
			maxConnections,
		)
	}
	if uint64(config.MaxConnections) > uint64(maxAggregateReadBufferSize)/uint64(config.ReadBufferBytes) {
		return fmt.Errorf(
			"%w: ReadBufferBytes * MaxConnections must not exceed %d",
			ErrInvalidConfig,
			maxAggregateReadBufferSize,
		)
	}
	timeouts := []struct {
		name  string
		value time.Duration
	}{
		{name: "ReadTimeout", value: config.ReadTimeout},
		{name: "WriteTimeout", value: config.WriteTimeout},
		{name: "IdleTimeout", value: config.IdleTimeout},
		{name: "RequestTimeout", value: config.RequestTimeout},
	}
	for _, timeout := range timeouts {
		if timeout.value <= 0 || timeout.value > maxTimeout {
			return fmt.Errorf(
				"%w: %s must be in (0,%s]",
				ErrInvalidConfig,
				timeout.name,
				maxTimeout,
			)
		}
	}
	return nil
}

type requestIDContextKey struct{}

// RequestID returns the bounded HTTP request ID stored in ctx, if present.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

func requestContextMiddleware(
	root context.Context,
	timeout time.Duration,
	sequence *atomic.Uint64,
) fiber.Handler {
	return func(ctx fiber.Ctx) error {
		requestID := acceptedRequestID(ctx.Get(requestIDHeader))
		if requestID == "" {
			requestID = "fw-http-" + strconv.FormatUint(sequence.Add(1), 36)
		}

		requestContext := context.WithValue(root, requestIDContextKey{}, requestID)
		requestContext, cancel := context.WithTimeout(requestContext, timeout)
		defer cancel()
		ctx.SetContext(requestContext)
		ctx.Set(requestIDHeader, requestID)
		ctx.Set(fiber.HeaderCacheControl, "no-store")
		ctx.Set(fiber.HeaderXContentTypeOptions, "nosniff")

		return ctx.Next()
	}
}

func accessLogMiddleware(logger *observability.Logger) fiber.Handler {
	return func(ctx fiber.Ctx) error {
		started := time.Now()
		err := ctx.Next()
		if err != nil {
			if responseErr := stableErrorHandler(ctx, err); responseErr != nil {
				return responseErr
			}
		}

		logger.Info("http_request_completed").
			Str("transport", "http").
			Str("method", safeMethod(ctx.Method())).
			Str("route", safeRoute(ctx)).
			Int("status_code", ctx.Response().StatusCode()).
			Str("request_id", RequestID(ctx.Context())).
			Int64("duration_ms", time.Since(started).Milliseconds()).
			Int("response_bytes", len(ctx.Response().Body())).
			Msg("HTTP request completed")

		return nil
	}
}

func recoverMiddleware(logger *observability.Logger) fiber.Handler {
	return func(ctx fiber.Ctx) (err error) {
		defer func() {
			if recover() == nil {
				return
			}
			logger.Error("http_request_panicked").
				Str("transport", "http").
				Str("method", safeMethod(ctx.Method())).
				Str("route", safeRoute(ctx)).
				Str("request_id", RequestID(ctx.Context())).
				Msg("HTTP request panic recovered")
			err = fiber.ErrInternalServerError
		}()
		return ctx.Next()
	}
}

func liveHandler(ctx fiber.Ctx) error {
	return ctx.Status(fiber.StatusOK).JSON(probeResponse{Status: "live"})
}

func readyHandler(provider *health.Tracker) fiber.Handler {
	return func(ctx fiber.Ctx) error {
		readiness := provider.Snapshot()
		if readiness.Ready {
			return ctx.Status(fiber.StatusOK).JSON(probeResponse{Status: "ready"})
		}
		return ctx.Status(fiber.StatusServiceUnavailable).JSON(probeResponse{
			Status: "not_ready",
			Reason: readiness.Reason,
		})
	}
}

func rejectProbeBodyMiddleware(ctx fiber.Ctx) error {
	if ctx.Request().Header.ContentLength() > 0 || len(ctx.Body()) > 0 {
		return fiber.ErrBadRequest
	}
	return ctx.Next()
}

type probeResponse struct {
	Status string        `json:"status"`
	Reason health.Reason `json:"reason,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func stableErrorHandler(ctx fiber.Ctx, err error) error {
	status := errorStatus(err)
	return ctx.Status(status).JSON(errorResponse{Error: errorCode(status)})
}

func errorStatus(err error) int {
	var fiberError *fiber.Error
	if errors.As(err, &fiberError) && fiberError != nil && fiberError.Code >= 400 && fiberError.Code <= 599 {
		return fiberError.Code
	}
	return fiber.StatusInternalServerError
}

func errorCode(status int) string {
	switch status {
	case fiber.StatusBadRequest:
		return "invalid_request"
	case fiber.StatusNotFound:
		return "not_found"
	case fiber.StatusMethodNotAllowed:
		return "method_not_allowed"
	case fiber.StatusRequestTimeout:
		return "request_timeout"
	case fiber.StatusRequestEntityTooLarge:
		return "request_too_large"
	case fiber.StatusRequestHeaderFieldsTooLarge:
		return "request_headers_too_large"
	case fiber.StatusServiceUnavailable:
		return "service_unavailable"
	default:
		return "internal_error"
	}
}

func acceptedRequestID(candidate string) string {
	if len(candidate) == 0 || len(candidate) > 64 {
		return ""
	}
	for _, character := range candidate {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return ""
	}
	return strings.Clone(candidate)
}

func safeMethod(method string) string {
	switch method {
	case fiber.MethodGet,
		fiber.MethodHead,
		fiber.MethodPost,
		fiber.MethodPut,
		fiber.MethodPatch,
		fiber.MethodDelete,
		fiber.MethodOptions,
		fiber.MethodConnect,
		fiber.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

func safeRoute(ctx fiber.Ctx) string {
	switch ctx.Route().Path {
	case livezPath:
		return livezPath
	case readyzPath:
		return readyzPath
	default:
		return "unmatched"
	}
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
