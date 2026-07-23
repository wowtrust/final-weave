package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/wowtrust/final-weave/internal/buildinfo"
)

var testBuildInfo = buildinfo.Info{
	Version:   "v0.0.0-test",
	Commit:    "0123456789abcdef",
	Date:      "2026-07-23T00:00:00Z",
	GoVersion: "go1.test",
	OS:        "testos",
	Arch:      "testarch",
}

func TestVersionText(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewNodeCommand(&stdout, &bytes.Buffer{}, testBuildInfo)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	for _, want := range []string{
		"finalweave-node v0.0.0-test",
		"commit: 0123456789abcdef",
		"built: 2026-07-23T00:00:00Z",
		"go: go1.test",
		"platform: testos/testarch",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("text output %q does not contain %q", stdout.String(), want)
		}
	}
}

func TestVersionJSON(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewNodeCommand(&stdout, &bytes.Buffer{}, testBuildInfo)
	cmd.SetArgs([]string{"version", "--output", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var got buildinfo.Info
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, stdout.String())
	}
	if got != testBuildInfo {
		t.Fatalf("JSON build info = %+v, want %+v", got, testBuildInfo)
	}
}

func TestVersionRejectsUnsupportedOutput(t *testing.T) {
	cmd := NewNodeCommand(&bytes.Buffer{}, &bytes.Buffer{}, testBuildInfo)
	cmd.SetArgs([]string{"version", "--output", "yaml"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("Execute() error = %v, want unsupported output format", err)
	}
}

func TestRootFailsClosedWithoutDiagnosticSubcommand(t *testing.T) {
	cmd := NewNodeCommand(&bytes.Buffer{}, &bytes.Buffer{}, testBuildInfo)
	if !cmd.CompletionOptions.DisableDefaultCmd {
		t.Fatal("bootstrap must not expose Cobra's implicit completion command")
	}
	if got := cmd.Commands(); len(got) != 1 || got[0].Name() != "version" {
		t.Fatalf("bootstrap subcommands = %v, want version only", got)
	}
	if err := cmd.Execute(); !errors.Is(err, ErrNodeRuntimeUnavailable) {
		t.Fatalf("bare Execute() error = %v, want ErrNodeRuntimeUnavailable", err)
	}
}
