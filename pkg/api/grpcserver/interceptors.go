package grpcserver

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wowtrust/final-weave/pkg/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const requestIDMetadataKey = "x-request-id"

const (
	healthCheckMethod = "/grpc.health.v1.Health/Check"
	healthListMethod  = "/grpc.health.v1.Health/List"
	healthWatchMethod = "/grpc.health.v1.Health/Watch"
)

type requestIDContextKey struct{}

// RequestID returns the validated gRPC request ID stored in ctx, if present.
func RequestID(ctx context.Context) string {
	if isNilInterface(ctx) {
		return ""
	}
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

type interceptorSet struct {
	root            context.Context
	timeout         time.Duration
	metadataLimit   uint64
	logger          *observability.Logger
	requestIDs      *atomic.Uint64
	healthRefresher func()
}

func (interceptors *interceptorSet) unary(
	ctx context.Context,
	request any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (response any, err error) {
	started := time.Now()
	method := safeRPCMethod(info.FullMethod)
	requestID := requestIDFromMetadata(ctx, interceptors.requestIDs)
	requestContext, cleanup := deriveRequestContext(
		ctx,
		interceptors.root,
		interceptors.timeout,
		requestID,
	)
	defer cleanup()

	defer func() {
		if recover() != nil {
			interceptors.logPanic(method, requestID)
			response = nil
			err = status.Error(codes.Internal, "internal server error")
		}
		interceptors.logCompletion(method, requestID, status.Code(err), started)
	}()

	if err := grpc.SetHeader(
		requestContext,
		metadata.Pairs(requestIDMetadataKey, requestID),
	); err != nil {
		return nil, status.Error(codes.Internal, "internal server error")
	}
	if metadataSize(requestContext) > interceptors.metadataLimit {
		return nil, status.Error(codes.ResourceExhausted, "request metadata exceeds limit")
	}
	if interceptors.healthRefresher != nil && isUnaryHealthMethod(info.FullMethod) {
		interceptors.healthRefresher()
	}
	if contextErr := requestContext.Err(); contextErr != nil {
		return nil, status.FromContextError(contextErr).Err()
	}

	response, err = handler(requestContext, request)
	if err == nil {
		if contextErr := requestContext.Err(); contextErr != nil {
			return nil, status.FromContextError(contextErr).Err()
		}
	}
	return response, err
}

func (interceptors *interceptorSet) stream(
	service any,
	stream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) (err error) {
	started := time.Now()
	method := safeRPCMethod(info.FullMethod)
	requestID := requestIDFromMetadata(stream.Context(), interceptors.requestIDs)
	requestContext, cleanup := deriveRequestContext(
		stream.Context(),
		interceptors.root,
		0,
		requestID,
	)
	defer cleanup()

	defer func() {
		if recover() != nil {
			interceptors.logPanic(method, requestID)
			err = status.Error(codes.Internal, "internal server error")
		}
		interceptors.logCompletion(method, requestID, status.Code(err), started)
	}()

	wrapped := &contextServerStream{ServerStream: stream, context: requestContext}
	if err := wrapped.SetHeader(metadata.Pairs(requestIDMetadataKey, requestID)); err != nil {
		return status.Error(codes.Internal, "internal server error")
	}
	if metadataSize(requestContext) > interceptors.metadataLimit {
		return status.Error(codes.ResourceExhausted, "request metadata exceeds limit")
	}
	if interceptors.healthRefresher != nil && info.FullMethod == healthWatchMethod {
		interceptors.healthRefresher()
	}
	if contextErr := requestContext.Err(); contextErr != nil {
		return status.FromContextError(contextErr).Err()
	}
	return handler(service, wrapped)
}

func (interceptors *interceptorSet) logPanic(method, requestID string) {
	interceptors.logger.Error("grpc_request_panicked").
		Str("transport", "grpc").
		Str("method", method).
		Str("request_id", requestID).
		Msg("gRPC request panic recovered")
}

func (interceptors *interceptorSet) logCompletion(
	method string,
	requestID string,
	code codes.Code,
	started time.Time,
) {
	interceptors.logger.Info("grpc_request_completed").
		Str("transport", "grpc").
		Str("method", method).
		Str("status_code", code.String()).
		Str("request_id", requestID).
		Int64("duration_ms", time.Since(started).Milliseconds()).
		Msg("gRPC request completed")
}

type contextServerStream struct {
	grpc.ServerStream
	context context.Context
}

func (stream *contextServerStream) Context() context.Context {
	return stream.context
}

func deriveRequestContext(
	incoming context.Context,
	root context.Context,
	timeout time.Duration,
	requestID string,
) (context.Context, func()) {
	requestContext, cancelRoot := context.WithCancel(incoming)
	stopRoot := context.AfterFunc(root, cancelRoot)
	if root.Err() != nil {
		cancelRoot()
	}
	requestContext = context.WithValue(requestContext, requestIDContextKey{}, requestID)

	if timeout <= 0 {
		return requestContext, func() {
			stopRoot()
			cancelRoot()
		}
	}

	requestContext, cancelTimeout := context.WithTimeout(requestContext, timeout)
	return requestContext, func() {
		cancelTimeout()
		stopRoot()
		cancelRoot()
	}
}

func requestIDFromMetadata(ctx context.Context, sequence *atomic.Uint64) string {
	if incoming, ok := metadata.FromIncomingContext(ctx); ok {
		values := incoming.Get(requestIDMetadataKey)
		if len(values) == 1 {
			if accepted := acceptedRequestID(values[0]); accepted != "" {
				return accepted
			}
		}
	}
	return "fw-grpc-" + strconv.FormatUint(sequence.Add(1), 36)
}

func metadataSize(ctx context.Context) uint64 {
	incoming, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return 0
	}
	var size uint64
	for key, values := range incoming {
		for _, value := range values {
			// HTTP/2 defines header-list size as name + value + 32 bytes for
			// every field. Counting each repeated value prevents empty or
			// repeated metadata from bypassing the application budget.
			fieldSize := saturatingAdd(uint64(len(key)), uint64(len(value)))
			fieldSize = saturatingAdd(fieldSize, 32)
			size = saturatingAdd(size, fieldSize)
		}
	}
	return size
}

func saturatingAdd(left, right uint64) uint64 {
	result := left + right
	if result < left {
		return ^uint64(0)
	}
	return result
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

func safeRPCMethod(method string) string {
	switch method {
	case healthCheckMethod, healthListMethod, healthWatchMethod:
		return method
	default:
		return "unmatched"
	}
}

func isUnaryHealthMethod(method string) bool {
	return method == healthCheckMethod || method == healthListMethod
}
