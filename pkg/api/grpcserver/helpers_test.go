package grpcserver

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/wowtrust/final-weave/pkg/api/health"
	"github.com/wowtrust/final-weave/pkg/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func newTestServer(
	t *testing.T,
	root context.Context,
	config Config,
	tracker *health.Tracker,
) (*Server, *synchronizedBuffer) {
	t.Helper()

	logger, output := testLogger(t)
	server, err := New(root, config, tracker, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return server, output
}

func testLogger(t *testing.T) (*observability.Logger, *synchronizedBuffer) {
	t.Helper()

	var output synchronizedBuffer
	logger, err := observability.NewLogger(&output, observability.DefaultLogConfig())
	if err != nil {
		t.Fatalf("observability.NewLogger() error = %v", err)
	}
	return logger, &output
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func startBufServer(t *testing.T, server *Server) (*grpc.ClientConn, func()) {
	t.Helper()

	listener := bufconn.Listen(1 << 20)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}

	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
		if err := connection.Close(); err != nil {
			t.Errorf("ClientConn.Close() error = %v", err)
		}
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				t.Errorf("Serve() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Error("Serve() did not return after Shutdown")
		}
	}

	return connection, stop
}
