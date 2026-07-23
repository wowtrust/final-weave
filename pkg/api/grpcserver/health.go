package grpcserver

import (
	"sync"

	"github.com/wowtrust/final-weave/pkg/api/health"
	grpc_health "google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type healthBridge struct {
	tracker *health.Tracker
	server  *grpc_health.Server

	mu      sync.Mutex
	stopped bool
}

func newHealthBridge(tracker *health.Tracker) *healthBridge {
	server := grpc_health.NewServer()
	server.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	return &healthBridge{tracker: tracker, server: server}
}

func (bridge *healthBridge) refresh() {
	status := healthpb.HealthCheckResponse_NOT_SERVING
	if bridge.tracker.Snapshot().Ready {
		status = healthpb.HealthCheckResponse_SERVING
	}

	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.stopped {
		return
	}
	bridge.server.SetServingStatus("", status)
}

func (bridge *healthBridge) shutdown() {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	if bridge.stopped {
		return
	}
	bridge.stopped = true
	bridge.server.Shutdown()
}
