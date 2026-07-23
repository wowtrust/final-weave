package grpcserver

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wowtrust/final-weave/pkg/api/health"
	"github.com/wowtrust/final-weave/pkg/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestNewValidatesDependenciesWithoutStartingWork(t *testing.T) {
	t.Parallel()

	tracker := health.NewTracker()
	logger, _ := testLogger(t)
	valid := DefaultConfig()
	var typedNilRoot *panicOnUseContext
	tests := []struct {
		name    string
		root    context.Context
		tracker *health.Tracker
		logger  *observability.Logger
		want    error
	}{
		{
			name:    "nil root",
			tracker: tracker,
			logger:  logger,
			want:    ErrNilRootContext,
		},
		{
			name:    "typed nil root",
			root:    typedNilRoot,
			tracker: tracker,
			logger:  logger,
			want:    ErrNilRootContext,
		},
		{
			name:   "nil tracker",
			root:   context.Background(),
			logger: logger,
			want:   ErrNilReadinessTracker,
		},
		{
			name:    "nil logger",
			root:    context.Background(),
			tracker: tracker,
			want:    ErrNilLogger,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(test.root, valid, test.tracker, test.logger)
			if !errors.Is(err, test.want) {
				t.Fatalf("New() error = %v, want %v", err, test.want)
			}
		})
	}

	invalid := valid
	invalid.MaxConcurrentStreams = 0
	if _, err := New(context.Background(), invalid, tracker, logger); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(invalid config) error = %v, want ErrInvalidConfig", err)
	}

	server, err := New(context.Background(), valid, tracker, logger)
	if err != nil {
		t.Fatalf("New(valid config) error = %v", err)
	}
	services := server.server.GetServiceInfo()
	if len(services) != 1 {
		t.Fatalf("registered service count = %d, want 1: %v", len(services), services)
	}
	if _, ok := services["grpc.health.v1.Health"]; !ok {
		t.Fatalf("standard health service is not the only registered service: %v", services)
	}
}

func TestShutdownBeforeServeIsTerminal(t *testing.T) {
	t.Parallel()

	tracker := health.NewTracker()
	server, _ := newTestServer(t, context.Background(), DefaultConfig(), tracker)
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

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() before Serve error = %v", err)
	}
	tracker.Store(health.Readiness{Ready: true})
	server.RefreshHealth()
	healthResponse, err := server.health.server.Check(context.Background(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Health.Check() after Shutdown error = %v", err)
	}
	if healthResponse.Status != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("health after Shutdown and refresh = %s, want NOT_SERVING", healthResponse.Status)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	address := listener.Addr().String()
	if err := server.Serve(listener); !errors.Is(err, ErrServerStopped) {
		t.Fatalf("Serve() after Shutdown error = %v, want ErrServerStopped", err)
	}
	if _, err := net.DialTimeout("tcp", address, 100*time.Millisecond); err == nil {
		t.Fatal("Serve() after Shutdown left the supplied listener open")
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

func TestStandardHealthTracksBoundedReadiness(t *testing.T) {
	t.Parallel()

	tracker := health.NewTracker()
	server, logs := newTestServer(t, context.Background(), DefaultConfig(), tracker)
	connection, stop := startBufServer(t, server)
	defer stop()
	client := healthpb.NewHealthClient(connection)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	requestContext := metadata.NewOutgoingContext(
		ctx,
		metadata.Pairs(requestIDMetadataKey, "safe.grpc-1"),
	)
	var headers metadata.MD
	response, err := client.Check(
		requestContext,
		&healthpb.HealthCheckRequest{},
		grpc.Header(&headers),
	)
	if err != nil {
		t.Fatalf("Health.Check() error = %v", err)
	}
	if response.Status != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("initial health status = %s, want NOT_SERVING", response.Status)
	}
	if got := headers.Get(requestIDMetadataKey); len(got) != 1 || got[0] != "safe.grpc-1" {
		t.Fatalf("response request ID = %v, want safe.grpc-1", got)
	}

	tracker.Store(health.Readiness{Ready: true})
	response, err = client.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("ready Health.Check() error = %v", err)
	}
	if response.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("ready health status = %s, want SERVING", response.Status)
	}

	list, err := client.List(ctx, &healthpb.HealthListRequest{})
	if err != nil {
		t.Fatalf("Health.List() error = %v", err)
	}
	if listed := list.Statuses[""]; listed == nil || listed.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("listed aggregate health = %+v, want SERVING", listed)
	}

	watchContext, cancelWatch := context.WithCancel(ctx)
	watch, err := client.Watch(watchContext, &healthpb.HealthCheckRequest{})
	if err != nil {
		cancelWatch()
		t.Fatalf("Health.Watch() error = %v", err)
	}
	watchStatus, err := watch.Recv()
	if err != nil {
		cancelWatch()
		t.Fatalf("Health.Watch().Recv() initial error = %v", err)
	}
	if watchStatus.Status != healthpb.HealthCheckResponse_SERVING {
		cancelWatch()
		t.Fatalf("initial watch status = %s, want SERVING", watchStatus.Status)
	}

	tracker.Store(health.Readiness{Reason: health.ReasonRecovering})
	server.RefreshHealth()
	watchStatus, err = watch.Recv()
	if err != nil {
		cancelWatch()
		t.Fatalf("Health.Watch().Recv() update error = %v", err)
	}
	if watchStatus.Status != healthpb.HealthCheckResponse_NOT_SERVING {
		cancelWatch()
		t.Fatalf("updated watch status = %s, want NOT_SERVING", watchStatus.Status)
	}
	for _, reason := range []health.Reason{
		health.ReasonRuntimeUnavailable,
		health.ReasonDraining,
		health.ReasonFailed,
		health.Reason("provider-secret"),
	} {
		tracker.Store(health.Readiness{Reason: reason})
		response, err = client.Check(ctx, &healthpb.HealthCheckRequest{})
		if err != nil {
			cancelWatch()
			t.Fatalf("Health.Check(%q) error = %v", reason, err)
		}
		if response.Status != healthpb.HealthCheckResponse_NOT_SERVING {
			cancelWatch()
			t.Fatalf("Health.Check(%q) = %s, want NOT_SERVING", reason, response.Status)
		}
	}
	cancelWatch()
	for {
		if _, err := watch.Recv(); err != nil {
			if status.Code(err) != codes.Canceled {
				t.Fatalf("canceled Health.Watch().Recv() code = %s, want Canceled", status.Code(err))
			}
			break
		}
	}

	_, err = client.Check(ctx, &healthpb.HealthCheckRequest{Service: "unknown-service"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("unknown Health.Check() code = %s, want NotFound", status.Code(err))
	}
	if strings.Contains(logs.String(), "unknown-service") || strings.Contains(logs.String(), "provider-secret") {
		t.Fatalf("gRPC logs disclose health request values: %q", logs.String())
	}
}

func TestRequestIDsAndAccessLogsExcludeMetadata(t *testing.T) {
	t.Parallel()
	if got := RequestID(nil); got != "" {
		t.Fatalf("RequestID(nil) = %q, want empty", got)
	}
	var typedNil *panicOnUseContext
	if got := RequestID(typedNil); got != "" {
		t.Fatalf("RequestID(typed nil) = %q, want empty", got)
	}

	server, logs := newTestServer(t, context.Background(), DefaultConfig(), health.NewTracker())
	registerTestService(server, &testService{})
	connection, stop := startBufServer(t, server)
	defer stop()

	invoke := func(ctx context.Context) string {
		t.Helper()
		response := &healthpb.HealthCheckRequest{}
		var headers metadata.MD
		if err := connection.Invoke(
			ctx,
			testUnaryMethod,
			&healthpb.HealthCheckRequest{Service: "request-body-secret"},
			response,
			grpc.Header(&headers),
		); err != nil {
			t.Fatalf("Invoke() error = %v", err)
		}
		got := headers.Get(requestIDMetadataKey)
		if len(got) != 1 || got[0] != response.Service {
			t.Fatalf("response header request ID = %v, response = %q", got, response.Service)
		}
		return response.Service
	}

	safeContext := metadata.NewOutgoingContext(
		context.Background(),
		metadata.Pairs(
			requestIDMetadataKey, "safe.grpc-2",
			"authorization", "Bearer authorization-secret",
			"cookie", "cookie-secret",
		),
	)
	if got := invoke(safeContext); got != "safe.grpc-2" {
		t.Fatalf("safe request ID = %q, want safe.grpc-2", got)
	}

	unsafeContext := metadata.NewOutgoingContext(
		context.Background(),
		metadata.Pairs(requestIDMetadataKey, "unsafe/id?request-secret"),
	)
	generated := invoke(unsafeContext)
	if !regexp.MustCompile(`^fw-grpc-[0-9a-z]+$`).MatchString(generated) {
		t.Fatalf("generated request ID = %q", generated)
	}

	repeatedContext := metadata.NewOutgoingContext(
		context.Background(),
		metadata.MD{requestIDMetadataKey: []string{"first-secret", "second-secret"}},
	)
	if got := invoke(repeatedContext); !regexp.MustCompile(`^fw-grpc-[0-9a-z]+$`).MatchString(got) {
		t.Fatalf("repeated metadata request ID = %q", got)
	}

	oversizedContext := metadata.NewOutgoingContext(
		context.Background(),
		metadata.Pairs(requestIDMetadataKey, strings.Repeat("x", 65)),
	)
	if got := invoke(oversizedContext); !regexp.MustCompile(`^fw-grpc-[0-9a-z]+$`).MatchString(got) {
		t.Fatalf("oversized metadata request ID = %q", got)
	}

	logText := logs.String()
	for _, secret := range []string{
		"request-body-secret",
		"authorization-secret",
		"cookie-secret",
		"request-secret",
		"first-secret",
		"second-secret",
	} {
		if strings.Contains(logText, secret) {
			t.Errorf("gRPC log discloses %q: %s", secret, logText)
		}
	}
	if strings.Contains(logText, testUnaryMethod) {
		t.Fatalf("unregistered method allowlist leaked test method: %q", logText)
	}
}

func TestHealthAccessLogUsesStableAllowlistedFields(t *testing.T) {
	t.Parallel()

	server, logs := newTestServer(t, context.Background(), DefaultConfig(), health.NewTracker())
	connection, stop := startBufServer(t, server)
	client := healthpb.NewHealthClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("Health.Check() error = %v", err)
	}
	stop()

	entry := decodeSingleLogEntry(t, logs.String())
	wantKeys := []string{
		"component",
		"duration_ms",
		"event",
		"level",
		"message",
		"method",
		"monotonic_sequence",
		"request_id",
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
		t.Fatalf("log fields = %v, want %v", gotKeys, wantKeys)
	}
	assertLogString(t, entry, "component", "grpc")
	assertLogString(t, entry, "event", "grpc_request_completed")
	assertLogString(t, entry, "method", healthCheckMethod)
	assertLogString(t, entry, "status_code", codes.OK.String())
	assertLogString(t, entry, "transport", "grpc")
	requestID, ok := entry["request_id"].(string)
	if !ok || !regexp.MustCompile(`^fw-grpc-[0-9a-z]+$`).MatchString(requestID) {
		t.Fatalf("generated log request ID = %#v", entry["request_id"])
	}
}

func TestUnaryDeadlineAndRootCancellation(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.UnaryRequestTimeout = 200 * time.Millisecond
	root, cancelRoot := context.WithCancel(context.Background())
	server, _ := newTestServer(t, root, config, health.NewTracker())
	registerTestService(server, &testService{})
	connection, stop := startBufServer(t, server)
	defer stop()

	started := time.Now()
	err := connection.Invoke(
		context.Background(),
		testUnaryMethod,
		&healthpb.HealthCheckRequest{Service: "deadline"},
		&healthpb.HealthCheckRequest{},
	)
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("deadline Invoke() code = %s, want DeadlineExceeded", status.Code(err))
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("server unary deadline elapsed = %s", elapsed)
	}

	clientContext, cancelClient := context.WithTimeout(context.Background(), 100*time.Millisecond)
	clientDeadline, _ := clientContext.Deadline()
	deadlineResponse := &healthpb.HealthCheckRequest{}
	err = connection.Invoke(
		clientContext,
		testUnaryMethod,
		&healthpb.HealthCheckRequest{Service: "deadline-value"},
		deadlineResponse,
	)
	cancelClient()
	if err != nil {
		t.Fatalf("client deadline observation error = %v", err)
	}
	observedUnixNano, err := strconv.ParseInt(deadlineResponse.Service, 10, 64)
	if err != nil {
		t.Fatalf("strconv.ParseInt(%q) error = %v", deadlineResponse.Service, err)
	}
	observedDeadline := time.Unix(0, observedUnixNano)
	if difference := observedDeadline.Sub(clientDeadline); difference < -10*time.Millisecond || difference > 10*time.Millisecond {
		t.Fatalf("handler deadline = %s, client deadline = %s", observedDeadline, clientDeadline)
	}

	cancelRoot()
	err = connection.Invoke(
		context.Background(),
		testUnaryMethod,
		&healthpb.HealthCheckRequest{Service: "echo"},
		&healthpb.HealthCheckRequest{},
	)
	if status.Code(err) != codes.Canceled {
		t.Fatalf("root-canceled Invoke() code = %s, want Canceled", status.Code(err))
	}
}

func TestConcurrentHealthRefreshAndChecksAreRaceSafe(t *testing.T) {
	t.Parallel()

	tracker := health.NewTracker()
	server, _ := newTestServer(t, context.Background(), DefaultConfig(), tracker)
	connection, stop := startBufServer(t, server)
	defer stop()
	client := healthpb.NewHealthClient(connection)

	const requests = 32
	var group sync.WaitGroup
	for index := 0; index < requests; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			if index%2 == 0 {
				tracker.Store(health.Readiness{Ready: true})
			} else {
				tracker.Store(health.Readiness{Reason: health.ReasonRecovering})
			}
			server.RefreshHealth()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			response, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
			if err != nil {
				t.Errorf("Health.Check() error = %v", err)
				return
			}
			if response.Status != healthpb.HealthCheckResponse_SERVING &&
				response.Status != healthpb.HealthCheckResponse_NOT_SERVING {
				t.Errorf("Health.Check() status = %s", response.Status)
			}
		}(index)
	}
	group.Wait()
}

func decodeSingleLogEntry(t *testing.T, text string) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 1 {
		t.Fatalf("log line count = %d, want 1: %q", len(lines), text)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", lines[0], err)
	}
	return entry
}

func assertLogString(t *testing.T, entry map[string]any, field, want string) {
	t.Helper()
	if got, ok := entry[field].(string); !ok || got != want {
		t.Fatalf("log field %s = %#v, want %q", field, entry[field], want)
	}
}
