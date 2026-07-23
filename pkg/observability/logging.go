// Package observability provides process-local operational telemetry adapters.
//
// Operational logs are not protocol evidence. Callers must only attach bounded,
// non-sensitive metadata; private keys, tokens, payloads, raw transactions,
// request bodies, and provider error details must never be logged.
package observability

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

const (
	fieldComponent         = "component"
	fieldEvent             = "event"
	fieldMonotonicSequence = "monotonic_sequence"
	fieldTimestamp         = "timestamp"
)

var (
	// ErrNilLogWriter reports that no concrete log destination was provided.
	ErrNilLogWriter = errors.New("log writer must not be nil")
	// ErrInvalidLogLevel reports a log level outside the supported contract.
	ErrInvalidLogLevel = errors.New("invalid log level")
	// ErrInvalidLogFormat reports a log format outside the supported contract.
	ErrInvalidLogFormat = errors.New("invalid log format")
)

// Level is the minimum severity emitted by a Logger.
type Level string

const (
	LevelDebug    Level = "debug"
	LevelInfo     Level = "info"
	LevelWarn     Level = "warn"
	LevelError    Level = "error"
	LevelDisabled Level = "disabled"
)

// Format controls the local presentation of operational logs.
type Format string

const (
	FormatJSON    Format = "json"
	FormatConsole Format = "console"
)

// LogConfig is the validated, process-local logging configuration. It is not a
// protocol object and must never be included in canonical hashes.
type LogConfig struct {
	Level  Level
	Format Format
}

// DefaultLogConfig returns the production-oriented stderr logging defaults.
func DefaultLogConfig() LogConfig {
	return LogConfig{
		Level:  LevelInfo,
		Format: FormatJSON,
	}
}

// Logger is an explicitly injected structured logger. It must be created with
// NewLogger or NopLogger; using an uninitialized Logger fails loudly instead of
// silently discarding operational events. Derived component loggers share one
// process-local monotonic sequence.
type Logger struct {
	logger *zerolog.Logger
}

// NewLogger constructs a synchronous structured logger. Construction validates
// all configuration and starts no goroutines. The caller owns the writer and
// its lifecycle.
func NewLogger(out io.Writer, config LogConfig) (*Logger, error) {
	if isNilWriter(out) {
		return nil, ErrNilLogWriter
	}

	level, err := parseLevel(config.Level)
	if err != nil {
		return nil, err
	}

	writer, err := logWriter(out, config.Format)
	if err != nil {
		return nil, err
	}

	sequence := &atomic.Uint64{}
	logger := zerolog.New(writer).
		Level(level).
		Hook(metadataHook{sequence: sequence})

	return &Logger{logger: &logger}, nil
}

// NopLogger returns a disabled logger for tests and deliberately silent
// components. Production composition roots should construct an explicit logger
// instead so accidental log loss remains visible in configuration.
func NopLogger() *Logger {
	logger := zerolog.Nop()
	return &Logger{logger: &logger}
}

// Component derives a logger carrying a stable, non-sensitive component name.
// The returned value shares the parent's monotonic sequence and is safe to copy.
func (l *Logger) Component(name string) *Logger {
	logger := l.mustLogger().With().Str(fieldComponent, name).Logger()
	return &Logger{logger: &logger}
}

// Debug starts a debug event with its stable machine-readable event name.
func (l *Logger) Debug(event string) *zerolog.Event {
	return l.mustLogger().Debug().Str(fieldEvent, event)
}

// Info starts an informational event with its stable machine-readable event name.
func (l *Logger) Info(event string) *zerolog.Event {
	return l.mustLogger().Info().Str(fieldEvent, event)
}

// Warn starts a warning event with its stable machine-readable event name.
func (l *Logger) Warn(event string) *zerolog.Event {
	return l.mustLogger().Warn().Str(fieldEvent, event)
}

// Error starts an error event with its stable machine-readable event name.
// Callers must attach bounded error codes, not raw security-provider errors.
func (l *Logger) Error(event string) *zerolog.Event {
	return l.mustLogger().Error().Str(fieldEvent, event)
}

func (l *Logger) mustLogger() *zerolog.Logger {
	if l == nil || l.logger == nil {
		panic("observability: Logger must be created with NewLogger or NopLogger")
	}
	return l.logger
}

func parseLevel(level Level) (zerolog.Level, error) {
	switch level {
	case LevelDebug:
		return zerolog.DebugLevel, nil
	case LevelInfo:
		return zerolog.InfoLevel, nil
	case LevelWarn:
		return zerolog.WarnLevel, nil
	case LevelError:
		return zerolog.ErrorLevel, nil
	case LevelDisabled:
		return zerolog.Disabled, nil
	default:
		return zerolog.NoLevel, fmt.Errorf("%w: %q", ErrInvalidLogLevel, level)
	}
}

func logWriter(out io.Writer, format Format) (io.Writer, error) {
	synchronized := zerolog.SyncWriter(out)

	switch format {
	case FormatJSON:
		return synchronized, nil
	case FormatConsole:
		return zerolog.ConsoleWriter{
			Out:        synchronized,
			NoColor:    true,
			TimeFormat: time.RFC3339Nano,
		}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidLogFormat, format)
	}
}

func isNilWriter(writer io.Writer) bool {
	if writer == nil {
		return true
	}

	value := reflect.ValueOf(writer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

type metadataHook struct {
	sequence *atomic.Uint64
}

func (h metadataHook) Run(event *zerolog.Event, _ zerolog.Level, _ string) {
	event.
		Str(fieldTimestamp, time.Now().UTC().Format(time.RFC3339Nano)).
		Uint64(fieldMonotonicSequence, h.sequence.Add(1))
}
