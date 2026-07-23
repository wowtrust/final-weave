// Package grpcserver provides the bounded operational gRPC adapter.
//
// The bootstrap adapter registers only the standard gRPC health service. It
// does not expose business, consensus, proof, admin, reflection, or gateway
// APIs. Constructing a Server opens no listener and starts no goroutine.
package grpcserver

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/wowtrust/final-weave/pkg/api/health"
	"github.com/wowtrust/final-weave/pkg/observability"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
)

var (
	// ErrNilRootContext reports that RPCs have no owned process context.
	ErrNilRootContext = errors.New("gRPC root context must not be nil")
	// ErrNilReadinessTracker reports a missing bounded readiness projection.
	ErrNilReadinessTracker = errors.New("gRPC readiness tracker must not be nil")
	// ErrNilLogger reports a missing explicitly injected logger.
	ErrNilLogger = errors.New("gRPC logger must not be nil")
	// ErrNilListener reports a missing listener passed to Serve.
	ErrNilListener = errors.New("gRPC listener must not be nil")
	// ErrNilShutdownContext reports a missing shutdown deadline owner.
	ErrNilShutdownContext = errors.New("gRPC shutdown context must not be nil")
	// ErrUnboundedShutdownContext reports a shutdown request without a deadline.
	ErrUnboundedShutdownContext = errors.New("gRPC shutdown context must have a deadline")
	// ErrServerAlreadyServing reports a duplicate concurrent Serve call.
	ErrServerAlreadyServing = errors.New("gRPC server is already serving")
	// ErrServerStopped reports an attempt to serve after terminal shutdown.
	ErrServerStopped = errors.New("gRPC server is stopped")
)

// Server is a grpc-go operational adapter with a single-use lifecycle.
type Server struct {
	server *grpc.Server
	health *healthBridge

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
	stateServing
	stateStopping
	stateStopped
)

// New constructs the health-only adapter without binding a listener or
// starting work. The supplied readiness tracker remains owned by the future
// runtime composition root.
func New(
	root context.Context,
	config Config,
	readiness *health.Tracker,
	logger *observability.Logger,
) (*Server, error) {
	if isNilInterface(root) {
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

	healthService := newHealthBridge(readiness)
	interceptors := &interceptorSet{
		root:            root,
		timeout:         config.UnaryRequestTimeout,
		metadataLimit:   uint64(config.MaxHeaderListBytes),
		logger:          logger.Component("grpc"),
		requestIDs:      &atomic.Uint64{},
		healthRefresher: healthService.refresh,
	}
	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(config.MaxReceiveMessageBytes),
		grpc.MaxSendMsgSize(config.MaxSendMessageBytes),
		// Keep a fixed wire-level ceiling so moderately oversized application
		// metadata reaches the interceptor and returns stable ResourceExhausted.
		// MaxConcurrentStreams caps this transport ceiling to 16 MiB per
		// HTTP/2 connection even when the application budget is smaller.
		grpc.MaxHeaderListSize(maxHeaderListBytes),
		grpc.MaxConcurrentStreams(config.MaxConcurrentStreams),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     config.MaxConnectionIdle,
			MaxConnectionAge:      config.MaxConnectionAge,
			MaxConnectionAgeGrace: config.MaxConnectionAgeGrace,
			Time:                  config.KeepaliveTime,
			Timeout:               config.KeepaliveTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             config.MinClientPingInterval,
			PermitWithoutStream: false,
		}),
		grpc.UnaryInterceptor(interceptors.unary),
		grpc.StreamInterceptor(interceptors.stream),
	)
	healthpb.RegisterHealthServer(grpcServer, healthService.server)

	return &Server{server: grpcServer, health: healthService}, nil
}

// RefreshHealth projects the current tracker snapshot into the standard gRPC
// health service. Unary Check/List requests refresh before reading; runtime
// owners call this method after state changes to notify existing Watch streams.
func (s *Server) RefreshHealth() {
	s.mustServer()
	s.health.refresh()
}

// Serve serves the already-bound listener. The caller owns listener creation,
// TLS wrapping, goroutine supervision, and shutdown order.
func (s *Server) Serve(listener net.Listener) error {
	if isNilInterface(listener) {
		return ErrNilListener
	}
	server := s.mustServer()

	s.mu.Lock()
	switch s.state {
	case stateIdle:
		s.state = stateServing
		s.listener = listener
		s.serveDone = make(chan struct{})
		s.shutdownDone = make(chan struct{})
	case stateServing:
		s.mu.Unlock()
		return ErrServerAlreadyServing
	case stateStopping, stateStopped:
		s.mu.Unlock()
		closeErr := listener.Close()
		if closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			return errors.Join(ErrServerStopped, closeErr)
		}
		return ErrServerStopped
	default:
		s.mu.Unlock()
		panic("grpcserver: invalid lifecycle state")
	}
	s.mu.Unlock()

	err := server.Serve(listener)

	s.mu.Lock()
	stopping := s.state == stateStopping || s.state == stateStopped
	s.listener = nil
	close(s.serveDone)
	if s.state == stateServing {
		s.state = stateStopped
		s.health.shutdown()
		close(s.shutdownDone)
	}
	s.mu.Unlock()

	if stopping && (err == nil || errors.Is(err, grpc.ErrServerStopped)) {
		return nil
	}
	return err
}

// Shutdown publishes NOT_SERVING, attempts a graceful drain until ctx expires,
// then forces Stop. Concurrent callers wait for the same completed result.
func (s *Server) Shutdown(ctx context.Context) error {
	if isNilInterface(ctx) {
		return ErrNilShutdownContext
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return ErrUnboundedShutdownContext
	}
	server := s.mustServer()

	s.mu.Lock()
	switch s.state {
	case stateIdle:
		s.state = stateStopped
		s.health.shutdown()
		s.mu.Unlock()
		return nil
	case stateStopped:
		err := s.shutdownErr
		s.mu.Unlock()
		return err
	case stateStopping:
		done := s.shutdownDone
		s.mu.Unlock()
		return s.waitForShutdown(done)
	case stateServing:
		s.state = stateStopping
		serveDone := s.serveDone
		s.health.shutdown()
		s.mu.Unlock()
		if contextErr := ctx.Err(); contextErr != nil {
			server.Stop()
			<-serveDone
			return s.finishShutdown(contextErr)
		}

		gracefulDone := make(chan struct{})
		go func() {
			server.GracefulStop()
			close(gracefulDone)
		}()

		var shutdownErr error
		select {
		case <-gracefulDone:
			if contextErr := ctx.Err(); contextErr != nil {
				shutdownErr = contextErr
				server.Stop()
			}
		case <-ctx.Done():
			shutdownErr = ctx.Err()
			server.Stop()
			<-gracefulDone
		}
		<-serveDone
		return s.finishShutdown(shutdownErr)
	default:
		s.mu.Unlock()
		panic("grpcserver: invalid lifecycle state")
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

func (s *Server) waitForShutdown(done <-chan struct{}) error {
	<-done
	s.mu.Lock()
	err := s.shutdownErr
	s.mu.Unlock()
	return err
}

func (s *Server) mustServer() *grpc.Server {
	if s == nil || s.server == nil || s.health == nil {
		panic("grpcserver: Server must be created with New")
	}
	return s.server
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
