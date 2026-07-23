package grpcserver

import (
	"errors"
	"fmt"
	"time"
)

const (
	maxMessageBytes                    = 16 << 20
	minApplicationMetadataBytes uint32 = 1 << 10
	maxHeaderListBytes                 = 64 << 10
	maxConcurrentStreams        uint32 = maxPerConnectionHeaderBytes / maxHeaderListBytes

	maxPerConnectionReceiveBytes = 64 << 20
	maxPerConnectionSendBytes    = 64 << 20
	maxPerConnectionHeaderBytes  = 16 << 20

	maxUnaryRequestTimeout = 10 * time.Minute
	maxConnectionIdle      = time.Hour
	maxConnectionAge       = 24 * time.Hour
	maxConnectionAgeGrace  = 10 * time.Minute
	maxKeepaliveTime       = time.Hour
	maxKeepaliveTimeout    = time.Minute
	maxClientPingInterval  = time.Hour
)

var (
	// ErrInvalidConfig reports an unsafe or unusable gRPC limit.
	ErrInvalidConfig = errors.New("invalid gRPC server config")
)

// Config contains process-local gRPC transport limits. It is not a protocol
// object and must not influence canonical encoding or consensus behavior.
type Config struct {
	MaxReceiveMessageBytes int
	MaxSendMessageBytes    int
	MaxHeaderListBytes     uint32
	MaxConcurrentStreams   uint32
	UnaryRequestTimeout    time.Duration
	MaxConnectionIdle      time.Duration
	MaxConnectionAge       time.Duration
	MaxConnectionAgeGrace  time.Duration
	KeepaliveTime          time.Duration
	KeepaliveTimeout       time.Duration
	MinClientPingInterval  time.Duration
}

// DefaultConfig returns bounded defaults for the health-only bootstrap
// adapter. Future business services must justify any larger limits against
// their concrete protocol envelopes.
func DefaultConfig() Config {
	return Config{
		MaxReceiveMessageBytes: 64 << 10,
		MaxSendMessageBytes:    64 << 10,
		MaxHeaderListBytes:     16 << 10,
		MaxConcurrentStreams:   128,
		UnaryRequestTimeout:    10 * time.Second,
		MaxConnectionIdle:      2 * time.Minute,
		MaxConnectionAge:       30 * time.Minute,
		MaxConnectionAgeGrace:  30 * time.Second,
		KeepaliveTime:          2 * time.Minute,
		KeepaliveTimeout:       10 * time.Second,
		MinClientPingInterval:  30 * time.Second,
	}
}

func (config Config) validate() error {
	if err := validateIntRange(
		"MaxReceiveMessageBytes",
		config.MaxReceiveMessageBytes,
		maxMessageBytes,
	); err != nil {
		return err
	}
	if err := validateIntRange(
		"MaxSendMessageBytes",
		config.MaxSendMessageBytes,
		maxMessageBytes,
	); err != nil {
		return err
	}
	if config.MaxHeaderListBytes < minApplicationMetadataBytes ||
		config.MaxHeaderListBytes > maxHeaderListBytes {
		return fmt.Errorf(
			"%w: MaxHeaderListBytes must be in [%d,%d]",
			ErrInvalidConfig,
			minApplicationMetadataBytes,
			maxHeaderListBytes,
		)
	}
	if config.MaxConcurrentStreams == 0 || config.MaxConcurrentStreams > maxConcurrentStreams {
		return fmt.Errorf(
			"%w: MaxConcurrentStreams must be in [1,%d]",
			ErrInvalidConfig,
			maxConcurrentStreams,
		)
	}

	streams := uint64(config.MaxConcurrentStreams)
	if uint64(config.MaxReceiveMessageBytes) > uint64(maxPerConnectionReceiveBytes)/streams {
		return fmt.Errorf(
			"%w: MaxReceiveMessageBytes * MaxConcurrentStreams must not exceed %d",
			ErrInvalidConfig,
			maxPerConnectionReceiveBytes,
		)
	}
	if uint64(config.MaxSendMessageBytes) > uint64(maxPerConnectionSendBytes)/streams {
		return fmt.Errorf(
			"%w: MaxSendMessageBytes * MaxConcurrentStreams must not exceed %d",
			ErrInvalidConfig,
			maxPerConnectionSendBytes,
		)
	}
	if uint64(config.MaxHeaderListBytes) > uint64(maxPerConnectionHeaderBytes)/streams {
		return fmt.Errorf(
			"%w: MaxHeaderListBytes * MaxConcurrentStreams must not exceed %d",
			ErrInvalidConfig,
			maxPerConnectionHeaderBytes,
		)
	}

	durations := []struct {
		name    string
		value   time.Duration
		maximum time.Duration
		minimum time.Duration
	}{
		{
			name:    "UnaryRequestTimeout",
			value:   config.UnaryRequestTimeout,
			maximum: maxUnaryRequestTimeout,
		},
		{name: "MaxConnectionIdle", value: config.MaxConnectionIdle, maximum: maxConnectionIdle},
		{name: "MaxConnectionAge", value: config.MaxConnectionAge, maximum: maxConnectionAge},
		{
			name:    "MaxConnectionAgeGrace",
			value:   config.MaxConnectionAgeGrace,
			maximum: maxConnectionAgeGrace,
		},
		{
			name:    "KeepaliveTime",
			value:   config.KeepaliveTime,
			minimum: time.Second,
			maximum: maxKeepaliveTime,
		},
		{
			name:    "KeepaliveTimeout",
			value:   config.KeepaliveTimeout,
			maximum: maxKeepaliveTimeout,
		},
		{
			name:    "MinClientPingInterval",
			value:   config.MinClientPingInterval,
			minimum: 10 * time.Second,
			maximum: maxClientPingInterval,
		},
	}
	for _, duration := range durations {
		minimum := duration.minimum
		if minimum == 0 {
			minimum = time.Nanosecond
		}
		if duration.value < minimum || duration.value > duration.maximum {
			return fmt.Errorf(
				"%w: %s must be in [%s,%s]",
				ErrInvalidConfig,
				duration.name,
				minimum,
				duration.maximum,
			)
		}
	}
	if config.MaxConnectionAgeGrace > config.MaxConnectionAge {
		return fmt.Errorf(
			"%w: MaxConnectionAgeGrace must not exceed MaxConnectionAge",
			ErrInvalidConfig,
		)
	}
	if config.KeepaliveTimeout >= config.KeepaliveTime {
		return fmt.Errorf(
			"%w: KeepaliveTimeout must be less than KeepaliveTime",
			ErrInvalidConfig,
		)
	}
	return nil
}

func validateIntRange(name string, value, maximum int) error {
	if value <= 0 || value > maximum {
		return fmt.Errorf(
			"%w: %s must be in [1,%d]",
			ErrInvalidConfig,
			name,
			maximum,
		)
	}
	return nil
}
