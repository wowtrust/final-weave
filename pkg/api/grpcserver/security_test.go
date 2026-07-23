package grpcserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wowtrust/final-weave/pkg/api/health"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestMessageAndMetadataLimitsFailClosedWithoutDisclosure(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.MaxReceiveMessageBytes = 128
	config.MaxSendMessageBytes = 128
	config.MaxHeaderListBytes = minApplicationMetadataBytes
	server, logs := newTestServer(t, context.Background(), config, health.NewTracker())
	registerTestService(server, &testService{})
	connection, stop := startBufServer(t, server)
	defer stop()

	err := connection.Invoke(
		context.Background(),
		testUnaryMethod,
		&healthpb.HealthCheckRequest{Service: strings.Repeat("request-secret", 128)},
		&healthpb.HealthCheckRequest{},
	)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("oversized request code = %s, want ResourceExhausted", status.Code(err))
	}

	err = connection.Invoke(
		context.Background(),
		testUnaryMethod,
		&healthpb.HealthCheckRequest{Service: "large"},
		&healthpb.HealthCheckRequest{},
	)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("oversized response code = %s, want ResourceExhausted", status.Code(err))
	}

	metadataContext := metadata.NewOutgoingContext(
		context.Background(),
		metadata.Pairs("authorization", strings.Repeat("metadata-secret", 128)),
	)
	err = connection.Invoke(
		metadataContext,
		testUnaryMethod,
		&healthpb.HealthCheckRequest{Service: "echo"},
		&healthpb.HealthCheckRequest{},
	)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("oversized metadata code = %s, want ResourceExhausted", status.Code(err))
	}

	client := healthpb.NewHealthClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("server unusable after bounded rejections: %v", err)
	}

	logText := logs.String()
	for _, secret := range []string{"request-secret", "response-secret", "metadata-secret"} {
		if strings.Contains(logText, secret) {
			t.Errorf("gRPC logs disclose %q: %s", secret, logText)
		}
	}
}

func TestUnaryAndStreamPanicsAreIsolated(t *testing.T) {
	t.Parallel()

	server, logs := newTestServer(t, context.Background(), DefaultConfig(), health.NewTracker())
	registerTestService(server, &testService{})
	connection, stop := startBufServer(t, server)
	defer stop()

	err := connection.Invoke(
		context.Background(),
		testUnaryMethod,
		&healthpb.HealthCheckRequest{Service: "panic"},
		&healthpb.HealthCheckRequest{},
	)
	if status.Code(err) != codes.Internal {
		t.Fatalf("panic unary code = %s, want Internal", status.Code(err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := connection.NewStream(
		ctx,
		&testOperationalServiceDesc.Streams[0],
		testStreamMethod,
	)
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	if err := stream.SendMsg(&healthpb.HealthCheckRequest{Service: "stream-request-secret"}); err != nil {
		t.Fatalf("stream.SendMsg() error = %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("stream.CloseSend() error = %v", err)
	}
	err = stream.RecvMsg(&healthpb.HealthCheckRequest{})
	if status.Code(err) != codes.Internal {
		t.Fatalf("panic stream code = %s, want Internal", status.Code(err))
	}

	client := healthpb.NewHealthClient(connection)
	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("server unusable after recovered panic: %v", err)
	}
	logText := logs.String()
	for _, secret := range []string{"panic-secret", "stream-panic-secret", "stream-request-secret"} {
		if strings.Contains(logText, secret) {
			t.Errorf("panic logs disclose %q: %s", secret, logText)
		}
	}
	if count := strings.Count(logText, `"event":"grpc_request_panicked"`); count != 2 {
		t.Fatalf("panic event count = %d, want 2: %q", count, logText)
	}
}
