package observability

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestNewLoggerWritesStructuredJSON(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger, err := NewLogger(&output, DefaultLogConfig())
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	logger.Component("node").Info("node_bootstrapped").
		Str("build_id", "test-build").
		Msg("node bootstrap completed")

	entry := decodeEntry(t, output.Bytes())
	assertStringField(t, entry, "level", "info")
	assertStringField(t, entry, "component", "node")
	assertStringField(t, entry, "event", "node_bootstrapped")
	assertStringField(t, entry, "build_id", "test-build")
	assertStringField(t, entry, "message", "node bootstrap completed")
	assertSequence(t, entry, 1)

	timestamp, ok := entry["timestamp"].(string)
	if !ok {
		t.Fatalf("timestamp type = %T, want string", entry["timestamp"])
	}
	if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
		t.Fatalf("timestamp %q is not RFC3339Nano: %v", timestamp, err)
	}
}

func TestLoggerFiltersBeforeAllocatingSequence(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger, err := NewLogger(&output, LogConfig{Level: LevelWarn, Format: FormatJSON})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	logger.Info("filtered_event").Msg("must not be emitted")
	logger.Warn("visible_event").Msg("visible")

	entry := decodeEntry(t, output.Bytes())
	assertStringField(t, entry, "event", "visible_event")
	assertSequence(t, entry, 1)
}

func TestDerivedLoggersShareConcurrentSequence(t *testing.T) {
	t.Parallel()

	const eventCount = 64

	var output bytes.Buffer
	logger, err := NewLogger(&output, DefaultLogConfig())
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	var group sync.WaitGroup
	for index := 0; index < eventCount; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			logger.Component(fmt.Sprintf("worker-%d", index%4)).
				Info("worker_completed").
				Int("worker_index", index).
				Msg("completed")
		}(index)
	}
	group.Wait()

	seen := make(map[uint64]struct{}, eventCount)
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte{'\n'})
	if len(lines) != eventCount {
		t.Fatalf("line count = %d, want %d", len(lines), eventCount)
	}
	for _, line := range lines {
		entry := decodeEntry(t, line)
		sequence := sequenceValue(t, entry)
		if _, exists := seen[sequence]; exists {
			t.Fatalf("duplicate monotonic_sequence %d", sequence)
		}
		seen[sequence] = struct{}{}
	}
	for sequence := uint64(1); sequence <= eventCount; sequence++ {
		if _, exists := seen[sequence]; !exists {
			t.Errorf("missing monotonic_sequence %d", sequence)
		}
	}
}

func TestNewLoggerWritesNoColorConsole(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger, err := NewLogger(&output, LogConfig{Level: LevelDebug, Format: FormatConsole})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	logger.Component("api").Debug("request_observed").Msg("request completed")

	text := output.String()
	for _, expected := range []string{"DBG", "api", "request_observed", "request completed"} {
		if !strings.Contains(text, expected) {
			t.Errorf("console output %q does not contain %q", text, expected)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("console output contains ANSI color sequence: %q", text)
	}
}

func TestNewLoggerRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	var typedNil *bytes.Buffer

	tests := []struct {
		name   string
		writer interface{ Write([]byte) (int, error) }
		config LogConfig
		want   error
	}{
		{
			name:   "nil writer",
			writer: nil,
			config: DefaultLogConfig(),
			want:   ErrNilLogWriter,
		},
		{
			name:   "typed nil writer",
			writer: typedNil,
			config: DefaultLogConfig(),
			want:   ErrNilLogWriter,
		},
		{
			name:   "empty level",
			writer: &output,
			config: LogConfig{Format: FormatJSON},
			want:   ErrInvalidLogLevel,
		},
		{
			name:   "unknown level",
			writer: &output,
			config: LogConfig{Level: Level("trace"), Format: FormatJSON},
			want:   ErrInvalidLogLevel,
		},
		{
			name:   "empty format",
			writer: &output,
			config: LogConfig{Level: LevelInfo},
			want:   ErrInvalidLogFormat,
		},
		{
			name:   "unknown format",
			writer: &output,
			config: LogConfig{Level: LevelInfo, Format: Format("text")},
			want:   ErrInvalidLogFormat,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewLogger(test.writer, test.config)
			if !errors.Is(err, test.want) {
				t.Fatalf("NewLogger() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDisabledAndNopLoggersRemainSilent(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger, err := NewLogger(&output, LogConfig{Level: LevelDisabled, Format: FormatJSON})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	logger.Error("disabled_event").Msg("must remain silent")
	NopLogger().Info("nop_event").Msg("must remain silent")

	if output.Len() != 0 {
		t.Fatalf("disabled logger output = %q, want empty", output.String())
	}
}

func TestUninitializedLoggerFailsLoudly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		logger *Logger
	}{
		{name: "nil pointer"},
		{name: "zero value", logger: &Logger{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recovered := recover(); recovered == nil {
					t.Fatal("uninitialized Logger.Info() did not panic")
				}
			}()
			test.logger.Info("must_fail").Msg("unreachable")
		})
	}
}

func TestNewLoggerDoesNotMutateZerologGlobals(t *testing.T) {
	timestampFieldName := zerolog.TimestampFieldName
	timeFieldFormat := zerolog.TimeFieldFormat

	var output bytes.Buffer
	if _, err := NewLogger(&output, DefaultLogConfig()); err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	if zerolog.TimestampFieldName != timestampFieldName {
		t.Fatalf("TimestampFieldName changed from %q to %q", timestampFieldName, zerolog.TimestampFieldName)
	}
	if zerolog.TimeFieldFormat != timeFieldFormat {
		t.Fatalf("TimeFieldFormat changed from %q to %q", timeFieldFormat, zerolog.TimeFieldFormat)
	}
}

func decodeEntry(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", data, err)
	}
	return entry
}

func assertStringField(t *testing.T, entry map[string]any, field, want string) {
	t.Helper()

	got, ok := entry[field].(string)
	if !ok || got != want {
		t.Fatalf("field %q = %#v, want %q", field, entry[field], want)
	}
}

func assertSequence(t *testing.T, entry map[string]any, want uint64) {
	t.Helper()

	if got := sequenceValue(t, entry); got != want {
		t.Fatalf("monotonic_sequence = %d, want %d", got, want)
	}
}

func sequenceValue(t *testing.T, entry map[string]any) uint64 {
	t.Helper()

	value, ok := entry[fieldMonotonicSequence].(float64)
	if !ok {
		t.Fatalf("monotonic_sequence type = %T, want JSON number", entry[fieldMonotonicSequence])
	}
	return uint64(value)
}
