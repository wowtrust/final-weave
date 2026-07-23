package grpcserver

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

const (
	testUnaryMethod  = "/finalweave.test.Operational/Unary"
	testStreamMethod = "/finalweave.test.Operational/PanicStream"
)

type testOperationalServer interface {
	Unary(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckRequest, error)
	PanicStream(grpc.ServerStream) error
}

type testService struct {
	blockEntered chan struct{}
	releaseBlock chan struct{}
	enterOnce    sync.Once
	calls        atomic.Int32
}

func newBlockingTestService() *testService {
	return &testService{
		blockEntered: make(chan struct{}),
		releaseBlock: make(chan struct{}),
	}
}

func (service *testService) Unary(
	ctx context.Context,
	request *healthpb.HealthCheckRequest,
) (*healthpb.HealthCheckRequest, error) {
	service.calls.Add(1)
	switch request.Service {
	case "panic":
		panic("panic-secret")
	case "deadline":
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	case "deadline-value":
		deadline, ok := ctx.Deadline()
		if !ok {
			return &healthpb.HealthCheckRequest{Service: "none"}, nil
		}
		return &healthpb.HealthCheckRequest{Service: strconv.FormatInt(deadline.UnixNano(), 10)}, nil
	case "block":
		service.enterOnce.Do(func() { close(service.blockEntered) })
		<-service.releaseBlock
		return &healthpb.HealthCheckRequest{Service: "released"}, nil
	case "cancel":
		service.enterOnce.Do(func() { close(service.blockEntered) })
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	case "large":
		return &healthpb.HealthCheckRequest{Service: strings.Repeat("response-secret", 128)}, nil
	default:
		return &healthpb.HealthCheckRequest{Service: RequestID(ctx)}, nil
	}
}

func (*testService) PanicStream(stream grpc.ServerStream) error {
	request := &healthpb.HealthCheckRequest{}
	if err := stream.RecvMsg(request); err != nil {
		return err
	}
	panic("stream-panic-secret")
}

func registerTestService(server *Server, service testOperationalServer) {
	server.server.RegisterService(&testOperationalServiceDesc, service)
}

var testOperationalServiceDesc = grpc.ServiceDesc{
	ServiceName: "finalweave.test.Operational",
	HandlerType: (*testOperationalServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Unary",
			Handler: func(
				service any,
				ctx context.Context,
				decode func(any) error,
				interceptor grpc.UnaryServerInterceptor,
			) (any, error) {
				request := &healthpb.HealthCheckRequest{}
				if err := decode(request); err != nil {
					return nil, err
				}
				if interceptor == nil {
					return service.(testOperationalServer).Unary(ctx, request)
				}
				info := &grpc.UnaryServerInfo{Server: service, FullMethod: testUnaryMethod}
				handler := func(ctx context.Context, request any) (any, error) {
					return service.(testOperationalServer).Unary(
						ctx,
						request.(*healthpb.HealthCheckRequest),
					)
				}
				return interceptor(ctx, request, info, handler)
			},
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName: "PanicStream",
			Handler: func(service any, stream grpc.ServerStream) error {
				return service.(testOperationalServer).PanicStream(stream)
			},
			ServerStreams: true,
			ClientStreams: true,
		},
	},
}
