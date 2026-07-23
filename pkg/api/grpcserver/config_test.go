package grpcserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"
)

func TestConfigValidationRejectsUnsafeLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "zero receive bytes", mutate: func(config *Config) { config.MaxReceiveMessageBytes = 0 }},
		{name: "negative receive bytes", mutate: func(config *Config) { config.MaxReceiveMessageBytes = -1 }},
		{
			name: "excessive receive bytes",
			mutate: func(config *Config) {
				config.MaxReceiveMessageBytes = maxMessageBytes + 1
			},
		},
		{name: "zero send bytes", mutate: func(config *Config) { config.MaxSendMessageBytes = 0 }},
		{name: "negative send bytes", mutate: func(config *Config) { config.MaxSendMessageBytes = -1 }},
		{
			name: "excessive send bytes",
			mutate: func(config *Config) {
				config.MaxSendMessageBytes = maxMessageBytes + 1
			},
		},
		{name: "zero header bytes", mutate: func(config *Config) { config.MaxHeaderListBytes = 0 }},
		{
			name: "undersized header bytes",
			mutate: func(config *Config) {
				config.MaxHeaderListBytes = minApplicationMetadataBytes - 1
			},
		},
		{
			name: "excessive header bytes",
			mutate: func(config *Config) {
				config.MaxHeaderListBytes = maxHeaderListBytes + 1
			},
		},
		{name: "zero streams", mutate: func(config *Config) { config.MaxConcurrentStreams = 0 }},
		{
			name: "excessive streams",
			mutate: func(config *Config) {
				config.MaxConcurrentStreams = maxConcurrentStreams + 1
			},
		},
		{
			name: "receive aggregate",
			mutate: func(config *Config) {
				config.MaxReceiveMessageBytes = maxMessageBytes
				config.MaxConcurrentStreams = 5
			},
		},
		{
			name: "send aggregate",
			mutate: func(config *Config) {
				config.MaxSendMessageBytes = maxMessageBytes
				config.MaxConcurrentStreams = 5
			},
		},
		{
			name: "header aggregate",
			mutate: func(config *Config) {
				config.MaxHeaderListBytes = maxHeaderListBytes
				config.MaxConcurrentStreams = 257
			},
		},
		{
			name: "zero unary timeout",
			mutate: func(config *Config) {
				config.UnaryRequestTimeout = 0
			},
		},
		{
			name: "negative unary timeout",
			mutate: func(config *Config) {
				config.UnaryRequestTimeout = -time.Nanosecond
			},
		},
		{
			name: "excessive unary timeout",
			mutate: func(config *Config) {
				config.UnaryRequestTimeout = maxUnaryRequestTimeout + time.Nanosecond
			},
		},
		{name: "zero connection idle", mutate: func(config *Config) { config.MaxConnectionIdle = 0 }},
		{
			name: "negative connection idle",
			mutate: func(config *Config) {
				config.MaxConnectionIdle = -time.Nanosecond
			},
		},
		{
			name: "excessive connection idle",
			mutate: func(config *Config) {
				config.MaxConnectionIdle = maxConnectionIdle + time.Nanosecond
			},
		},
		{name: "zero connection age", mutate: func(config *Config) { config.MaxConnectionAge = 0 }},
		{
			name: "negative connection age",
			mutate: func(config *Config) {
				config.MaxConnectionAge = -time.Nanosecond
			},
		},
		{
			name: "excessive connection age",
			mutate: func(config *Config) {
				config.MaxConnectionAge = maxConnectionAge + time.Nanosecond
			},
		},
		{
			name: "zero connection age grace",
			mutate: func(config *Config) {
				config.MaxConnectionAgeGrace = 0
			},
		},
		{
			name: "negative connection age grace",
			mutate: func(config *Config) {
				config.MaxConnectionAgeGrace = -time.Nanosecond
			},
		},
		{
			name: "excessive connection age grace",
			mutate: func(config *Config) {
				config.MaxConnectionAgeGrace = maxConnectionAgeGrace + time.Nanosecond
			},
		},
		{
			name: "age grace exceeds age",
			mutate: func(config *Config) {
				config.MaxConnectionAge = time.Second
				config.MaxConnectionAgeGrace = 2 * time.Second
			},
		},
		{
			name: "zero keepalive time",
			mutate: func(config *Config) {
				config.KeepaliveTime = 0
			},
		},
		{
			name: "negative keepalive time",
			mutate: func(config *Config) {
				config.KeepaliveTime = -time.Nanosecond
			},
		},
		{
			name: "keepalive below grpc minimum",
			mutate: func(config *Config) {
				config.KeepaliveTime = time.Second - time.Nanosecond
			},
		},
		{
			name: "excessive keepalive time",
			mutate: func(config *Config) {
				config.KeepaliveTime = maxKeepaliveTime + time.Nanosecond
			},
		},
		{
			name: "zero keepalive timeout",
			mutate: func(config *Config) {
				config.KeepaliveTimeout = 0
			},
		},
		{
			name: "negative keepalive timeout",
			mutate: func(config *Config) {
				config.KeepaliveTimeout = -time.Nanosecond
			},
		},
		{
			name: "excessive keepalive timeout",
			mutate: func(config *Config) {
				config.KeepaliveTimeout = maxKeepaliveTimeout + time.Nanosecond
			},
		},
		{
			name: "keepalive timeout not less than time",
			mutate: func(config *Config) {
				config.KeepaliveTime = time.Second
				config.KeepaliveTimeout = time.Second
			},
		},
		{
			name: "zero client ping interval",
			mutate: func(config *Config) {
				config.MinClientPingInterval = 0
			},
		},
		{
			name: "negative client ping interval",
			mutate: func(config *Config) {
				config.MinClientPingInterval = -time.Nanosecond
			},
		},
		{
			name: "client ping below grpc minimum",
			mutate: func(config *Config) {
				config.MinClientPingInterval = 10*time.Second - time.Nanosecond
			},
		},
		{
			name: "excessive client ping interval",
			mutate: func(config *Config) {
				config.MinClientPingInterval = maxClientPingInterval + time.Nanosecond
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := DefaultConfig()
			test.mutate(&config)
			if err := config.validate(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestConfigValidationAcceptsAggregateBoundaries(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.MaxReceiveMessageBytes = maxMessageBytes
	config.MaxSendMessageBytes = maxMessageBytes
	config.MaxHeaderListBytes = maxHeaderListBytes
	config.MaxConcurrentStreams = 4
	if err := config.validate(); err != nil {
		t.Fatalf("validate() aggregate boundary error = %v", err)
	}
}

func TestMetadataSizeCountsEveryRepeatedHeaderField(t *testing.T) {
	t.Parallel()

	ctx := metadata.NewIncomingContext(
		context.Background(),
		metadata.MD{"x": []string{"", "ab"}},
	)
	const want = uint64((1 + 0 + 32) + (1 + 2 + 32))
	if got := metadataSize(ctx); got != want {
		t.Fatalf("metadataSize() = %d, want %d", got, want)
	}
	if got := saturatingAdd(^uint64(0), 1); got != ^uint64(0) {
		t.Fatalf("saturatingAdd(max, 1) = %d, want max uint64", got)
	}
}
