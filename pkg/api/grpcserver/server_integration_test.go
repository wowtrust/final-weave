package grpcserver

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/wowtrust/final-weave/pkg/api/health"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestRealListenerRejectsDuplicateServe(t *testing.T) {
	t.Parallel()

	server, _ := newTestServer(t, context.Background(), DefaultConfig(), health.NewTracker())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()
	connection := dialRealServer(t, listener.Addr().String())
	client := healthpb.NewHealthClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("Health.Check() error = %v", err)
	}

	second, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("second net.Listen() error = %v", err)
	}
	defer second.Close()
	if err := server.Serve(second); !errors.Is(err, ErrServerAlreadyServing) {
		t.Fatalf("second Serve() error = %v, want ErrServerAlreadyServing", err)
	}

	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("ClientConn.Close() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}

func TestConcurrentShutdownWaitsForActiveRPC(t *testing.T) {
	t.Parallel()

	service := newBlockingTestService()
	tracker := health.NewTracker()
	tracker.Store(health.Readiness{Ready: true})
	server, _ := newTestServer(t, context.Background(), DefaultConfig(), tracker)
	registerTestService(server, service)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()
	connection := dialRealServer(t, listener.Addr().String())
	defer connection.Close()
	healthClient := healthpb.NewHealthClient(connection)
	watchContext, cancelWatch := context.WithCancel(context.Background())
	healthWatch, err := healthClient.Watch(watchContext, &healthpb.HealthCheckRequest{})
	if err != nil {
		cancelWatch()
		t.Fatalf("Health.Watch() error = %v", err)
	}
	healthStatus, err := healthWatch.Recv()
	if err != nil {
		cancelWatch()
		t.Fatalf("Health.Watch().Recv() initial error = %v", err)
	}
	if healthStatus.Status != healthpb.HealthCheckResponse_SERVING {
		cancelWatch()
		t.Fatalf("initial health status = %s, want SERVING", healthStatus.Status)
	}

	requestResult := make(chan error, 1)
	go func() {
		requestResult <- connection.Invoke(
			context.Background(),
			testUnaryMethod,
			&healthpb.HealthCheckRequest{Service: "block"},
			&healthpb.HealthCheckRequest{},
		)
	}()
	select {
	case <-service.blockEntered:
	case <-time.After(time.Second):
		t.Fatal("blocking RPC did not enter handler")
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
	healthStatus, err = healthWatch.Recv()
	if err != nil {
		cancelWatch()
		t.Fatalf("Health.Watch().Recv() shutdown error = %v", err)
	}
	if healthStatus.Status != healthpb.HealthCheckResponse_NOT_SERVING {
		cancelWatch()
		t.Fatalf("shutdown health status = %s, want NOT_SERVING", healthStatus.Status)
	}
	cancelWatch()
	for {
		if _, err := healthWatch.Recv(); err != nil {
			if status.Code(err) != codes.Canceled {
				t.Fatalf("canceled Health.Watch() code = %s, want Canceled", status.Code(err))
			}
			break
		}
	}
	earlyReturned := false
	var earlyErr error
	select {
	case earlyErr = <-results:
		earlyReturned = true
	case <-time.After(50 * time.Millisecond):
	}
	close(service.releaseBlock)
	group.Wait()
	close(results)
	if earlyReturned {
		t.Errorf("Shutdown() returned before active RPC drained: %v", earlyErr)
	}
	for err := range results {
		if err != nil {
			t.Errorf("concurrent Shutdown() error = %v", err)
		}
	}
	if err := <-requestResult; err != nil {
		t.Fatalf("blocking RPC error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("repeated Shutdown() error = %v", err)
	}
}

func TestShutdownDeadlineForcesStop(t *testing.T) {
	t.Parallel()

	service := newBlockingTestService()
	server, _ := newTestServer(t, context.Background(), DefaultConfig(), health.NewTracker())
	registerTestService(server, service)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()
	connection := dialRealServer(t, listener.Addr().String())
	defer connection.Close()

	requestResult := make(chan error, 1)
	go func() {
		requestResult <- connection.Invoke(
			context.Background(),
			testUnaryMethod,
			&healthpb.HealthCheckRequest{Service: "cancel"},
			&healthpb.HealthCheckRequest{},
		)
	}()
	select {
	case <-service.blockEntered:
	case <-time.After(time.Second):
		t.Fatal("cancelable RPC did not enter handler")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := server.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("forced Shutdown() error = %v, want DeadlineExceeded", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Serve() after forced Stop error = %v", err)
	}
	if code := status.Code(<-requestResult); code != codes.Canceled && code != codes.Unavailable {
		t.Fatalf("forced RPC code = %s, want Canceled or Unavailable", code)
	}

	retryCtx, retryCancel := context.WithTimeout(context.Background(), time.Second)
	defer retryCancel()
	if err := server.Shutdown(retryCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("repeated forced Shutdown() error = %v, want original DeadlineExceeded", err)
	}
}

func TestExpiredInitiatingShutdownForcesStop(t *testing.T) {
	t.Parallel()

	service := newBlockingTestService()
	server, _ := newTestServer(t, context.Background(), DefaultConfig(), health.NewTracker())
	registerTestService(server, service)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- server.Serve(listener)
	}()
	connection := dialRealServer(t, listener.Addr().String())
	defer connection.Close()

	requestResult := make(chan error, 1)
	go func() {
		requestResult <- connection.Invoke(
			context.Background(),
			testUnaryMethod,
			&healthpb.HealthCheckRequest{Service: "cancel"},
			&healthpb.HealthCheckRequest{},
		)
	}()
	select {
	case <-service.blockEntered:
	case <-time.After(time.Second):
		t.Fatal("cancelable RPC did not enter handler")
	}

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := server.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired Shutdown() error = %v, want DeadlineExceeded", err)
	}
	if err := <-serveResult; err != nil {
		t.Fatalf("Serve() after expired shutdown error = %v", err)
	}
	if code := status.Code(<-requestResult); code != codes.Canceled && code != codes.Unavailable {
		t.Fatalf("forced RPC code = %s, want Canceled or Unavailable", code)
	}
}

func TestConcurrentShutdownSharesInitiatorResult(t *testing.T) {
	t.Parallel()

	service := newBlockingTestService()
	server, _ := newTestServer(t, context.Background(), DefaultConfig(), health.NewTracker())
	registerTestService(server, service)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- server.Serve(listener)
	}()
	connection := dialRealServer(t, listener.Addr().String())
	defer connection.Close()

	requestResult := make(chan error, 1)
	go func() {
		requestResult <- connection.Invoke(
			context.Background(),
			testUnaryMethod,
			&healthpb.HealthCheckRequest{Service: "block"},
			&healthpb.HealthCheckRequest{},
		)
	}()
	select {
	case <-service.blockEntered:
	case <-time.After(time.Second):
		t.Fatal("blocking RPC did not enter handler")
	}

	initiatorContext, cancelInitiator := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelInitiator()
	initiatorResult := make(chan error, 1)
	go func() {
		initiatorResult <- server.Shutdown(initiatorContext)
	}()
	waitForLifecycleState(t, server, stateStopping)

	waiterContext, cancelWaiter := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelWaiter()
	waiterResult := make(chan error, 1)
	go func() {
		waiterResult <- server.Shutdown(waiterContext)
	}()
	<-waiterContext.Done()
	select {
	case err := <-waiterResult:
		t.Fatalf("concurrent Shutdown() returned before shared drain: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(service.releaseBlock)
	if err := <-requestResult; err != nil {
		t.Fatalf("blocking RPC error = %v", err)
	}
	if err := <-initiatorResult; err != nil {
		t.Fatalf("initiating Shutdown() error = %v", err)
	}
	if err := <-waiterResult; err != nil {
		t.Fatalf("concurrent Shutdown() shared error = %v", err)
	}
	if err := <-serveResult; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}

func TestServeShutdownStartupRaceIsTerminal(t *testing.T) {
	for iteration := 0; iteration < 64; iteration++ {
		server, _ := newTestServer(t, context.Background(), DefaultConfig(), health.NewTracker())
		listener := bufconn.Listen(1 << 10)
		start := make(chan struct{})
		serveResult := make(chan error, 1)
		shutdownResult := make(chan error, 1)
		go func() {
			<-start
			serveResult <- server.Serve(listener)
		}()
		go func() {
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			shutdownResult <- server.Shutdown(ctx)
		}()
		close(start)

		select {
		case err := <-shutdownResult:
			if err != nil {
				t.Fatalf("iteration %d Shutdown() error = %v", iteration, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("iteration %d Shutdown() did not return", iteration)
		}
		select {
		case err := <-serveResult:
			if err != nil && !errors.Is(err, ErrServerStopped) {
				t.Fatalf("iteration %d Serve() error = %v", iteration, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("iteration %d Serve() did not return", iteration)
		}
		if _, err := listener.DialContext(context.Background()); err == nil {
			t.Fatalf("iteration %d listener remained open", iteration)
		}
	}
}

func TestConcurrentStreamLimitQueuesAdditionalRPC(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.MaxConcurrentStreams = 1
	service := newBlockingTestService()
	server, _ := newTestServer(t, context.Background(), config, health.NewTracker())
	registerTestService(server, service)
	connection, stop := startBufServer(t, server)
	defer stop()

	firstResult := make(chan error, 1)
	go func() {
		firstResult <- connection.Invoke(
			context.Background(),
			testUnaryMethod,
			&healthpb.HealthCheckRequest{Service: "block"},
			&healthpb.HealthCheckRequest{},
		)
	}()
	select {
	case <-service.blockEntered:
	case <-time.After(time.Second):
		t.Fatal("first RPC did not enter handler")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := connection.Invoke(
		ctx,
		testUnaryMethod,
		&healthpb.HealthCheckRequest{Service: "echo"},
		&healthpb.HealthCheckRequest{},
	)
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("queued RPC code = %s, want DeadlineExceeded", status.Code(err))
	}
	if got := service.calls.Load(); got != 1 {
		t.Fatalf("handler calls while stream saturated = %d, want 1", got)
	}
	close(service.releaseBlock)
	if err := <-firstResult; err != nil {
		t.Fatalf("first RPC error = %v", err)
	}
}

func dialRealServer(t *testing.T, address string) *grpc.ClientConn {
	t.Helper()
	connection, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	return connection
}

func waitForLifecycleState(t *testing.T, server *Server, want lifecycleState) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		state := server.state
		server.mu.Unlock()
		if state == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	server.mu.Lock()
	state := server.state
	server.mu.Unlock()
	t.Fatalf("server lifecycle state = %d, want %d", state, want)
}
