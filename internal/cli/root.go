// Package cli assembles FinalWeave command-line interfaces.
//
// The bootstrap intentionally exposes diagnostics only. A node run command
// will be added with the first real runtime; this package must never present a
// placeholder process as a functioning validator.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/wowtrust/final-weave/internal/buildinfo"
)

const (
	outputText = "text"
	outputJSON = "json"
)

// ErrNodeRuntimeUnavailable is returned when the bootstrap binary is invoked
// as a node. Only diagnostic subcommands are available until a real runtime is
// implemented.
var ErrNodeRuntimeUnavailable = errors.New("node runtime is not implemented; use 'finalweave-node version' or '--help'")

// NewNodeCommand returns the root command for finalweave-node.
func NewNodeCommand(out, errOut io.Writer, info buildinfo.Info) *cobra.Command {
	root := &cobra.Command{
		Use:           "finalweave-node",
		Short:         "FinalWeave node bootstrap diagnostics",
		Long:          "FinalWeave node bootstrap diagnostics. The consensus runtime is not implemented yet.",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return ErrNodeRuntimeUnavailable
		},
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(newVersionCommand(info))
	return root
}

func newVersionCommand(info buildinfo.Info) *cobra.Command {
	output := outputText
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show build version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch output {
			case outputText:
				_, err := fmt.Fprintf(
					cmd.OutOrStdout(),
					"finalweave-node %s\ncommit: %s\nbuilt: %s\ngo: %s\nplatform: %s/%s\n",
					info.Version,
					info.Commit,
					info.Date,
					info.GoVersion,
					info.OS,
					info.Arch,
				)
				return err
			case outputJSON:
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetEscapeHTML(false)
				return encoder.Encode(info)
			default:
				return fmt.Errorf("unsupported output format %q: use text or json", output)
			}
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", outputText, "output format: text or json")
	return cmd
}
